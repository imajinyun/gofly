package storage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestIsReadOnly(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"SELECT * FROM users", true},
		{"  select id from users  ", true},
		{"WITH t AS (SELECT 1) SELECT * FROM t", true},
		{"SELECT * FROM users FOR UPDATE", false},
		{"SELECT * FROM users FOR SHARE", false},
		{"INSERT INTO users VALUES (1)", false},
		{"UPDATE users SET name = 'x'", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsReadOnly(tt.query); got != tt.want {
			t.Fatalf("IsReadOnly(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestClusterReplicaRoundRobin(t *testing.T) {
	primary := NewSQLStore(nil)
	r1 := NewSQLStore(nil)
	r2 := NewSQLStore(nil)
	cluster, err := NewCluster(primary, r1, r2, nil)
	if err != nil {
		t.Fatalf("NewCluster returned error: %v", err)
	}
	if cluster.Writer() != primary {
		t.Fatal("Writer should return primary")
	}
	// round-robin across the two healthy replicas
	got := []*SQLStore{cluster.Reader(), cluster.Reader(), cluster.Reader(), cluster.Reader()}
	if got[0] != r1 || got[1] != r2 || got[2] != r1 || got[3] != r2 {
		t.Fatalf("replica round-robin = %v, want r1,r2,r1,r2", got)
	}
}

func TestClusterNoReplicasFallback(t *testing.T) {
	primary := NewSQLStore(nil)
	cluster, err := NewCluster(primary)
	if err != nil {
		t.Fatalf("NewCluster returned error: %v", err)
	}
	if cluster.Replica() != primary {
		t.Fatal("Replica should fall back to primary when no replicas")
	}
}

func TestClusterForQueryRouting(t *testing.T) {
	primary := NewSQLStore(nil)
	replica := NewSQLStore(nil)
	cluster, _ := NewCluster(primary, replica)
	if cluster.ForQuery("SELECT 1") != replica {
		t.Fatal("read should route to replica")
	}
	if cluster.ForQuery("UPDATE t SET x=1") != primary {
		t.Fatal("write should route to primary")
	}
	if cluster.ForQuery("SELECT * FROM t FOR UPDATE") != primary {
		t.Fatal("locking read should route to primary")
	}
	if cluster.For(RolePrimary) != primary || cluster.For(RoleReplica) != replica || cluster.For(RoleAuto) != primary {
		t.Fatal("For role routing mismatch")
	}
}

func TestIsReadOnlyRejectsDataModifyingCTE(t *testing.T) {
	queries := []string{
		"WITH deleted AS (DELETE FROM jobs WHERE state = 'done' RETURNING id) SELECT * FROM deleted",
		"WITH updated AS (UPDATE jobs SET state = 'done' RETURNING id) SELECT * FROM updated",
		"WITH inserted AS (INSERT INTO jobs(id) VALUES (1) RETURNING id) SELECT * FROM inserted",
		"WITH merged AS (MERGE INTO jobs USING incoming ON jobs.id = incoming.id WHEN MATCHED THEN UPDATE SET state = incoming.state) SELECT 1",
	}
	for _, query := range queries {
		if IsReadOnly(query) {
			t.Fatalf("IsReadOnly(%q) = true, want false for data-modifying CTE", query)
		}
	}
}

func TestClusterForQueryRoutesDataModifyingCTEToPrimary(t *testing.T) {
	primary := NewSQLStore(nil)
	replica := NewSQLStore(nil)
	cluster, _ := NewCluster(primary, replica)
	query := "WITH deleted AS (DELETE FROM jobs WHERE state = 'done' RETURNING id) SELECT * FROM deleted"
	if got := cluster.ForQuery(query); got != primary {
		t.Fatalf("ForQuery(data-modifying CTE) = %p, want primary %p", got, primary)
	}
}

func TestNewClusterRequiresPrimary(t *testing.T) {
	if _, err := NewCluster(nil); err == nil {
		t.Fatal("NewCluster(nil) should error")
	}
}

func TestHashAndModShard(t *testing.T) {
	if HashShard("key", 0) != 0 {
		t.Fatal("HashShard with count 0 should return 0")
	}
	for i := 0; i < 100; i++ {
		idx := HashShard("user-"+string(rune(i)), 4)
		if idx < 0 || idx >= 4 {
			t.Fatalf("HashShard out of range: %d", idx)
		}
	}
	// stable
	stable := HashShard("stable", 8)
	if stable != HashShard("stable", 8) {
		t.Fatal("HashShard must be deterministic")
	}
	if ModShard(7, 4) != 3 || ModShard(-1, 4) != 3 || ModShard(5, 0) != 0 {
		t.Fatal("ModShard mismatch")
	}
	if strconv.IntSize == 64 {
		largeCount := int(uint64(math.MaxUint32) + 1)
		idx := HashShard("large-count", largeCount)
		if idx < 0 || idx >= largeCount {
			t.Fatalf("HashShard large count index = %d, want in [0,%d)", idx, largeCount)
		}
	}
}

func TestShardedClusterRouting(t *testing.T) {
	s0, _ := NewCluster(NewSQLStore(nil))
	s1, _ := NewCluster(NewSQLStore(nil))
	// custom strategy: even-length key -> 0, odd -> 1
	strategy := func(key string, count int) int {
		if len(key)%2 == 0 {
			return 0
		}
		return 1
	}
	sharded, err := NewShardedCluster(strategy, s0, s1)
	if err != nil {
		t.Fatalf("NewShardedCluster returned error: %v", err)
	}
	if sharded.Count() != 2 {
		t.Fatalf("Count = %d, want 2", sharded.Count())
	}
	if sharded.ShardFor("ab") != s0 {
		t.Fatal("even key should route to shard 0")
	}
	if sharded.ShardFor("abc") != s1 {
		t.Fatal("odd key should route to shard 1")
	}
	if sharded.Shard(1) != s1 || sharded.Shard(99) != nil {
		t.Fatal("Shard index lookup mismatch")
	}
	visited := 0
	_ = sharded.Each(func(idx int, cluster *Cluster) error {
		visited++
		return nil
	})
	if visited != 2 {
		t.Fatalf("Each visited %d shards, want 2", visited)
	}
}

func TestNewShardedClusterValidation(t *testing.T) {
	if _, err := NewShardedCluster(nil); err == nil {
		t.Fatal("NewShardedCluster with no shards should error")
	}
	if _, err := NewShardedCluster(nil, nil); err == nil {
		t.Fatal("NewShardedCluster with nil shard should error")
	}
	// default strategy applied
	s0, _ := NewCluster(NewSQLStore(nil))
	sharded, err := NewShardedCluster(nil, s0)
	if err != nil {
		t.Fatalf("NewShardedCluster default strategy error: %v", err)
	}
	if sharded.ShardFor("anything") != s0 {
		t.Fatal("single shard should always be selected")
	}
}

func TestShardTable(t *testing.T) {
	name, err := ShardTable("orders", "user-42", 4)
	if err != nil {
		t.Fatalf("ShardTable returned error: %v", err)
	}
	want := "orders_" + string(rune('0'+HashShard("user-42", 4)))
	if name != want {
		t.Fatalf("ShardTable = %q, want %q", name, want)
	}
	if _, err := ShardTable("orders; drop", "k", 4); !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("ShardTable unsafe base error = %v, want ErrInvalidIdentifier", err)
	}
	if _, err := ShardTable("orders", "k", 0); err == nil {
		t.Fatal("ShardTable count 0 should error")
	}
	idxName, err := ShardTableIndex("orders", 3)
	if err != nil || idxName != "orders_3" {
		t.Fatalf("ShardTableIndex = %q, %v", idxName, err)
	}
	if _, err := ShardTableIndex("orders", -1); err == nil {
		t.Fatal("ShardTableIndex negative should error")
	}
}

func TestClusterNilGuards(t *testing.T) {
	var nilCluster *Cluster
	if nilCluster.Primary() != nil {
		t.Fatal("nil Primary should return nil")
	}
	if nilCluster.Replica() != nil {
		t.Fatal("nil Replica should return nil")
	}
	if nilCluster.For(RolePrimary) != nil {
		t.Fatal("nil For should return nil")
	}
	if nilCluster.ForQuery("SELECT 1") != nil {
		t.Fatal("nil ForQuery should return nil")
	}
	if err := nilCluster.Close(); err != nil {
		t.Fatalf("nil Close = %v, want nil", err)
	}
}

func TestShardedClusterIndexNormalizationAndClose_BitsUT(t *testing.T) {
	s0, _ := NewCluster(NewSQLStore(nil))
	s1, _ := NewCluster(NewSQLStore(nil))
	cases := []struct {
		name     string
		strategy ShardStrategy
		want     *Cluster
	}{
		{name: "negative", strategy: func(string, int) int { return -1 }, want: s1},
		{name: "oversized", strategy: func(string, int) int { return 3 }, want: s1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			sharded, err := NewShardedCluster(tt.strategy, s0, s1)
			if err != nil {
				t.Fatalf("NewShardedCluster error = %v", err)
			}
			if got := sharded.ShardFor("key"); got != tt.want {
				t.Fatalf("ShardFor = %p, want %p", got, tt.want)
			}
			if err := sharded.Close(); err != nil {
				t.Fatalf("Close error = %v", err)
			}
		})
	}
}

func TestIsReadOnlyCTEBoundaries_BitsUT(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{
			name:  "nested read only cte",
			query: "WITH outer_cte AS (SELECT * FROM (SELECT id FROM users) nested) SELECT * FROM outer_cte",
			want:  true,
		},
		{
			name:  "keyword embedded in identifier does not mark write",
			query: "WITH updater AS (SELECT last_update FROM audit_log) SELECT * FROM updater",
			want:  true,
		},
		{
			name:  "delete in second cte marks write",
			query: "WITH first AS (SELECT 1), second AS (DELETE FROM jobs WHERE id = 1 RETURNING id) SELECT * FROM second",
			want:  false,
		},
		{
			name:  "merge separated by punctuation marks write",
			query: "WITH changed AS (MERGE INTO jobs USING incoming ON jobs.id = incoming.id) SELECT 1",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReadOnly(tt.query); got != tt.want {
				t.Fatalf("IsReadOnly(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestShardedClusterNilAndIterationBoundaries_BitsUT(t *testing.T) {
	var nilSharded *ShardedCluster
	if nilSharded.Count() != 0 {
		t.Fatal("nil Count should return 0")
	}
	if nilSharded.IndexFor("key") != 0 {
		t.Fatal("nil IndexFor should return 0")
	}
	if nilSharded.ShardFor("key") != nil {
		t.Fatal("nil ShardFor should return nil")
	}
	if nilSharded.Shard(0) != nil {
		t.Fatal("nil Shard should return nil")
	}
	if err := nilSharded.Each(func(int, *Cluster) error { return errors.New("unexpected") }); err != nil {
		t.Fatalf("nil Each = %v, want nil", err)
	}
	if err := nilSharded.Close(); err != nil {
		t.Fatalf("nil Close = %v, want nil", err)
	}

	s0, _ := NewCluster(NewSQLStore(nil))
	s1, _ := NewCluster(NewSQLStore(nil))
	sharded, err := NewShardedCluster(func(string, int) int { return 0 }, s0, s1)
	if err != nil {
		t.Fatalf("NewShardedCluster error = %v", err)
	}
	stop := errors.New("stop")
	visited := 0
	err = sharded.Each(func(idx int, cluster *Cluster) error {
		visited++
		if idx != 0 || cluster != s0 {
			t.Fatalf("first Each callback idx=%d cluster=%p, want idx=0 cluster=%p", idx, cluster, s0)
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("Each error = %v, want stop", err)
	}
	if visited != 1 {
		t.Fatalf("Each visited %d shards, want stop after first", visited)
	}
}

func TestShardTableIndexInvalidBaseBoundary_BitsUT(t *testing.T) {
	if _, err := ShardTableIndex("orders;drop", 0); !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("ShardTableIndex invalid base error = %v, want ErrInvalidIdentifier", err)
	}
}

func TestShardedClusterNilGuards(t *testing.T) {
	var nilSharded *ShardedCluster
	if nilSharded.Count() != 0 {
		t.Fatalf("nil Count = %d, want 0", nilSharded.Count())
	}
	if nilSharded.IndexFor("k") != 0 {
		t.Fatalf("nil IndexFor = %d, want 0", nilSharded.IndexFor("k"))
	}
	if nilSharded.ShardFor("k") != nil {
		t.Fatal("nil ShardFor should return nil")
	}
	if nilSharded.Shard(0) != nil {
		t.Fatal("nil Shard should return nil")
	}
	if err := nilSharded.Each(func(int, *Cluster) error { return nil }); err != nil {
		t.Fatalf("nil Each = %v, want nil", err)
	}
	if err := nilSharded.Close(); err != nil {
		t.Fatalf("nil Close = %v, want nil", err)
	}
}

func TestShardedClusterEachError(t *testing.T) {
	s0, _ := NewCluster(NewSQLStore(nil))
	sharded, _ := NewShardedCluster(nil, s0)
	boom := errors.New("boom")
	err := sharded.Each(func(int, *Cluster) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("Each error = %v, want boom", err)
	}
}

func TestClusterCloseJoinsErrors(t *testing.T) {
	primary := NewSQLStore(nil)
	replica := NewSQLStore(nil)
	cluster, _ := NewCluster(primary, replica)
	if err := cluster.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestClusterQueryOneAllExecTransact(t *testing.T) {
	primary := NewSQLStore(fakeDBForCluster(t))
	replica := NewSQLStore(fakeDBForCluster(t))
	cluster, _ := NewCluster(primary, replica)

	// fake driver returns no rows, so Scan returns sql.ErrNoRows — valid outcome.
	if err := cluster.QueryOne(context.Background(), "SELECT 1", func(r *sql.Row) error { return r.Scan(new(int)) }); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("QueryOne: %v", err)
	}
	if err := cluster.QueryAll(context.Background(), "SELECT 1", func(r *sql.Rows) error { return nil }); err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if _, err := cluster.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := cluster.Transact(context.Background(), nil, func(context.Context, *sql.Tx) error { return nil }); err != nil {
		t.Fatalf("Transact: %v", err)
	}
}

func TestClusterQueryOneAllExecTransactNilGuards(t *testing.T) {
	var nilCluster *Cluster
	if err := nilCluster.QueryOne(context.Background(), "SELECT 1", nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil QueryOne = %v, want nil error", err)
	}
	if err := nilCluster.QueryAll(context.Background(), "SELECT 1", nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil QueryAll = %v, want nil error", err)
	}
	if _, err := nilCluster.Exec(context.Background(), "SELECT 1"); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil Exec = %v, want nil error", err)
	}
	if err := nilCluster.Transact(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil Transact = %v, want nil error", err)
	}
}

func fakeDBForCluster(t *testing.T) *sql.DB {
	t.Helper()
	registerFakeDriver.Do(func() { sql.Register(fakeDriverName, fakeDriver{}) })
	db, err := sql.Open(fakeDriverName, "")
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
