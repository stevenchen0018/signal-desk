package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"signal-desk/internal/store"
)

func TestAdminFormsAndSessionRotation(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.BootstrapAdmin(ctx, "admin", "first-test-password"); err != nil {
		t.Fatal(err)
	}
	var smtpRecipient string
	handler := New(s, false, func(ctx context.Context, recipient string, _ store.Notification) (int64, error) {
		smtpRecipient = recipient
		items, err := s.List(ctx, "smtp")
		if err != nil {
			return 0, err
		}
		id, err := s.StartSMTPTrace(ctx, items[0])
		if err != nil {
			return 0, err
		}
		return id, s.FinishTrace(ctx, id, "failed", "MAIL FROM · SMTP 550",
			[]store.TraceStep{{Stage: "MAIL FROM", Status: "failed", Message: "发件地址未授权"}})
	})
	var cookie *http.Cookie
	var csrf string
	call := func(method, path string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		if form != nil && csrf != "" {
			form.Set("csrf", csrf)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %.1400s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}

	idFor := func(kind string) string {
		t.Helper()
		items, err := s.List(ctx, kind)
		if err != nil || len(items) != 1 {
			t.Fatalf("list %s: %v", kind, err)
		}
		return strconv.FormatInt(items[0].ID, 10)
	}

	call("GET", "/", nil, http.StatusSeeOther)
	call("GET", "/login", nil, http.StatusOK)
	call("HEAD", "/login", nil, http.StatusOK)
	call("GET", "/logout", nil, http.StatusMethodNotAllowed)
	call("GET", "/does-not-exist", nil, http.StatusNotFound)
	if response := call("GET", "/assets/style.css", nil, http.StatusOK); !strings.Contains(response.Body.String(), ":root") {
		t.Fatal("embedded CSS did not load")
	}
	crossSite := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin"))
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, crossSite)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST got %d, want 403", blocked.Code)
	}
	call("POST", "/login", url.Values{"username": {"admin"}, "password": {"wrong"}}, http.StatusUnauthorized)
	login := call("POST", "/login", url.Values{"username": {"admin"}, "password": {"first-test-password"}}, http.StatusSeeOther)
	cookie = login.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected cookie options: %v", cookie)
	}
	call("POST", "/logout", url.Values{}, http.StatusForbidden)
	csrf = csrfToken(cookie.Value)

	for _, page := range []string{"/", "/channels?new", "/mailboxes?new", "/rules?new", "/models", "/smtp", "/history", "/account"} {
		call("GET", page, nil, http.StatusOK)
	}
	call("POST", "/channels", url.Values{"id": {"0"}, "action": {"save"}, "name": {"测试渠道"}, "enabled": {"1"},
		"type": {"webhook"}, "endpoint": {"https://example.com/private-hook"}, "secret": {"secret-token"}}, http.StatusSeeOther)
	channelID := idFor("channels")
	if body := call("GET", "/channels?id="+channelID, nil, http.StatusOK).Body.String(); strings.Contains(body, "secret-token") || strings.Contains(body, "private-hook") {
		t.Fatal("saved credentials were echoed into the edit page")
	}
	mailbox := url.Values{"id": {"0"}, "action": {"save"}, "name": {"测试收件箱"},
		"host": {"imap.example.com"}, "port": {"993"}, "tls": {"tls"}, "user": {"a@example.com"}, "password": {"mail-secret"},
		"folder": {"INBOX"}, "cron": {"invalid"}}
	call("POST", "/mailboxes", mailbox, http.StatusUnprocessableEntity)
	mailbox.Set("cron", "CRON_TZ=Asia/Shanghai 0 9 * * *")
	call("POST", "/mailboxes", mailbox, http.StatusSeeOther)
	if body := call("GET", "/mailboxes", nil, http.StatusOK).Body.String(); !strings.Contains(body, mailbox.Get("cron")) {
		t.Fatal("mailbox list did not show its cron schedule")
	}
	rule := url.Values{"id": {"0"}, "action": {"preview"}, "name": {"工单通知"}, "enabled": {"1"}, "mailbox": {idFor("mailboxes")},
		"subject": {"^工单"}, "extract": {`工单号*=编号:\s*([A-Z0-9-]+)`}, "channels": {channelID},
		"title": {"{{.Subject}}"}, "body": {`工单 {{index .Fields "工单号"}}`},
		"sample_subject": {"工单测试"}, "sample_body": {"编号: OPS-42"}}
	if body := call("POST", "/rules", rule, http.StatusOK).Body.String(); !strings.Contains(body, "工单 OPS-42") {
		t.Fatal("preview did not render the extracted field")
	}
	rule.Set("action", "save")
	call("POST", "/rules", rule, http.StatusSeeOther)
	edit := url.Values{"action": {"save"}, "id": {channelID}, "revision": {"1"}, "name": {"更新渠道"}, "enabled": {"1"}, "type": {"webhook"}}
	call("POST", "/channels", edit, http.StatusSeeOther)
	edit.Set("name", "过期编辑")
	if response := call("POST", "/channels", edit, http.StatusUnprocessableEntity); !strings.Contains(response.Body.String(), "配置已被其他页面更新") ||
		!strings.Contains(response.Body.String(), `name="revision" value="1"`) {
		t.Fatal("a stale edit must still require a refresh")
	}
	call("POST", "/channels", url.Values{"action": {"delete"}, "id": {channelID}}, http.StatusUnprocessableEntity)
	call("POST", "/channels", url.Values{"action": {"test"}, "id": {channelID}}, http.StatusSeeOther)
	if body := call("GET", "/history?tab=deliveries", nil, http.StatusOK).Body.String(); !strings.Contains(body, "待发送") {
		t.Fatal("test delivery was not shown in the queue")
	}

	model := url.Values{"id": {"0"}, "action": {"save"}, "name": {"ModelVerse"},
		"url": {"https://api.modelverse.cn/v1/models"}, "cron": {"CRON_TZ=Asia/Shanghai"},
		"up_title": {"上线"}, "up_body": {"{{.Count}}"}, "down_title": {"下线"}, "down_body": {"{{.Count}}"}}
	if body := call("POST", "/models", model, http.StatusUnprocessableEntity).Body.String(); !strings.Contains(body, "Cron 需填写") {
		t.Fatal("invalid cron did not produce a validation error")
	}
	model.Set("cron", "CRON_TZ=Asia/Shanghai 0 9 * * *")
	call("POST", "/models", model, http.StatusSeeOther)
	if body := call("GET", "/models?tab=settings", nil, http.StatusOK).Body.String(); !strings.Contains(body, model.Get("cron")) {
		t.Fatal("saved cron did not round trip to the settings form")
	}
	call("POST", "/models", url.Values{"id": {idFor("models")}, "action": {"run"}}, http.StatusSeeOther)
	if body := call("GET", "/models", nil, http.StatusOK).Body.String(); !strings.Contains(body, "data-model-query-pending") {
		t.Fatal("manual query was not queued")
	}
	call("GET", "/models?tab=logs", nil, http.StatusOK)

	presets, err := s.List(ctx, "templates")
	if err != nil || len(presets) != 3 {
		t.Fatalf("missing default templates: %v", err)
	}
	call("POST", "/policies", url.Values{"id": {"0"}, "action": {"save"}, "name": {"给客户通知"}, "enabled": {"1"},
		"trigger": {"mail"}, "rule_id": {idFor("rules")}, "template_id": {strconv.FormatInt(presets[2].ID, 10)},
		"channel_id": {channelID}}, http.StatusSeeOther)
	if body := call("GET", "/policies", nil, http.StatusOK).Body.String(); !strings.Contains(body, "模型提前下线 → 更新渠道") {
		t.Fatal("policy list did not show template and destination")
	}
	call("POST", "/templates", url.Values{"id": {strconv.FormatInt(presets[2].ID, 10)}, "action": {"delete"}}, http.StatusUnprocessableEntity)
	response := call("GET", "/models?secret=LOG_SECRET_TEST", nil, http.StatusOK)
	ref := response.Header().Get("X-Request-ID")
	if body := call("GET", "/interface-logs?direction=inbound", nil, http.StatusOK).Body.String(); ref == "" ||
		!strings.Contains(body, ref) || strings.Contains(body, "LOG_SECRET_TEST") {
		t.Fatal("admin request was not logged safely")
	}

	call("POST", "/smtp", url.Values{"action": {"save"}, "id": {"0"}, "host": {"smtp.example.test"},
		"port": {"465"}, "tls": {"tls"}, "from": {"sender@example.test"}}, http.StatusSeeOther)
	if body := call("GET", "/smtp", nil, http.StatusOK).Body.String(); !strings.Contains(body, "检测发件地址") || !strings.Contains(body, "发送测试邮件") {
		t.Fatal("missing SMTP test controls")
	}
	call("POST", "/smtp/test", url.Values{"mode": {"send"}}, http.StatusUnprocessableEntity)
	probe := call("POST", "/smtp/test", url.Values{"mode": {"probe"}, "test_to": {"ignored@example.test"}}, http.StatusSeeOther)
	if smtpRecipient != "" {
		t.Fatal("SMTP probe passed a recipient")
	}
	if body := call("GET", probe.Header().Get("Location"), nil, http.StatusOK).Body.String(); !strings.Contains(body, "SMTP 550") || !strings.Contains(body, "发件地址未授权") {
		t.Fatal("missing SMTP test result")
	}
	call("POST", "/smtp/test", url.Values{"mode": {"send"}, "test_to": {"test@example.test"}}, http.StatusSeeOther)
	if smtpRecipient != "test@example.test" {
		t.Fatal("test send did not use explicit recipient")
	}

	call("POST", "/account", url.Values{"old": {"first-test-password"}, "password": {"second-test-password"}, "confirm": {"second-test-password"}}, http.StatusSeeOther)
	call("GET", "/", nil, http.StatusSeeOther)
	call("POST", "/login", url.Values{"username": {"admin"}, "password": {"first-test-password"}}, http.StatusUnauthorized)
	call("POST", "/login", url.Values{"username": {"admin"}, "password": {"second-test-password"}}, http.StatusSeeOther)
}
