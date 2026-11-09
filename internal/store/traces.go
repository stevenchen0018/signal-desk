package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type Trace struct {
	ID, Started, Finished               int64
	Kind, Source, Trigger, Status, Note string
}

type TraceStep struct {
	At                     int64
	Stage, Status, Message string
}

type TraceEvent struct {
	ID                  int64
	Kind, Title, Detail string
	Deliveries          []TraceDelivery
}

type TraceDelivery struct {
	ID, Attempts, Next            int64
	PolicyID                      int64
	Channel, Title, Status, Error string
	RecipientEmail                string
	History                       []DeliveryAttempt
}

type DeliveryAttempt struct {
	Steps                []TraceStep
	At, Next, DurationMS int64
	Status, Note         string
}

func (s *Store) StartJobTrace(ctx context.Context, job Resource) (int64, error) {
	trigger := "scheduled"
	if job.ManualQueryPending() {
		trigger = "manual"
	}
	var traceID int64
	err := s.transact(ctx, func(tx *sql.Tx) error {
		// A job claim is exclusive until its lease expires.
		if _, err := tx.ExecContext(ctx, `UPDATE job_traces SET status='interrupted',finished_at=?,note='上次检查未写入结束记录'
			WHERE resource_id=? AND kind=? AND status='running'`, time.Now().Unix(), job.ID, job.Kind); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO job_traces(resource_id,kind,source,trigger,started_at) VALUES(?,?,?,?,?)",
			job.ID, job.Kind, job.Name, trigger, time.Now().Unix())
		if err != nil {
			return err
		}
		traceID, err = result.LastInsertId()
		return err
	})
	return traceID, err
}

func (s *Store) StartSMTPTrace(ctx context.Context, resource Resource) (int64, error) {
	res, err := s.db.ExecContext(ctx, "INSERT INTO job_traces(resource_id,kind,source,trigger,started_at) VALUES(?,'smtp',?,'manual',?)",
		resource.ID, resource.Name, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishTrace(ctx context.Context, id int64, status, note string, steps []TraceStep) error {
	return s.transact(ctx, func(tx *sql.Tx) error { return finishTrace(ctx, tx, id, status, note, steps) })
}

func finishTrace(ctx context.Context, tx *sql.Tx, id int64, status, note string, steps []TraceStep) error {
	if id == 0 {
		return nil
	}
	result, err := tx.ExecContext(ctx, "UPDATE job_traces SET finished_at=?,status=?,note=? WHERE id=? AND status='running'",
		time.Now().Unix(), status, note, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	for i, step := range steps {
		if _, err := tx.ExecContext(ctx, "INSERT INTO trace_steps(trace_id,ordinal,at,stage,status,message) VALUES(?,?,?,?,?,?)",
			id, i, step.At, step.Stage, step.Status, step.Message); err != nil {
			return err
		}
	}
	return nil
}

const tracesPageSize = 30
const traceColumns = "id,started_at,finished_at,kind,source,trigger,status,note"

func scanTrace(row interface{ Scan(...any) error }) (Trace, error) {
	var t Trace
	err := row.Scan(&t.ID, &t.Started, &t.Finished, &t.Kind, &t.Source, &t.Trigger, &t.Status, &t.Note)
	return t, err
}

func (s *Store) Traces(ctx context.Context, kind string, resource, before int64) ([]Trace, int64, error) {
	query := "SELECT " + traceColumns + " FROM job_traces WHERE id<?"
	args := []any{before}
	if kind != "" {
		query += " AND kind=?"
		args = append(args, kind)
	}
	if resource > 0 {
		query += " AND resource_id=?"
		args = append(args, resource)
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY id DESC LIMIT ?", append(args, tracesPageSize+1)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var list []Trace
	for rows.Next() {
		trace, err := scanTrace(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, trace)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(list) > tracesPageSize {
		return list[:tracesPageSize], list[tracesPageSize-1].ID, nil
	}
	return list, 0, nil
}

func (s *Store) Trace(ctx context.Context, id int64) (Trace, error) {
	return scanTrace(s.db.QueryRowContext(ctx, "SELECT "+traceColumns+" FROM job_traces WHERE id=?", id))
}

func (s *Store) TraceSteps(ctx context.Context, id int64) ([]TraceStep, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT at,stage,status,message FROM trace_steps WHERE trace_id=? ORDER BY ordinal", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []TraceStep
	for rows.Next() {
		var step TraceStep
		if err := rows.Scan(&step.At, &step.Stage, &step.Status, &step.Message); err != nil {
			return nil, err
		}
		list = append(list, step)
	}
	return list, rows.Err()
}

func (s *Store) TraceEvents(ctx context.Context, id int64) ([]TraceEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.kind,e.title,e.detail FROM trace_events t JOIN events e ON e.id=t.event_id
		WHERE t.trace_id=? ORDER BY e.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []TraceEvent
	for rows.Next() {
		var e TraceEvent
		if err := rows.Scan(&e.ID, &e.Kind, &e.Title, &e.Detail); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	deliveries, err := s.traceDeliveries(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range events {
		events[i].Deliveries = deliveries[events[i].ID]
	}
	return events, nil
}

func (s *Store) traceDeliveries(ctx context.Context, id int64) (map[int64][]TraceDelivery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.event_id,d.id,d.channel_name,d.policy_id,d.title,d.status,d.attempts,d.error,d.next_try,d.recipient_email,
		a.at,a.status,a.note,a.next_try,a.duration_ms,a.steps
		FROM trace_events t JOIN deliveries d ON d.event_id=t.event_id LEFT JOIN delivery_attempts a ON a.delivery_id=d.id
		WHERE t.trace_id=? ORDER BY d.event_id,d.id,a.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64][]TraceDelivery)
	for rows.Next() {
		var eventID int64
		var d TraceDelivery
		var aAt, aNext, duration sql.NullInt64
		var status, note, steps sql.NullString
		if err := rows.Scan(&eventID, &d.ID, &d.Channel, &d.PolicyID, &d.Title, &d.Status, &d.Attempts, &d.Error, &d.Next, &d.RecipientEmail,
			&aAt, &status, &note, &aNext, &duration, &steps); err != nil {
			return nil, err
		}
		list := result[eventID]
		if len(list) == 0 || list[len(list)-1].ID != d.ID {
			list = append(list, d)
			result[eventID] = list
		}
		if aAt.Valid {
			current := &list[len(list)-1]
			attempt := DeliveryAttempt{At: aAt.Int64, Next: aNext.Int64, DurationMS: duration.Int64,
				Status: status.String, Note: note.String}
			if err := json.Unmarshal([]byte(steps.String), &attempt.Steps); err != nil {
				return nil, err
			}
			current.History = append(current.History, attempt)
		}
	}
	return result, rows.Err()
}
