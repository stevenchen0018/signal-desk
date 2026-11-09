package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

type smtpTestFunc func(context.Context, string, store.Notification) (int64, error)

func (a *adminUI) testSMTP(c *gin.Context) {
	if a.smtpTest == nil {
		c.String(http.StatusServiceUnavailable, "SMTP 测试服务未启用")
		return
	}
	recipient := ""
	switch c.PostForm("mode") {
	case "probe":
	case "send":
		recipient = strings.TrimSpace(c.PostForm("test_to"))
		if _, err := config.ParseAddress(recipient); err != nil || len(recipient) > 320 {
			c.String(http.StatusUnprocessableEntity, "请输入有效的测试收件邮箱")
			return
		}
	default:
		c.String(http.StatusBadRequest, "未知的 SMTP 测试方式")
		return
	}
	id, err := a.smtpTest(c.Request.Context(), recipient, store.Notification{})
	if err != nil {
		a.internal(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/traces?id=%d", id))
}

func (a *adminUI) traces(c *gin.Context) {
	data := basePage("traces", c)
	ctx := c.Request.Context()
	if id := number(c.Query("id")); id > 0 {
		trace, err := a.store.Trace(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			c.String(http.StatusNotFound, "未找到该链路记录")
			return
		}
		if err != nil {
			a.internal(c, err)
			return
		}
		data.Trace = &trace
		data.TraceSteps, err = a.store.TraceSteps(ctx, id)
		if err == nil {
			data.TraceEvents, err = a.store.TraceEvents(ctx, id)
		}
		if err != nil {
			a.internal(c, err)
			return
		}
	} else {
		switch kind := c.Query("kind"); kind {
		case "mailboxes", "models", "smtp", "notification_test":
			data.TraceKind = kind
		}
		before := number(c.Query("before"))
		if before <= 0 {
			before = math.MaxInt64
		}
		data.TraceResource = number(c.Query("resource"))
		var err error
		data.Traces, data.TraceNext, err = a.store.Traces(ctx, data.TraceKind, data.TraceResource, before)
		if err != nil {
			a.internal(c, err)
			return
		}
	}
	a.render(c, http.StatusOK, data)
}
