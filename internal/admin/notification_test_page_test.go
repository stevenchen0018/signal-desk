package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

func TestManualNotificationTemplateDelivery(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.BootstrapAdmin(ctx, "admin", "local-test-password"); err != nil {
		t.Fatal(err)
	}
	session, err := s.NewSession(ctx, "admin", "local-test-password")
	if err != nil {
		t.Fatal(err)
	}
	save := func(kind, name string, cfg any) store.Resource {
		t.Helper()
		if err := s.Save(ctx, kind, 0, 0, store.ResourceInput{Name: name, Enabled: true, Config: cfg}); err != nil {
			t.Fatal(err)
		}
		items, err := s.List(ctx, kind)
		if err != nil {
			t.Fatal(err)
		}
		return items[len(items)-1]
	}
	tmpl := save("templates", "HTML 表格通知", config.NotificationTemplate{
		Category: config.ModelEarlyDown, Format: config.BodyHTML,
		MessageTemplate: config.MessageTemplate{
			Title: "{{.Customer}} 模型通知",
			Body:  "<table><tr><td>{{.Model}}</td><td>{{.OfflineAt}}</td></tr></table>",
		},
	})
	smtp := save("smtp", "SMTP", config.SMTP{Host: "smtp.example.test", Port: "465", TLS: "tls", From: "sender@example.test"})
	var smtpRecipient string
	var smtpMessage store.Notification
	handler := New(s, false, func(ctx context.Context, recipient string, message store.Notification) (int64, error) {
		smtpRecipient, smtpMessage = recipient, message
		return s.StartSMTPTrace(ctx, smtp)
	})

	form := url.Values{
		"csrf": {csrfToken(session)}, "template_id": {strconv.FormatInt(tmpl.ID, 10)},
		"fields": {"{\"model\":\"sample/model\",\"offline_at\":\"18:00\"}"},
	}
	call := func(action string, want int) *httptest.ResponseRecorder {
		t.Helper()
		form.Set("action", action)
		r := httptest.NewRequest(http.MethodPost, "/notification-test", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: got HTTP %d, want %d: %.2000s", action, w.Code, want, w.Body.String())
		}
		return w
	}
	expectNoDelivery := func() {
		t.Helper()
		if d, err := s.ClaimDelivery(ctx); !errors.Is(err, store.ErrNoWork) {
			t.Fatalf("unexpected queued delivery: %#v %v", d, err)
		}
	}

	for _, tc := range []struct {
		channel config.Channel
		indexes []int
	}{
		{config.Channel{Type: "email", Customer: "客户", Address: "first@example.test\r\nsecond@example.test，FIRST@example.test"}, []int{1, 2}},
		{config.Channel{Type: "feishu", Customer: "客户", Address: "https://example.test/private-feishu"}, []int{0}},
		{config.Channel{Type: "webhook", Customer: "客户", Address: "https://example.test/private-webhook"}, []int{0}},
	} {
		t.Run(tc.channel.Type, func(t *testing.T) {
			channel := save("channels", tc.channel.Type, tc.channel)
			form.Set("channel_id", strconv.FormatInt(channel.ID, 10))
			preview := call("preview", http.StatusOK).Body.String()
			for _, part := range []string{"客户 模型通知", "&lt;table&gt;", "sample/model", "18:00"} {
				if !strings.Contains(preview, part) {
					t.Fatalf("preview did not render %q", part)
				}
			}
			if strings.Contains(preview, "/private-") || strings.Contains(preview, "<table><tr><td>sample/model") {
				t.Fatal("preview exposes an endpoint or runs template markup")
			}
			if tc.channel.Type == "email" && (!strings.Contains(preview, "2 个收件人") || !strings.Contains(preview, "second@example.test")) {
				t.Fatal("preview did not display deduplicated recipient list")
			}
			expectNoDelivery()
			location := call("send", http.StatusSeeOther).Header().Get("Location")
			traceID := number(strings.TrimPrefix(location, "/traces?id="))
			events, err := s.TraceEvents(ctx, traceID)
			if err != nil || len(events) != 1 || len(events[0].Deliveries) != len(tc.indexes) {
				t.Fatalf("wrong number of traced deliveries: %#v, %v", events, err)
			}
			if tc.channel.Type == "email" {
				first, second := events[0].Deliveries[0], events[0].Deliveries[1]
				if first.RecipientEmail != "first@example.test" || second.RecipientEmail != "second@example.test" {
					t.Fatalf("trace did not save each recipient: %q, %q", first.RecipientEmail, second.RecipientEmail)
				}
			}
			for i, index := range tc.indexes {
				d, err := s.ClaimDelivery(ctx)
				if err != nil || d.ChannelID != channel.ID || d.RecipientIndex != index || d.EventType != "notification_test" ||
					d.BodyFormat != config.BodyHTML || !strings.Contains(d.Body, "<td>sample/model</td><td>18:00</td>") {
					t.Fatalf("wrong notification: %#v, %v", d, err)
				}
				status := "sent"
				if tc.channel.Type == "email" && i == 0 {
					status = "failed"
				}
				if err := s.FinishDelivery(ctx, d, store.DeliveryAttempt{Status: status}); err != nil {
					t.Fatal(err)
				}
			}
			expectNoDelivery()
			if tc.channel.Type == "email" {
				failed := events[0].Deliveries[0]
				if err := s.RetryDelivery(ctx, failed.ID); err != nil {
					t.Fatal(err)
				}
				if retried, err := s.ClaimDelivery(ctx); err != nil || retried.ID != failed.ID || retried.RecipientIndex != 1 {
					t.Fatalf("retry did not isolate failed recipient: %#v, %v", retried, err)
				}
				expectNoDelivery()
			}
		})
	}
	form.Set("channel_id", temporarySMTP)
	form.Set("test_to", "temporary@example.test")
	call("preview", http.StatusOK)
	if smtpRecipient != "" {
		t.Fatal("preview sent an SMTP message")
	}
	call("send", http.StatusSeeOther)
	if smtpRecipient != "temporary@example.test" || smtpMessage.BodyFormat != config.BodyHTML || !strings.Contains(smtpMessage.Body, "sample/model") {
		t.Fatalf("unexpected temporary SMTP test: %q %#v", smtpRecipient, smtpMessage)
	}
}
