package worker

import (
	"strings"
	"time"

	"signal-desk/internal/store"
)

func (w *Worker) externalLog(operation, method, host, reference string, status int, start time.Time, err error) {
	note := ""
	if err != nil {
		note = err.Error()
	}
	w.store.RecordInterface(store.InterfaceLog{Direction: "outbound", Operation: strings.TrimSpace(operation + " " + method),
		Target: host, Reference: reference, Status: status, Success: err == nil, DurationMS: time.Since(start).Milliseconds(), Note: note})
}
