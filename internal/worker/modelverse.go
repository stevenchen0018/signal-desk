package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

func (w *Worker) pollModels(ctx context.Context, r store.Resource, trace *jobTrace) (result jobResult, runErr error) {
	var cfg config.ModelMonitor
	if err := w.store.Decode(r, &cfg); err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return result, errors.New("模型接口地址无效")
	}
	req.Header.Set("Accept", "application/json")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	plan, err := w.store.NotificationPlan(ctx)
	if err != nil {
		return result, err
	}
	if err := w.store.StartModelRequest(ctx, r); err != nil {
		return result, err
	}
	log := &store.ModelRequestResult{Status: "failed"}
	started := time.Now()
	trace.record("模型接口请求", "ok", "请求 "+req.URL.Host)
	result.modelRequest = log
	defer func() {
		log.Note = result.note
		if runErr != nil {
			log.Note = runErr.Error()
		}
		if runErr != nil {
			trace.record("模型接口结果", "failed", log.Note)
		} else {
			trace.record("模型接口结果", "ok", log.Note)
		}
		w.externalLog("模型列表", req.Method, req.URL.Host, trace.ref(fmt.Sprintf("模型任务 #%d", r.ID)), log.HTTPStatus, started, runErr)
	}()

	raw, status, err := w.fetchModelList(req)
	log.HTTPStatus = status
	if err != nil {
		return result, err
	}
	models, err := parseModels(raw)
	if err != nil {
		return result, err
	}
	count := len(models)
	log.ModelCount = &count
	now := time.Now().Unix()
	snapshot := store.ModelSnapshot{Models: models, UpdatedAt: now, Snapshot: raw}
	if r.State == "" {
		result.state = snapshot
		result.note = fmt.Sprintf("已建立基线，当前 %d 个模型", count)
		log.Status = "baseline"
		return result, nil
	}

	var previous store.ModelSnapshot
	if err = json.Unmarshal([]byte(r.State), &previous); err != nil {
		return result, errors.New("无法读取上一份模型快照")
	}
	up, down := diffModels(previous.Models, models)
	log.Added, log.Removed = up, down
	result.note = fmt.Sprintf("当前 %d 个模型，上线 %d 个，下线 %d 个", count, len(up), len(down))
	switch {
	case len(up)+len(down) > 0:
		log.Status = "changed"
	default:
		changed, err := modelMetadataChanged(previous.Snapshot, raw)
		if err != nil {
			return result, err
		}
		log.Status = "unchanged"
		if changed {
			log.Status = "metadata"
			result.note += "，模型元数据已更新"
		}
	}
	result.state = snapshot
	if len(up) > 0 {
		result.events = append(result.events, modelEvent(r.Name, "model_up", "上线", up, cfg.Up, now, plan, trace))
	}
	if len(down) > 0 {
		result.events = append(result.events, modelEvent(r.Name, "model_down", "下线", down, cfg.Down, now, plan, trace))
	}
	return result, nil
}

func (w *Worker) fetchModelList(req *http.Request) ([]byte, int, error) {
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, 0, requestError("模型接口", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("模型接口返回 HTTP %d", resp.StatusCode)
	}
	raw, err := readBounded(resp.Body, 16<<20)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取模型列表失败：%w", err)
	}
	return raw, resp.StatusCode, nil
}

func parseModels(raw []byte) ([]store.Model, error) {
	var payload struct {
		Data  []store.Model   `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Data == nil ||
		(len(payload.Error) > 0 && string(payload.Error) != "null") {
		return nil, errors.New("模型响应格式无效，需包含 data 数组")
	}
	seen := make(map[string]bool, len(payload.Data))
	for _, m := range payload.Data {
		if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.ID) != m.ID || len(m.ID) > 512 || seen[m.ID] {
			return nil, errors.New("模型列表包含空白、重复或无效的模型 ID")
		}
		seen[m.ID] = true
	}
	slices.SortFunc(payload.Data, func(a, b store.Model) int { return strings.Compare(a.ID, b.ID) })
	return payload.Data, nil
}

func diffModels(previous, current []store.Model) (up, down []string) {
	old := make(map[string]bool, len(previous))
	for _, m := range previous {
		old[m.ID] = true
	}
	seen := make(map[string]bool, len(current))
	for _, m := range current {
		seen[m.ID] = true
		if !old[m.ID] {
			up = append(up, m.ID)
		}
	}
	for _, m := range previous {
		if !seen[m.ID] {
			down = append(down, m.ID)
		}
	}
	return up, down
}

func modelMetadataChanged(previous, current []byte) (bool, error) {
	if bytes.Equal(previous, current) {
		return false, nil
	}
	if len(previous) == 0 || bytes.Equal(previous, []byte("null")) {
		return true, nil
	}
	old, err := modelObjects(previous)
	if err != nil {
		return false, errors.New("无法解析上一份模型快照的元数据")
	}
	next, err := modelObjects(current)
	if err != nil {
		return false, errors.New("无法解析模型元数据")
	}
	return !reflect.DeepEqual(old, next), nil
}

func modelObjects(raw []byte) (map[string]map[string]any, error) {
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	objects := make(map[string]map[string]any, len(payload.Data))
	for _, m := range payload.Data {
		id, _ := m["id"].(string)
		objects[id] = m
	}
	return objects, nil
}

func modelEvent(source, kind, action string, ids []string, routing config.Route, now int64, plan []store.PlannedNotification, trace *jobTrace) event {
	e := event{Event: store.Event{Kind: kind, Title: fmt.Sprintf("%d 个模型%s", len(ids), action), Detail: ids}}
	data := config.TemplateData{Models: ids, Count: len(ids), Event: kind, Source: source, Time: config.Stamp(now)}
	if len(ids) == 1 {
		data.Model = ids[0]
	}
	e.prepare(routing, data)
	e.applyPolicies(plan, kind, 0, data, trace)
	return e
}
