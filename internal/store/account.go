package store

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"
)

const SessionLifetime = 12 * time.Hour

func (s *Store) AdminName(ctx context.Context) (string, error) {
	var username string
	err := s.db.QueryRowContext(ctx, "SELECT username FROM admin WHERE id=1").Scan(&username)
	return username, err
}

func (s *Store) HasSession(ctx context.Context, token string) (bool, error) {
	sum := sha256.Sum256([]byte(token))
	var expires int64
	err := s.db.QueryRowContext(ctx, "SELECT expires FROM sessions WHERE token_hash=?", sum[:]).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return expires > time.Now().Unix(), err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	sum := sha256.Sum256([]byte(token))
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", sum[:])
	return err
}

func passwordHash(password string, salt []byte) []byte {
	hash, err := pbkdf2.Key(sha256.New, password, salt, 600_000, 32)
	if err != nil {
		panic(err)
	}
	return hash
}

func validPassword(password string) bool {
	return len([]rune(password)) >= 12 && len(password) <= 256
}

func (s *Store) BootstrapAdmin(ctx context.Context, username, password string) error {
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM admin WHERE id=1)").Scan(&exists)
	if err != nil || exists {
		return err
	}
	if username == "" || len(username) > 80 || !validPassword(password) {
		return errors.New("首次启动请设置 SIGNALDESK_ADMIN_PASSWORD（至少 12 字符、最多 256 字节），管理员用户名默认 admin")
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	_, err = s.db.ExecContext(ctx, "INSERT OR IGNORE INTO admin(id,username,salt,password) VALUES(1,?,?,?)",
		username, salt, passwordHash(password, salt))
	return err
}

func (s *Store) ChangePassword(ctx context.Context, token, old, password string) error {
	if !validPassword(password) || len(old) > 256 {
		return errors.New("新密码需至少 12 字符、最多 256 字节")
	}
	var salt, hash []byte
	if err := s.db.QueryRowContext(ctx, "SELECT salt,password FROM admin WHERE id=1").Scan(&salt, &hash); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(hash, passwordHash(old, salt)) != 1 {
		return errors.New("当前密码不正确")
	}
	newSalt := make([]byte, 16)
	rand.Read(newSalt)
	newHash := passwordHash(password, newSalt)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sum := sha256.Sum256([]byte(token))
	var active bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash=? AND expires>?)",
		sum[:], time.Now().Unix()).Scan(&active); err != nil {
		return err
	}
	if !active {
		return errors.New("会话已过期，请重新登录")
	}
	changed, err := tx.ExecContext(ctx, "UPDATE admin SET salt=?,password=? WHERE id=1 AND password=?", newSalt, newHash, hash)
	if err != nil {
		return err
	}
	if n, err := changed.RowsAffected(); err != nil || n != 1 {
		return errors.New("密码已更新，请重新登录")
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	return tx.Commit()
}

var ErrLogin = errors.New("用户名或密码不正确")

func (s *Store) NewSession(ctx context.Context, username, password string) (string, error) {
	if len(password) > 256 || len(username) > 80 {
		return "", ErrLogin
	}
	var name string
	var salt, hash []byte
	if err := s.db.QueryRowContext(ctx, "SELECT username,salt,password FROM admin WHERE id=1").Scan(&name, &salt, &hash); err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare(passwordHash(password, salt), hash) != 1 || name != username {
		return "", ErrLogin
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires<=?", now); err != nil {
		return "", err
	}
	token := rand.Text()
	sum := sha256.Sum256([]byte(token))
	res, err := tx.ExecContext(ctx, `INSERT INTO sessions(token_hash,expires)
		SELECT ?,? FROM admin WHERE id=1 AND password=?`, sum[:], now+int64(SessionLifetime/time.Second), hash)
	if err != nil {
		return "", err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return "", ErrLogin
	}
	return token, tx.Commit()
}
