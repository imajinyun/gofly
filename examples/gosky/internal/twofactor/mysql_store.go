package twofactor

import (
	"context"
	"database/sql"
	"time"

	"github.com/imajinyun/gofly/core/storage"
)

// MySQLCodeStore persists login verification codes in twofactor_login_codes.
// The table is global (not tenant scoped) because codes are issued at the
// pre-authentication login boundary; the login name is the lookup key.
type MySQLCodeStore struct {
	store *storage.SQLStore
}

func NewMySQLCodeStore(store *storage.SQLStore) *MySQLCodeStore {
	return &MySQLCodeStore{store: store}
}

func (s *MySQLCodeStore) Save(ctx context.Context, loginName, codeHash string, expiresAt time.Time) error {
	_, err := s.store.Exec(ctx, `INSERT INTO twofactor_login_codes (login_name, code_hash, expires_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE code_hash = VALUES(code_hash), expires_at = VALUES(expires_at), updated_at = VALUES(updated_at)`,
		loginName, codeHash, expiresAt, time.Now().UTC(), time.Now().UTC())
	return err
}

func (s *MySQLCodeStore) Load(ctx context.Context, loginName string) (string, time.Time, bool, error) {
	rows, err := s.store.QueryRows(ctx,
		`SELECT code_hash, expires_at FROM twofactor_login_codes WHERE login_name = ?`, loginName)
	if err != nil {
		return "", time.Time{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return "", time.Time{}, false, nil
	}
	var hash string
	var expiresAt time.Time
	if err := rows.Scan(&hash, &expiresAt); err != nil {
		return "", time.Time{}, false, err
	}
	return hash, expiresAt, true, nil
}

func (s *MySQLCodeStore) Delete(ctx context.Context, loginName string) error {
	_, err := s.store.Exec(ctx, `DELETE FROM twofactor_login_codes WHERE login_name = ?`, loginName)
	return err
}

func (s *MySQLCodeStore) CountAttempts(ctx context.Context, loginName string, since time.Time) (int, error) {
	rows, err := s.store.QueryRows(ctx,
		`SELECT COUNT(*) FROM twofactor_login_attempts WHERE login_name = ? AND attempted_at > ?`, loginName, since)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var count int
	if !rows.Next() {
		return 0, sql.ErrNoRows
	}
	return count, rows.Scan(&count)
}

func (s *MySQLCodeStore) RecordAttempt(ctx context.Context, loginName string, at time.Time) error {
	_, err := s.store.Exec(ctx,
		`INSERT INTO twofactor_login_attempts (login_name, attempted_at, created_at) VALUES (?, ?, ?)`,
		loginName, at, at)
	return err
}
