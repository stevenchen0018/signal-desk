package admin

import (
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

func (a *adminUI) ruleForm(form *settingsForm, in formRequest, current store.Resource, basic []field, channels, mailboxes []store.Resource, name string) (config.Rule, error) {
	cfg := config.Rule{Route: config.Route{MessageTemplate: config.DefaultMailTemplate()}}
	if err := a.decodeSaved(current, &cfg); err != nil {
		return cfg, err
	}
	var err error
	if in.post {
		cfg = config.Rule{
			Mailbox: number(in.text("mailbox")), Subject: in.text("subject"), Extract: in.text("extract"),
			Route: readRoute(in.values, ""),
		}
	}
	options := []option{{Value: "", Label: "选择一个收件邮箱"}}
	for _, box := range mailboxes {
		options = append(options, option{Value: strconv.FormatInt(box.ID, 10), Label: box.Name})
	}
	subject := input("subject", "邮件主题正则", cfg.Subject, "区分大小写，可用 (?i) 忽略大小写。")
	subject.Placeholder = "(?i)告警|alert"
	extraction := textArea("extract", "正文提取规则", cfg.Extract, "每行填 字段名=正则，默认取第一个捕获组。字段名末尾加 * 表示必填。用 models 提取下线表格，可在预览查看逐条模型及时间。")
	extraction.Placeholder = `工单号*=工单号[：:]\s*([A-Z0-9-]+)`
	form.add("筛选与提取", "", append(basic,
		selectField("mailbox", "收件邮箱", strconv.FormatInt(cfg.Mailbox, 10), options...), subject, extraction,
	)...)
	if len(cfg.Channels) > 0 {
		form.add("旧版通知设置", "可在告警策略中配置更多接收方式。", routeFields("", cfg.Route, channels, "邮件模板变量")...)
	}
	form.add("样例预览", "使用样例检查主题、字段提取和模板渲染。", input("sample_subject", "样例主题", in.text("sample_subject"), ""),
		textArea("sample_body", "样例正文", in.values.Get("sample_body"), ""))
	if in.post {
		if cfg.Mailbox <= 0 {
			return cfg, errors.New("请选择收件邮箱")
		}
		rule, compileErr := config.CompileRule(name, cfg)
		err = compileErr
		if err == nil {
			sample := config.SampleTemplateData()
			for _, f := range rule.Fields {
				sample.Fields[f.Name] = "示例值"
			}
			sample.Event = "mail"
			_, _, err = cfg.Render(sample)
		}
	}
	return cfg, err
}

func (a *adminUI) modelForm(form *settingsForm, in formRequest, current store.Resource, basic []field, channels []store.Resource) (config.ModelMonitor, error) {
	cfg := config.DefaultModelMonitor()
	if current.ID > 0 {
		cfg = config.ModelMonitor{}
		if err := a.store.Decode(current, &cfg); err != nil {
			return cfg, err
		}
	}
	var err error
	if in.post {
		cfg = config.ModelMonitor{
			URL: in.text("url"), Token: in.secret("token", cfg.Token), Cron: in.text("cron"),
			Up: readRoute(in.values, "up_"), Down: readRoute(in.values, "down_"),
		}
	}
	scheduleHint := "按服务器本地时区执行。可填 5 个字段，或在最前面加秒字段。支持 CRON_TZ=Asia/Shanghai。"
	if current.ID > 0 && cfg.Cron == "" && current.Interval > 0 {
		scheduleHint += " 现有任务按 " + strconv.FormatInt(current.Interval, 10) + " 秒运行，填写 Cron 后切换。"
	}
	schedule := input("cron", "Cron 表达式", cfg.Cron, scheduleHint)
	schedule.Placeholder = "*/5 * * * *"
	form.add("定时调度", "例如 */5 * * * * 每 5 分钟执行，0 9 * * * 每天 09:00 执行。", append(basic, schedule)...)
	form.add("数据来源", "首次成功同步只保存基线。更换接口地址会重新建立基线。",

		input("url", "模型列表 API", cfg.URL, "兼容 OpenAI 模型列表格式"), secret("token", "Bearer Token（可选）", cfg.Token),
		checkbox("clear_token", "清除已保存的 Token", false, ""),
	)
	if len(cfg.Up.Channels)+len(cfg.Down.Channels) > 0 {
		form.add("旧版上线通知", "可在告警策略中配置更多接收方式。", routeFields("up_", cfg.Up, channels, "模型模板变量")...)
		form.add("旧版下线通知", "可在告警策略中配置更多接收方式。", routeFields("down_", cfg.Down, channels, "模型模板变量")...)
	}
	if in.post {
		if cfg.Cron != "" || current.Interval == 0 {
			if _, err := config.NextCronRun(cfg.Cron, time.Now()); err != nil {
				return cfg, err
			}
		}
		if err = config.ValidateURL(cfg.URL, cfg.Token != ""); err == nil && strings.ContainsAny(cfg.Token, "\r\n") {
			err = errors.New("Token 不能换行")
		}
		if err == nil {
			sample := config.SampleTemplateData()
			if _, _, err = cfg.Up.Render(sample); err == nil {
				sample.Event = "model_down"
				_, _, err = cfg.Down.Render(sample)
			}
		}
	}
	return cfg, err
}

func readRoute(values url.Values, prefix string) config.Route {
	var ids []int64
	for _, raw := range values[prefix+"channels"] {
		if id := number(raw); id > 0 && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	message := config.MessageTemplate{Title: strings.TrimSpace(values.Get(prefix + "title")), Body: values.Get(prefix + "body")}
	if message.Title == "" && message.Body == "" && len(ids) == 0 {
		switch prefix {
		case "up_":
			message = config.DefaultModelMonitor().Up.MessageTemplate
		case "down_":
			message = config.DefaultModelMonitor().Down.MessageTemplate
		default:
			message = config.DefaultMailTemplate()
		}
	}
	return config.Route{
		MessageTemplate: message,
		Channels:        ids,
	}
}

func routeFields(prefix string, routing config.Route, channels []store.Resource, label string) []field {
	hint := "可用 {{.Models}}、{{.Count}}、{{.Time}}、{{.Source}}。用 {{range .Models}}{{.}}{{end}} 遍历模型。"
	if prefix == "" {
		hint = `可用 {{.Subject}}、{{.From}}、{{.ReceivedAt}}、{{.Body}}、{{.Rule}}。用 {{index .Fields "字段名"}} 引用提取结果。`
	}
	choices := field{Key: prefix + "channels", Label: "接收渠道", Type: "choices", Wide: true}
	for _, ch := range channels {
		name := ch.Name
		if !ch.Enabled {
			name += "（停用）"
		}
		choices.Options = append(choices.Options, option{strconv.FormatInt(ch.ID, 10), name, slices.Contains(routing.Channels, ch.ID)})
	}
	title := input(prefix+"title", "通知标题", routing.Title, "")
	title.Wide = true
	return []field{choices, title, textArea(prefix+"body", "通知正文", routing.Body, label+"。"+hint)}
}
