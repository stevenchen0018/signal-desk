package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

func (a *adminUI) processSettings(c *gin.Context, kind string, current store.Resource, data *page) (int, bool) {
	r := c.Request
	ctx := r.Context()
	var channels, mailboxes []store.Resource
	var err error
	if kind == "rules" || kind == "models" {
		channels, err = a.store.List(ctx, "channels")
	}
	if err == nil && kind == "rules" {
		mailboxes, err = a.store.List(ctx, "mailboxes")
	}
	if err != nil {
		a.internal(c, err)
		return 0, true
	}

	form, values, validation := a.settingsForm(ctx, formRequest{post: r.Method == http.MethodPost, values: r.PostForm}, kind, current, channels, mailboxes)
	data.Form = &form
	if r.Method != http.MethodPost {
		if validation != nil {
			a.internal(c, validation)
			return 0, true
		}
		return http.StatusOK, false
	}
	if validation != nil {
		data.Error = validation.Error()
		return http.StatusUnprocessableEntity, false
	}

	if r.PostForm.Get("action") == "preview" {
		cfg, ok := values.Config.(config.Rule)
		if !ok {
			return http.StatusBadRequest, false
		}
		rule, err := config.CompileRule(values.Name, cfg)
		sample := config.SampleTemplateData()
		sample.Subject = r.PostForm.Get("sample_subject")
		sample.Body = strings.ReplaceAll(r.PostForm.Get("sample_body"), "\r\n", "\n")
		if err != nil {
			data.Error = err.Error()
		} else if !rule.Matches(sample.Subject) {
			form.Preview = "样例主题未命中规则"
		} else {
			rendered, detail := rule.Apply(sample, 0)
			fields, _ := json.MarshalIndent(detail, "", "  ")
			form.Preview = string(fields)
			if detail.Error == "" {
				title, body, err := cfg.Render(rendered)
				if err != nil {
					data.Error = err.Error()
				} else {
					form.Preview += "\n\n" + title + "\n" + body
				}
			}
		}
		return http.StatusOK, false
	}

	if err = a.store.Save(ctx, kind, current.ID, form.Revision, values); err != nil {
		data.Error = err.Error()
		return http.StatusUnprocessableEntity, false
	}
	path := "/" + kind
	if kind == "models" {
		path += "?tab=settings"
	}
	redirect(c, path, "saved")
	return 0, true
}

func (a *adminUI) settingsForm(ctx context.Context, in formRequest, kind string, current store.Resource, channels, mailboxes []store.Resource) (settingsForm, store.ResourceInput, error) {
	form := settingsForm{ID: current.ID, Revision: current.Revision, Title: "新建配置"}
	v := store.ResourceInput{Name: current.Name, Enabled: current.Enabled, Interval: current.Interval}
	if in.post {
		form.Revision = number(in.text("revision"))
	}
	if current.ID == 0 {
		v.Enabled = true
	}
	if kind == "models" {
		form.Title = "模型监控配置"
		if current.ID == 0 {
			v.Name, v.Enabled = "ModelVerse", false
		}
	} else if kind == "smtp" {
		form.Title = "SMTP 配置"
		v.Name, v.Enabled = "SMTP", true
	} else if current.ID > 0 {
		form.Title = "编辑配置"
	}
	if kind == "mailboxes" && current.ID == 0 {
		v.Interval = 60
	}
	if in.post {
		if kind != "smtp" {
			v.Name, v.Enabled = in.text("name"), in.text("enabled") == "1"
		}
		if kind == "mailboxes" {
			v.Interval = number(in.text("interval"))
		}
	}
	var basic []field
	if kind != "smtp" {
		basic = append(basic, input("name", "配置名称", v.Name, ""), checkbox("enabled", "启用", v.Enabled, ""))
	}
	if kind == "mailboxes" {
		basic = append(basic, intervalField(v.Interval))
	}

	var err error
	switch kind {
	case "mailboxes":
		var cfg config.Mailbox
		cfg, err = a.mailboxForm(&form, in, current, basic)
		v.Config = cfg
		if in.post && err == nil && cfg.Cron == "" && (v.Interval < 10 || v.Interval > 86400) {
			err = errors.New("未填写 Cron 时，轮询间隔需为 10–86400 秒")
		}
	case "rules":
		v.Config, err = a.ruleForm(&form, in, current, basic, channels, mailboxes, v.Name)
	case "models":
		v.Config, err = a.modelForm(&form, in, current, basic, channels)
	case "channels":
		v.Config, err = a.channelForm(&form, in, current, basic)
	case "templates":
		v.Config, err = a.notificationTemplateForm(&form, in, current, basic)
	case "policies":
		v.Config, err = a.policyForm(ctx, &form, in, current, basic)
	case "smtp":
		v.Config, err = a.smtpForm(&form, in, current)
	}
	if in.post && err == nil {
		if v.Name == "" || len([]rune(v.Name)) > 80 {
			err = errors.New("配置名称需为 1–80 个字符")
		}
	}
	return form, v, err
}

func (a *adminUI) decodeSaved(current store.Resource, target any) error {
	if current.ID == 0 {
		return nil
	}
	return a.store.Decode(current, target)
}
