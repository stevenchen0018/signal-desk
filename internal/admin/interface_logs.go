package admin

import (
	"crypto/rand"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/store"
)

func (a *adminUI) accessLog(c *gin.Context) {
	path := c.FullPath()
	if path == "/assets/style.css" || path == "/assets/app.js" {
		c.Next()
		return
	}
	if path == "" {
		path = "未匹配路由"
	}
	id := rand.Text()
	c.Header("X-Request-ID", id)
	start := time.Now()
	c.Next()
	status := c.Writer.Status()
	note := ""
	if status >= 400 {
		note = http.StatusText(status)
	}
	a.store.RecordInterface(store.InterfaceLog{Direction: "inbound", Operation: c.Request.Method, Target: path, Reference: id,
		Status: status, Success: status < 400, DurationMS: time.Since(start).Milliseconds(), Note: note})
}

func (a *adminUI) interfaceLogs(c *gin.Context) {
	data := basePage("interface-logs", c)
	switch c.Query("direction") {
	case "inbound":
		data.InterfaceDirection = "inbound"
	case "outbound":
		data.InterfaceDirection = "outbound"
	}
	before := number(c.Query("before"))
	if before <= 0 {
		before = math.MaxInt64
	}
	var err error
	data.InterfaceLogs, data.InterfaceNext, err = a.store.InterfaceLogs(c.Request.Context(), data.InterfaceDirection, before)
	if err != nil {
		a.internal(c, err)
		return
	}
	a.render(c, http.StatusOK, data)
}
