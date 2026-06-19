package lock

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeepaliveRefreshesLease(t *testing.T) {
	locker := NewMemoryLocker(WithDefaultTTL(50 * time.Millisecond))
	lease, err := locker.Lock(context.Background(), "k", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	g := Keepalive(context.Background(), locker, lease, 50*time.Millisecond, WithGuardInterval(10*time.Millisecond))
	time.Sleep(120 * time.Millisecond)
	g.Stop()
	if g.Refreshes() == 0 {
		t.Fatalf("expected at least one refresh, got %d", g.Refreshes())
	}
	// Lease should still be held after the original TTL would have expired.
	if _, err := locker.Refresh(context.Background(), g.Lease(), 50*time.Millisecond); err != nil {
		t.Fatalf("lease not alive after keepalive: %v", err)
	}
}

func TestKeepaliveStopAllowsExpiry(t *testing.T) {
	locker := NewMemoryLocker(WithDefaultTTL(40 * time.Millisecond))
	lease, err := locker.Lock(context.Background(), "k", 40*time.Millisecond)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	g := Keepalive(context.Background(), locker, lease, 40*time.Millisecond, WithGuardInterval(10*time.Millisecond))
	g.Stop()
	// After stopping, the lease should eventually expire on its own.
	time.Sleep(60 * time.Millisecond)
	if _, err := locker.Refresh(context.Background(), lease, 40*time.Millisecond); !errors.Is(err, ErrLeaseExpired) && !errors.Is(err, ErrNotHeld) {
		t.Fatalf("expected lease to expire after stop, got %v", err)
	}
}

func TestKeepaliveReportsRefreshError(t *testing.T) {
	locker := NewMemoryLocker(WithDefaultTTL(50 * time.Millisecond))
	lease, err := locker.Lock(context.Background(), "k", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	// Release the lock so refresh fails.
	if err := locker.Unlock(context.Background(), lease); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	g := Keepalive(context.Background(), locker, lease, 50*time.Millisecond, WithGuardInterval(10*time.Millisecond))
	select {
	case err := <-g.Err():
		if err == nil {
			t.Fatal("expected error from guard")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for guard error")
	}
	<-g.Done()
}

func TestKeepaliveContextCancel(t *testing.T) {
	locker := NewMemoryLocker(WithDefaultTTL(50 * time.Millisecond))
	lease, err := locker.Lock(context.Background(), "k", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := Keepalive(ctx, locker, lease, 50*time.Millisecond, WithGuardInterval(10*time.Millisecond))
	cancel()
	select {
	case err := <-g.Err():
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for cancellation")
	}
	<-g.Done()
}
