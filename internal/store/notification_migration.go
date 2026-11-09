package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"signal-desk/internal/config"
)

func (s *Store) migrateNotifications(ctx context.Context) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version >= 1 {
			return err
		}
		for _, table := range []struct{ name, marker, index, columns string }{
			{"resources", "'policies'", "DROP INDEX singleton_resources; DROP INDEX resource_schedule;", "id,kind,name,enabled,config,source,revision,interval_seconds,state,next_run,requested,lease_until,run_token"},
			{"deliveries", "policy_id", "DROP INDEX delivery_ready;", "id,event_id,channel_id,channel_name,channel_source,title,body,status,attempts,next_try,error,updated,claim"},
		} {
			var definition string
			if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE name=?", table.name).Scan(&definition); err != nil {
				return err
			}
			if strings.Contains(definition, table.marker) {
				continue
			}
			old := table.name + "_before_notifications"
			if _, err := tx.ExecContext(ctx, "ALTER TABLE "+table.name+" RENAME TO "+old+"; "+table.index); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, schema); err != nil {
				return err
			}
			query := fmt.Sprintf(`INSERT INTO %[1]s(%[2]s) SELECT %[2]s FROM %[3]s;
				INSERT INTO sqlite_sequence(name,seq) SELECT '%[1]s',seq FROM sqlite_sequence WHERE name='%[3]s'
					AND NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='%[1]s');
				UPDATE sqlite_sequence SET seq=max(seq,coalesce((SELECT seq FROM sqlite_sequence WHERE name='%[3]s'),0)) WHERE name='%[1]s';
				DROP TABLE %[3]s;`, table.name, table.columns, old)
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return err
			}
		}
		for _, category := range []string{config.ModelUp, config.ModelDown, config.ModelEarlyDown} {
			if _, err := s.insertNotificationConfig(ctx, tx, "templates", config.TemplateCategoryLabel(category), true, config.DefaultNotificationTemplate(category)); err != nil {
				return err
			}
		}
		if err := s.importLegacyNotifications(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "PRAGMA user_version=1")
		return err
	})
}

func (s *Store) insertNotificationConfig(ctx context.Context, tx *sql.Tx, kind, name string, enabled bool, value any) (int64, error) {
	sealed, err := s.encode(kind, value)
	if err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO resources(kind,name,enabled,config) VALUES(?,?,?,?)", kind, name, enabled, sealed)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) importLegacyNotifications(ctx context.Context, tx *sql.Tx) error {
	items, err := listResources(ctx, tx, "kind IN ('rules','models')")
	if err != nil {
		return err
	}
	for _, item := range items {
		var value any
		switch item.Kind {
		case "rules":
			var cfg config.Rule
			if err := s.Decode(item, &cfg); err != nil {
				return err
			}
			if len(cfg.Channels) == 0 {
				continue
			}
			if err := s.importLegacyRoute(ctx, tx, item, config.MailMatched, item.ID, cfg.Route); err != nil {
				return err
			}
			cfg.Channels = nil
			value = cfg
		case "models":
			var cfg config.ModelMonitor
			if err := s.Decode(item, &cfg); err != nil {
				return err
			}
			if len(cfg.Up.Channels)+len(cfg.Down.Channels) == 0 {
				continue
			}
			if err := s.importLegacyRoute(ctx, tx, item, config.ModelUp, 0, cfg.Up); err != nil {
				return err
			}
			if err := s.importLegacyRoute(ctx, tx, item, config.ModelDown, 0, cfg.Down); err != nil {
				return err
			}
			cfg.Up.Channels, cfg.Down.Channels = nil, nil
			value = cfg
		}
		sealed, err := s.encode(item.Kind, value)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE resources SET config=?,revision=revision+1 WHERE id=?", sealed, item.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) importLegacyRoute(ctx context.Context, tx *sql.Tx, item Resource, trigger string, ruleID int64, route config.Route) error {
	if len(route.Channels) == 0 {
		return nil
	}
	category := trigger
	if trigger == config.MailMatched {
		category = "custom"
	}
	name := item.Name + " · " + config.TemplateCategoryLabel(category)
	templateID, err := s.insertNotificationConfig(ctx, tx, "templates", name, true, config.NotificationTemplate{Category: category, MessageTemplate: route.MessageTemplate})
	if err != nil {
		return err
	}
	for _, channel := range route.Channels {
		p := config.Policy{Trigger: trigger, RuleID: ruleID, TemplateID: templateID, ChannelID: channel}
		if _, err := s.insertNotificationConfig(ctx, tx, "policies", fmt.Sprintf("%s · 渠道 %d", name, channel), true, p); err != nil {
			return err
		}
	}
	return nil
}
