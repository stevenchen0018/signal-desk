package store

import (
	"context"
	"database/sql"
)

func (s *Store) migrateDeliveryFormat(ctx context.Context) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version >= 2 {
			return err
		}
		_, err := tx.ExecContext(ctx, `ALTER TABLE deliveries ADD COLUMN body_format TEXT NOT NULL DEFAULT 'text'
			CHECK (body_format IN ('text','html')); PRAGMA user_version=2;`)
		return err
	})
}
