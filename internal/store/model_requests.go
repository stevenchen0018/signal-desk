package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const ModelRequestsPageSize = 25

type ModelRequestResult struct {
	Status, Note   string
	HTTPStatus     int
	ModelCount     *int
	Added, Removed []string
}

type ModelRequest struct {
	ID, StartedAt, FinishedAt int64
	Trigger                   string
	ModelRequestResult
}

func (r ModelRequest) StartedUnix() int64 { return r.StartedAt / 1000 }

func (r ModelRequest) DurationMillis() int64 {
	if r.FinishedAt == 0 {
		return 0
	}
	return max(0, r.FinishedAt-r.StartedAt)
}

func (s *Store) StartModelRequest(ctx context.Context, job Resource) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		trigger := "scheduled"
		if job.requested > 0 {
			trigger = "manual"
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO model_requests(run_token,started_at,trigger)
			SELECT run_token,?,? FROM resources WHERE id=? AND run_token=? AND revision=?`,
			now, trigger, job.ID, job.runToken, job.Revision)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("任务配置已变更，查询已取消")
		}
		// A previous lease can only be replaced after its deadline has passed.
		_, err = tx.ExecContext(ctx, `UPDATE model_requests SET status='interrupted',note='上次执行中断'
			WHERE status='running' AND run_token<>?`, job.runToken)
		return err
	})
}

func finishModelRequest(ctx context.Context, tx *sql.Tx, job Resource, result ModelRequestResult) error {
	added, err := json.Marshal(result.Added)
	if err != nil {
		return err
	}
	removed, err := json.Marshal(result.Removed)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE model_requests SET finished_at=?,status=?,http_status=?,model_count=?,added=?,removed=?,note=?
		WHERE run_token=? AND status='running'`, time.Now().UnixMilli(), result.Status, result.HTTPStatus,
		result.ModelCount, string(added), string(removed), result.Note, job.runToken)
	return err
}

func (s *Store) ModelRequests(ctx context.Context, page int64) ([]ModelRequest, bool, error) {
	logs, err := s.readModelRequests(ctx, ModelRequestsPageSize+1, (page-1)*ModelRequestsPageSize)
	more := len(logs) > ModelRequestsPageSize
	if more {
		logs = logs[:ModelRequestsPageSize]
	}
	return logs, more, err
}

func (s *Store) LatestModelRequest(ctx context.Context) (*ModelRequest, error) {
	logs, err := s.readModelRequests(ctx, 1, 0)
	if err != nil || len(logs) == 0 {
		return nil, err
	}
	return &logs[0], nil
}

func (s *Store) readModelRequests(ctx context.Context, limit, offset int64) ([]ModelRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,started_at,finished_at,trigger,status,http_status,model_count,added,removed,note
		FROM model_requests ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []ModelRequest
	for rows.Next() {
		var r ModelRequest
		var added, removed string
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.FinishedAt, &r.Trigger, &r.Status, &r.HTTPStatus,
			&r.ModelCount, &added, &removed, &r.Note); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(added), &r.Added); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(removed), &r.Removed); err != nil {
			return nil, err
		}
		logs = append(logs, r)
	}
	return logs, rows.Err()
}
