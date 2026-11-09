package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

type pipeListener struct {
	conn   net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	if l.conn != nil {
		conn := l.conn
		l.conn = nil
		return conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *pipeListener) Addr() net.Addr { return nil }

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func TestIMAPSubjectFilterAndIncrementalBodyExtraction(t *testing.T) {
	ctx := context.Background()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("user", "test-password")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(user)
	server := imapserver.New(&imapserver.Options{
		InsecureAuth: true, NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
	})
	conn, peer := net.Pipe()
	defer conn.Close()
	ln := &pipeListener{conn: peer, closed: make(chan struct{})}
	defer ln.Close()
	go server.Serve(ln)
	defer server.Close()
	c := imapclient.New(conn, nil)
	defer c.Close()
	if err := c.Login("user", "test-password").Wait(); err != nil {
		t.Fatal(err)
	}

	appendMail := func(subject, body string) {
		t.Helper()
		raw := fmt.Sprintf("From: Operations <ops@example.com>\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n%s\r\n",
			mime.QEncoding.Encode("UTF-8", subject), body)
		cmd := c.Append("INBOX", int64(len(raw)), nil)
		if _, err := io.WriteString(cmd, raw); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, channel := range []config.Channel{
		{Type: "email", Address: "a@example.test"},
		{Type: "feishu", Address: "https://fixture.invalid/b"},
	} {
		if err := db.Save(ctx, "channels", 0, 0, store.ResourceInput{Name: channel.Type, Enabled: true, Config: channel}); err != nil {
			t.Fatal(err)
		}
	}
	channels, err := db.List(ctx, "channels")
	if err != nil || len(channels) != 2 {
		t.Fatalf("read fixture channels: %v", err)
	}

	cfg := config.Mailbox{Folder: "INBOX"}
	rule, err := config.CompileRule("告警", config.Rule{Subject: "^告警", Extract: `model*=Ticket:\s*([A-Z0-9-]+)
offline_at=Offline:\s*([^\n]+)`})
	if err != nil {
		t.Fatal(err)
	}
	rule.ID = 7
	plan := []store.PlannedNotification{
		{ID: 101, Destination: channels[0].Source, Customer: "A客户", Channel: "邮箱", Template: config.DefaultNotificationTemplate(config.ModelEarlyDown),
			Policy: config.Policy{Trigger: config.MailMatched, RuleID: rule.ID, ChannelID: channels[0].ID}},
		{ID: 102, Destination: channels[1].Source, Customer: "B客户", Channel: "飞书", Template: config.DefaultNotificationTemplate(config.ModelDown),
			Policy: config.Policy{Trigger: config.MailMatched, RuleID: rule.ID, ChannelID: channels[1].ID}},
		{ID: 103, Customer: "A客户", Channel: "邮箱", Template: config.DefaultNotificationTemplate(config.ModelUp),
			Policy: config.Policy{Trigger: config.ModelUp, ChannelID: channels[0].ID}},
	}
	appendMail("告警历史", "<p>Ticket: OPS-1</p>")
	baseline, err := scanMailbox(ctx, c, cfg, "", []config.CompiledRule{rule}, plan, nil)
	if err != nil || len(baseline.events) != 0 {
		t.Fatalf("mail baseline: %#v %v", baseline, err)
	}
	appendMail("其他主题", "<p>Ticket: OPS-2</p>")
	appendMail("告警新邮件", "<style>Ticket: WRONG</style><p>Ticket: <b>OPS-42</b></p><p>Offline: 11-10 09:00</p>")
	state, err := json.Marshal(baseline.state)
	if err != nil {
		t.Fatal(err)
	}
	trace := &jobTrace{}
	next, err := scanMailbox(ctx, c, cfg, string(state), []config.CompiledRule{rule}, plan, trace)
	if err != nil || len(next.events) != 1 {
		t.Fatalf("mail scan: %#v %v", next, err)
	}
	event := next.events[0]
	if event.Kind != "mail" || len(event.Notifications) != 2 {
		t.Fatalf("unexpected parsed event: %#v, %v", event, event.Notifications)
	}
	a, b := event.Notifications[0], event.Notifications[1]
	if a.PolicyID != 101 || a.Channels[0] != channels[0].ID || !strings.Contains(a.Title, "提前下线") ||
		!strings.Contains(a.Body, "A客户") || !strings.Contains(a.Body, "OPS-42") || !strings.Contains(a.Body, "11-10 09:00") {
		t.Fatalf("incorrect email alert: %#v", a)
	}
	if b.PolicyID != 102 || b.Channels[0] != channels[1].ID || !strings.Contains(b.Title, "完成下线") || !strings.Contains(b.Body, "B客户") {
		t.Fatalf("incorrect Feishu alert: %#v", b)
	}
	if err := db.Save(ctx, "mailboxes", 0, 0, store.ResourceInput{Name: "fixture", Enabled: true, Interval: 60, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	job, err := db.ClaimJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	traceID, err := db.StartJobTrace(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	outcome := next.outcome(nil, false)
	outcome.TraceID, outcome.Steps = traceID, trace.steps
	if err := db.FinishJob(ctx, job, outcome); err != nil {
		t.Fatal(err)
	}
	failed, err := db.ClaimDelivery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishDelivery(ctx, failed, store.DeliveryAttempt{Status: "pending", Note: "MAIL FROM · SMTP 450",
		Next: time.Now().Unix() + 60, Steps: []store.TraceStep{{Stage: "MAIL FROM", Status: "failed"}}}); err != nil {
		t.Fatal(err)
	}
	if err := db.RetryDelivery(ctx, failed.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		d, err := db.ClaimDelivery(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.FinishDelivery(ctx, d, store.DeliveryAttempt{Status: "sent"}); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := db.TraceSteps(ctx, traceID)
	if err != nil {
		t.Fatal(err)
	}
	stages := make(map[string]int)
	for _, step := range logs {
		stages[step.Stage]++
	}
	if stages["主题正则"] != 2 || stages["正文正则提取"] != 1 || stages["通知入队"] != 2 {
		t.Fatalf("incomplete trace stages: %v", stages)
	}
	events, err := db.TraceEvents(ctx, traceID)
	if err != nil || len(events) != 1 || !strings.Contains(events[0].Detail, "OPS-42") || len(events[0].Deliveries) != 2 {
		t.Fatalf("missing extracted fields or deliveries: %#v %v", events, err)
	}
	retried := events[0].Deliveries[0]
	if retried.Status != "sent" || len(retried.History) != 2 || retried.History[0].Note != "MAIL FROM · SMTP 450" ||
		len(retried.History[0].Steps) != 1 || retried.History[1].Status != "sent" {
		t.Fatalf("missing retry history: %#v", retried)
	}

	state, err = json.Marshal(next.state)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := scanMailbox(ctx, c, cfg, string(state), []config.CompiledRule{rule}, plan, nil)
	if err != nil || len(repeat.events) != 0 {
		t.Fatalf("duplicate notification: %#v %v", repeat, err)
	}
	messages, err := c.Fetch(imap.UIDSetNum(3), &imap.FetchOptions{Flags: true}).Collect()
	if err != nil || len(messages) != 1 {
		t.Fatalf("read flags: %v", err)
	}
	for _, flag := range messages[0].Flags {
		if flag == imap.FlagSeen {
			t.Fatal("polling marked the message as read")
		}
	}
	if err := c.Logout().Wait(); err != nil {
		t.Fatal(err)
	}
}
