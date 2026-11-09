package store

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed delivery_recipient_migration.sql
var deliveryRecipientSchema string

func (s *Store) migrateDeliveryRecipients(ctx context.Context) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version >= 3 {
			return err
		}
		if _, err := tx.ExecContext(ctx, deliveryRecipientSchema); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, traceSchema); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO delivery_attempts(id,delivery_id,at,status,note,next_try,duration_ms,steps)
				SELECT id,delivery_id,at,status,note,next_try,duration_ms,steps FROM delivery_attempts_v2;
			INSERT INTO sqlite_sequence(name,seq)
				SELECT CASE name WHEN 'deliveries_v2' THEN 'deliveries' ELSE 'delivery_attempts' END,seq
				FROM sqlite_sequence old WHERE old.name IN ('deliveries_v2','delivery_attempts_v2')
				AND NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name=substr(old.name,1,length(old.name)-3));
			UPDATE sqlite_sequence SET seq=max(seq,coalesce((SELECT seq FROM sqlite_sequence old WHERE old.name=sqlite_sequence.name||'_v2'),0))
				WHERE name IN ('deliveries','delivery_attempts');
			DROP TABLE delivery_attempts_v2;
			DROP TABLE deliveries_v2;
			PRAGMA user_version=3;`)
		return err
	})
}
