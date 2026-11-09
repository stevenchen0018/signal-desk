package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrTestChannelChanged = errors.New("接收渠道已修改或停用，请重新选择")

func (s *Store) QueueTemplateTest(ctx context.Context, channel Resource, templateName string, message Notification) (int64, error) {
	var traceID int64
	err := s.transact(ctx, func(tx *sql.Tx) error {
		var available bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM resources
			WHERE id=? AND kind='channels' AND enabled=1 AND revision=?)`, channel.ID, channel.Revision).Scan(&available); err != nil {
			return err
		}
		if !available {
			return ErrTestChannelChanged
		}

		now := time.Now().Unix()
		res, err := tx.ExecContext(ctx, `INSERT INTO job_traces(resource_id,kind,source,trigger,started_at)
			VALUES(?,'notification_test',?,'manual',?)`, channel.ID, channel.Name, now)
		if err != nil {
			return err
		}
		traceID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		detail, err := json.Marshal(map[string]string{"template": templateName, "format": message.BodyFormat})
		if err != nil {
			return err
		}
		eventID, err := insertEvent(ctx, tx, "notification_test", channel.Name, message.Title, string(detail))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO trace_events(trace_id,event_id) VALUES(?,?)", traceID, eventID); err != nil {
			return err
		}
		message.Channels = []int64{channel.ID}
		count, err := s.enqueue(ctx, tx, eventID, message)
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrTestChannelChanged
		}
		return finishTrace(ctx, tx, traceID, "ok", "测试通知已入队，投递结果见下方", []TraceStep{
			{At: now, Stage: "渲染通知模板", Status: "ok", Message: templateName},
			{At: now, Stage: "测试通知入队", Status: "ok", Message: fmt.Sprintf("%s · %d 条投递", channel.Name, count)},
		})
	})
	if err != nil {
		return 0, err
	}
	return traceID, nil
}
