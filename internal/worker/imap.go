package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"slices"
	"strings"
	"time"

	"signal-desk/internal/config"
	"signal-desk/internal/store"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
)

const maxMailBytes = 5 << 20

type mailState struct {
	Validity uint32
	Last     imap.UID
}

func (w *Worker) mailRules(ctx context.Context, mailbox int64) ([]config.CompiledRule, error) {
	configs, err := w.store.List(ctx, "rules")
	if err != nil {
		return nil, err
	}
	var rules []config.CompiledRule
	for _, r := range configs {
		if !r.Enabled {
			continue
		}
		var cfg config.Rule
		if err = w.store.Decode(r, &cfg); err != nil {
			return nil, err
		}
		if cfg.Mailbox != mailbox {
			continue
		}
		rule, err := config.CompileRule(r.Name, cfg)
		if err != nil {
			return nil, err
		}
		rule.ID = r.ID
		rules = append(rules, rule)
	}
	return rules, nil
}

func openMailbox(ctx context.Context, cfg config.Mailbox) (*imapclient.Client, func(), error) {
	conn, tlsConfig, closeConn, err := dialServer(ctx, cfg.Host, cfg.Port, cfg.TLS)
	if err != nil {
		return nil, nil, requestError("IMAP", err)
	}
	options := &imapclient.Options{TLSConfig: tlsConfig, WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader}}
	var c *imapclient.Client
	if cfg.TLS == "starttls" {
		c, err = imapclient.NewStartTLS(conn, options)
	} else {
		c = imapclient.New(conn, options)
		err = c.WaitGreeting()
	}
	if err != nil {
		closeConn()
		return nil, nil, errors.New("IMAP 握手失败，请检查连接方式与证书")
	}
	closeClient := func() { closeConn(); c.Close() }
	if err = c.Login(cfg.User, cfg.Password).Wait(); err != nil {
		closeClient()
		return nil, nil, errors.New("IMAP 登录失败，请检查用户名、授权码与邮箱 IMAP 设置")
	}
	return c, closeClient, nil
}

func (w *Worker) pollMail(ctx context.Context, r store.Resource, trace *jobTrace) (result jobResult, runErr error) {
	var cfg config.Mailbox
	if err := w.store.Decode(r, &cfg); err != nil {
		return jobResult{}, err
	}
	rules, err := w.mailRules(ctx, r.ID)
	if err != nil {
		return jobResult{}, err
	}
	plan, err := w.store.NotificationPlan(ctx)
	if err != nil {
		return jobResult{}, err
	}
	started := time.Now()
	trace.record("监听收件邮箱", "ok", fmt.Sprintf("连接 %s:%s，读取 %s，启用 %d 条规则", cfg.Host, cfg.Port, cfg.Folder, len(rules)))
	defer func() {
		w.externalLog("IMAP", "", cfg.Host, trace.ref(fmt.Sprintf("邮箱 #%d", r.ID)), 0, started, runErr)
	}()
	c, closeClient, err := openMailbox(ctx, cfg)
	if err != nil {
		trace.record("IMAP 连接与认证", "failed", err.Error())
		return jobResult{}, err
	}
	defer closeClient()
	trace.record("IMAP 连接与认证", "ok", "已登录收件邮箱")
	return scanMailbox(ctx, c, cfg, r.State, rules, plan, trace)
}

func scanMailbox(ctx context.Context, c *imapclient.Client, cfg config.Mailbox, savedState string, rules []config.CompiledRule, plan []store.PlannedNotification, trace *jobTrace) (jobResult, error) {
	box, err := c.Select(cfg.Folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		fmt.Printf(
			"[IMAP] host=imap.163.com folder=INBOX operation=SELECT error_type=%T error=%v",
			err, err,
		)
		return jobResult{}, errors.New("无法打开 IMAP 文件夹，请检查文件夹名称与访问权限")
	}
	trace.record("打开邮件目录", "ok", fmt.Sprintf("%s · 邮件 %d 封", cfg.Folder, box.NumMessages))
	if box.UIDValidity == 0 || box.UIDNext == 0 {
		return jobResult{}, errors.New("IMAP 服务端未提供 UIDVALIDITY 或 UIDNEXT")
	}
	var state mailState
	if savedState != "" {
		if err = json.Unmarshal([]byte(savedState), &state); err != nil {
			return jobResult{}, err
		}
	}
	lastAvailable := box.UIDNext - 1
	if state.Validity != box.UIDValidity {
		state = mailState{Validity: box.UIDValidity}
		if !cfg.Replay {
			state.Last = lastAvailable
			return jobResult{state: state, note: "已建立邮件游标，等待新邮件"}, nil
		}
	}
	if state.Last >= lastAvailable {
		return jobResult{state: state, note: "暂无新邮件"}, nil
	}
	if len(rules) == 0 {
		return jobResult{state: state, note: "暂无启用的邮件规则，保留当前游标"}, nil
	}

	var span imap.UIDSet
	span.AddRange(state.Last+1, lastAvailable)
	found, err := c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{span}}, nil).Wait()
	if err != nil {
		return jobResult{}, errors.New("IMAP 搜索新邮件失败")
	}
	uids := found.AllUIDs()
	slices.Sort(uids)
	if len(uids) == 0 {
		state.Last = lastAvailable
		return jobResult{state: state, note: "暂无新邮件"}, nil
	}
	trace.record("搜索新邮件", "ok", fmt.Sprintf("待检查 %d 封，本次最多处理 40 封", len(uids)))
	more := len(uids) > 40
	if more {
		uids = uids[:40]
	}
	headers, err := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID: true, Envelope: true, RFC822Size: true, InternalDate: true,
	}).Collect()
	if err != nil {
		return jobResult{}, errors.New("读取邮件主题失败")
	}
	slices.SortFunc(headers, func(a, b *imapclient.FetchMessageBuffer) int {
		if a.UID < b.UID {
			return -1
		}
		if a.UID > b.UID {
			return 1
		}
		return 0
	})
	result := jobResult{state: state, more: more}
	var matched, failed int
	for _, h := range headers {
		if h.UID <= state.Last || h.UID > lastAvailable {
			continue
		}
		if ctx.Err() != nil {
			return result, errors.New("处理邮件超时，下次继续读取")
		}
		if h.Envelope == nil {
			return result, errors.New("IMAP 未返回邮件主题，下次继续读取")
		}
		var selected []config.CompiledRule
		for _, rule := range rules {
			if rule.Matches(h.Envelope.Subject) {
				selected = append(selected, rule)
			}
		}
		subject := []rune(strings.Join(strings.Fields(h.Envelope.Subject), " "))
		mailLabel := fmt.Sprintf("UID %d · %s", h.UID, string(subject[:min(len(subject), 160)]))
		if len(selected) == 0 {
			trace.record("主题正则", "skipped", mailLabel+" · 未命中启用的规则")
		} else {
			matched++
			trace.record("主题正则", "ok", fmt.Sprintf("%s · 命中 %d 条规则", mailLabel, len(selected)))
			data, available, err := fetchMail(c, h)
			if err != nil {
				trace.record("读取正文", "failed", fmt.Sprintf("UID %d · %v", h.UID, err))
				if !available {
					result.note = fmt.Sprintf("已处理至 UID %d", state.Last)
					return result, err
				}
				failed++
				result.events = append(result.events, event{Event: store.Event{Kind: "mail_error", Title: h.Envelope.Subject,
					Detail: map[string]any{"uid": h.UID, "error": err.Error()}}})
			} else if available {
				trace.record("读取正文", "ok", fmt.Sprintf("UID %d · 已解析文本正文", h.UID))
				for _, rule := range selected {
					rendered, detail := rule.Apply(data, uint32(h.UID))
					e := event{Event: store.Event{Kind: "mail", Title: data.Subject, Detail: detail}}
					if detail.Error == "" {
						found := 0
						for _, value := range detail.Fields {
							if value != "" {
								found++
							}
						}
						note := fmt.Sprintf("UID %d · %s · %d/%d 个字段有值", h.UID, rule.Name, found, len(detail.Fields))
						if detail.MailModelList != nil {
							note += fmt.Sprintf(" · %d 条模型记录", len(detail.Models))
						}
						trace.record("正文正则提取", "ok", note+"，见下方提取结果")
						e.prepare(rule.Route, rendered)
						rendered.Source = cfg.Folder
						e.applyPolicies(plan, config.MailMatched, rule.ID, rendered, trace)
					} else {
						e.Kind = "mail_error"
						failed++
						trace.record("正文正则提取", "failed", fmt.Sprintf("UID %d · %s · %s", h.UID, rule.Name, detail.Error))
					}
					result.events = append(result.events, e)
				}
			} else {
				trace.record("读取正文", "skipped", fmt.Sprintf("UID %d · 邮件已移除或属于系统自身的通知", h.UID))
			}
		}
		state.Last = h.UID
		result.state = state
	}
	state.Last = lastAvailable
	if more {
		state.Last = uids[len(uids)-1]
	}
	result.state = state
	result.note = fmt.Sprintf("检查 %d 封邮件，命中 %d 封，解析异常 %d 条", len(headers), matched, failed)
	return result, nil
}

// A transient fetch error leaves this UID uncommitted. A malformed message is recorded and skipped.
func fetchMail(c *imapclient.Client, h *imapclient.FetchMessageBuffer) (config.TemplateData, bool, error) {
	if h.RFC822Size > maxMailBytes {
		return config.TemplateData{}, true, errors.New("原邮件超过 5 MiB，已跳过")
	}
	section := &imap.FetchItemBodySection{Peek: true, Partial: &imap.SectionPartial{Size: maxMailBytes + 1}}
	response, err := c.Fetch(imap.UIDSetNum(h.UID), &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return config.TemplateData{}, false, errors.New("读取邮件正文失败，下次继续读取")
	}
	if len(response) == 0 {
		return config.TemplateData{}, false, nil // Deleted after the envelope was fetched.
	}
	raw := response[0].FindBodySection(section)
	if len(raw) == 0 {
		return config.TemplateData{}, false, errors.New("IMAP 未返回邮件正文，下次继续读取")
	}
	if len(raw) > maxMailBytes {
		return config.TemplateData{}, true, errors.New("原邮件超过 5 MiB，已跳过")
	}
	body, err := readMailBody(raw)
	if errors.Is(err, errOwnMail) {
		return config.TemplateData{}, false, nil
	}
	if err != nil {
		return config.TemplateData{}, true, err
	}
	var from []string
	for _, a := range h.Envelope.From {
		from = append(from, a.Addr())
	}
	return config.TemplateData{Subject: h.Envelope.Subject, From: strings.Join(from, ", "),
		ReceivedAt: config.Stamp(h.InternalDate.Unix()), Body: body, Time: config.Stamp(time.Now().Unix())}, true, nil
}
