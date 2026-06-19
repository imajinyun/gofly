package rpc

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnPoolReusesConnections(t *testing.T) {
	pool, cleanup, created, closed := newTestConnPool(ConnPoolConfig{MaxIdle: 2, MaxActive: 2})
	defer cleanup()

	first, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	second, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if first != second {
		t.Fatalf("second conn = %p, want reused %p", second, first)
	}
	if got := created.Load(); got != 1 {
		t.Fatalf("created = %d, want 1", got)
	}
	if stats := pool.Snapshot(); stats.Idle != 0 || stats.Active != 1 || stats.Created != 1 || stats.Reused != 1 {
		t.Fatalf("stats = %#v, want active reused connection", stats)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if stats := pool.Snapshot(); stats.Idle != 1 || stats.Active != 1 {
		t.Fatalf("stats after return = %#v, want one idle active connection", stats)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("pool Close: %v", err)
	}
	if got := closed.Load(); got != 1 {
		t.Fatalf("underlying closed = %d, want 1", got)
	}
}

func TestConnPoolMaxActiveWaitsForReturn(t *testing.T) {
	pool, cleanup, _, _ := newTestConnPool(ConnPoolConfig{MaxIdle: 1, MaxActive: 1, WaitInterval: time.Millisecond})
	defer cleanup()

	first, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	got := make(chan struct {
		conn *PooledConn
		err  error
	}, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		conn, err := pool.Get(ctx)
		got <- struct {
			conn *PooledConn
			err  error
		}{conn: conn, err: err}
	}()

	select {
	case res := <-got:
		t.Fatalf("second Get returned before first was put back: %#v", res)
	case <-time.After(20 * time.Millisecond):
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	select {
	case res := <-got:
		if res.err != nil {
			t.Fatalf("second Get: %v", res.err)
		}
		if res.conn != first {
			t.Fatalf("second conn = %p, want reused first %p", res.conn, first)
		}
		_ = res.conn.Close()
	case <-time.After(time.Second):
		t.Fatal("second Get did not resume after first connection was returned")
	}
}

func TestConnPoolContextCancellation(t *testing.T) {
	pool, cleanup, _, _ := newTestConnPool(ConnPoolConfig{MaxIdle: 1, MaxActive: 1, WaitInterval: time.Millisecond})
	defer cleanup()

	first, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	defer first.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := pool.Get(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Get error = %v, want DeadlineExceeded", err)
	}
	if stats := pool.Snapshot(); stats.Waits == 0 {
		t.Fatalf("stats = %#v, want waits recorded", stats)
	}
}

func TestConnPoolDiscardAndClose(t *testing.T) {
	pool, cleanup, _, closed := newTestConnPool(ConnPoolConfig{MaxIdle: 1, MaxActive: 2})
	defer cleanup()

	conn, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := conn.Discard(); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if got := closed.Load(); got != 1 {
		t.Fatalf("closed after discard = %d, want 1", got)
	}
	if stats := pool.Snapshot(); stats.Active != 0 || stats.Closed != 1 {
		t.Fatalf("stats after discard = %#v, want no active and one closed", stats)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := pool.Get(context.Background()); !errors.Is(err, ErrConnPoolClosed) {
		t.Fatalf("Get after Close error = %v, want ErrConnPoolClosed", err)
	}
}

func TestConnPoolPutAndDiscardNilAreNoops(t *testing.T) {
	pool, cleanup, _, closed := newTestConnPool(ConnPoolConfig{MaxIdle: 1, MaxActive: 1})
	defer cleanup()

	if err := pool.Put(nil); err != nil {
		t.Fatalf("Put(nil) error = %v, want nil", err)
	}
	if err := pool.Discard(nil); err != nil {
		t.Fatalf("Discard(nil) error = %v, want nil", err)
	}
	if got := closed.Load(); got != 0 {
		t.Fatalf("closed after nil operations = %d, want 0", got)
	}
}

func TestConnPoolDefaultAndNilDialerBoundaries_BitsUT(t *testing.T) {
	pool := NewConnPoolWithDialer(nil, ConnPoolConfig{MaxIdle: -1, MaxActive: -1, WaitInterval: -time.Second})
	if pool.conf.MaxIdle != DefaultConnPoolConfig().MaxIdle || pool.conf.MaxActive != DefaultConnPoolConfig().MaxActive {
		t.Fatalf("normalized pool config = %#v, want defaults for non-positive limits", pool.conf)
	}
	if pool.conf.WaitInterval != DefaultConnPoolConfig().WaitInterval {
		t.Fatalf("WaitInterval = %v, want default %v", pool.conf.WaitInterval, DefaultConnPoolConfig().WaitInterval)
	}
	if _, err := pool.Get(context.Background()); err == nil || !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "dialer is nil") {
		t.Fatalf("nil dialer Get error = %v, want wrapped nil dialer error", err)
	}
	if err := (*ConnPool)(nil).Close(); err != nil {
		t.Fatalf("nil pool Close = %v, want nil", err)
	}
}

func TestNewConnPoolConfiguresDefaultDialer_BitsUT(t *testing.T) {
	pool := NewConnPool("tcp", "127.0.0.1:1", ConnPoolConfig{DialTimeout: time.Millisecond})
	defer pool.Close()
	if pool == nil || pool.dial == nil {
		t.Fatal("NewConnPool returned nil pool or dialer")
	}
	if pool.conf.DialTimeout != time.Millisecond {
		t.Fatalf("DialTimeout = %v, want 1ms", pool.conf.DialTimeout)
	}
}

func TestHTTPServerGovernanceSnapshotBoundaries_BitsUT(t *testing.T) {
	server := NewServer(WithServerAdaptiveBreaker(nil))
	snapshot := server.Governance()
	if len(snapshot.Components) != 1 {
		t.Fatalf("Governance components = %#v, want one adaptive breaker", snapshot.Components)
	}
	component := snapshot.Components[0]
	if component.Name != "adaptive-breaker" || component.Kind != "adaptive_breaker" || component.Target != "server" {
		t.Fatalf("Governance component = %#v, want adaptive-breaker server component", component)
	}
}

func TestConnPoolPutReturnsConnectionToPool(t *testing.T) {
	pool, cleanup, _, _ := newTestConnPool(ConnPoolConfig{MaxIdle: 1, MaxActive: 1})
	defer cleanup()

	conn, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := pool.Put(conn); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if stats := pool.Snapshot(); stats.Idle != 1 || stats.Active != 1 {
		t.Fatalf("stats after Put = %#v, want one idle active connection", stats)
	}
	reused, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("Get reused: %v", err)
	}
	if reused != conn {
		t.Fatalf("reused conn = %p, want %p", reused, conn)
	}
	_ = reused.Close()
}

func TestPooledConnTransport(t *testing.T) {
	var nilConn *PooledConn
	if got := nilConn.Transport(); got != nil {
		t.Fatalf("nil PooledConn Transport() = %v, want nil", got)
	}

	pool, cleanup, _, _ := newTestConnPool(ConnPoolConfig{MaxIdle: 1, MaxActive: 1})
	defer cleanup()
	conn, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer conn.Close()
	if got := conn.Transport(); got == nil {
		t.Fatal("PooledConn.Transport() = nil, want transport")
	}
}

func TestConnPoolDialFailureReleasesActiveSlot_BitsUT(t *testing.T) {
	wantErr := errors.New("dial failed")
	pool := NewConnPoolWithDialer(func(context.Context) (net.Conn, error) {
		return nil, wantErr
	}, ConnPoolConfig{MaxActive: 1})

	if _, err := pool.Get(context.Background()); err == nil || !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("Get dial error = %v, want wrapped dial failure", err)
	}
	if stats := pool.Snapshot(); stats.Active != 0 || stats.Created != 0 {
		t.Fatalf("stats after dial failure = %#v, want active released and no created connection", stats)
	}
}

func TestConnPoolExpiredIdleIsClosedAndRedialed_BitsUT(t *testing.T) {
	pool, cleanup, created, closed := newTestConnPool(ConnPoolConfig{MaxIdle: 1, MaxActive: 1, IdleTimeout: time.Minute})
	defer cleanup()

	conn, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close to idle: %v", err)
	}
	pool.mu.Lock()
	conn.lastUsed = time.Now().Add(-2 * time.Minute)
	pool.mu.Unlock()

	replacement, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("Get replacement: %v", err)
	}
	if replacement == conn {
		t.Fatal("Get returned expired idle connection, want redialed replacement")
	}
	if got := created.Load(); got != 2 {
		t.Fatalf("created = %d, want 2", got)
	}
	if got := closed.Load(); got != 1 {
		t.Fatalf("closed expired = %d, want 1", got)
	}
	_ = replacement.Close()
}

func TestConnPoolMaxIdleAndNilSnapshot_BitsUT(t *testing.T) {
	pool, cleanup, _, closed := newTestConnPool(ConnPoolConfig{MaxIdle: 1, MaxActive: 2})
	defer cleanup()

	first, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	second, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := closed.Load(); got != 1 {
		t.Fatalf("closed due to max idle = %d, want 1", got)
	}
	if stats := (*ConnPool)(nil).Snapshot(); stats != (ConnPoolStats{}) {
		t.Fatalf("nil Snapshot = %#v, want zero stats", stats)
	}
}

func newTestConnPool(conf ConnPoolConfig) (*ConnPool, func(), *atomic.Int64, *atomic.Int64) {
	var mu sync.Mutex
	var servers []net.Conn
	var created atomic.Int64
	var closed atomic.Int64
	pool := NewConnPoolWithDialer(func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		mu.Lock()
		servers = append(servers, server)
		mu.Unlock()
		created.Add(1)
		return &trackedConn{Conn: client, closed: &closed}, nil
	}, conf)
	cleanup := func() {
		_ = pool.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, server := range servers {
			_ = server.Close()
		}
	}
	return pool, cleanup, &created, &closed
}

type trackedConn struct {
	net.Conn
	closed *atomic.Int64
	once   sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { c.closed.Add(1) })
	return c.Conn.Close()
}
