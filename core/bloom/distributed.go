// Package bloom provides in-memory and distributed bloom filter implementations
// for negative caching and set-membership tests.
package bloom

import (
	"context"
	"errors"
)

// BitStore is the minimal bit-array contract a distributed bloom filter needs.
// It is satisfied by *redis.Client (SetBit/GetBit operate on a single key).
type BitStore interface {
	SetBit(ctx context.Context, key string, offset uint64, value int) (int64, error)
	GetBit(ctx context.Context, key string, offset uint64) (int64, error)
}

// ErrStoreNil is returned when a DistributedFilter is built without a store.
var ErrStoreNil = errors.New("bloom: bit store is nil")

// DistributedFilter is a bloom filter whose bit array lives in a shared
// BitStore (e.g. a Redis bitmap), allowing it to be consistent across
// processes. Sizing parameters must match across all participants.
type DistributedFilter struct {
	store  BitStore
	key    string
	bits   uint64
	hashes int
}

// NewDistributed builds a distributed bloom filter backed by store under key,
// sized for n elements at false-positive probability p.
func NewDistributed(store BitStore, key string, n uint64, p float64) *DistributedFilter {
	bits, hashes := optimalParams(n, p)
	return &DistributedFilter{store: store, key: key, bits: bits, hashes: hashes}
}

// Add inserts data into the shared bit array.
func (f *DistributedFilter) Add(ctx context.Context, data []byte) error {
	if f == nil || f.store == nil {
		return ErrStoreNil
	}
	for _, off := range offsets(data, f.bits, f.hashes) {
		if _, err := f.store.SetBit(ctx, f.key, off, 1); err != nil {
			return err
		}
	}
	return nil
}

// AddString inserts a string key.
func (f *DistributedFilter) AddString(ctx context.Context, key string) error {
	return f.Add(ctx, []byte(key))
}

// Contains reports whether data may be present in the shared bit array.
func (f *DistributedFilter) Contains(ctx context.Context, data []byte) (bool, error) {
	if f == nil || f.store == nil {
		return false, ErrStoreNil
	}
	for _, off := range offsets(data, f.bits, f.hashes) {
		bit, err := f.store.GetBit(ctx, f.key, off)
		if err != nil {
			return false, err
		}
		if bit == 0 {
			return false, nil
		}
	}
	return true, nil
}

// ContainsString reports possible membership of a string key.
func (f *DistributedFilter) ContainsString(ctx context.Context, key string) (bool, error) {
	return f.Contains(ctx, []byte(key))
}

// Stats returns sizing information.
func (f *DistributedFilter) Stats() Stats {
	if f == nil {
		return Stats{}
	}
	return Stats{Bits: f.bits, Hashes: f.hashes}
}
