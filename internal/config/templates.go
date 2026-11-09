package config

import (
	"bytes"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"strings"
	"text/template"
	"time"
)

type MessageTemplate struct {
	Title, Body string
}

type TemplateData struct {
	Subject, From, ReceivedAt, Body, Rule string
	Fields                                map[string]string
	Models                                []string
	MailModels                            []MailModel
	Count                                 int
	Event, Time, Source                   string
	Customer, Channel, Model, OfflineAt   string
}

func parseMessageTemplate(raw string) (*template.Template, error) {
	return template.New("message").Option("missingkey=error").Funcs(template.FuncMap{"join": joinModels}).Parse(raw)
}

func renderText(raw string, data any, limit int) (string, error) {
	t, err := parseMessageTemplate(raw)
	if err != nil {
		return "", err
	}
	return executeMessage(t, data, limit)
}

func parseHTMLMessage(raw string) (*htmltemplate.Template, error) {
	return htmltemplate.New("message").Option("missingkey=error").Funcs(htmltemplate.FuncMap{"join": joinModels}).Parse(raw)
}

func joinModels(values any, sep string) (string, error) {
	switch list := values.(type) {
	case []string:
		return strings.Join(list, sep), nil
	case []MailModel:
		names := make([]string, len(list))
		for i, row := range list {
			names[i] = row.ModelName
		}
		return strings.Join(names, sep), nil
	default:
		return "", errors.New("join 需要列表")
	}
}

func executeMessage(t interface{ Execute(io.Writer, any) error }, data any, limit int) (string, error) {
	buf := &limitedBuffer{max: limit}
	if err := t.Execute(buf, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.buffer.String()), nil
}

type limitedBuffer struct {
	buffer bytes.Buffer
	max    int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > b.max {
		return 0, errors.New("渲染结果超过长度限制")
	}
	return b.buffer.Write(p)
}

func (t MessageTemplate) Render(data TemplateData) (string, string, error) {
	return t.render(data, false)
}

func (t MessageTemplate) render(data TemplateData, html bool) (string, string, error) {
	var values any = data
	if data.MailModels != nil {
		values = struct {
			TemplateData
			Models []MailModel
		}{data, data.MailModels}
	}
	title, err := renderText(t.Title, values, 512)
	if err != nil {
		return "", "", fmt.Errorf("通知标题：%w", err)
	}
	var body string
	if html {
		var compiled *htmltemplate.Template
		compiled, err = parseHTMLMessage(t.Body)
		if err == nil {
			body, err = executeMessage(compiled, values, 128<<10)
		}
	} else {
		body, err = renderText(t.Body, values, 128<<10)
	}
	if err != nil {
		return "", "", fmt.Errorf("通知正文：%w", err)
	}
	title = strings.Join(strings.Fields(title), " ")
	if title == "" || body == "" {
		return "", "", errors.New("通知标题和正文渲染后不能为空")
	}
	return title, body, nil
}

func SampleTemplateData() TemplateData {
	return TemplateData{
		Subject: "示例告警", From: "alerts@example.com", ReceivedAt: Stamp(time.Now().Unix()), Body: "示例正文",
		Rule: "示例规则", Fields: map[string]string{}, Models: []string{"provider/example-model"},
		Count: 1, Event: "model_up", Time: Stamp(time.Now().Unix()), Source: "示例来源",
	}
}

func Stamp(value int64) string {
	if value <= 0 {
		return "—"
	}
	return time.Unix(value, 0).Local().Format("2006-01-02 15:04:05")
}
