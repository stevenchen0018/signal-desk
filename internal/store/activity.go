package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) Summary(ctx context.Context) ([3]int, error) {
	var counts [3]int
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM resources WHERE kind='mailboxes' AND enabled=1),
		(SELECT count(*) FROM resources WHERE kind='models' AND enabled=1),
		(SELECT count(*) FROM deliveries WHERE status IN ('pending','sending'))`).
		Scan(&counts[0], &counts[1], &counts[2])
	return counts, err
}

type Record struct {
	ID, At, Attempts                    int64
	Kind, Source, Title, Detail, Status string
}

func (s *Store) History(ctx context.Context, tab string) ([]Record, error) {
	queries := map[string]string{
		"events":     "SELECT id,at,kind,source,title,detail,'',0 FROM events ORDER BY id DESC LIMIT 100",
		"runs":       "SELECT id,at,kind,name,message,'',CASE success WHEN 1 THEN 'ok' ELSE 'failed' END,0 FROM runs ORDER BY id DESC LIMIT 100",
		"deliveries": "SELECT id,updated,'',CASE WHEN policy_id>0 THEN channel_name || ' · 策略 #' || policy_id ELSE channel_name END,title,error,status,attempts FROM deliveries ORDER BY id DESC LIMIT 100",
	}
	query, ok := queries[tab]
	if !ok {
		return nil, errors.New("未知记录类型")
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Record
	for rows.Next() {
		var r Record
		if err = rows.Scan(&r.ID, &r.At, &r.Kind, &r.Source, &r.Title, &r.Detail, &r.Status, &r.Attempts); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func insertEvent(ctx context.Context, tx *sql.Tx, kind, source, title, detail string) (int64, error) {
	res, err := tx.ExecContext(ctx, "INSERT INTO events(at,kind,source,title,detail) VALUES(?,?,?,?,?)", time.Now().Unix(), kind, source, title, detail)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
