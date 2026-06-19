package limit

import (
	"testing"
	"time"
)

func TestTokenBucketNewDefaults(t *testing.T) {
	l := New(0, 0)
	if l.rate != 1 {
		t.Fatalf("rate = %v, want 1", l.rate)
	}
	if l.burst != 1 {
		t.Fatalf("burst = %v, want 1", l.burst)
	}
}

func TestTokenBucketAllowConsumesTokens(t *testing.T) {
	l := New(10, 2)
	if !l.Allow() {
		t.Fatal("first allow should succeed")
	}
	if !l.Allow() {
		t.Fatal("second allow should succeed")
	}
	if l.Allow() {
		t.Fatal("third allow should fail (burst=2)")
	}
}

func TestTokenBucketRefillsOverTime(t *testing.T) {
	l := New(100, 1)
	if !l.Allow() {
		t.Fatal("first allow should succeed")
	}
	if l.Allow() {
		t.Fatal("second allow should fail immediately")
	}
	time.Sleep(20 * time.Millisecond)
	if !l.Allow() {
		t.Fatal("allow should succeed after refill")
	}
}

func TestTokenBucketBurstLargerThanRate(t *testing.T) {
	l := New(1, 5)
	for i := 0; i < 5; i++ {
		if !l.Allow() {
			t.Fatalf("allow %d should succeed with burst=5", i+1)
		}
	}
	if l.Allow() {
		t.Fatal("sixth allow should fail")
	}
}
