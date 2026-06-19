// Package outbox implements the transactional outbox pattern with pluggable
// stores (memory, SQL) and a relay that forwards messages to a Publisher.
package outbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// MemoryStore is an in-process Store used for testing and single-node setups.
// Enqueue is not transactional with any external database; it simply records the
// message in memory.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]*Record
	leases  map[string]time.Time
	closed  bool
	newID   func() (string, error)
	now     func() time.Time
}

// NewMemoryStore returns an empty in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records: make(map[string]*Record),
		leases:  make(map[string]time.Time),
		newID:   randomID,
		now:     time.Now,
	}
}

// Enqueue stores a message as a pending record available for immediate delivery.
func (s *MemoryStore) Enqueue(_ context.Context, msg Message) (string, error) {
	if err := validateMessage(msg); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrClosed
	}
	id, err := s.newID()
	if err != nil {
		return "", err
	}
	now := s.now()
	s.records[id] = &Record{
		ID:          id,
		Message:     cloneMessage(msg),
		Status:      StatusPending,
		CreatedAt:   now,
		AvailableAt: now,
	}
	return id, nil
}

// Fetch claims due pending records, leasing them for the visibility window.
func (s *MemoryStore) Fetch(_ context.Context, limit int, visibility time.Duration) ([]Record, error) {
	if limit <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	now := s.now()
	ids := make([]string, 0, len(s.records))
	for id, rec := range s.records {
		if rec.Status != StatusPending {
			continue
		}
		if rec.AvailableAt.After(now) {
			continue
		}
		if lease, ok := s.leases[id]; ok && lease.After(now) {
			continue
		}
		ids = append(ids, id)
	}
	// Deliver oldest-available first for fairness and determinism.
	sort.Slice(ids, func(i, j int) bool {
		ri, rj := s.records[ids[i]], s.records[ids[j]]
		if ri.AvailableAt.Equal(rj.AvailableAt) {
			return ri.CreatedAt.Before(rj.CreatedAt)
		}
		return ri.AvailableAt.Before(rj.AvailableAt)
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]Record, 0, len(ids))
	for _, id := range ids {
		rec := s.records[id]
		rec.Attempts++
		s.leases[id] = now.Add(visibility)
		out = append(out, copyRecord(rec))
	}
	return out, nil
}

// MarkDelivered marks a record delivered and releases its lease.
func (s *MemoryStore) MarkDelivered(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	rec, ok := s.records[id]
	if !ok {
		return nil
	}
	rec.Status = StatusDelivered
	rec.DeliveredAt = s.now()
	rec.LastError = ""
	delete(s.leases, id)
	return nil
}

// Retry reschedules a record for a later attempt and releases its lease.
func (s *MemoryStore) Retry(_ context.Context, id string, availableAt time.Time, lastErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	rec, ok := s.records[id]
	if !ok {
		return nil
	}
	rec.AvailableAt = availableAt
	rec.LastError = lastErr
	delete(s.leases, id)
	return nil
}

// MarkDead moves a record to the dead state and releases its lease.
func (s *MemoryStore) MarkDead(_ context.Context, id string, lastErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	rec, ok := s.records[id]
	if !ok {
		return nil
	}
	rec.Status = StatusDead
	rec.LastError = lastErr
	delete(s.leases, id)
	return nil
}

// Get returns a copy of a record by ID for inspection.
func (s *MemoryStore) Get(id string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return Record{}, false
	}
	return copyRecord(rec), true
}

// Len returns the number of stored records.
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// Close marks the store closed; subsequent operations return ErrClosed.
func (s *MemoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func copyRecord(rec *Record) Record {
	out := *rec
	out.Message = cloneMessage(rec.Message)
	return out
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
