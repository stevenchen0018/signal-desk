package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

// TestSMTP uses the saved server. An empty recipient checks MAIL FROM only.
func (w *Worker) TestSMTP(ctx context.Context, recipient string, message store.Notification) (int64, error) {
	configs, err := w.store.List(ctx, "smtp")
	if err != nil {
		return 0, err
	}
	if len(configs) == 0 {
		return 0, errors.New("请先保存 SMTP 配置")
	}
	var smtpConfig config.SMTP
	if err := w.store.Decode(configs[0], &smtpConfig); err != nil {
		return 0, err
	}
	id, err := w.store.StartSMTPTrace(ctx, configs[0])
	if err != nil {
		return 0, err
	}
	trace := &jobTrace{ID: id}
	probe := recipient == ""
	operation, note := "SMTP 测试发信", "SMTP 服务器已接受测试邮件"
	if probe {
		operation, note = "SMTP 发件检测", "服务器已接受 MAIL FROM，发件检测完成"
	}
	trace.record("开始测试", "ok", operation+" · 使用已保存配置")
	if !probe {
		if address, err := config.ParseAddress(recipient); err == nil {
			trace.record("目标邮箱", "ok", address.Address)
		}
		if message.Title == "" {
			message.Title = "Signal Desk SMTP 测试"
			message.Body = "这是一封由管理员主动发起的测试邮件，用于验证发信服务器配置。"
		} else {
			trace.record("渲染通知模板", "ok", message.Title)
		}
	}
	start := time.Now()
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = sendEmail(checkCtx, smtpConfig, recipient, probe, store.Delivery{
		Title: message.Title, Body: message.Body, BodyFormat: message.BodyFormat,
	}, trace)
	cancel()
	status := "ok"
	if err != nil {
		status, note = "failed", err.Error()
	}
	finishCtx, finish := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer finish()
	if finishErr := w.store.FinishTrace(finishCtx, id, status, note, trace.steps); finishErr != nil {
		return 0, fmt.Errorf("保存 SMTP 测试结果：%w", finishErr)
	}
	w.externalLog(operation, "", smtpConfig.Host, trace.ref(""), smtpCode(err), start, err)
	return id, nil
}
