package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/store"
)

func basePage(view string, c *gin.Context) page {
	for _, s := range navigation {
		if s.Kind == view {
			return page{View: view, Title: s.Label, Description: s.Description, CSRF: csrfToken(c.GetString(sessionKey)), Notice: notice(c.Query("ok"))}
		}
	}
	return page{}
}

func notice(key string) string {
	return map[string]string{
		"logs_cleared":          "已清除所选类别中可清理的历史日志",
		"workflow_logs_cleared": "已清除已结束的日志（含投递失败）。执行中或有待投递通知的链路仍会保留。",
		"model_queued":          "查询已加入队列，完成后将显示最新结果",
		"saved":                 "配置已保存", "deleted": "已删除", "queued": "任务已加入队列，可在运行记录查看结果",
		"test": "测试通知已加入队列，可在通知投递中查看结果", "retry": "通知已重新加入队列",
		"password": "密码已更新，请使用新密码重新登录",
	}[key]
}

func redirect(c *gin.Context, path, key string) {
	u, _ := url.Parse(path)
	query := u.Query()
	query.Set("ok", key)
	u.RawQuery = query.Encode()
	c.Redirect(http.StatusSeeOther, u.String())
}

func (a *adminUI) dashboard(c *gin.Context) {
	data := basePage("home", c)
	var err error
	data.Counts, err = a.store.Summary(c.Request.Context())
	if err == nil {
		data.Records, err = a.store.History(c.Request.Context(), "runs")
	}
	if err != nil {
		a.internal(c, err)
		return
	}
	if len(data.Records) > 7 {
		data.Records = data.Records[:7]
	}
	data.HistoryTab = "runs"
	a.render(c, http.StatusOK, data)
}

func (a *adminUI) history(c *gin.Context) {
	data := basePage("history", c)
	data.HistoryTab = c.Query("tab")
	if data.HistoryTab != "runs" && data.HistoryTab != "deliveries" {
		data.HistoryTab = "events"
	}
	var err error
	data.Records, err = a.store.History(c.Request.Context(), data.HistoryTab)
	if err != nil {
		a.internal(c, err)
		return
	}
	a.render(c, http.StatusOK, data)
}

func (a *adminUI) retry(c *gin.Context) {
	id := number(c.PostForm("id"))
	err := a.store.RetryDelivery(c.Request.Context(), id)
	if errors.Is(err, store.ErrDeliveryNotFailed) {
		c.String(http.StatusConflict, "%s", err)
		return
	}
	if err != nil {
		a.internal(c, err)
		return
	}
	if traceID := number(c.PostForm("trace_id")); traceID > 0 {
		redirect(c, fmt.Sprintf("/traces?id=%d", traceID), "retry")
		return
	}
	redirect(c, "/history?tab=deliveries", "retry")
}

func (a *adminUI) account(c *gin.Context) {
	data := basePage("account", c)
	var err error
	data.Username, err = a.store.AdminName(c.Request.Context())
	if err != nil {
		a.internal(c, err)
		return
	}
	if c.Request.Method != http.MethodPost {
		a.render(c, http.StatusOK, data)
		return
	}
	if c.PostForm("password") != c.PostForm("confirm") {
		data.Error = "两次输入的新密码不一致"
	} else if err := a.store.ChangePassword(c.Request.Context(), c.GetString(sessionKey), c.PostForm("old"), c.PostForm("password")); err != nil {
		data.Error = err.Error()
	} else {
		a.cookie(c, "", -1)
		redirect(c, "/login", "password")
		return
	}
	a.render(c, http.StatusUnprocessableEntity, data)
}

func (a *adminUI) logout(c *gin.Context) {
	if err := a.store.DeleteSession(c.Request.Context(), c.GetString(sessionKey)); err != nil {
		a.internal(c, err)
		return
	}
	a.cookie(c, "", -1)
	c.Redirect(http.StatusSeeOther, "/login")
}
