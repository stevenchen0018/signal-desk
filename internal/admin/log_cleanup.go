package admin

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/store"
)

func (a *adminUI) clearLogs(c *gin.Context) {
	scope := c.PostForm("scope")
	path := "/history"
	switch scope {
	case "interfaces":
		path = "/interface-logs"
	case "models":
		path = "/models?tab=logs"
	case "workflow":
		if c.PostForm("return") == "traces" {
			path = "/traces"
		} else if tab := c.PostForm("tab"); tab == "runs" || tab == "deliveries" {
			path += "?tab=" + tab
		}
	}
	err := a.store.ClearLogs(c.Request.Context(), scope)
	if errors.Is(err, store.ErrLogScope) {
		c.String(http.StatusBadRequest, "%s", err)
		return
	}
	if err != nil {
		a.internal(c, err)
		return
	}
	if scope == "workflow" {
		redirect(c, path, "workflow_logs_cleared")
		return
	}
	redirect(c, path, "logs_cleared")
}
