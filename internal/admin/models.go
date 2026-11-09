package admin

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/store"
)

func modelTab(tab string) string {
	if tab == "logs" || tab == "settings" {
		return tab
	}
	return "catalog"
}

func (a *adminUI) loadModelPage(c *gin.Context, data *page, selected store.Resource) error {
	ctx := c.Request.Context()
	if data.ModelTab == "logs" {
		pageNumber := min(100_000_000, max(1, number(c.Query("page"))))
		logs, more, err := a.store.ModelRequests(ctx, pageNumber)
		if err != nil {
			return err
		}
		data.ModelRequests = logs
		if pageNumber > 1 {
			data.PreviousLogPage = pageNumber - 1
		}
		if more {
			data.NextLogPage = pageNumber + 1
		}
		return nil
	}
	if selected.State != "" {
		var snapshot store.ModelSnapshot
		if err := json.Unmarshal([]byte(selected.State), &snapshot); err != nil {
			return err
		}
		data.Models = &snapshot
	}
	if c.Query("ok") == "model_queued" && !selected.ManualQueryPending() {
		data.Notice = "查询已结束，结果见最近请求"
	}
	var err error
	data.LatestModelRequest, err = a.store.LatestModelRequest(ctx)
	return err
}
