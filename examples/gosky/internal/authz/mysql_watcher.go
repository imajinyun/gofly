package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// MySQLPolicyWatcher polls the append-only policy event outbox. It reloads the
// complete policy after a committed event rather than incrementally patching
// in-memory roles, which keeps revocation and hierarchy recovery deterministic.
type MySQLPolicyWatcher struct {
	db       *sql.DB
	tables   MySQLPolicyTables
	interval time.Duration
	reload   func() error

	mu      sync.RWMutex
	lastID  uint64
	lastErr error
	stop    context.CancelFunc
	done    chan struct{}
}

func NewMySQLPolicyWatcher(db *sql.DB, interval time.Duration, reload func() error) (*MySQLPolicyWatcher, error) {
	return NewMySQLPolicyWatcherWithTables(db, DefaultMySQLPolicyTables(), interval, reload)
}

func NewMySQLPolicyWatcherWithTables(db *sql.DB, tables MySQLPolicyTables, interval time.Duration, reload func() error) (*MySQLPolicyWatcher, error) {
	if db == nil {
		return nil, errors.New("casbin policy database is nil")
	}
	if interval <= 0 {
		return nil, errors.New("casbin policy watcher interval must be positive")
	}
	if reload == nil {
		return nil, errors.New("casbin policy watcher reload callback is required")
	}
	validated, err := NewMySQLPolicyTables(tables.Rules, tables.Events)
	if err != nil {
		return nil, err
	}
	return &MySQLPolicyWatcher{db: db, tables: validated, interval: interval, reload: reload}, nil
}

func (w *MySQLPolicyWatcher) Start(parent context.Context) error {
	if w == nil {
		return errors.New("casbin policy watcher is nil")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stop != nil {
		return errors.New("casbin policy watcher already started")
	}
	ctx, cancel := context.WithCancel(parent)
	w.stop = cancel
	w.done = make(chan struct{})
	go w.run(ctx)
	return nil
}

func (w *MySQLPolicyWatcher) run(ctx context.Context) {
	defer close(w.done)
	_ = w.poll(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = w.poll(ctx)
		}
	}
}

func (w *MySQLPolicyWatcher) poll(ctx context.Context) error {
	lastID := w.LastEventID()
	var nextID uint64
	err := w.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(event_id), 0) FROM `+w.tables.eventsSQL()+` WHERE event_id > ?`, lastID).Scan(&nextID)
	if err != nil {
		w.setError(fmt.Errorf("read Casbin policy events: %w", err))
		return err
	}
	if nextID == 0 {
		w.setError(nil)
		return nil
	}
	if err := w.reload(); err != nil {
		w.setError(fmt.Errorf("reload Casbin policy after event %d: %w", nextID, err))
		return err
	}
	w.mu.Lock()
	w.lastID = nextID
	w.lastErr = nil
	w.mu.Unlock()
	return nil
}

func (w *MySQLPolicyWatcher) LastEventID() uint64 {
	if w == nil {
		return 0
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastID
}

func (w *MySQLPolicyWatcher) Err() error {
	if w == nil {
		return errors.New("casbin policy watcher is nil")
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastErr
}

func (w *MySQLPolicyWatcher) setError(err error) {
	w.mu.Lock()
	w.lastErr = err
	w.mu.Unlock()
}

func (w *MySQLPolicyWatcher) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	stop := w.stop
	done := w.done
	w.stop = nil
	w.done = nil
	w.mu.Unlock()
	if stop != nil {
		stop()
	}
	if done != nil {
		<-done
	}
}
