package twofactor

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is a process-local CodeStore for tests and local development
// without a database. Production deployments use MySQLCodeStore.
type MemoryStore struct {
	mu       sync.Mutex
	rows     map[string]memoryCodeRow
	attempts map[string][]time.Time
	emails   map[string]string
}

type memoryCodeRow struct {
	hash      string
	expiresAt time.Time
}

func NewMemoryStore(emails map[string]string) *MemoryStore {
	return &MemoryStore{
		rows:     make(map[string]memoryCodeRow),
		attempts: make(map[string][]time.Time),
		emails:   emails,
	}
}

func (s *MemoryStore) Save(_ context.Context, loginName, codeHash string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[loginName] = memoryCodeRow{hash: codeHash, expiresAt: expiresAt}
	return nil
}

func (s *MemoryStore) Load(_ context.Context, loginName string) (string, time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[loginName]
	return row.hash, row.expiresAt, ok, nil
}

func (s *MemoryStore) Delete(_ context.Context, loginName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, loginName)
	delete(s.attempts, loginName)
	return nil
}

func (s *MemoryStore) CountAttempts(_ context.Context, loginName string, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, at := range s.attempts[loginName] {
		if at.After(since) {
			count++
		}
	}
	return count, nil
}

func (s *MemoryStore) RecordAttempt(_ context.Context, loginName string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts[loginName] = append(s.attempts[loginName], at)
	return nil
}

func (s *MemoryStore) LoginEmail(_ context.Context, loginName string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	email, ok := s.emails[loginName]
	return email, ok, nil
}
