package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"signal-desk/internal/config"
)

func (s *Store) migrateDeliveryEmails(ctx context.Context) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version >= 4 {
			return err
		}
		if _, err := tx.ExecContext(ctx, "ALTER TABLE deliveries ADD COLUMN recipient_email TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
		channels, err := listResources(ctx, tx, "kind='channels'")
		if err != nil {
			return err
		}
		for _, channel := range channels {
			var cfg config.Channel
			if s.Decode(channel, &cfg) != nil || cfg.Type != "email" {
				continue
			}
			recipients, err := config.ParseRecipients(cfg.Address)
			if err != nil {
				continue
			}
			addresses := make([]string, len(recipients))
			for i, recipient := range recipients {
				addresses[i] = recipient.Address
			}
			raw, err := json.Marshal(addresses)
			if err != nil {
				return err
			}
			// Only recover a historical address when the configured destination still matches.
			if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET recipient_email=(
				SELECT value FROM json_each(?) WHERE key=max(recipient_index-1,0))
				WHERE channel_id=? AND channel_source=? AND (
					recipient_index BETWEEN 1 AND ? OR (recipient_index=0 AND ?=1))`,
				string(raw), channel.ID, channel.Source, len(addresses), len(addresses)); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "PRAGMA user_version=4")
		return err
	})
}
