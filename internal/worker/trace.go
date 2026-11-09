package worker

import (
	"fmt"
	"time"

	"signal-desk/internal/store"
)

const maxTraceSteps = 600

type jobTrace struct {
	ID    int64
	steps []store.TraceStep
}

func (t *jobTrace) record(stage, status, message string) {
	if t == nil || len(t.steps) > maxTraceSteps {
		return
	}
	if len(t.steps) == maxTraceSteps {
		stage, status, message = "日志", "skipped", "阶段记录达到上限，可继续查看下方提取结果和投递明细"
	}
	t.steps = append(t.steps, store.TraceStep{At: time.Now().Unix(), Stage: stage, Status: status, Message: message})
}

func (t *jobTrace) ref(fallback string) string {
	if t != nil && t.ID > 0 {
		return fmt.Sprintf("链路 #%d", t.ID)
	}
	return fallback
}
