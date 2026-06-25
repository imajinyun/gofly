package lock

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/kv"
)

func TestMemoryLockerTryLockUnlockOwnership(t *testing.T) {
	locker := newDeterministicLocker()
	lease, err := locker.TryLock(context.Background(), "job", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if _, err := locker.TryLock(context.Background(), "job", time.Minute); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock = %v, want ErrLocked", err)
	}
	if err := locker.Unlock(context.Background(), Lease{Key: "job", Token: "wrong"}); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock wrong token = %v, want ErrNotHeld", err)
	}
	if err := locker.Unlock(context.Background(), lease); err != nil {
		t.Fatalf("Unlock owner: %v", err)
	}
	if snapshot := locker.Snapshot(); snapshot.Active != 0 || snapshot.Acquired != 1 || snapshot.Released != 1 || snapshot.Failed != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestMemoryLockerLockWaitsForRelease(t *testing.T) {
	locker := newDeterministicLocker()
	lease, err := locker.TryLock(context.Background(), "job", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got := make(chan Lease, 1)
	errs := make(chan error, 1)
	go func() {
		lease, err := locker.Lock(ctx, "job", time.Minute)
		if err != nil {
			errs <- err
			return
		}
		got <- lease
	}()
	time.Sleep(30 * time.Millisecond)
	select {
	case lease := <-got:
		t.Fatalf("Lock acquired before release: %+v", lease)
	default:
	}
	if err := locker.Unlock(context.Background(), lease); err != nil {
		t.Fatalf("Unlock owner: %v", err)
	}
	select {
	case err := <-errs:
		t.Fatalf("Lock waiter error: %v", err)
	case lease := <-got:
		if lease.Key != "job" || lease.Token == "" {
			t.Fatalf("unexpected lease: %+v", lease)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lock acquisition")
	}
}

func TestMemoryLockerExpiryAndRefresh(t *testing.T) {
	locker := newDeterministicLocker()
	lease, err := locker.TryLock(context.Background(), "leader", 25*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	refreshed, err := locker.Refresh(context.Background(), lease, time.Minute)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !refreshed.ExpiresAt.After(lease.ExpiresAt) || refreshed.Token != lease.Token {
		t.Fatalf("unexpected refreshed lease: before=%+v after=%+v", lease, refreshed)
	}
	if err := locker.Unlock(context.Background(), refreshed); err != nil {
		t.Fatalf("Unlock refreshed lease: %v", err)
	}
	expiring, err := locker.TryLock(context.Background(), "leader", 20*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock expiring: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := locker.TryLock(context.Background(), "leader", time.Minute); err != nil {
		t.Fatalf("TryLock after expiry: %v", err)
	}
	if err := locker.Unlock(context.Background(), expiring); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock expired old lease = %v, want ErrNotHeld", err)
	}
	if snapshot := locker.Snapshot(); snapshot.Expired == 0 || snapshot.Refreshed != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestMemoryLockerContextCancellation(t *testing.T) {
	locker := newDeterministicLocker()
	if _, err := locker.TryLock(context.Background(), "job", time.Minute); err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := locker.Lock(ctx, "job", time.Minute); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock canceled = %v, want DeadlineExceeded", err)
	}
}

func TestKVLockerUsesStoreSetNXAndTokenOwnership(t *testing.T) {
	store := kv.NewMemoryStore()
	locker := NewKVLocker(store,
		WithKVRetryInterval(time.Millisecond),
		WithKVTokenGenerator(sequenceTokenGenerator()),
	)
	lease, err := locker.TryLock(context.Background(), "leader", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if _, err := locker.TryLock(context.Background(), "leader", time.Minute); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock = %v, want ErrLocked", err)
	}
	if err := locker.Unlock(context.Background(), Lease{Key: "leader", Token: "wrong"}); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock wrong token = %v, want ErrNotHeld", err)
	}
	refreshed, err := locker.Refresh(context.Background(), lease, time.Minute)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.Token != lease.Token || !refreshed.ExpiresAt.After(lease.ExpiresAt) {
		t.Fatalf("refreshed = %+v, original = %+v", refreshed, lease)
	}
	if err := locker.Unlock(context.Background(), refreshed); err != nil {
		t.Fatalf("Unlock refreshed: %v", err)
	}
	if ok, err := store.Exists(context.Background(), "leader"); err != nil || ok {
		t.Fatalf("Exists after unlock = %v, %v; want false, nil", ok, err)
	}
}

func TestKVLockerLockWaitsForStoreLeaseExpiry(t *testing.T) {
	store := kv.NewMemoryStore()
	locker := NewKVLocker(store,
		WithKVRetryInterval(time.Millisecond),
		WithKVTokenGenerator(sequenceTokenGenerator()),
	)
	if _, err := locker.TryLock(context.Background(), "leader", 20*time.Millisecond); err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := locker.Lock(ctx, "leader", time.Minute)
	if err != nil {
		t.Fatalf("Lock after expiry: %v", err)
	}
	if lease.Key != "leader" || lease.Token == "" {
		t.Fatalf("lease = %+v, want leader lease", lease)
	}
}

func newDeterministicLocker() *MemoryLocker {
	var seq atomic.Int64
	return NewMemoryLocker(
		WithRetryInterval(time.Millisecond),
		WithTokenGenerator(func() (string, error) { return "token-" + strconv.FormatInt(seq.Add(1), 10), nil }),
	)
}

func sequenceTokenGenerator() func() (string, error) {
	var seq atomic.Int64
	return func() (string, error) { return "token-" + strconv.FormatInt(seq.Add(1), 10), nil }
}

func TestMemoryLockerNilContextAndEmptyKey(t *testing.T) {
	locker := newDeterministicLocker()
	var nilCtx context.Context
	if _, err := locker.TryLock(nilCtx, "k", time.Minute); err != nil {
		t.Fatalf("TryLock nil ctx = %v, want nil", err)
	}
	if _, err := locker.TryLock(context.Background(), "", time.Minute); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("TryLock empty key = %v, want ErrNotHeld", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := locker.TryLock(ctx, "k", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("TryLock canceled ctx = %v, want Canceled", err)
	}
}

func TestMemoryLockerRefreshErrors(t *testing.T) {
	locker := newDeterministicLocker()
	lease, err := locker.TryLock(context.Background(), "k", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	// wrong token
	_, err = locker.Refresh(context.Background(), Lease{Key: "k", Token: "wrong"}, time.Minute)
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Refresh wrong token = %v, want ErrNotHeld", err)
	}

	// missing key
	_, err = locker.Refresh(context.Background(), Lease{Key: "missing", Token: lease.Token}, time.Minute)
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Refresh missing key = %v, want ErrNotHeld", err)
	}

	// expired lease (cleanup removes it, so ErrNotHeld)
	short, err := locker.TryLock(context.Background(), "expire", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	_, err = locker.Refresh(context.Background(), short, time.Minute)
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Refresh expired = %v, want ErrNotHeld (cleaned up)", err)
	}

	// canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = locker.Refresh(ctx, lease, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Refresh canceled ctx = %v, want Canceled", err)
	}
}

func TestMemoryLockerUnlockErrors(t *testing.T) {
	locker := newDeterministicLocker()
	lease, err := locker.TryLock(context.Background(), "k", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	// wrong token
	if err := locker.Unlock(context.Background(), Lease{Key: "k", Token: "wrong"}); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock wrong token = %v, want ErrNotHeld", err)
	}

	// missing key
	if err := locker.Unlock(context.Background(), Lease{Key: "missing", Token: lease.Token}); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock missing key = %v, want ErrNotHeld", err)
	}

	// expired lease (cleanup removes it, so ErrNotHeld)
	short, err := locker.TryLock(context.Background(), "expire", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := locker.Unlock(context.Background(), short); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock expired = %v, want ErrNotHeld (cleaned up)", err)
	}

	// canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := locker.Unlock(ctx, lease); !errors.Is(err, context.Canceled) {
		t.Fatalf("Unlock canceled ctx = %v, want Canceled", err)
	}
}

func TestMemoryLockerDefaultTTLOption(t *testing.T) {
	locker := NewMemoryLocker(WithDefaultTTL(5 * time.Second))
	lease, err := locker.TryLock(context.Background(), "k", 0)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if time.Until(lease.ExpiresAt) < 4*time.Second {
		t.Fatalf("default TTL too short: %v", time.Until(lease.ExpiresAt))
	}
}

func TestKVLockerNilAndEmptyKey(t *testing.T) {
	var nilLocker *KVLocker
	if _, err := nilLocker.TryLock(context.Background(), "k", time.Minute); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("nil TryLock = %v, want ErrNotHeld", err)
	}

	store := kv.NewMemoryStore()
	locker := NewKVLocker(store, WithKVTokenGenerator(sequenceTokenGenerator()))
	if _, err := locker.TryLock(context.Background(), "", time.Minute); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("TryLock empty key = %v, want ErrNotHeld", err)
	}
}

func TestKVLockerRefreshErrors(t *testing.T) {
	store := kv.NewMemoryStore()
	locker := NewKVLocker(store,
		WithKVRetryInterval(time.Millisecond),
		WithKVTokenGenerator(sequenceTokenGenerator()),
	)
	lease, err := locker.TryLock(context.Background(), "k", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	// wrong token in store
	_ = store.Set(context.Background(), "k", []byte("tampered"), time.Minute)
	_, err = locker.Refresh(context.Background(), lease, time.Minute)
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Refresh tampered token = %v, want ErrNotHeld", err)
	}

	// missing key (expired)
	_, _ = store.Delete(context.Background(), "k")
	_, err = locker.Refresh(context.Background(), lease, time.Minute)
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Refresh missing key = %v, want ErrLeaseExpired", err)
	}

	// nil locker
	var nilLocker *KVLocker
	_, err = nilLocker.Refresh(context.Background(), lease, time.Minute)
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("nil Refresh = %v, want ErrNotHeld", err)
	}

	// empty lease key
	_, err = locker.Refresh(context.Background(), Lease{Key: "", Token: "t"}, time.Minute)
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Refresh empty key = %v, want ErrNotHeld", err)
	}

	// empty lease token
	_, err = locker.Refresh(context.Background(), Lease{Key: "k", Token: ""}, time.Minute)
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Refresh empty token = %v, want ErrNotHeld", err)
	}
}

func TestKVLockerUnlockErrors(t *testing.T) {
	store := kv.NewMemoryStore()
	locker := NewKVLocker(store,
		WithKVRetryInterval(time.Millisecond),
		WithKVTokenGenerator(sequenceTokenGenerator()),
	)
	lease, err := locker.TryLock(context.Background(), "k", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	// wrong token
	if err := locker.Unlock(context.Background(), Lease{Key: "k", Token: "wrong"}); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock wrong token = %v, want ErrNotHeld", err)
	}

	// missing key
	_, _ = store.Delete(context.Background(), "k")
	if err := locker.Unlock(context.Background(), lease); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock missing key = %v, want ErrNotHeld", err)
	}

	// nil locker
	var nilLocker *KVLocker
	if err := nilLocker.Unlock(context.Background(), lease); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("nil Unlock = %v, want ErrNotHeld", err)
	}

	// empty key
	if err := locker.Unlock(context.Background(), Lease{Key: "", Token: "t"}); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock empty key = %v, want ErrNotHeld", err)
	}

	// empty token
	if err := locker.Unlock(context.Background(), Lease{Key: "k", Token: ""}); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock empty token = %v, want ErrNotHeld", err)
	}
}

func TestKVLockerDefaultTTLOption(t *testing.T) {
	store := kv.NewMemoryStore()
	locker := NewKVLocker(store, WithKVDefaultTTL(5*time.Second), WithKVTokenGenerator(sequenceTokenGenerator()))
	lease, err := locker.TryLock(context.Background(), "k", 0)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if time.Until(lease.ExpiresAt) < 4*time.Second {
		t.Fatalf("default TTL too short: %v", time.Until(lease.ExpiresAt))
	}
}

func TestKVLockerNilStore(t *testing.T) {
	locker := NewKVLocker(nil)
	if _, err := locker.TryLock(context.Background(), "k", time.Minute); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("nil store TryLock = %v, want ErrNotHeld", err)
	}
}

func TestKVLockerTTLNilGuard(t *testing.T) {
	var nilLocker *KVLocker
	if d := nilLocker.ttl(0); d != 30*time.Second {
		t.Fatalf("nil ttl = %v, want 30s", d)
	}
}
