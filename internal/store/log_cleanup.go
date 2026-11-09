package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrLogScope = errors.New("请选择有效的日志类别")

func (s *Store) ClearLogs(ctx context.Context, scope string) error {
	switch scope {
	case "interfaces":
		_, err := s.db.ExecContext(ctx, "DELETE FROM interface_logs")
		return err
	case "models":
		_, err := s.db.ExecContext(ctx, "DELETE FROM model_requests WHERE status<>'running'")
		return err
	case "workflow":
		return s.clearWorkflowLogs(ctx)
	default:
		return ErrLogScope
	}
}

func (s *Store) clearWorkflowLogs(ctx context.Context) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		// Keep active jobs and the traces needed by the delivery queue.
		_, err := tx.ExecContext(ctx, `
			CREATE TEMP TABLE clear_trace_ids(id INTEGER PRIMARY KEY);
			INSERT INTO clear_trace_ids SELECT t.id FROM job_traces t
				WHERE t.status<>'running' AND NOT EXISTS (
					SELECT 1 FROM trace_events te JOIN deliveries d ON d.event_id=te.event_id
					WHERE te.trace_id=t.id AND d.status IN ('pending','sending')
				);
			DELETE FROM trace_steps WHERE trace_id IN (SELECT id FROM clear_trace_ids);
			DELETE FROM trace_events WHERE trace_id IN (SELECT id FROM clear_trace_ids);
			DELETE FROM job_traces WHERE id IN (SELECT id FROM clear_trace_ids);
			DROP TABLE clear_trace_ids;

			CREATE TEMP TABLE clear_event_ids(id INTEGER PRIMARY KEY);
			INSERT INTO clear_event_ids SELECT e.id FROM events e
				WHERE NOT EXISTS (SELECT 1 FROM trace_events te WHERE te.event_id=e.id)
				AND NOT EXISTS (SELECT 1 FROM deliveries d WHERE d.event_id=e.id AND d.status IN ('pending','sending'));
			DELETE FROM delivery_attempts WHERE delivery_id IN (
				SELECT id FROM deliveries WHERE status IN ('sent','failed')
			);
			DELETE FROM deliveries WHERE status IN ('sent','failed');
			DELETE FROM events WHERE id IN (SELECT id FROM clear_event_ids);
			DROP TABLE clear_event_ids;
			DELETE FROM runs;`)
		return err
	})
}
