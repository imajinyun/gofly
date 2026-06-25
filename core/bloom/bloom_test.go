package bloom

import (
	"context"
	"sync"
	"testing"
)

func TestFilterNoFalseNegatives(t *testing.T) {
	f := New(1000, 0.01)
	keys := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	for _, k := range keys {
		f.AddString(k)
	}
	for _, k := range keys {
		if !f.ContainsString(k) {
			t.Fatalf("Contains(%q) = false, want true (false negative)", k)
		}
	}
}

func TestOptimalParamsAndDistributedStatsBoundaries(t *testing.T) {
	tests := []struct {
		name string
		n    uint64
		p    float64
	}{
		{name: "zero capacity defaults", n: 0, p: 0.01},
		{name: "zero false positive defaults", n: 10, p: 0},
		{name: "negative false positive defaults", n: 10, p: -0.1},
		{name: "one false positive defaults", n: 10, p: 1},
		{name: "normal input", n: 100, p: 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bits, hashes := optimalParams(tt.n, tt.p)
			if bits == 0 || hashes <= 0 {
				t.Fatalf("optimalParams(%d, %f) = bits=%d hashes=%d, want positive sizing", tt.n, tt.p, bits, hashes)
			}
		})
	}

	if stats := ((*DistributedFilter)(nil)).Stats(); stats != (Stats{}) {
		t.Fatalf("nil distributed stats = %#v, want zero", stats)
	}
	distributed := NewDistributed(newFakeBitStore(), "users", 128, 0.01)
	stats := distributed.Stats()
	if stats.Bits == 0 || stats.Hashes <= 0 || stats.Added != 0 {
		t.Fatalf("distributed stats = %#v, want sizing without local added counter", stats)
	}
}

func TestFilterFalsePositiveRateBounded(t *testing.T) {
	const n = 2000
	f := New(n, 0.01)
	for i := 0; i < n; i++ {
		f.AddString("present-" + itoa(i))
	}
	falsePositives := 0
	const trials = 5000
	for i := 0; i < trials; i++ {
		if f.ContainsString("absent-" + itoa(i)) {
			falsePositives++
		}
	}
	rate := float64(falsePositives) / float64(trials)
	if rate > 0.05 {
		t.Fatalf("false positive rate = %.4f, want <= 0.05", rate)
	}
}

func TestFilterReset(t *testing.T) {
	f := New(100, 0.01)
	f.AddString("key")
	if !f.ContainsString("key") {
		t.Fatal("Contains before reset = false")
	}
	f.Reset()
	if f.ContainsString("key") {
		t.Fatal("Contains after reset = true, want false")
	}
	if got := f.Stats().Added; got != 0 {
		t.Fatalf("added after reset = %d, want 0", got)
	}
}

func TestFilterConcurrentAccess(t *testing.T) {
	f := New(10000, 0.01)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				key := "k-" + itoa(base*500+j)
				f.AddString(key)
				f.ContainsString(key)
			}
		}(i)
	}
	wg.Wait()
}

// fakeBitStore is an in-memory BitStore for distributed filter tests.
type fakeBitStore struct {
	mu   sync.Mutex
	bits map[string]map[uint64]int
}

func newFakeBitStore() *fakeBitStore {
	return &fakeBitStore{bits: make(map[string]map[uint64]int)}
}

func (s *fakeBitStore) SetBit(_ context.Context, key string, offset uint64, value int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bits[key] == nil {
		s.bits[key] = make(map[uint64]int)
	}
	prev := s.bits[key][offset]
	s.bits[key][offset] = value
	return int64(prev), nil
}

func (s *fakeBitStore) GetBit(_ context.Context, key string, offset uint64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bits[key] == nil {
		return 0, nil
	}
	return int64(s.bits[key][offset]), nil
}

func TestDistributedFilterRoundTrip(t *testing.T) {
	store := newFakeBitStore()
	f := NewDistributed(store, "bf:test", 1000, 0.01)
	ctx := context.Background()
	if err := f.AddString(ctx, "user-1"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	present, err := f.ContainsString(ctx, "user-1")
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if !present {
		t.Fatal("Contains(user-1) = false, want true")
	}
	absent, err := f.ContainsString(ctx, "user-2")
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if absent {
		t.Fatal("Contains(user-2) = true, want false")
	}
}

func TestDistributedFilterNilStore(t *testing.T) {
	var f *DistributedFilter
	if _, err := f.ContainsString(context.Background(), "x"); err != ErrStoreNil {
		t.Fatalf("err = %v, want ErrStoreNil", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
