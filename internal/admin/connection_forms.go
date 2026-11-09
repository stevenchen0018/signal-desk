package admin

import (
	"errors"
	"strings"
	"time"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

func (a *adminUI) mailboxForm(form *settingsForm, in formRequest, current store.Resource, basic []field) (config.Mailbox, error) {
	cfg := config.Mailbox{Port: "993", TLS: "tls", Folder: "INBOX"}
	if err := a.decodeSaved(current, &cfg); err != nil {
		return cfg, err
	}
	var err error
	if in.post {
		cfg = config.Mailbox{
			Host: in.text("host"), Port: in.text("port"), TLS: in.text("tls"),
			User: in.text("user"), Password: in.secret("password", cfg.Password),
			Folder: in.text("folder"), Replay: in.text("replay") == "1",
			Cron: in.text("cron"),
		}
	}
	schedule := input("cron", "Cron 表达式（可选）", cfg.Cron, "填写后按 Cron 执行。支持 5 个字段或前置秒字段，也可加 CRON_TZ=Asia/Shanghai。")
	schedule.Placeholder = "*/5 * * * *"
	form.add("邮箱配置", "", append(basic,
		schedule,
		input("host", "IMAP 主机", cfg.Host, "例如 imap.example.com"), input("port", "端口", cfg.Port, ""),
		tlsField(cfg.TLS), input("folder", "邮件文件夹", cfg.Folder, "通常为 INBOX"),
		input("user", "邮箱账号", cfg.User, ""), secret("password", "密码或应用授权码", cfg.Password),
		checkbox("replay", "首次读取已有邮件", cfg.Replay, "仅在首次建立游标或 IMAP UIDVALIDITY 变化时生效。"),
	)...)
	if in.post {
		err = config.ValidateServer(cfg.Host, cfg.Port, cfg.TLS)
		if err == nil && (cfg.User == "" || cfg.Password == "" || cfg.Folder == "" || strings.ContainsAny(cfg.User+cfg.Password+cfg.Folder, "\r\n\x00")) {
			err = errors.New("邮箱账号、授权码和文件夹均需填写，且不能换行")
		}
		if err == nil && cfg.Cron != "" {
			_, err = config.NextCronRun(cfg.Cron, time.Now())
		}
	}
	return cfg, err
}

func (a *adminUI) channelForm(form *settingsForm, in formRequest, current store.Resource, basic []field) (config.Channel, error) {
	cfg := config.Channel{Type: "email"}
	if err := a.decodeSaved(current, &cfg); err != nil {
		return cfg, err
	}
	var err error
	if in.post {
		t := in.text("type")
		address := in.text("email")
		if t != "email" {
			address = in.text("endpoint")
			if address == "" && t == cfg.Type {
				address = cfg.Address
			}
		}
		original := ""
		if t == cfg.Type {
			original = cfg.Secret
		}
		cfg = config.Channel{Type: t, Address: address, Secret: in.secret("secret", original), Customer: in.text("customer")}
		if t == "email" {
			cfg.Secret = ""
		}
	}
	email := textArea("email", "目标邮箱", "", "每行填写一个邮箱，或用逗号分隔。最多 50 个，重复地址只投递一次。")
	email.For = "email"
	if cfg.Type == "email" {
		email.Value = cfg.Address
	}
	endpoint := secret("endpoint", "Webhook URL", "")
	if cfg.Type != "email" && cfg.Address != "" {
		endpoint.Placeholder = "已设置 · 留空保留"
	}
	endpoint.For = "feishu webhook"
	key := secret("secret", "飞书签名密钥 / Webhook Bearer Token（可选）", cfg.Secret)
	key.For = "feishu webhook"
	clear := checkbox("clear_secret", "清除已保存的密钥", false, "")
	clear.For = "feishu webhook"
	form.add("渠道配置", "邮件渠道可配置多个目标邮箱。飞书和 Webhook 渠道各使用一个接收地址。", append(basic,
		input("customer", "客户名称（可选）", cfg.Customer, "同一客户可配置多个渠道，模板可通过 {{.Customer}} 引用。"),
		selectField("type", "渠道类型", cfg.Type, option{Value: "email", Label: "邮件"},
			option{Value: "feishu", Label: "飞书群机器人"}, option{Value: "webhook", Label: "通用 Webhook"}),
		email, endpoint, key, clear,
	)...)
	if in.post {
		switch cfg.Type {
		case "email":
			_, err = config.ParseRecipients(cfg.Address)
		case "feishu", "webhook":
			err = config.ValidateURL(cfg.Address, cfg.Type == "feishu" || cfg.Secret != "")
			if err == nil && strings.ContainsAny(cfg.Secret, "\r\n") {
				err = errors.New("密钥不能换行")
			}
		default:
			err = errors.New("请选择有效的通知类型")
		}
	}
	return cfg, err
}

func (a *adminUI) smtpForm(form *settingsForm, in formRequest, current store.Resource) (config.SMTP, error) {
	cfg := config.SMTP{Port: "465", TLS: "tls"}
	if err := a.decodeSaved(current, &cfg); err != nil {
		return cfg, err
	}
	var err error
	if in.post {
		cfg = config.SMTP{
			Host: in.text("host"), Port: in.text("port"), TLS: in.text("tls"),
			User: in.text("user"), Password: in.secret("password", cfg.Password), From: in.text("from"),
		}
		if cfg.User == "" {
			cfg.Password = ""
		}
	}
	form.add("SMTP 连接", "所有邮件渠道共用这台发信服务器。", input("host", "SMTP 主机", cfg.Host, "例如 smtp.example.com"),
		input("port", "端口", cfg.Port, ""), tlsField(cfg.TLS),
		input("from", "发件地址", cfg.From, "支持显示名，如 Signal Desk <alerts@example.com>"),
		input("user", "SMTP 用户名（可选）", cfg.User, "留空时不进行 SMTP AUTH"),
		secret("password", "密码或应用授权码", cfg.Password))
	if in.post {
		if err = config.ValidateServer(cfg.Host, cfg.Port, cfg.TLS); err == nil {
			_, err = config.ParseAddress(cfg.From)
		}
		if err == nil && (len(cfg.From) > 320 || (cfg.User != "" && cfg.Password == "") || strings.ContainsAny(cfg.User+cfg.Password, "\r\n\x00")) {
			err = errors.New("请检查发件地址长度、SMTP 用户名和授权码")
		}
	}
	return cfg, err
}
