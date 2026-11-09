package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"signal-desk/internal/config"
)

type Resource struct {
	ID, Revision, Interval, Next int64
	Kind, Name, Source, State    string
	Enabled                      bool

	config    []byte
	requested int64
	runToken  string
}

const resourceColumns = "id, kind, name, enabled, config, source, revision, interval_seconds, state, next_run, run_token, requested"

func scanResource(row interface{ Scan(...any) error }) (Resource, error) {
	var r Resource
	err := row.Scan(&r.ID, &r.Kind, &r.Name, &r.Enabled, &r.config, &r.Source, &r.Revision, &r.Interval, &r.State, &r.Next, &r.runToken, &r.requested)
	return r, err
}

func (s *Store) List(ctx context.Context, kind string) ([]Resource, error) {
	return listResources(ctx, s.db, "kind=?", kind)
}

func (s *Store) Get(ctx context.Context, kind string, id int64) (Resource, error) {
	return scanResource(s.db.QueryRowContext(ctx, "SELECT "+resourceColumns+" FROM resources WHERE kind=? AND id=?", kind, id))
}

type resourceQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// The condition comes from the fixed queries used by List and Delete.
func listResources(ctx context.Context, db resourceQuerier, condition string, args ...any) ([]Resource, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+resourceColumns+" FROM resources WHERE "+condition+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Resource
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

type ResourceInput struct {
	Name     string
	Enabled  bool
	Interval int64
	Config   any
}

var ErrStaleConfig = errors.New("配置已被其他页面更新，请刷新后重试")

func (s *Store) Save(ctx context.Context, kind string, id, revision int64, input ResourceInput) error {
	sealed, err := s.encode(kind, input.Config)
	if err != nil {
		return err
	}
	source := sourceKey(input.Config)
	var expression string
	switch cfg := input.Config.(type) {
	case config.Mailbox:
		expression = cfg.Cron
	case config.ModelMonitor:
		expression = cfg.Cron
	}
	var nextRun int64
	if expression != "" {
		next, err := config.NextCronRun(expression, time.Now())
		if err != nil {
			return err
		}
		nextRun = next.Unix()
		input.Interval = 0
	}
	return s.transact(ctx, func(tx *sql.Tx) error {
		var current Resource
		if id != 0 {
			r, err := scanResource(tx.QueryRowContext(ctx, "SELECT "+resourceColumns+" FROM resources WHERE id=? AND kind=?", id, kind))
			if errors.Is(err, sql.ErrNoRows) {
				return ErrStaleConfig
			}
			if err != nil {
				return err
			}
			if r.Revision != revision {
				return ErrStaleConfig
			}
			current = r
		}

		var channels []int64
		var mailbox, oldMailbox int64
		switch cfg := input.Config.(type) {
		case config.Rule:
			channels, mailbox = cfg.Channels, cfg.Mailbox
			if err := checkReference(ctx, tx, "mailboxes", mailbox); err != nil {
				return err
			}
			if current.ID != 0 {
				var previous config.Rule
				if err := s.Decode(current, &previous); err != nil {
					return err
				}
				oldMailbox = previous.Mailbox
			}
		case config.ModelMonitor:
			channels = append(slices.Clone(cfg.Up.Channels), cfg.Down.Channels...)
		case config.Channel:
			if cfg.Type == "email" {
				if _, err := config.ParseRecipients(cfg.Address); err != nil {
					return err
				}
			}
		case config.NotificationTemplate:
			if err := cfg.Validate(); err != nil {
				return err
			}
		case config.Policy:
			if err := s.validatePolicy(ctx, tx, id, cfg); err != nil {
				return err
			}

		}
		for _, id := range channels {
			if err := checkReference(ctx, tx, "channels", id); err != nil {
				return err
			}
		}

		var err error
		if id == 0 {
			_, err = tx.ExecContext(ctx, "INSERT INTO resources(kind,name,enabled,config,source,interval_seconds,next_run) VALUES(?,?,?,?,?,?,?)",
				kind, input.Name, input.Enabled, sealed, source, input.Interval, nextRun)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE resources SET name=?,enabled=?,config=?,interval_seconds=?,
				state=CASE WHEN source=? THEN state ELSE '' END,source=?,next_run=?,revision=revision+1
				WHERE id=?`, input.Name, input.Enabled, sealed, input.Interval, source, source, nextRun, id)
		}
		if err != nil {
			return err
		}
		if mailbox != 0 {
			return invalidateMailJobs(ctx, tx, mailbox, oldMailbox)
		}
		return nil
	})
}

func (s *Store) Delete(ctx context.Context, kind string, id int64) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		if kind == "rules" || kind == "templates" || kind == "channels" {
			if err := s.checkPolicyReferences(ctx, tx, id); err != nil {
				return err
			}
		}
		if kind == "rules" {
			r, err := scanResource(tx.QueryRowContext(ctx, "SELECT "+resourceColumns+" FROM resources WHERE id=? AND kind=?", id, kind))
			if err != nil {
				return err
			}
			var cfg config.Rule
			if err = s.Decode(r, &cfg); err != nil {
				return err
			}
			if err = invalidateMailJobs(ctx, tx, cfg.Mailbox, 0); err != nil {
				return err
			}
		} else {
			if err := s.checkReferencesBeforeDelete(ctx, tx, id); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM resources WHERE id=? AND kind=?", id, kind)
		return err
	})
}

func (s *Store) checkReferencesBeforeDelete(ctx context.Context, tx *sql.Tx, id int64) error {
	items, err := listResources(ctx, tx, "kind IN ('rules','models')")
	if err != nil {
		return err
	}
	for _, r := range items {
		switch r.Kind {
		case "rules":
			var cfg config.Rule
			if err = s.Decode(r, &cfg); err != nil {
				return err
			}
			if cfg.Mailbox == id || slices.Contains(cfg.Channels, id) {
				return errors.New("请先从邮件规则中解除关联")
			}
		case "models":
			var cfg config.ModelMonitor
			if err = s.Decode(r, &cfg); err != nil {
				return err
			}
			if slices.Contains(cfg.Up.Channels, id) || slices.Contains(cfg.Down.Channels, id) {
				return errors.New("请先从模型监控中解除关联")
			}
		}
	}
	return nil
}

func invalidateMailJobs(ctx context.Context, tx *sql.Tx, mailbox, previous int64) error {
	// A polling job that read the old rules must discard its results.
	_, err := tx.ExecContext(ctx, "UPDATE resources SET revision=revision+1 WHERE kind='mailboxes' AND id IN (?,?)", mailbox, previous)
	return err
}

func checkReference(ctx context.Context, tx *sql.Tx, kind string, id int64) error {
	var found bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM resources WHERE kind=? AND id=?)", kind, id).Scan(&found); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("配置关联已失效（#%d），请刷新后重选", id)
	}
	return nil
}

func sourceKey(value any) string {
	var parts []string
	switch cfg := value.(type) {
	case config.Mailbox:
		parts = []string{cfg.Host, cfg.Port, cfg.User, cfg.Folder}
	case config.ModelMonitor:
		parts = []string{cfg.URL}
	case config.Channel:
		parts = []string{cfg.Type, cfg.Address}
	default:
		return ""
	}
	// This encoding is persisted in existing databases. Keep it stable.
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *Store) transact(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) validatePolicy(ctx context.Context, tx *sql.Tx, id int64, cfg config.Policy) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.RuleID > 0 {
		if err := checkReference(ctx, tx, "rules", cfg.RuleID); err != nil {
			return err
		}
	}
	if err := checkReference(ctx, tx, "templates", cfg.TemplateID); err != nil {
		return err
	}
	if err := checkReference(ctx, tx, "channels", cfg.ChannelID); err != nil {
		return err
	}
	items, err := listResources(ctx, tx, "kind='policies' AND id<>?", id)
	if err != nil {
		return err
	}
	for _, item := range items {
		var existing config.Policy
		if err := s.Decode(item, &existing); err != nil {
			return err
		}
		if existing == cfg {
			return errors.New("同一触发事件、模板和接收渠道已存在告警策略")
		}
	}
	return nil
}

func (s *Store) checkPolicyReferences(ctx context.Context, tx *sql.Tx, id int64) error {
	items, err := listResources(ctx, tx, "kind='policies'")
	if err != nil {
		return err
	}
	for _, item := range items {
		var cfg config.Policy
		if err := s.Decode(item, &cfg); err != nil {
			return err
		}
		if cfg.RuleID == id || cfg.TemplateID == id || cfg.ChannelID == id {
			return errors.New("请先从告警策略中解除关联")
		}
	}
	return nil
}
