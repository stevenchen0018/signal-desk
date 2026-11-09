package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

const maxAttempts = 5

func (w *Worker) claimDelivery(ctx context.Context) (func(), error) {
	d, err := w.store.ClaimDelivery(ctx)
	if err != nil {
		return nil, err
	}
	return func() { w.runDelivery(ctx, d) }, nil
}

func (w *Worker) runDelivery(ctx context.Context, d store.Delivery) {
	sendCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	started := time.Now()
	trace := &jobTrace{}
	err := w.send(sendCtx, d, trace)
	cancel()
	attempts := d.Attempts + 1
	now := time.Now().Unix()
	status, note, next := "sent", "", int64(0)
	if err != nil {
		note = err.Error()
		switch {
		case d.EventType == "notification_test" || smtpCode(err) >= 500 || attempts >= maxAttempts:
			status = "failed"
		default:
			status = "pending"
			next = now + (30 << (attempts - 1))
		}
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
	defer finishCancel()
	if err = w.store.FinishDelivery(finishCtx, d, store.DeliveryAttempt{Status: status, Note: note, Next: next, DurationMS: time.Since(started).Milliseconds(), Steps: trace.steps}); err != nil {
		log.Printf("写入投递结果（#%d）：%v", d.ID, err)
	}
}

func (w *Worker) send(ctx context.Context, d store.Delivery, trace *jobTrace) error {
	channel, err := w.store.Get(ctx, "channels", d.ChannelID)
	if err != nil || !channel.Enabled {
		return errors.New("通知渠道已删除或停用")
	}
	if channel.Source != d.ChannelSource {
		return errors.New("接收目标已变更，此通知仍绑定原地址")
	}
	var cfg config.Channel
	if err := w.store.Decode(channel, &cfg); err != nil {
		return err
	}
	switch cfg.Type {
	case "email":
		list, err := w.store.List(ctx, "smtp")
		if err != nil {
			return errors.New("无法读取 SMTP 配置")
		}
		if len(list) == 0 {
			return errors.New("请先配置发信服务器 SMTP")
		}
		var sender config.SMTP
		if err := w.store.Decode(list[0], &sender); err != nil {
			return err
		}
		recipients, err := config.ParseRecipients(cfg.Address)
		if err != nil {
			return err
		}
		index := d.RecipientIndex - 1
		if d.RecipientIndex == 0 && len(recipients) == 1 {
			index = 0
		}
		if index < 0 || index >= len(recipients) {
			return errors.New("找不到本次投递绑定的收件人")
		}
		start := time.Now()
		err = sendEmail(ctx, sender, recipients[index].String(), false, d, trace)
		w.externalLog("SMTP", "", sender.Host, fmt.Sprintf("投递 #%d", d.ID), smtpCode(err), start, err)
		return err
	case "feishu", "webhook":
		return w.sendWebhook(ctx, cfg, d, trace)
	default:
		return errors.New("未知通知渠道")
	}
}

func (w *Worker) sendWebhook(ctx context.Context, cfg config.Channel, d store.Delivery, trace *jobTrace) (runErr error) {
	payload := map[string]any{
		"delivery_id": d.ID, "event_id": d.EventID, "event_type": d.EventType, "title": d.Title, "body": d.Body,
	}
	if d.BodyFormat == config.BodyHTML {
		payload["body_format"] = config.BodyHTML
	}
	if cfg.Type == "feishu" {
		payload = map[string]any{"msg_type": "text", "content": map[string]string{"text": d.Title + "\n" + d.Body}}
		if d.BodyFormat == config.BodyHTML {
			card, err := feishuHTMLCard(d.Title, d.Body)
			if err != nil {
				return err
			}
			payload = map[string]any{"msg_type": "interactive", "card": card}
		}
		if cfg.Secret != "" {
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			sig := hmac.New(sha256.New, []byte(timestamp+"\n"+cfg.Secret))
			payload["timestamp"] = timestamp
			payload["sign"] = base64.StdEncoding.EncodeToString(sig.Sum(nil))
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if cfg.Type == "feishu" && len(raw) > 20*1024 {
		return errors.New("飞书通知超过 20 KB，请缩短模板输出")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Address, bytes.NewReader(raw))
	if err != nil {
		return errors.New("通知地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Type == "webhook" {
		req.Header.Set("Idempotency-Key", fmt.Sprintf("signal-desk-%d", d.ID))
		if cfg.Secret != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.Secret)
		}
	}
	start := time.Now()
	status := 0
	defer func() {
		operation := "Webhook"
		if cfg.Type == "feishu" {
			operation = "飞书"
		}
		result, message := "ok", fmt.Sprintf("HTTP %d", status)
		if runErr != nil {
			result, message = "failed", runErr.Error()
		}
		trace.record(operation+" 请求", result, message)
		w.externalLog(operation, req.Method, req.URL.Host, fmt.Sprintf("投递 #%d", d.ID), status, start, runErr)
	}()
	resp, err := w.client.Do(req)
	if err != nil {
		return requestError("Webhook", err)
	}
	status = resp.StatusCode
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Webhook 返回 HTTP %d", resp.StatusCode)
	}
	if cfg.Type == "webhook" {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil
	}
	raw, err = readBounded(resp.Body, 64<<10)
	if err != nil {
		return errors.New("无法读取飞书回执")
	}
	var response struct {
		Code       *int `json:"code"`
		StatusCode *int `json:"StatusCode"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return errors.New("飞书返回了无效的 JSON")
	}
	if response.Code == nil {
		response.Code = response.StatusCode
	}
	if response.Code == nil {
		return errors.New("飞书回执缺少状态码")
	}
	if *response.Code != 0 {
		return fmt.Errorf("飞书业务错误 %d，请检查签名、关键词、白名单或限流", *response.Code)
	}
	return nil
}
