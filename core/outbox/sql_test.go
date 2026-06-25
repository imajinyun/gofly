package outbox

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/mq"
	"github.com/imajinyun/gofly/core/storage"
)

func TestBrokerPublisherForwardsMessage(t *testing.T) {
	broker := mq.NewMemoryBroker()
	defer broker.Close(context.Background())

	received := make(chan mq.Message, 1)
	if _, err := broker.Subscribe(context.Background(), "orders", "g1", func(_ context.Context, m mq.Message) error {
		received <- m
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	pub := BrokerPublisher(mq.AsBroker(broker))
	if err := pub.Publish(context.Background(), Message{Topic: "orders", Key: "k", Body: []byte("hi"), Headers: map[string]string{"h": "v"}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case m := <-received:
		if m.Topic != "orders" || m.Key != "k" || string(m.Body) != "hi" || m.Headers["h"] != "v" {
			t.Fatalf("received = %+v", m)
		}
	case <-context.Background().Done():
	}
}

// execRecorder is a minimal Querier that captures the executed statement.
type execRecorder struct {
	query string
	args  []any
}

func (e *execRecorder) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	e.query = query
	e.args = args
	return driverResult{}, nil
}

type driverResult struct{}

func (driverResult) LastInsertId() (int64, error) { return 0, nil }
func (driverResult) RowsAffected() (int64, error) { return 1, nil }

func TestSQLStoreEnqueueUsesTransactionalQuerier(t *testing.T) {
	store, err := NewSQLStore(&sql.DB{}, storage.DialectPostgres, WithTableName("outbox_messages"))
	if err != nil {
		t.Fatalf("NewSQLStore: %v", err)
	}
	rec := &execRecorder{}
	id, err := store.Enqueue(context.Background(), rec, Message{Topic: "orders", Key: "k", Body: []byte("b"), Headers: map[string]string{"a": "1"}})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if id == "" {
		t.Fatal("Enqueue returned empty id")
	}
	if !strings.HasPrefix(rec.query, "INSERT INTO outbox_messages") || !strings.Contains(rec.query, "$1") {
		t.Fatalf("query = %q", rec.query)
	}
	if len(rec.args) != 9 {
		t.Fatalf("args len = %d, want 9", len(rec.args))
	}
	if rec.args[0] != id || rec.args[1] != "orders" || rec.args[5] != string(StatusPending) {
		t.Fatalf("args = %v", rec.args)
	}
}

func TestSQLStoreRejectsUnsafeTable(t *testing.T) {
	if _, err := NewSQLStore(&sql.DB{}, storage.DialectMySQL, WithTableName("outbox; drop")); err == nil {
		t.Fatal("expected error for unsafe table name")
	}
}

func TestNewSQLStoreNilDB(t *testing.T) {
	if _, err := NewSQLStore(nil, storage.DialectMySQL); err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestSQLStoreFetchLeasesPendingRows(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	script := &scriptedSQL{
		queries: []scriptedQuery{{
			columns: []string{"id", "topic", "message_key", "body", "headers", "attempts", "created_at", "available_at"},
			rows: [][]driver.Value{{
				"rec-1", "orders", "key-1", []byte("payload"), `{"trace":"abc"}`, int64(2), now.Add(-time.Hour), now,
			}},
		}},
		execs: []scriptedExec{{result: driver.RowsAffected(1)}},
	}
	db := newScriptedSQLDB(t, script)
	store, err := NewSQLStore(db, storage.DialectPostgres)
	if err != nil {
		t.Fatalf("NewSQLStore: %v", err)
	}
	store.now = func() time.Time { return now }

	got, err := store.Fetch(context.Background(), 3, 5*time.Minute)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Fetch returned %d records, want 1", len(got))
	}
	rec := got[0]
	if rec.ID != "rec-1" || rec.Message.Topic != "orders" || rec.Message.Key != "key-1" || string(rec.Message.Body) != "payload" {
		t.Fatalf("record identity/message = %+v", rec)
	}
	if rec.Message.Headers["trace"] != "abc" || rec.Attempts != 3 || !rec.AvailableAt.Equal(now.Add(5*time.Minute)) || rec.Status != StatusPending {
		t.Fatalf("record lease/header state = %+v", rec)
	}
	if len(script.execsSeen) != 1 || !strings.Contains(script.execsSeen[0].query, "UPDATE outbox_messages SET attempts = attempts + 1") {
		t.Fatalf("lease execs = %+v, want attempts update", script.execsSeen)
	}
}

func TestSQLStoreFetchBoundaries(t *testing.T) {
	t.Run("non-positive limit skips transaction", func(t *testing.T) {
		script := &scriptedSQL{}
		store := newScriptedOutboxStore(t, script)
		got, err := store.Fetch(context.Background(), 0, time.Minute)
		if err != nil || got != nil {
			t.Fatalf("Fetch limit 0 = %#v, %v; want nil, nil", got, err)
		}
		if script.beginCount != 0 {
			t.Fatalf("beginCount = %d, want 0", script.beginCount)
		}
	})

	t.Run("query error rolls back", func(t *testing.T) {
		script := &scriptedSQL{queries: []scriptedQuery{{err: errors.New("select failed")}}}
		store := newScriptedOutboxStore(t, script)
		_, err := store.Fetch(context.Background(), 10, time.Minute)
		if err == nil || !strings.Contains(err.Error(), "outbox fetch select") {
			t.Fatalf("Fetch query error = %v, want wrapped select error", err)
		}
		if script.rollbackCount != 1 || script.commitCount != 0 {
			t.Fatalf("rollback/commit = %d/%d, want 1/0", script.rollbackCount, script.commitCount)
		}
	})

	t.Run("invalid headers rolls back", func(t *testing.T) {
		now := time.Now()
		script := &scriptedSQL{queries: []scriptedQuery{{
			columns: []string{"id", "topic", "message_key", "body", "headers", "attempts", "created_at", "available_at"},
			rows:    [][]driver.Value{{"rec-1", "orders", nil, []byte("payload"), `{bad`, int64(0), now, now}},
		}}}
		store := newScriptedOutboxStore(t, script)
		_, err := store.Fetch(context.Background(), 10, time.Minute)
		if err == nil || !strings.Contains(err.Error(), "outbox unmarshal headers") {
			t.Fatalf("Fetch invalid headers error = %v, want wrapped unmarshal error", err)
		}
		if script.rollbackCount != 1 {
			t.Fatalf("rollbackCount = %d, want 1", script.rollbackCount)
		}
	})
}

func TestSQLStoreStateUpdates(t *testing.T) {
	db := newScriptedSQLDB(t, &scriptedSQL{execs: []scriptedExec{{result: driver.RowsAffected(1)}, {result: driver.RowsAffected(1)}, {result: driver.RowsAffected(1)}}})
	store, err := NewSQLStore(db, storage.DialectMySQL, WithTableName("outbox_messages"))
	if err != nil {
		t.Fatalf("NewSQLStore: %v", err)
	}
	now := time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	if err := store.MarkDelivered(context.Background(), "rec-1"); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	availableAt := now.Add(time.Minute)
	if err := store.Retry(context.Background(), "rec-2", availableAt, "retry later"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if err := store.MarkDead(context.Background(), "rec-3", "exhausted"); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}
}

func TestSQLStoreStateUpdateErrors(t *testing.T) {
	cases := []struct {
		name    string
		call    func(*SQLStore) error
		wrapped string
	}{
		{
			name: "mark delivered",
			call: func(store *SQLStore) error {
				return store.MarkDelivered(context.Background(), "rec-1")
			},
			wrapped: "outbox mark delivered",
		},
		{
			name: "retry",
			call: func(store *SQLStore) error {
				return store.Retry(context.Background(), "rec-1", time.Now(), "boom")
			},
			wrapped: "outbox retry",
		},
		{
			name: "mark dead",
			call: func(store *SQLStore) error {
				return store.MarkDead(context.Background(), "rec-1", "boom")
			},
			wrapped: "outbox mark dead",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newScriptedOutboxStore(t, &scriptedSQL{execs: []scriptedExec{{err: errors.New("exec failed")}}})
			err := tc.call(store)
			if err == nil || !strings.Contains(err.Error(), tc.wrapped) {
				t.Fatalf("error = %v, want wrapped %q", err, tc.wrapped)
			}
		})
	}
}

func TestSQLStoreTransactBeginAndCommitErrors(t *testing.T) {
	t.Run("begin error", func(t *testing.T) {
		store := newScriptedOutboxStore(t, &scriptedSQL{beginErr: errors.New("begin failed")})
		err := store.transact(context.Background(), func(*sql.Tx) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "outbox begin tx") {
			t.Fatalf("transact begin error = %v, want wrapped begin error", err)
		}
	})

	t.Run("commit error", func(t *testing.T) {
		script := &scriptedSQL{commitErr: errors.New("commit failed")}
		store := newScriptedOutboxStore(t, script)
		err := store.transact(context.Background(), func(*sql.Tx) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "outbox commit tx") {
			t.Fatalf("transact commit error = %v, want wrapped commit error", err)
		}
		if script.rollbackCount != 0 {
			t.Fatalf("rollbackCount = %d, want 0 for commit failure", script.rollbackCount)
		}
	})
}

func TestUnmarshalHeaders(t *testing.T) {
	got, err := unmarshalHeaders(`{"a":"1","b":"2"}`)
	if err != nil {
		t.Fatalf("unmarshalHeaders valid: %v", err)
	}
	if got["a"] != "1" || got["b"] != "2" {
		t.Fatalf("headers = %#v, want decoded map", got)
	}
	if _, err := unmarshalHeaders(`{bad`); err == nil || !strings.Contains(err.Error(), "outbox unmarshal headers") {
		t.Fatalf("unmarshalHeaders invalid error = %v, want wrapped JSON error", err)
	}
}

func newScriptedOutboxStore(t *testing.T, script *scriptedSQL) *SQLStore {
	t.Helper()
	db := newScriptedSQLDB(t, script)
	store, err := NewSQLStore(db, storage.DialectPostgres)
	if err != nil {
		t.Fatalf("NewSQLStore: %v", err)
	}
	return store

}

const scriptedSQLDriverName = "outbox_bitsut_sql"

var (
	scriptedSQLRegisterOnce sync.Once
	scriptedSQLMu           sync.Mutex
	scriptedSQLScripts      = map[string]*scriptedSQL{}
)

func newScriptedSQLDB(t *testing.T, script *scriptedSQL) *sql.DB {
	t.Helper()
	scriptedSQLRegisterOnce.Do(func() {
		sql.Register(scriptedSQLDriverName, scriptedSQLDriver{})
	})
	dsn := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	scriptedSQLMu.Lock()
	scriptedSQLScripts[dsn] = script
	scriptedSQLMu.Unlock()
	db, err := sql.Open(scriptedSQLDriverName, dsn)
	if err != nil {
		t.Fatalf("open scripted sql db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		scriptedSQLMu.Lock()
		delete(scriptedSQLScripts, dsn)
		scriptedSQLMu.Unlock()
	})
	return db
}

type scriptedSQL struct {
	mu            sync.Mutex
	beginErr      error
	commitErr     error
	queries       []scriptedQuery
	execs         []scriptedExec
	queriesSeen   []scriptedStatement
	execsSeen     []scriptedStatement
	beginCount    int
	commitCount   int
	rollbackCount int
}

type scriptedQuery struct {
	columns []string
	rows    [][]driver.Value
	err     error
}

type scriptedExec struct {
	result driver.Result
	err    error
}

type scriptedStatement struct {
	query string
	args  []driver.NamedValue
}

type scriptedSQLDriver struct{}

func (scriptedSQLDriver) Open(name string) (driver.Conn, error) {
	scriptedSQLMu.Lock()
	script := scriptedSQLScripts[name]
	scriptedSQLMu.Unlock()
	if script == nil {
		return nil, fmt.Errorf("missing scripted sql dsn %q", name)
	}
	return &scriptedSQLConn{script: script}, nil
}

type scriptedSQLConn struct {
	script *scriptedSQL
}

func (c *scriptedSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare not implemented")
}
func (c *scriptedSQLConn) Close() error { return nil }
func (c *scriptedSQLConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *scriptedSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.script.mu.Lock()
	defer c.script.mu.Unlock()
	c.script.beginCount++
	if c.script.beginErr != nil {
		return nil, c.script.beginErr
	}
	return &scriptedSQLTx{script: c.script}, nil
}

func (c *scriptedSQLConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.script.query(query, args)
}

func (c *scriptedSQLConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.script.exec(query, args)
}

type scriptedSQLTx struct {
	script *scriptedSQL
}

func (tx *scriptedSQLTx) Commit() error {
	tx.script.mu.Lock()
	defer tx.script.mu.Unlock()
	tx.script.commitCount++
	return tx.script.commitErr
}

func (tx *scriptedSQLTx) Rollback() error {
	tx.script.mu.Lock()
	defer tx.script.mu.Unlock()
	tx.script.rollbackCount++
	return nil
}

func (s *scriptedSQL) query(query string, args []driver.NamedValue) (driver.Rows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queriesSeen = append(s.queriesSeen, scriptedStatement{query: query, args: append([]driver.NamedValue(nil), args...)})
	if len(s.queries) == 0 {
		return nil, fmt.Errorf("unexpected query %q", query)
	}
	next := s.queries[0]
	s.queries = s.queries[1:]
	if next.err != nil {
		return nil, next.err
	}
	return &scriptedRows{columns: next.columns, rows: next.rows}, nil
}

func (s *scriptedSQL) exec(query string, args []driver.NamedValue) (driver.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execsSeen = append(s.execsSeen, scriptedStatement{query: query, args: append([]driver.NamedValue(nil), args...)})
	if len(s.execs) == 0 {
		return nil, fmt.Errorf("unexpected exec %q", query)
	}
	next := s.execs[0]
	s.execs = s.execs[1:]
	if next.err != nil {
		return nil, next.err
	}
	if next.result == nil {
		return driver.RowsAffected(1), nil
	}
	return next.result, nil
}

type scriptedRows struct {
	columns []string
	rows    [][]driver.Value
	idx     int
}

func (r *scriptedRows) Columns() []string { return r.columns }
func (r *scriptedRows) Close() error      { return nil }

func (r *scriptedRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.idx])
	r.idx++
	return nil
}
