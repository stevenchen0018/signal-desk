package store

import (
	"context"
	"testing"

	"signal-desk/internal/config"
)

func TestDeliveryFormatMigrationPreservesLegacyQueue(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`
		ALTER TABLE deliveries DROP COLUMN body_format;
		PRAGMA user_version=1;
		INSERT INTO events(id,at,kind,source,title,detail) VALUES(10,1,'mail','fixture','previous','{}');
		INSERT INTO deliveries(id,event_id,channel_id,channel_name,channel_source,title,body,updated)
			VALUES(12,10,1,'fixture','address','previous','<legacy text>',1);
		INSERT INTO delivery_attempts(id,delivery_id,at,status,note,next_try,duration_ms)
			VALUES(15,12,1,'failed','previous error',0,5);
		UPDATE sqlite_sequence SET seq=100 WHERE name IN ('deliveries','delivery_attempts');`)
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := s.ClaimDelivery(ctx)
	if err != nil || d.ID != 12 || d.EventID != 10 || d.Body != "<legacy text>" || d.BodyFormat != config.BodyText || d.RecipientIndex != 0 {
		t.Fatalf("legacy notification changed: %#v %v", d, err)
	}
	var deliveryID, sequence int64
	var note string
	if err := s.db.QueryRow("SELECT delivery_id,note FROM delivery_attempts WHERE id=15").Scan(&deliveryID, &note); err != nil ||
		deliveryID != d.ID || note != "previous error" {
		t.Fatalf("legacy delivery attempt changed: %d %q %v", deliveryID, note, err)
	}
	if err := s.FinishDelivery(ctx, d, DeliveryAttempt{Status: "sent"}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT id FROM delivery_attempts WHERE status='sent'").Scan(&sequence); err != nil || sequence != 101 {
		t.Fatalf("delivery attempt sequence changed: %d %v", sequence, err)
	}
}
