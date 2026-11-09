package store

import (
	"context"
	"log"
	"time"
)

type InterfaceLog struct {
	ID, At, DurationMS                            int64
	Direction, Operation, Target, Reference, Note string
	Status                                        int
	Success                                       bool
}

func (s *Store) RecordInterface(entry InterfaceLog) {
	entry.At = time.Now().Unix()
	log.Printf("接口 %s %s %s ref=%s status=%d ok=%t duration_ms=%d note=%q",
		entry.Direction, entry.Operation, entry.Target, entry.Reference, entry.Status, entry.Success, entry.DurationMS, entry.Note)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO interface_logs(at,direction,operation,target,reference,status,success,duration_ms,note)
		VALUES(?,?,?,?,?,?,?,?,?)`, entry.At, entry.Direction, entry.Operation, entry.Target, entry.Reference,
		entry.Status, entry.Success, entry.DurationMS, entry.Note); err != nil {
		log.Printf("保存接口日志失败：%v", err)
	}
}

const interfacePageSize = 30

func (s *Store) InterfaceLogs(ctx context.Context, direction string, before int64) ([]InterfaceLog, int64, error) {
	query := "SELECT id,at,direction,operation,target,reference,status,success,duration_ms,note FROM interface_logs WHERE id<?"
	args := []any{before}
	if direction != "" {
		query += " AND direction=?"
		args = append(args, direction)
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY id DESC LIMIT ?", append(args, interfacePageSize+1)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var entries []InterfaceLog
	for rows.Next() {
		var entry InterfaceLog
		if err := rows.Scan(&entry.ID, &entry.At, &entry.Direction, &entry.Operation, &entry.Target, &entry.Reference,
			&entry.Status, &entry.Success, &entry.DurationMS, &entry.Note); err != nil {
			return nil, 0, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(entries) > interfacePageSize {
		return entries[:interfacePageSize], entries[interfacePageSize-1].ID, nil
	}
	return entries, 0, nil
}
