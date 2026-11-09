package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"signal-desk/internal/config"
)

type Delivery struct {
	ID, EventID, ChannelID, Attempts      int64
	Title, Body, ChannelSource, EventType string
	BodyFormat                            string
	RecipientIndex                        int

	claim string
}

type Notification struct {
	BodyFormat  string
	Title, Body string
	Channels    []int64
	PolicyID    int64
	Destination string
}

func (s *Store) ClaimDelivery(ctx context.Context) (Delivery, error) {
	now := time.Now().Unix()
	var d Delivery
	err := s.db.QueryRowContext(ctx, `UPDATE deliveries
		SET status='sending',claim=?,next_try=?,updated=?
		WHERE id=(SELECT id FROM deliveries WHERE status IN ('pending','sending') AND next_try<=? ORDER BY next_try,id LIMIT 1)
		RETURNING id,event_id,channel_id,title,body,body_format,recipient_index,attempts,claim,channel_source,
		(SELECT kind FROM events WHERE id=deliveries.event_id)`, rand.Text(), now+90, now, now).
		Scan(&d.ID, &d.EventID, &d.ChannelID, &d.Title, &d.Body, &d.BodyFormat, &d.RecipientIndex, &d.Attempts, &d.claim, &d.ChannelSource, &d.EventType)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNoWork
	}
	return d, err
}

func (s *Store) FinishDelivery(ctx context.Context, d Delivery, attempt DeliveryAttempt) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		now := time.Now().Unix()
		result, err := tx.ExecContext(ctx, `UPDATE deliveries SET status=?,error=?,attempts=?,next_try=?,updated=?,claim=''
			WHERE id=? AND claim=?`, attempt.Status, attempt.Note, d.Attempts+1, attempt.Next, now, d.ID, d.claim)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		steps, err := json.Marshal(attempt.Steps)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO delivery_attempts(delivery_id,at,status,note,next_try,duration_ms,steps)
			VALUES(?,?,?,?,?,?,?)`, d.ID, now, attempt.Status, attempt.Note, attempt.Next, attempt.DurationMS, string(steps))
		return err
	})
}

var ErrDeliveryNotFailed = errors.New("只有投递失败或正在等待重试的通知可以重新投递")

func (s *Store) RetryDelivery(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE deliveries SET status='pending',attempts=0,next_try=0,error='',updated=?
		WHERE id=? AND (status='failed' OR (status='pending' AND error<>''))`, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrDeliveryNotFailed
	}
	return nil
}

func (s *Store) QueueTestDelivery(ctx context.Context, channelID int64) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		var name string
		var enabled bool
		if err := tx.QueryRowContext(ctx, "SELECT name,enabled FROM resources WHERE id=? AND kind='channels'", channelID).Scan(&name, &enabled); err != nil {
			return err
		}
		if !enabled {
			return errors.New("请先启用该渠道")
		}
		id, err := insertEvent(ctx, tx, "test", name, "测试通知", "{}")
		if err != nil {
			return err
		}
		_, err = s.enqueue(ctx, tx, id, Notification{
			Title: "Signal Desk 测试通知", Body: "这条消息用于验证通知渠道配置。", Channels: []int64{channelID},
		})
		return err
	})
}

func (s *Store) enqueue(ctx context.Context, tx *sql.Tx, eventID int64, message Notification) (int64, error) {
	var queued int64
	bodyFormat := config.BodyText
	if message.BodyFormat == config.BodyHTML {
		bodyFormat = config.BodyHTML
	}
	for _, id := range message.Channels {
		channel, err := scanResource(tx.QueryRowContext(ctx, "SELECT "+resourceColumns+
			" FROM resources WHERE id=? AND kind='channels' AND enabled=1", id))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if message.PolicyID != 0 && channel.Source != message.Destination {
			continue
		}
		var cfg config.Channel
		if err := s.Decode(channel, &cfg); err != nil {
			return 0, err
		}
		first, last := 0, 0
		var recipients []*mail.Address
		if cfg.Type == "email" {
			recipients, err = config.ParseRecipients(cfg.Address)
			if err != nil {
				return 0, err
			}
			first, last = 1, len(recipients)
		}
		for recipient := first; recipient <= last; recipient++ {
			name := channel.Name
			email := ""
			if recipient > 0 {
				email = recipients[recipient-1].Address
			}
			if last > 1 {
				name += fmt.Sprintf(" · 收件人 %d", recipient)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO deliveries(event_id,channel_id,channel_name,channel_source,title,body,updated,policy_id,body_format,recipient_index,recipient_email)
				VALUES(?,?,?,?,?,?,?,?,?,?,?)`, eventID, id, name, channel.Source, message.Title, message.Body,
				time.Now().Unix(), message.PolicyID, bodyFormat, recipient, email)
			if err != nil {
				return 0, err
			}
			queued++
		}
	}
	return queued, nil
}
