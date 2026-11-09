package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"signal-desk/internal/config"
)

func TestCronManualRunKeepsScheduledTime(t *testing.T) {
	const expression = "CRON_TZ=Asia/Shanghai 0 9 * * *"
	t.Run("models", func(t *testing.T) {
		cfg := config.DefaultModelMonitor()
		cfg.Cron = expression
		checkCronManualRun(t, "models", cfg)
	})
	t.Run("mailboxes", func(t *testing.T) {
		checkCronManualRun(t, "mailboxes", config.Mailbox{Cron: expression})
	})
}

func checkCronManualRun(t *testing.T, kind string, cfg any) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Save(ctx, kind, 0, 0, ResourceInput{Name: "Cron", Enabled: true, Interval: 60, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	items, err := s.List(ctx, kind)
	if err != nil || len(items) != 1 {
		t.Fatalf("list saved job: %v", err)
	}
	saved := items[0]
	if saved.Interval != 0 || saved.Next <= time.Now().Unix() {
		t.Fatalf("expected a future cron occurrence: %#v, %v", saved, err)
	}
	if _, err := s.ClaimJob(ctx); !errors.Is(err, ErrNoWork) {
		t.Fatalf("cron job ran before its scheduled time: %v", err)
	}
	if err := s.RequestJob(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishJob(ctx, claimed, JobOutcome{Success: true}); err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(ctx, kind, saved.ID)
	if err != nil || current.Next != saved.Next || current.ManualQueryPending() {
		t.Fatalf("manual job changed scheduled time: %#v, %v", current, err)
	}
	after := time.Date(2026, 11, 8, 1, 0, 3, 0, time.UTC)
	claimed.Next = after.Unix() - 3
	want := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC)
	if got := s.nextJobRun(claimed, JobOutcome{}, after); got != want.Unix() {
		t.Fatalf("next cron time = %v, want %v", time.Unix(got, 0), want)
	}
	if next, err := config.NextCronRun("*/15 * * * * *", after); err != nil || next.Second() != 15 {
		t.Fatalf("seconds expression: %v, %v", next, err)
	}
}
