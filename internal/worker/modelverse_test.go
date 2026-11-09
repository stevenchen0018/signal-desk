package worker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestModelJobPersistsBaselineAndRoutesChanges(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Advance the persisted retry time without sleeping during the test.
	db, err := sql.Open("sqlite", filepath.Join(dir, "signal-desk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, cfg := range []config.Channel{
		{Type: "webhook", Address: "https://notify.test/up/private-hook", Secret: "test-bearer", Customer: "A客户"},
		{Type: "feishu", Address: "https://notify.test/down", Secret: "test-sign", Customer: "B客户"},
	} {
		if err := s.Save(ctx, "channels", 0, 0, store.ResourceInput{Name: cfg.Type, Enabled: true, Config: cfg}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultModelMonitor()
	channels, err := s.List(ctx, "channels")
	if err != nil || len(channels) != 2 {
		t.Fatalf("list channels: %v", err)
	}
	upID := channels[0].ID
	templates, err := s.List(ctx, "templates")
	if err != nil || len(templates) != 3 {
		t.Fatalf("list notification templates: %v", err)
	}
	for i, trigger := range []string{config.ModelUp, config.ModelDown} {
		template := templates[i]
		html := config.NotificationTemplate{Category: trigger, Format: config.BodyHTML, MessageTemplate: config.MessageTemplate{
			Title: "模型变更",
			Body:  `<p>{{.Customer}}</p><table><tr><th>模型</th><th>数量</th></tr>{{range .Models}}<tr><td>{{.}}</td><td>{{$.Count}}</td></tr>{{end}}</table>`,
		}}
		if err := s.Save(ctx, "templates", template.ID, template.Revision, store.ResourceInput{Name: template.Name, Enabled: true, Config: html}); err != nil {
			t.Fatal(err)
		}
		policy := config.Policy{Trigger: trigger, ChannelID: channels[i].ID, TemplateID: templates[i].ID}
		if trigger == config.ModelUp {
			policy.ModelPattern = "^new$"
		}
		if err := s.Save(ctx, "policies", 0, 0, store.ResourceInput{Name: trigger, Enabled: true, Config: policy}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Save(ctx, "models", 0, 0, store.ResourceInput{Name: "ModelVerse", Enabled: true, Interval: 300, Config: cfg}); err != nil {
		t.Fatal(err)
	}

	modelJobs, err := s.List(ctx, "models")
	if err != nil || len(modelJobs) != 1 {
		t.Fatalf("list model jobs: %v", err)
	}
	modelID := modelJobs[0].ID

	api := `{"object":"list","data":[{"id":"keep","pricing":{"input":1}},{"id":"old"}]}`
	requests := make(map[string]string)
	failWebhook := true
	apiStatus := http.StatusOK
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, body := apiStatus, api
		if r.Method == http.MethodPost {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			requests[r.URL.Path] = string(b)
			switch r.URL.Path {
			case "/up/private-hook":
				if !strings.Contains(string(b), `"event_type":"model_up"`) {
					t.Error("missing webhook event type")
				}
				if r.Header.Get("Idempotency-Key") == "" || r.Header.Get("Authorization") != "Bearer test-bearer" {
					t.Error("missing webhook idempotency key or bearer token")
				}
				if failWebhook {
					status = http.StatusServiceUnavailable
				}
				body = ""
			case "/down":
				var data struct{ Timestamp, Sign string }
				if err := json.Unmarshal(b, &data); err != nil {
					t.Error(err)
				}
				h := hmac.New(sha256.New, []byte(data.Timestamp+"\ntest-sign"))
				if data.Timestamp == "" || data.Sign != base64.StdEncoding.EncodeToString(h.Sum(nil)) {
					t.Error("invalid Feishu signature")
				}
				body = `{"code":0}`
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	w := New(s, client)
	run := func() {
		t.Helper()
		if err := s.RequestJob(ctx, modelID); err != nil {
			t.Fatal(err)
		}
		work, err := w.claimJob(ctx)
		if err != nil {
			t.Fatal(err)
		}
		work()
	}
	count := func(table string, want int) {
		t.Helper()
		var n int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s count: %d, %v (want %d)", table, n, err, want)
		}
	}
	run()
	count("events", 0)
	r, err := s.Get(ctx, "models", modelID)
	if err != nil || !strings.Contains(r.State, "pricing") {
		t.Fatalf("initial snapshot: %s, %v", r.State, err)
	}
	api = `{"data":[{"id":"new"},{"id":"unsubscribed"},{"id":"keep","pricing":{"input":2}}]}`
	run()
	count("events", 2)
	count("deliveries", 2)
	api = `{"data":[{"pricing":{"input":2},"id":"keep"},{"id":"new"},{"id":"unsubscribed"}]}`
	run()
	count("deliveries", 2)
	api = `{"data":[{"id":"keep","pricing":{"input":3}},{"id":"new"},{"id":"unsubscribed"}]}`
	run()
	count("deliveries", 2)
	before, err := s.Get(ctx, "models", modelID)
	if err != nil {
		t.Fatal(err)
	}
	api = `{"data":null}`
	run()
	after, err := s.Get(ctx, "models", modelID)
	if err != nil || after.State != before.State {
		t.Fatal("malformed response changed the snapshot")
	}

	apiStatus = http.StatusServiceUnavailable
	if _, err := db.Exec("UPDATE resources SET next_run=0 WHERE kind='models'"); err != nil {
		t.Fatal(err)
	}
	scheduled, err := w.claimJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scheduled()
	apiStatus = http.StatusOK
	logs, more, err := s.ModelRequests(ctx, 1)
	if err != nil || more {
		t.Fatalf("load request history: %v", err)
	}
	var statuses []string
	for _, r := range logs {
		statuses = append(statuses, r.Status)
		if r.FinishedAt < r.StartedAt {
			t.Fatalf("unfinished request log: %#v", r)
		}
	}
	want := []string{"failed", "failed", "metadata", "unchanged", "changed", "baseline"}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("request statuses = %v, want %v", statuses, want)
	}
	if logs[0].Trigger != "scheduled" || logs[0].HTTPStatus != http.StatusServiceUnavailable ||
		logs[1].HTTPStatus != http.StatusOK || logs[1].ModelCount != nil ||
		logs[4].Trigger != "manual" || !reflect.DeepEqual(logs[4].Added, []string{"new", "unsubscribed"}) ||
		!reflect.DeepEqual(logs[4].Removed, []string{"old"}) || logs[5].ModelCount == nil || *logs[5].ModelCount != 2 {
		t.Fatalf("unexpected request details: %#v", logs)
	}

	for i := 0; i < 2; i++ {
		work, err := w.claimDelivery(ctx)
		if err != nil {
			t.Fatal(err)
		}
		work()
	}
	var webhook struct {
		Body   string
		Format string `json:"body_format"`
	}
	if err := json.Unmarshal([]byte(requests["/up/private-hook"]), &webhook); err != nil ||
		webhook.Format != config.BodyHTML || !strings.Contains(webhook.Body, "<td>new</td>") ||
		!strings.Contains(webhook.Body, "A客户") || strings.Contains(webhook.Body, "unsubscribed") {
		t.Fatalf("webhook HTML was not preserved: %#v (%v)", webhook, err)
	}
	if !strings.Contains(requests["/down"], `"msg_type":"interactive"`) ||
		!strings.Contains(requests["/down"], "| old | 1 |") || !strings.Contains(requests["/down"], "B客户") {
		t.Fatalf("Feishu did not receive a card table: %s", requests["/down"])
	}
	interfaceLogs, _, err := s.InterfaceLogs(ctx, "outbound", 1<<62)
	if err != nil || len(interfaceLogs) < 8 {
		t.Fatalf("outbound operations were not logged: %v", err)
	}
	for _, entry := range interfaceLogs {
		if strings.Contains(entry.Target+entry.Note, "private-hook") || strings.Contains(entry.Target+entry.Note, "test-bearer") {
			t.Fatal("external log contains private endpoint or credentials")
		}
	}

	var status string
	if err := db.QueryRow("SELECT status FROM deliveries WHERE channel_id=?", upID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("failed delivery was not retained for retry: %s, %v", status, err)
	}
	if _, err := w.claimDelivery(ctx); !errors.Is(err, store.ErrNoWork) {
		t.Fatalf("retry backoff ignored: %v", err)
	}
	failWebhook = false
	setWebhook := func(address string) {
		t.Helper()
		channel, err := s.Get(ctx, "channels", upID)
		if err != nil {
			t.Fatal(err)
		}
		cfg := config.Channel{Type: "webhook", Address: address, Secret: "test-bearer"}
		if err = s.Save(ctx, "channels", channel.ID, channel.Revision, store.ResourceInput{Name: channel.Name, Enabled: true, Config: cfg}); err != nil {
			t.Fatal(err)
		}
	}
	setWebhook("https://notify.test/replacement")
	if _, err := db.Exec("UPDATE deliveries SET next_try=0 WHERE status='pending'"); err != nil {
		t.Fatal(err)
	}
	work, err := w.claimDelivery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	work()
	var note string
	if err := db.QueryRow("SELECT status,error FROM deliveries WHERE channel_id=?", upID).Scan(&status, &note); err != nil || status != "pending" || !strings.Contains(note, "原地址") {
		t.Fatalf("queued notice was not pinned to its destination: %s %s %v", status, note, err)
	}
	setWebhook("https://notify.test/up/private-hook")
	if _, err := db.Exec("UPDATE deliveries SET next_try=0 WHERE status='pending'"); err != nil {
		t.Fatal(err)
	}
	work, err = w.claimDelivery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	work()
	if err := db.QueryRow("SELECT status FROM deliveries WHERE channel_id=?", upID).Scan(&status); err != nil || status != "sent" {
		t.Fatalf("retry failed: %s, %v", status, err)
	}
}
