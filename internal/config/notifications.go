package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	MailMatched    = "mail"
	ModelUp        = "model_up"
	ModelDown      = "model_down"
	ModelEarlyDown = "model_early_down"
)

const (
	BodyText = "text"
	BodyHTML = "html"
)

type NotificationTemplate struct {
	Category string
	Format   string `json:",omitempty"`
	MessageTemplate
}

type Policy struct {
	ModelPattern                  string `json:",omitempty"`
	Trigger                       string
	RuleID, TemplateID, ChannelID int64
}

func (p Policy) Validate() error {
	switch p.Trigger {
	case MailMatched:
		if p.RuleID <= 0 {
			return errors.New("请选择触发告警的邮件规则")
		}
	case ModelUp, ModelDown:
		if p.RuleID != 0 {
			return errors.New("模型监控策略无需选择邮件规则")
		}
	default:
		return errors.New("请选择有效的触发事件")
	}
	if p.TemplateID <= 0 || p.ChannelID <= 0 {
		return errors.New("请选择通知模板和客户接收渠道")
	}
	_, err := p.CompileModelPattern()
	return err
}

func (p Policy) CompileModelPattern() (*regexp.Regexp, error) {
	if p.ModelPattern == "" {
		return nil, nil
	}
	if len(p.ModelPattern) > 4096 {
		return nil, errors.New("模型 ID 正则最多 4096 字节")
	}
	re, err := regexp.Compile(p.ModelPattern)
	if err != nil {
		return nil, fmt.Errorf("模型 ID 正则无效：%w", err)
	}
	return re, nil
}

func TemplateCategoryLabel(category string) string {
	switch category {
	case ModelUp:
		return "模型已完成上线"
	case ModelDown:
		return "模型已完成下线"
	case ModelEarlyDown:
		return "模型提前下线"
	default:
		return "自定义通知"
	}
}

func DefaultNotificationTemplate(category string) NotificationTemplate {
	labels := map[string]string{ModelUp: "已完成上线", ModelDown: "已完成下线", ModelEarlyDown: "将提前下线"}
	action, ok := labels[category]
	if !ok {
		return NotificationTemplate{Category: "custom", MessageTemplate: DefaultMailTemplate()}
	}
	return NotificationTemplate{Category: category, MessageTemplate: MessageTemplate{
		Title: "模型" + action,
		Body: "尊敬的{{if .Customer}}{{.Customer}}{{else}}客户{{end}}：\n{{if .Models}}{{range .Models}}- {{.}}\n{{end}}{{else if .Model}}模型：{{.Model}}\n{{else}}通知：{{.Subject}}\n{{end}}状态：" + action +
			"\n{{if .OfflineAt}}下线时间：{{.OfflineAt}}\n{{end}}{{if .Fields}}{{range $name, $value := .Fields}}{{if $value}}{{$name}}：{{$value}}\n{{end}}{{end}}{{end}}",
	}}
}

func (t NotificationTemplate) Validate() error {
	switch t.Category {
	case ModelUp, ModelDown, ModelEarlyDown, "custom":
	default:
		return errors.New("请选择有效的模板类别")
	}
	if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Body) == "" {
		return errors.New("请填写通知标题和正文")
	}
	if _, err := parseMessageTemplate(t.Title); err != nil {
		return fmt.Errorf("通知标题：%w", err)
	}
	var err error
	switch t.Format {
	case "", BodyText:
		_, err = parseMessageTemplate(t.Body)
	case BodyHTML:
		_, err = parseHTMLMessage(t.Body)
	default:
		return errors.New("请选择有效的正文格式")
	}
	if err != nil {
		return fmt.Errorf("通知正文：%w", err)
	}
	return nil
}

func (t NotificationTemplate) Render(data TemplateData) (string, string, error) {
	return t.MessageTemplate.render(data, t.Format == BodyHTML)
}
