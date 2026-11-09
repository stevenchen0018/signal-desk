package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

const temporarySMTP = "smtp"

type notificationTestPage struct {
	Selection, Sample []field
	HasTemplates      bool
	HasChannels       bool
	Preview           *store.Notification
	Destination       string
}

func (a *adminUI) notificationTest(c *gin.Context) {
	ctx := c.Request.Context()
	data := basePage("notification-test", c)
	form := &notificationTestPage{}
	data.NotificationTest = form
	post := c.Request.Method == http.MethodPost
	in := formRequest{values: c.Request.URL.Query()}
	if post {
		in.values = c.Request.PostForm
	}
	templateID, channelID := in.text("template_id"), in.text("channel_id")
	templates, err := a.store.List(ctx, "templates")
	if err != nil {
		a.internal(c, err)
		return
	}
	channels, err := a.store.List(ctx, "channels")
	if err != nil {
		a.internal(c, err)
		return
	}
	smtp, err := a.store.List(ctx, "smtp")
	if err != nil {
		a.internal(c, err)
		return
	}
	hasSMTP := len(smtp) > 0 && a.smtpTest != nil

	templateOptions := []option{{Label: "请选择已启用的模板"}}
	channelOptions := []option{{Label: "请选择接收渠道"}}
	var selectedTemplate, selectedChannel store.Resource
	var target config.Channel
	for _, item := range templates {
		if !item.Enabled {
			continue
		}
		id := strconv.FormatInt(item.ID, 10)
		templateOptions = append(templateOptions, option{Value: id, Label: item.Name})
		if id == templateID {
			selectedTemplate = item
		}
	}
	for _, item := range channels {
		if !item.Enabled {
			continue
		}
		var cfg config.Channel
		if err := a.store.Decode(item, &cfg); err != nil {
			a.internal(c, err)
			return
		}
		id := strconv.FormatInt(item.ID, 10)
		label := item.Name + " · " + channelTypeLabel(cfg.Type)
		if cfg.Customer != "" {
			label = cfg.Customer + " · " + label
		}
		channelOptions = append(channelOptions, option{Value: id, Label: label})
		if id == channelID {
			selectedChannel, target = item, cfg
		}
	}
	if hasSMTP {
		channelOptions = append(channelOptions, option{Value: temporarySMTP, Label: "邮件 · 使用 SMTP 发送到临时地址"})
	}
	form.HasTemplates, form.HasChannels = len(templateOptions) > 1, len(channelOptions) > 1
	recipient := input("test_to", "临时测试邮箱", in.text("test_to"), "填写一个邮箱。测试多个地址时，可选择已配置多收件人的邮件渠道。")
	recipient.For = temporarySMTP
	form.Selection = []field{
		selectField("template_id", "通知模板", templateID, templateOptions...),
		selectField("channel_id", "接收渠道", channelID, channelOptions...),
		recipient,
	}
	form.Sample = []field{
		selectField("event", "示例事件", in.text("event"), option{Label: "按模板类别自动选择"},
			option{Value: config.MailMatched, Label: "邮件规则命中"}, option{Value: config.ModelUp, Label: "模型上线"},
			option{Value: config.ModelDown, Label: "模型下线"}),
		input("subject", "示例邮件主题", in.text("subject"), "留空使用默认主题。"),
		textArea("models", "示例模型 ID", in.text("models"), "每行一个模型 ID。模拟邮件时也可在提取字段中填写 models，粘贴表格文本。"),
		textArea("fields", "示例提取字段（JSON）", in.text("fields"),
			"用于模拟邮件正则提取结果，例如 {\"model\":\"provider/example-model\",\"offline_at\":\"2026-12-31 18:00\"}"),
	}
	if !post {
		a.render(c, http.StatusOK, data)
		return
	}

	invalid := func(msg string) {
		data.Error = msg
		a.render(c, http.StatusUnprocessableEntity, data)
	}
	action := in.text("action")
	direct := channelID == temporarySMTP && hasSMTP
	if selectedTemplate.ID == 0 || (selectedChannel.ID == 0 && !direct) {
		invalid("请选择已启用的模板和可用的接收渠道")
		return
	}
	if action != "preview" && action != "send" {
		invalid("请选择预览内容或发送测试")
		return
	}
	var tmpl config.NotificationTemplate
	if err := a.store.Decode(selectedTemplate, &tmpl); err != nil {
		a.internal(c, err)
		return
	}
	sample, err := notificationTestSample(in, tmpl.Category)
	if err != nil {
		invalid(err.Error())
		return
	}
	sample.Customer, sample.Channel = target.Customer, selectedChannel.Name
	form.Destination = selectedChannel.Name
	switch {
	case direct:
		sample.Customer, sample.Channel = "测试收件人", "SMTP 测试"
		form.Destination = "临时测试邮箱（未填写）"
		if recipient.Value != "" || action == "send" {
			addr, err := config.ParseAddress(recipient.Value)
			if err != nil || len(recipient.Value) > 320 {
				invalid("请输入一个有效的临时测试邮箱")
				return
			}
			form.Destination = addr.Address
		}
	case target.Type == "email":
		addresses, err := config.ParseRecipients(target.Address)
		if err != nil {
			invalid(err.Error())
			return
		}
		var destinations []string
		for _, address := range addresses {
			destinations = append(destinations, address.Address)
		}
		form.Destination += fmt.Sprintf(" · %d 个收件人\n%s", len(addresses), strings.Join(destinations, "\n"))
	default:
		form.Destination += " · " + channelTypeLabel(target.Type)
	}
	if sample.Customer == "" {
		sample.Customer = sample.Channel
	}

	title, body, err := tmpl.Render(sample)
	if err != nil {
		invalid(err.Error())
		return
	}
	format := tmpl.Format
	if format == "" {
		format = config.BodyText
	}
	message := store.Notification{Title: title, Body: body, BodyFormat: format}
	if action == "preview" {
		form.Preview = &message
		a.render(c, http.StatusOK, data)
		return
	}

	var traceID int64
	if direct {
		traceID, err = a.smtpTest(ctx, recipient.Value, message)
	} else {
		traceID, err = a.store.QueueTemplateTest(ctx, selectedChannel, selectedTemplate.Name, message)
	}
	if errors.Is(err, store.ErrTestChannelChanged) {
		invalid(err.Error())
		return
	}
	if err != nil {
		a.internal(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/traces?id=%d", traceID))
}

func notificationTestSample(in formRequest, category string) (config.TemplateData, error) {
	sample := config.SampleTemplateData()
	sample.Source = "手动测试"
	sample.Event = in.text("event")
	if sample.Event == "" {
		switch category {
		case config.ModelUp, config.ModelDown:
			sample.Event = category
		default:
			sample.Event = config.MailMatched
		}
	}
	if subject := in.text("subject"); subject != "" {
		sample.Subject = subject
	}
	models := strings.Fields(in.text("models"))
	if len(models) > 0 {
		sample.Models = models
	}
	switch sample.Event {
	case config.MailMatched:
		fields := map[string]string{}
		if raw := in.text("fields"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields == nil {
				return sample, errors.New("示例提取字段须为 JSON 对象，字段值须为字符串")
			}
		}
		if _, ok := fields["models"]; !ok && len(models) > 0 {
			fields["models"] = strings.Join(models, "\n")
		}
		if _, ok := fields["models"]; !ok && fields["model"] == "" && fields["模型"] == "" && fields["模型ID"] == "" {
			fields["model"] = sample.Models[0]
		}
		sample.SetMailFields(fields)
	case config.ModelUp, config.ModelDown:
		sample.Count = len(sample.Models)
		if sample.Count == 1 {
			sample.Model = sample.Models[0]
		}
	default:
		return sample, errors.New("请选择有效的示例事件")
	}
	return sample, nil
}
