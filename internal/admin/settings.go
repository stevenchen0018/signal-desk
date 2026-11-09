package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

func (a *adminUI) settings(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		r := c.Request
		data := basePage(kind, c)
		items, err := a.store.List(r.Context(), kind)
		if err != nil {
			a.internal(c, err)
			return
		}
		var selected store.Resource
		rawID := r.URL.Query().Get("id")
		if r.Method == http.MethodPost {
			rawID = r.PostForm.Get("id")
		}
		id, err := strconv.ParseInt(rawID, 10, 64)
		if (rawID != "" && err != nil) || id < 0 {
			c.String(http.StatusNotFound, "404 page not found")
			return
		}
		var names map[int64]string
		if kind == "policies" && len(items) > 0 {
			choices, err := a.loadPolicyChoices(r.Context())
			if err != nil {
				a.internal(c, err)
				return
			}
			names = choices.names
		}
		for _, item := range items {
			row, err := a.describe(item, names)
			if err != nil {
				a.internal(c, err)
				return
			}
			data.Rows = append(data.Rows, row)
			if item.ID == id || kind == "smtp" || kind == "models" {
				selected = item
			}
		}
		if id > 0 && selected.ID != id {
			c.String(http.StatusNotFound, "404 page not found")
			return
		}
		if kind == "models" {
			data.ModelTab = modelTab(c.Query("tab"))
			if r.Method != http.MethodPost && data.ModelTab != "settings" {
				if err := a.loadModelPage(c, &data, selected); err != nil {
					a.internal(c, err)
					return
				}
				a.render(c, http.StatusOK, data)
				return
			}
		}
		if r.Method == http.MethodPost {
			action := r.PostForm.Get("action")
			if action != "save" && action != "preview" {
				key, err := a.resourceAction(r.Context(), kind, selected, action)
				if err != nil {
					data.Error = err.Error()
					a.render(c, http.StatusUnprocessableEntity, data)
					return
				}
				path := "/" + kind
				if kind == "models" && key == "queued" {
					key = "model_queued"
				}
				if key == "test" {
					path = "/history?tab=deliveries"
				}
				redirect(c, path, key)
				return
			}
		}
		showForm := rawID != "" || r.URL.Query().Has("new") || kind == "smtp" || kind == "models" || r.Method == http.MethodPost
		if showForm {
			if kind == "models" {
				data.ModelTab = "settings"
			}
			status, saved := a.processSettings(c, kind, selected, &data)
			if saved {
				return
			}
			a.render(c, status, data)
			return
		}
		a.render(c, http.StatusOK, data)
	}
}

func (a *adminUI) describe(item store.Resource, names map[int64]string) (resourceRow, error) {
	out := resourceRow{Resource: item}
	switch item.Kind {
	case "mailboxes":
		var c config.Mailbox
		if err := a.store.Decode(item, &c); err != nil {
			return out, err
		}
		out.Subtitle = c.User + " · " + c.Host + " · " + c.Folder
		out.Cron = c.Cron
	case "rules":
		var c config.Rule
		if err := a.store.Decode(item, &c); err != nil {
			return out, err
		}
		out.Subtitle = c.Subject
	case "channels":
		var c config.Channel
		if err := a.store.Decode(item, &c); err != nil {
			return out, err
		}
		out.Subtitle = map[string]string{"email": "邮件 · " + c.Address, "feishu": "飞书群机器人", "webhook": "通用 Webhook"}[c.Type]
		if c.Customer != "" {
			out.Subtitle = c.Customer + " · " + out.Subtitle
		}
	case "templates":
		var cfg config.NotificationTemplate
		if err := a.store.Decode(item, &cfg); err != nil {
			return out, err
		}
		out.Subtitle = config.TemplateCategoryLabel(cfg.Category)
	case "policies":
		var cfg config.Policy
		if err := a.store.Decode(item, &cfg); err != nil {
			return out, err
		}
		out.Subtitle = triggerLabel(cfg.Trigger)
		if cfg.RuleID > 0 {
			out.Subtitle += " · " + names[cfg.RuleID]
		}
		if cfg.ModelPattern != "" {
			out.Subtitle += " · 模型 " + cfg.ModelPattern
		}
		out.Subtitle += " → " + names[cfg.TemplateID] + " → " + names[cfg.ChannelID]
	}
	return out, nil
}

func (a *adminUI) resourceAction(ctx context.Context, kind string, item store.Resource, action string) (string, error) {
	if item.ID == 0 {
		return "", errors.New("请先保存配置")
	}
	switch action {
	case "run":
		if kind != "mailboxes" && kind != "models" {
			break
		}
		return "queued", a.store.RequestJob(ctx, item.ID)
	case "test":
		if kind != "channels" {
			break
		}
		return "test", a.store.QueueTestDelivery(ctx, item.ID)
	case "delete":
		if kind != "mailboxes" && kind != "rules" && kind != "channels" && kind != "templates" && kind != "policies" {
			break
		}
		return "deleted", a.store.Delete(ctx, kind, item.ID)
	}
	return "", errors.New("不支持此操作")
}

func number(value string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return n
}
