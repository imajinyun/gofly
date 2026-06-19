// Package storage provides SQL database connectivity with connection pooling,
// clustering and transaction helpers for gofly services.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync/atomic"
)

// Role indicates the intended access path for an operation against a Cluster.
type Role int

const (
	// RoleAuto routes read-only statements to a replica and everything else to
	// the primary. It is the zero value so an unset role behaves sensibly.
	RoleAuto Role = iota
	// RolePrimary forces the operation onto the write node.
	RolePrimary
	// RoleReplica forces the operation onto a read replica (falling back to the
	// primary when no replica is configured).
	RoleReplica
)

// Cluster groups a primary (write) store with zero or more read replicas and
// routes operations between them. Replica selection is round-robin.
type Cluster struct {
	primary  *SQLStore
	replicas []*SQLStore
	next     uint64
}

// NewCluster builds a read/write split cluster. The primary handles writes and
// the replicas serve reads; with no replicas every operation hits the primary.
func NewCluster(primary *SQLStore, replicas ...*SQLStore) (*Cluster, error) {
	if primary == nil {
		return nil, errors.New("storage: cluster primary is required")
	}
	healthy := make([]*SQLStore, 0, len(replicas))
	for _, replica := range replicas {
		if replica != nil {
			healthy = append(healthy, replica)
		}
	}
	return &Cluster{primary: primary, replicas: healthy}, nil
}

// Primary returns the write store.
func (c *Cluster) Primary() *SQLStore {
	if c == nil {
		return nil
	}
	return c.primary
}

// Writer is an alias for Primary expressing read/write intent.
func (c *Cluster) Writer() *SQLStore { return c.Primary() }

// Replica returns the next read replica using round-robin, falling back to the
// primary when no replicas are configured.
func (c *Cluster) Replica() *SQLStore {
	if c == nil {
		return nil
	}
	if len(c.replicas) == 0 {
		return c.primary
	}
	idx := atomic.AddUint64(&c.next, 1) - 1
	return c.replicas[idx%uint64(len(c.replicas))]
}

// Reader is an alias for Replica expressing read/write intent.
func (c *Cluster) Reader() *SQLStore { return c.Replica() }

// For returns the store appropriate for the given role.
func (c *Cluster) For(role Role) *SQLStore {
	if c == nil {
		return nil
	}
	switch role {
	case RoleReplica:
		return c.Replica()
	case RolePrimary:
		return c.primary
	default:
		return c.primary
	}
}

// ForQuery routes based on the statement: read-only SELECT statements go to a
// replica, everything else (including SELECT ... FOR UPDATE) hits the primary.
func (c *Cluster) ForQuery(query string) *SQLStore {
	if c == nil {
		return nil
	}
	if IsReadOnly(query) {
		return c.Replica()
	}
	return c.primary
}

// QueryAll runs a read against a replica.
func (c *Cluster) QueryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {
	if c == nil {
		return errors.New("cluster is nil")
	}
	return c.Replica().QueryAll(ctx, query, scan, args...)
}

// QueryOne runs a single-row read against a replica.
func (c *Cluster) QueryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {
	if c == nil {
		return errors.New("cluster is nil")
	}
	return c.Replica().QueryOne(ctx, query, scan, args...)
}

// Exec runs a write against the primary.
func (c *Cluster) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if c == nil {
		return nil, errors.New("cluster is nil")
	}
	return c.primary.Exec(ctx, query, args...)
}

// Transact runs a transaction against the primary.
func (c *Cluster) Transact(ctx context.Context, opts *sql.TxOptions, fn TxFunc) error {
	if c == nil {
		return errors.New("cluster is nil")
	}
	return c.primary.Transact(ctx, opts, fn)
}

// Close closes the primary and every replica, joining any errors.
func (c *Cluster) Close() error {
	if c == nil {
		return nil
	}
	var errs []error
	if err := c.primary.Close(); err != nil {
		errs = append(errs, err)
	}
	for _, replica := range c.replicas {
		if err := replica.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// IsReadOnly reports whether a SQL statement is a plain read that may be served
// by a replica. SELECT ... FOR UPDATE / FOR SHARE are treated as writes because
// they acquire locks on the primary.
func IsReadOnly(query string) bool {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return false
	}
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return false
	}
	if strings.HasPrefix(upper, "WITH") && containsDataModifyingCTE(upper) {
		return false
	}
	if strings.Contains(upper, "FOR UPDATE") || strings.Contains(upper, "FOR SHARE") {
		return false
	}
	return true
}

func containsDataModifyingCTE(upper string) bool {
	normalized := strings.NewReplacer("(", " ", ")", " ", ",", " ", ";", " ").Replace(upper)
	normalized = " " + strings.Join(strings.Fields(normalized), " ") + " "
	for _, keyword := range []string{" INSERT ", " UPDATE ", " DELETE ", " MERGE "} {
		if strings.Contains(normalized, keyword) {
			return true
		}
	}
	return false
}

// ShardStrategy maps a shard key to a shard index in [0, count).
type ShardStrategy func(key string, count int) int

// HashShard distributes keys across shards using an FNV-1a hash.
func HashShard(key string, count int) int {
	if count <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	idx := uint64(h.Sum32()) % uint64(count)
	// #nosec G115 -- idx is modulo-bounded by positive int count, so it cannot exceed math.MaxInt.
	return int(idx)
}

// ModShard distributes integer ids across shards by modulo.
func ModShard(id int64, count int) int {
	if count <= 0 {
		return 0
	}
	idx := id % int64(count)
	if idx < 0 {
		idx += int64(count)
	}
	return int(idx)
}

// ShardedCluster spreads data across multiple Clusters by a shard key.
type ShardedCluster struct {
	shards   []*Cluster
	strategy ShardStrategy
}

// NewShardedCluster builds a sharded cluster. When strategy is nil, HashShard is
// used. At least one shard is required.
func NewShardedCluster(strategy ShardStrategy, shards ...*Cluster) (*ShardedCluster, error) {
	if len(shards) == 0 {
		return nil, errors.New("storage: at least one shard is required")
	}
	for i, shard := range shards {
		if shard == nil {
			return nil, fmt.Errorf("storage: shard %d is nil", i)
		}
	}
	if strategy == nil {
		strategy = HashShard
	}
	return &ShardedCluster{shards: shards, strategy: strategy}, nil
}

// Count returns the number of shards.
func (s *ShardedCluster) Count() int {
	if s == nil {
		return 0
	}
	return len(s.shards)
}

// IndexFor returns the shard index for the given key.
func (s *ShardedCluster) IndexFor(key string) int {
	if s == nil || len(s.shards) == 0 {
		return 0
	}
	idx := s.strategy(key, len(s.shards))
	if idx < 0 || idx >= len(s.shards) {
		idx = ((idx % len(s.shards)) + len(s.shards)) % len(s.shards)
	}
	return idx
}

// ShardFor returns the Cluster responsible for the given shard key.
func (s *ShardedCluster) ShardFor(key string) *Cluster {
	if s == nil || len(s.shards) == 0 {
		return nil
	}
	return s.shards[s.IndexFor(key)]
}

// Shard returns the Cluster at the given index.
func (s *ShardedCluster) Shard(idx int) *Cluster {
	if s == nil || idx < 0 || idx >= len(s.shards) {
		return nil
	}
	return s.shards[idx]
}

// Each iterates over every shard, stopping and returning the first error.
func (s *ShardedCluster) Each(fn func(idx int, cluster *Cluster) error) error {
	if s == nil {
		return nil
	}
	for i, shard := range s.shards {
		if err := fn(i, shard); err != nil {
			return err
		}
	}
	return nil
}

// Close closes every shard, joining any errors.
func (s *ShardedCluster) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	for _, shard := range s.shards {
		if err := shard.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ShardTable derives the physical table name for a shard key, e.g. table
// "orders" with key "u-42" across 4 shards yields "orders_3". The base name is
// validated to guard against SQL injection.
func ShardTable(base string, key string, count int) (string, error) {
	if err := ValidateIdentifier(base); err != nil {
		return "", err
	}
	if count <= 0 {
		return "", errors.New("storage: shard count must be positive")
	}
	return fmt.Sprintf("%s_%d", base, HashShard(key, count)), nil
}

// ShardTableIndex derives the physical table name for an explicit shard index.
func ShardTableIndex(base string, idx int) (string, error) {
	if err := ValidateIdentifier(base); err != nil {
		return "", err
	}
	if idx < 0 {
		return "", errors.New("storage: shard index must be non-negative")
	}
	return fmt.Sprintf("%s_%d", base, idx), nil
}
