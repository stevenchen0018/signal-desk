package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

//go:embed trace_schema.sql
var traceSchema string

type Store struct {
	db  *sql.DB
	box cipher.AEAD
}

func (s *Store) Close() error { return s.db.Close() }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "config.key")
	dbPath := filepath.Join(dir, "signal-desk.db")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Stat(dbPath); !errors.Is(statErr, os.ErrNotExist) {
			return nil, errors.New("数据库已存在，但配置密钥缺失，请从备份恢复 config.key")
		}
		f, createErr := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr != nil {
			return nil, createErr
		}
		key = make([]byte, 32)
		rand.Read(key)
		_, err = f.Write(key)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("配置密钥长度错误")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	box, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	dbFile, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = dbFile.Close(); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path),
		RawQuery: "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, box: box}
	if err = s.migrateNotifications(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrateDeliveryFormat(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(traceSchema); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrateDeliveryRecipients(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrateDeliveryEmails(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) encode(kind string, value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return s.box.Seal(nil, nil, data, []byte(kind)), nil
}

func (s *Store) Decode(r Resource, value any) error {
	data, err := s.box.Open(nil, nil, r.config, []byte(r.Kind))
	if err != nil {
		return errors.New("无法解密配置，请检查 config.key")
	}
	return json.Unmarshal(data, value)
}
