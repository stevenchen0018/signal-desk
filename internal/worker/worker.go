package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

type event struct {
	store.Event
	templateError error
}

func (e *event) prepare(routing config.Route, data config.TemplateData) {
	if len(routing.Channels) == 0 {
		return
	}
	title, body, err := routing.Render(data)
	if err != nil {
		e.templateError = err
		return
	}
	e.Notifications = append(e.Notifications, store.Notification{Title: title, Body: body, Channels: routing.Channels})
}

func (e *event) applyPolicies(plan []store.PlannedNotification, trigger string, ruleID int64, data config.TemplateData, trace *jobTrace) {
	for _, policy := range plan {
		if policy.Trigger != trigger || policy.RuleID != ruleID {
			continue
		}
		values := data
		if policy.Pattern != nil {
			if trigger == config.MailMatched {
				if len(data.MailModels) > 0 {
					var rows []config.MailModel
					for _, row := range data.MailModels {
						if row.ModelName != "" && policy.Pattern.MatchString(row.ModelName) {
							rows = append(rows, row)
						}
					}
					if len(rows) == 0 {
						trace.record("匹配告警策略", "skipped", fmt.Sprintf("%s：邮件提取的模型不匹配", policy.Name))
						continue
					}
					values.SetMailModels(rows)
				} else if data.Model == "" || !policy.Pattern.MatchString(data.Model) {
					trace.record("匹配告警策略", "skipped", fmt.Sprintf("%s：邮件提取的模型不匹配", policy.Name))
					continue
				}
			} else {
				values.Models = nil
				values.Model = ""
				for _, id := range data.Models {
					if policy.Pattern.MatchString(id) {
						values.Models = append(values.Models, id)
					}
				}
				if len(values.Models) == 0 {
					trace.record("匹配告警策略", "skipped", fmt.Sprintf("%s：本次变更的模型不匹配", policy.Name))
					continue
				}
				values.Count = len(values.Models)
				if values.Count == 1 {
					values.Model = values.Models[0]
				}
			}
		}
		trace.record("匹配告警策略", "ok", fmt.Sprintf("策略 #%d · %s → %s", policy.ID, policy.Name, policy.Channel))
		values.Customer, values.Channel = policy.Customer, policy.Channel
		title, body, err := policy.Template.Render(values)
		if err != nil {
			e.templateError = errors.Join(e.templateError, fmt.Errorf("告警策略 #%d 模板渲染失败：%w", policy.ID, err))
			trace.record("渲染通知模板", "failed", fmt.Sprintf("策略 #%d：%v", policy.ID, err))
			continue
		}
		trace.record("渲染通知模板", "ok", fmt.Sprintf("策略 #%d：%s", policy.ID, title))
		e.Notifications = append(e.Notifications, store.Notification{Title: title, Body: body, BodyFormat: policy.Template.Format,
			Channels: []int64{policy.ChannelID}, PolicyID: policy.ID, Destination: policy.Destination})
	}
	if len(e.Notifications) == 0 && e.templateError == nil {
		trace.record("匹配告警策略", "skipped", "无可用的告警策略，保留事件")
	}
}

type jobResult struct {
	state        any
	modelRequest *store.ModelRequestResult
	events       []event
	note         string
	more         bool
}

func (r jobResult) outcome(runErr error, interrupted bool) store.JobOutcome {
	note := r.note
	if runErr != nil {
		note = runErr.Error()
	}
	result := store.JobOutcome{
		State: r.state, ModelRequest: r.modelRequest, Note: note, Success: runErr == nil, More: r.more && runErr == nil, Interrupted: interrupted,
	}
	var failures int
	for _, e := range r.events {
		result.Events = append(result.Events, e.Event)
		if e.templateError != nil {
			failures++
			result.Events = append(result.Events, store.Event{Kind: "template_error", Title: e.Title, Detail: e.templateError.Error()})
		}
	}
	if failures > 0 {
		result.Success = false
		result.Note += fmt.Sprintf("，%d 条通知渲染失败", failures)
	}
	return result
}

type Worker struct {
	store  *store.Store
	client *http.Client
}

func New(s *store.Store, client *http.Client) *Worker {
	return &Worker{store: s, client: client}
}

func (w *Worker) Run(ctx context.Context) {
	var active sync.WaitGroup
	defer active.Wait()
	var jobs = make(chan struct{}, 3)
	var deliveries = make(chan struct{}, 4)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		w.launch(ctx, &active, jobs, w.claimJob)
		w.launch(ctx, &active, deliveries, w.claimDelivery)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) launch(ctx context.Context, active *sync.WaitGroup, slots chan struct{}, claim func(context.Context) (func(), error)) {
	for len(slots) < cap(slots) && ctx.Err() == nil {
		work, err := claim(ctx)
		if errors.Is(err, store.ErrNoWork) || ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("调度：%v", err)
			return
		}
		slots <- struct{}{}
		active.Add(1)
		go func() {
			defer active.Done()
			defer func() { <-slots }()
			work()
		}()
	}
}

func (w *Worker) claimJob(ctx context.Context) (func(), error) {
	r, err := w.store.ClaimJob(ctx)
	if err != nil {
		return nil, err
	}
	return func() { w.runJob(ctx, r) }, nil
}

func (w *Worker) runJob(ctx context.Context, r store.Resource) {
	runCtx, cancel := context.WithTimeout(ctx, 110*time.Second)
	trace := &jobTrace{}
	id, err := w.store.StartJobTrace(runCtx, r)
	if err != nil {
		log.Printf("创建链路（任务 #%d）：%v", r.ID, err)
	}
	trace.ID = id
	trace.record("开始执行", "ok", r.Name)
	var result jobResult
	var runErr error
	switch r.Kind {
	case "mailboxes":
		result, runErr = w.pollMail(runCtx, r, trace)
	case "models":
		result, runErr = w.pollModels(runCtx, r, trace)
	}
	cancel()
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer finishCancel()
	outcome := result.outcome(runErr, ctx.Err() != nil)
	if runErr != nil {
		trace.record("任务异常", "failed", outcome.Note)
	}
	outcome.TraceID, outcome.Steps = trace.ID, trace.steps
	if err := w.store.FinishJob(finishCtx, r, outcome); err != nil {
		log.Printf("写入任务结果（#%d）：%v", r.ID, err)
		if trace.ID > 0 {
			if traceErr := w.store.FinishTrace(finishCtx, trace.ID, "failed", "任务结果写入失败，检查服务端日志", trace.steps); traceErr != nil {
				log.Printf("更新链路（#%d）：%v", trace.ID, traceErr)
			}
		}
		if releaseErr := w.store.ReleaseJob(finishCtx, r); releaseErr != nil {
			log.Printf("释放任务（#%d）：%v", r.ID, releaseErr)
		}
	}
}
