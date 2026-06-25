// Package outbox implements the transactional outbox pattern with pluggable
// stores (memory, SQL) and a relay that forwards messages to a Publisher.
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/imajinyun/gofly/core/storage"
)

// DefaultTableName is the table used when SQLStore is created without a custom
// table name.
const DefaultTableName = "outbox_messages"

// DDL columns (kept reserved-word safe across PostgreSQL and MySQL):
//
//	id            TEXT/VARCHAR PRIMARY KEY
//	topic         TEXT/VARCHAR NOT NULL
//	message_key   TEXT/VARCHAR
//	body          BYTEA/BLOB
//	headers       TEXT/JSON
//	status        TEXT/VARCHAR NOT NULL
//	attempts      INT NOT NULL DEFAULT 0
//	created_at    TIMESTAMP NOT NULL
//	available_at  TIMESTAMP NOT NULL
//	delivered_at  TIMESTAMP NULL
//	last_error    TEXT NULL
//
// An index on (status, available_at) keeps Fetch efficient.

// Querier abstracts *sql.DB and *sql.Tx so Enqueue can run inside the caller's
// transaction.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SQLStore is a database-backed Store implementing the transactional outbox
// pattern over the shared storage.SQLStore connection.
type SQLStore struct {
	db      *sql.DB
	table   string
	dialect storage.Dialect
	newID   func() (string, error)
	now     func() time.Time
}

// SQLOption customises a SQLStore.
type SQLOption func(*SQLStore)

// WithTableName overrides the default outbox table name.
func WithTableName(name string) SQLOption {
	return func(s *SQLStore) {
		if name != "" {
			s.table = name
		}
	}
}

// NewSQLStore builds a SQLStore over the given database connection and dialect.
func NewSQLStore(db *sql.DB, dialect storage.Dialect, opts ...SQLOption) (*SQLStore, error) {
	if db == nil {
		return nil, errors.New("outbox: db is nil")
	}
	s := &SQLStore{
		db:      db,
		table:   DefaultTableName,
		dialect: dialect,
		newID:   randomID,
		now:     time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if err := storage.ValidateIdentifier(s.table); err != nil {
		return nil, err
	}
	return s, nil
}

// Enqueue inserts a pending record. Pass the caller's *sql.Tx as q to make the
// message commit atomically with the business write; pass the *sql.DB for a
// standalone insert.
func (s *SQLStore) Enqueue(ctx context.Context, q Querier, msg Message) (string, error) {
	if err := validateMessage(msg); err != nil {
		return "", err
	}
	if q == nil {
		q = s.db
	}
	id, err := s.newID()
	if err != nil {
		return "", err
	}
	headers, err := marshalHeaders(msg.Headers)
	if err != nil {
		return "", err
	}
	now := s.now()
	query := fmt.Sprintf(
		"INSERT INTO %s (id, topic, message_key, body, headers, status, attempts, created_at, available_at) VALUES (%s)",
		s.table, storage.Placeholders(s.dialect, 9),
	)
	if _, err := q.ExecContext(ctx, query, id, msg.Topic, msg.Key, msg.Body, headers, string(StatusPending), 0, now, now); err != nil {
		return "", fmt.Errorf("outbox enqueue: %w", err)
	}
	return id, nil
}

// Fetch claims due pending records using SELECT ... FOR UPDATE SKIP LOCKED,
// leasing them by pushing available_at past the visibility window so other
// relays skip them until the lease expires.
func (s *SQLStore) Fetch(ctx context.Context, limit int, visibility time.Duration) ([]Record, error) {
	if limit <= 0 {
		return nil, nil
	}
	var records []Record
	err := s.transact(ctx, func(tx *sql.Tx) error {
		now := s.now()
		// #nosec G201 -- table name is validated by storage.ValidateIdentifier; values use dialect placeholders.
		selectSQL := fmt.Sprintf(
			"SELECT id, topic, message_key, body, headers, attempts, created_at, available_at FROM %s WHERE status = %s AND available_at <= %s ORDER BY available_at LIMIT %s FOR UPDATE SKIP LOCKED",
			s.table, storage.Placeholder(s.dialect, 1), storage.Placeholder(s.dialect, 2), storage.Placeholder(s.dialect, 3),
		)
		rows, err := tx.QueryContext(ctx, selectSQL, string(StatusPending), now, limit)
		if err != nil {
			return fmt.Errorf("outbox fetch select: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			rec, scanErr := scanRecord(rows)
			if scanErr != nil {
				return scanErr
			}
			records = append(records, rec)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("outbox fetch iterate: %w", err)
		}
		leaseUntil := now.Add(visibility)
		// #nosec G201 -- table name is validated by storage.ValidateIdentifier; values use dialect placeholders.
		updateSQL := fmt.Sprintf(
			"UPDATE %s SET attempts = attempts + 1, available_at = %s WHERE id = %s",
			s.table, storage.Placeholder(s.dialect, 1), storage.Placeholder(s.dialect, 2),
		)
		for i := range records {
			records[i].Attempts++
			records[i].Status = StatusPending
			records[i].AvailableAt = leaseUntil
			if _, err := tx.ExecContext(ctx, updateSQL, leaseUntil, records[i].ID); err != nil {
				return fmt.Errorf("outbox fetch lease: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

// MarkDelivered marks a record delivered.
func (s *SQLStore) MarkDelivered(ctx context.Context, id string) error {
	// #nosec G201 -- table name is validated by storage.ValidateIdentifier; values use dialect placeholders.
	query := fmt.Sprintf(
		"UPDATE %s SET status = %s, delivered_at = %s, last_error = NULL WHERE id = %s",
		s.table, storage.Placeholder(s.dialect, 1), storage.Placeholder(s.dialect, 2), storage.Placeholder(s.dialect, 3),
	)
	if _, err := s.db.ExecContext(ctx, query, string(StatusDelivered), s.now(), id); err != nil {
		return fmt.Errorf("outbox mark delivered: %w", err)
	}
	return nil
}

// Retry reschedules a record for a later attempt.
func (s *SQLStore) Retry(ctx context.Context, id string, availableAt time.Time, lastErr string) error {
	// #nosec G201 -- table name is validated by storage.ValidateIdentifier; values use dialect placeholders.
	query := fmt.Sprintf(
		"UPDATE %s SET available_at = %s, last_error = %s WHERE id = %s",
		s.table, storage.Placeholder(s.dialect, 1), storage.Placeholder(s.dialect, 2), storage.Placeholder(s.dialect, 3),
	)
	if _, err := s.db.ExecContext(ctx, query, availableAt, lastErr, id); err != nil {
		return fmt.Errorf("outbox retry: %w", err)
	}
	return nil
}

// MarkDead moves a record to the dead state.
func (s *SQLStore) MarkDead(ctx context.Context, id string, lastErr string) error {
	// #nosec G201 -- table name is validated by storage.ValidateIdentifier; values use dialect placeholders.
	query := fmt.Sprintf(
		"UPDATE %s SET status = %s, last_error = %s WHERE id = %s",
		s.table, storage.Placeholder(s.dialect, 1), storage.Placeholder(s.dialect, 2), storage.Placeholder(s.dialect, 3),
	)
	if _, err := s.db.ExecContext(ctx, query, string(StatusDead), lastErr, id); err != nil {
		return fmt.Errorf("outbox mark dead: %w", err)
	}
	return nil
}

func (s *SQLStore) transact(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("outbox begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("outbox commit tx: %w", err)
	}
	return nil
}

func scanRecord(rows *sql.Rows) (Record, error) {
	var (
		rec     Record
		key     sql.NullString
		body    []byte
		headers sql.NullString
	)
	if err := rows.Scan(&rec.ID, &rec.Message.Topic, &key, &body, &headers, &rec.Attempts, &rec.CreatedAt, &rec.AvailableAt); err != nil {
		return Record{}, fmt.Errorf("outbox scan record: %w", err)
	}
	rec.Status = StatusPending
	rec.Message.Key = key.String
	rec.Message.Body = body
	if headers.Valid && headers.String != "" {
		parsed, err := unmarshalHeaders(headers.String)
		if err != nil {
			return Record{}, err
		}
		rec.Message.Headers = parsed
	}
	return rec, nil
}

func marshalHeaders(headers map[string]string) (string, error) {
	if len(headers) == 0 {
		return "", nil
	}
	data, err := json.Marshal(headers)
	if err != nil {
		return "", fmt.Errorf("outbox marshal headers: %w", err)
	}
	return string(data), nil
}

func unmarshalHeaders(data string) (map[string]string, error) {
	headers := make(map[string]string)
	if err := json.Unmarshal([]byte(data), &headers); err != nil {
		return nil, fmt.Errorf("outbox unmarshal headers: %w", err)
	}
	return headers, nil
}
