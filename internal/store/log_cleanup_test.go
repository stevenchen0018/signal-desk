package store

import (
	"context"
	"errors"
	"testing"
)

func TestClearWorkflowLogsKeepsRunnableDeliveriesAndActiveTrace(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.db.Exec(`
		INSERT INTO job_traces(id,resource_id,kind,source,trigger,started_at,status) VALUES
			(10,1,'models','Fixture','manual',1,'ok'), (20,1,'models','Fixture','manual',2,'ok'),
			(30,1,'models','Fixture','manual',3,'running'), (40,1,'models','Fixture','manual',4,'ok');
		INSERT INTO trace_steps(trace_id,ordinal,at,stage,status,message) VALUES
			(10,0,1,'test','ok',''), (20,0,2,'test','ok','');
		INSERT INTO events(id,at,kind,source,title,detail) VALUES
			(10,1,'mail','fixture','sent only','{}'), (20,2,'mail','fixture','retryable','{}'),
			(30,3,'mail','fixture','active','{}'), (40,4,'mail','fixture','pending','{}');
		INSERT INTO trace_events(trace_id,event_id) VALUES(10,10),(20,20),(30,30),(40,40);
		INSERT INTO deliveries(id,event_id,channel_id,channel_name,channel_source,title,body,updated,status) VALUES
			(10,10,1,'test','a','sent','old',1,'sent'),
			(20,20,1,'test','a','failed','retryable',2,'failed'),
			(40,40,1,'test','a','pending','pending',4,'pending'),
			(41,40,2,'test','b','failed','failed alongside pending',4,'failed');
		INSERT INTO delivery_attempts(delivery_id,at,status,note,next_try,duration_ms) VALUES
			(10,1,'sent','',0,1),(20,2,'failed','timeout',0,1);
		INSERT INTO runs(resource_id,name,kind,at,success,message) VALUES(1,'test','models',1,1,'done');
		INSERT INTO model_requests(run_token,started_at,trigger,status) VALUES('r1',1,'manual','changed'),('r2',2,'scheduled','running');
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClearLogs(ctx, "workflow"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearLogs(ctx, "models"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		sql string
		n   int
	}{
		{"SELECT count(*) FROM job_traces WHERE id IN (30,40)", 2},
		{"SELECT count(*) FROM job_traces", 2},
		{"SELECT count(*) FROM trace_steps", 0},
		{"SELECT count(*) FROM events WHERE id IN (30,40)", 2},
		{"SELECT count(*) FROM events", 2},
		{"SELECT count(*) FROM deliveries WHERE id=40", 1},
		{"SELECT count(*) FROM deliveries", 1},
		{"SELECT count(*) FROM delivery_attempts", 0},
		{"SELECT count(*) FROM runs", 0},
		{"SELECT count(*) FROM model_requests WHERE status='running'", 1},
		{"SELECT count(*) FROM model_requests", 1},
	} {
		var got int
		if err := s.db.QueryRow(want.sql).Scan(&got); err != nil || got != want.n {
			t.Fatalf("%s = %d, want %d (%v)", want.sql, got, want.n, err)
		}
	}
	if err := s.RetryDelivery(ctx, 20); !errors.Is(err, ErrDeliveryNotFailed) {
		t.Fatalf("cleared failure still retryable: %v", err)
	}
}
