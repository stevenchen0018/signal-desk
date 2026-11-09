package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"signal-desk/internal/config"
)

type Model struct {
	ID    string `json:"id"`
	Owner string `json:"owned_by"`
}

type ModelSnapshot struct {
	Models    []Model
	UpdatedAt int64
	Snapshot  json.RawMessage
}

type Event struct {
	Kind, Title   string
	Detail        any
	Notifications []Notification
}

type JobOutcome struct {
	TraceID                    int64
	Steps                      []TraceStep
	State                      any
	ModelRequest               *ModelRequestResult
	Events                     []Event
	Note                       string
	Success, More, Interrupted bool
}

func (s *Store) RequestJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE resources SET requested=requested+1 WHERE id=? AND kind IN ('mailboxes','models')", id)
	return err
}

var ErrNoWork = errors.New("no pending work")

func (s *Store) ClaimJob(ctx context.Context) (Resource, error) {
	now := time.Now().Unix()
	r, err := scanResource(s.db.QueryRowContext(ctx, `UPDATE resources
		SET lease_until=?, run_token=?
		WHERE id=(SELECT id FROM resources WHERE kind IN ('mailboxes','models') AND lease_until<=?
			AND (requested>0 OR (enabled=1 AND next_run<=?)) ORDER BY requested DESC,next_run,id LIMIT 1)
		RETURNING `+resourceColumns, now+180, rand.Text(), now, now))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNoWork
	}
	return r, err
}

func (s *Store) ReleaseJob(ctx context.Context, r Resource) error {
	// Keep the last checkpoint if writing the result transaction failed.
	_, err := s.db.ExecContext(ctx, `UPDATE resources SET run_token='',lease_until=0,next_run=?
		WHERE id=? AND revision=? AND run_token=?`, time.Now().Unix()+15, r.ID, r.Revision, r.runToken)
	return err
}

func (r Resource) ManualQueryPending() bool {
	return r.requested > 0
}

func (s *Store) FinishJob(ctx context.Context, r Resource, result JobOutcome) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		var token string
		var revision int64
		err := tx.QueryRowContext(ctx, "SELECT run_token,revision FROM resources WHERE id=?", r.ID).Scan(&token, &revision)
		missing := errors.Is(err, sql.ErrNoRows)
		if err != nil && !missing {
			return err
		}
		stale := missing || token != r.runToken || revision != r.Revision
		if result.ModelRequest != nil {
			request := *result.ModelRequest
			if stale {
				request.Status = "discarded"
				request.Note = "任务配置已更新，本次结果未应用"
			}
			if err := finishModelRequest(ctx, tx, r, request); err != nil {
				return err
			}
		}
		if stale {
			if err := finishTrace(ctx, tx, result.TraceID, "interrupted", "任务配置已变更，本次结果未应用", result.Steps); err != nil {
				return err
			}
		}
		if missing || token != r.runToken {
			return nil
		}
		if stale {
			_, err = tx.ExecContext(ctx, "UPDATE resources SET lease_until=0,run_token='' WHERE id=?", r.ID)
			return err
		}

		now := time.Now()
		next := s.nextJobRun(r, result, now)
		state := r.State
		if result.State != nil {
			data, err := json.Marshal(result.State)
			if err != nil {
				return err
			}
			state = string(data)
		}
		for _, e := range result.Events {
			detail, ok := e.Detail.(string)
			if !ok {
				data, err := json.MarshalIndent(e.Detail, "", "  ")
				if err != nil {
					return err
				}
				detail = string(data)
			}
			id, err := insertEvent(ctx, tx, e.Kind, r.Name, e.Title, detail)
			if err != nil {
				return err
			}
			if result.TraceID != 0 {
				if _, err := tx.ExecContext(ctx, "INSERT INTO trace_events(trace_id,event_id) VALUES(?,?)", result.TraceID, id); err != nil {
					return err
				}
			}
			for _, message := range e.Notifications {
				queued, err := s.enqueue(ctx, tx, id, message)
				if err != nil {
					return err
				}
				if result.TraceID != 0 {
					status := "ok"
					if queued == 0 {
						status = "skipped"
					}
					note := fmt.Sprintf("事件 #%d · 策略 #%d · 已入队 %d 条", id, message.PolicyID, queued)
					if queued == 0 {
						note += "，有渠道停用、已删除或接收地址已变更"
					}
					result.Steps = append(result.Steps, TraceStep{At: now.Unix(), Stage: "通知入队", Status: status, Message: note})
				}
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO runs(resource_id,name,kind,at,success,message) VALUES(?,?,?,?,?,?)`,
			r.ID, r.Name, r.Kind, now.Unix(), result.Success, result.Note)
		if err != nil {
			return err
		}
		traceStatus := "ok"
		if !result.Success {
			traceStatus = "failed"
		}
		if result.Interrupted {
			traceStatus = "interrupted"
		}
		if err := finishTrace(ctx, tx, result.TraceID, traceStatus, result.Note, result.Steps); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE resources SET state=?,next_run=?,run_token='',lease_until=0,
			requested=CASE WHEN requested=? AND NOT ? THEN 0 ELSE requested END WHERE id=?`,
			state, next, r.requested, result.Interrupted, r.ID)
		return err
	})
}

func (s *Store) nextJobRun(r Resource, result JobOutcome, now time.Time) int64 {
	if result.More || result.Interrupted {
		return now.Unix() + 1
	}
	var schedule struct{ Cron string }
	if err := s.Decode(r, &schedule); err != nil {
		return now.Unix() + max(r.Interval, 60)
	}
	if schedule.Cron == "" {
		return now.Unix() + r.Interval
	}
	if r.Next > now.Unix() {
		// A manual check before the scheduled time leaves the schedule intact.
		return r.Next
	}
	next, err := config.NextCronRun(schedule.Cron, now)
	if err != nil {
		return now.Unix() + 300
	}
	return next.Unix()
}
