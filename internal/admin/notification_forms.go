package admin

import (
	"context"
	"strconv"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

func (a *adminUI) notificationTemplateForm(form *settingsForm, in formRequest, current store.Resource, basic []field) (config.NotificationTemplate, error) {
	cfg := config.DefaultNotificationTemplate("custom")
	if err := a.decodeSaved(current, &cfg); err != nil {
		return cfg, err
	}
	if in.post {
		cfg = config.NotificationTemplate{Category: in.text("category"), Format: in.text("format"),
			MessageTemplate: config.MessageTemplate{Title: in.text("title"), Body: in.values.Get("body")}}
	}
	categories := []option{
		{Value: config.ModelUp, Label: config.TemplateCategoryLabel(config.ModelUp)},
		{Value: config.ModelDown, Label: config.TemplateCategoryLabel(config.ModelDown)},
		{Value: config.ModelEarlyDown, Label: config.TemplateCategoryLabel(config.ModelEarlyDown)},
		{Value: "custom", Label: "自定义通知"},
	}
	format := cfg.Format
	if format == "" {
		format = config.BodyText
	}
	title := input("title", "通知标题", cfg.Title, "可使用与正文相同的变量。")
	title.Wide = true
	form.add("模板内容", "模板可在多条告警策略中复用。选择 HTML 可粘贴排版代码或插入表格示例。", append(basic,
		selectField("category", "模板类别", cfg.Category, categories...),
		selectField("format", "正文格式", format, option{Value: config.BodyText, Label: "纯文本"}, option{Value: config.BodyHTML, Label: "HTML（支持表格）"}),
		title, textArea("body", "通知正文", cfg.Body,
			`通用 {{.Customer}}、{{.Channel}}、{{.Time}}；模型监控 {{.Models}}、{{.Count}}；邮件下线表格 {{range .MailModels}}{{.ModelName}} {{.OfflineTime}} {{.ReplacementModel}}{{end}}，共同下线时间 {{.OfflineAt}}。其他邮件字段可用 {{index .Fields "字段名"}}。`),
	)...)
	if in.post {
		return cfg, cfg.Validate()
	}
	return cfg, nil
}

type policyChoices struct {
	rules, templates, channels []option
	names                      map[int64]string
}

func (a *adminUI) loadPolicyChoices(ctx context.Context) (policyChoices, error) {
	p := policyChoices{names: make(map[int64]string)}
	for _, group := range []struct {
		kind    string
		options *[]option
	}{{"rules", &p.rules}, {"templates", &p.templates}, {"channels", &p.channels}} {
		items, err := a.store.List(ctx, group.kind)
		if err != nil {
			return p, err
		}
		for _, item := range items {
			label := item.Name
			if group.kind == "channels" {
				var cfg config.Channel
				if err := a.store.Decode(item, &cfg); err != nil {
					return p, err
				}
				if cfg.Customer != "" {
					label = cfg.Customer + " · " + label
				}
				label += " · " + channelTypeLabel(cfg.Type)
			}
			if !item.Enabled {
				label += "（停用）"
			}
			p.names[item.ID] = label
			*group.options = append(*group.options, option{Value: strconv.FormatInt(item.ID, 10), Label: label})
		}
	}
	return p, nil
}

func channelTypeLabel(kind string) string {
	return map[string]string{"email": "邮件", "feishu": "飞书", "webhook": "Webhook"}[kind]
}

func triggerLabel(kind string) string {
	return map[string]string{config.MailMatched: "邮件规则命中", config.ModelUp: "模型监控上线", config.ModelDown: "模型监控下线"}[kind]
}

func (a *adminUI) policyForm(ctx context.Context, form *settingsForm, in formRequest, current store.Resource, basic []field) (config.Policy, error) {
	cfg := config.Policy{Trigger: config.MailMatched}
	if err := a.decodeSaved(current, &cfg); err != nil {
		return cfg, err
	}
	if in.post {
		cfg = config.Policy{Trigger: in.text("trigger"), ModelPattern: in.text("model_pattern"), RuleID: number(in.text("rule_id")),
			TemplateID: number(in.text("template_id")), ChannelID: number(in.text("channel_id"))}
		if cfg.Trigger != config.MailMatched {
			cfg.RuleID = 0
		}
	}
	options, err := a.loadPolicyChoices(ctx)
	if err != nil {
		return cfg, err
	}
	choose := func(key, title string, selected int64, items []option) field {
		return selectField(key, title, strconv.FormatInt(selected, 10), append([]option{{Value: "", Label: "请选择"}}, items...)...)
	}
	rule := choose("rule_id", "邮件规则", cfg.RuleID, options.rules)
	rule.For = config.MailMatched
	form.add("触发与接收", "同一事件可建多条策略，分别选择文案和客户接收渠道。", append(basic,
		selectField("trigger", "触发事件", cfg.Trigger,
			option{Value: config.MailMatched, Label: triggerLabel(config.MailMatched)},
			option{Value: config.ModelUp, Label: triggerLabel(config.ModelUp)},
			option{Value: config.ModelDown, Label: triggerLabel(config.ModelDown)}),
		rule, choose("template_id", "通知模板", cfg.TemplateID, options.templates),
		choose("channel_id", "客户接收渠道", cfg.ChannelID, options.channels),
		input("model_pattern", "模型 ID 正则（可选）", cfg.ModelPattern, "留空匹配全部模型。用于邮件时，请在邮件规则中提取模型字段。"),
	)...)
	if in.post {
		return cfg, cfg.Validate()
	}
	return cfg, nil
}
