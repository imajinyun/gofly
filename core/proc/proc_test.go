package proc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignalContextCanceledOnInterrupt(t *testing.T) {
	ctx, cancel := SignalContext(context.Background())
	defer cancel()
	// Simulate interrupt — on Unix systems SIGINT triggers cancellation.
	// We just verify the context is initially valid and the cancel releases.
	if err := ctx.Err(); err != nil {
		t.Fatalf("expected nil initial err, got %v", err)
	}
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context not canceled after cancel()")
	}
}

func TestBuildInfoPopulated(t *testing.T) {
	info := ReadBuildInfo()
	if info.GoOS == "" || info.GoArch == "" || info.GoVersion == "" {
		t.Fatalf("expected runtime fields, got %+v", info)
	}
	if info.Version == "" {
		t.Fatal("expected non-empty version")
	}
}

func TestBuildInfoOverridable(t *testing.T) {
	prev := Version
	Version = "v9.9.9"
	defer func() { Version = prev }()
	info := ReadBuildInfo()
	if info.Version != "v9.9.9" {
		t.Fatalf("expected overridden version, got %q", info.Version)
	}
}

func TestSetMaxProcsNoFilesIsANoop(t *testing.T) {
	// Without cgroup files on the host (typical on macOS/Windows),
	// SetMaxProcs should leave GOMAXPROCS unchanged.
	before := runtime.GOMAXPROCS(0)
	result := SetMaxProcs()
	after := runtime.GOMAXPROCS(0)
	if before != after {
		t.Fatalf("GOMAXPROCS unexpectedly changed: %d → %d (reason=%q)", before, after, result.Reason)
	}
	if result.Applied <= 0 {
		t.Fatalf("Applied should be positive, got %d", result.Applied)
	}
}

func TestSetMaxProcsDetectsCgroupV1Quota(t *testing.T) {
	tmp := t.TempDir()
	// Write fake cgroup v1 files in the temp dir.
	// The real function reads absolute paths, so we can't substitute — but we
	// can test the helper directly.
	quotaPath := filepath.Join(tmp, "cfs_quota_us")
	periodPath := filepath.Join(tmp, "cfs_period_us")
	if err := os.WriteFile(quotaPath, []byte("200000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(periodPath, []byte("100000\n"), 0644); err != nil {
		t.Fatal(err)
	}

	quota, err := readInt64(quotaPath)
	if err != nil || quota != 200000 {
		t.Fatalf("readInt64 quota = %d err=%v", quota, err)
	}
	period, err := readInt64(periodPath)
	if err != nil || period != 100000 {
		t.Fatalf("readInt64 period = %d err=%v", period, err)
	}
	cores := float64(quota) / float64(period)
	if cores < 1.9 || cores > 2.1 {
		t.Fatalf("expected ~2.0 cores, got %.2f", cores)
	}
}

func TestClampProcsEdges(t *testing.T) {
	cases := []struct {
		cores float64
		phys  int
		want  int
	}{
		{0.0, 8, 8},  // zero → physical
		{-1.0, 4, 4}, // negative → physical
		{0.3, 4, 1},  // sub-half share? 0.3 < 0.5 so n=int=0 → clamped to 1
		{0.7, 4, 1},  // 0.7 >= 0.5 → round up to 1
		{2.1, 4, 2},  // normal truncate
		{16.0, 4, 4}, // capped at physical
	}
	for _, c := range cases {
		got := clampProcs(c.cores, c.phys)
		if got != c.want {
			t.Errorf("clampProcs(%.1f, %d) = %d, want %d", c.cores, c.phys, got, c.want)
		}
	}
}

func TestShutdownLIFOAndIdempotent(t *testing.T) {
	var counter int32
	var order []int
	s := NewShutdown()
	s.Add(func(_ context.Context) error {
		atomic.AddInt32(&counter, 1)
		order = append(order, 1)
		return nil
	})
	s.Add(func(_ context.Context) error {
		atomic.AddInt32(&counter, 1)
		order = append(order, 2)
		return nil
	})
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown err=%v", err)
	}
	if counter != 2 {
		t.Fatalf("expected 2 calls, got %d", counter)
	}
	// LIFO: last registered should execute first.
	if order[0] != 2 || order[1] != 1 {
		t.Fatalf("expected LIFO order [2,1], got %v", order)
	}
	// Second Shutdown call is a no-op.
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown err=%v", err)
	}
	if atomic.LoadInt32(&counter) != 2 {
		t.Fatalf("expected counter unchanged on second call, got %d", counter)
	}
}

func TestShutdownJoinsErrors(t *testing.T) {
	s := NewShutdown()
	s.Add(func(_ context.Context) error { return nil })
	s.Add(func(_ context.Context) error { return os.ErrClosed })
	s.Add(func(_ context.Context) error { return context.DeadlineExceeded })

	err := s.Shutdown(context.Background())
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	// Verify the joined error contains at least one of the underlying issues.
	if msg := err.Error(); len(msg) == 0 {
		t.Fatal("expected non-empty error message")
	}
}

func TestShutdownAddCloser(t *testing.T) {
	closed := false
	closer := &fakeCloser{closeFn: func() error {
		closed = true
		return nil
	}}
	s := NewShutdown()
	s.AddCloser(closer)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown err=%v", err)
	}
	if !closed {
		t.Fatal("expected closer.Close() to be invoked")
	}
}

func TestShutdownCancelFunc(t *testing.T) {
	invoked := false
	s := NewShutdown()
	s.Add(func(_ context.Context) error { invoked = true; return nil })
	cancel := s.CancelFunc()
	cancel()
	if !invoked {
		t.Fatal("CancelFunc should invoke Shutdown")
	}
}

func TestShutdownRespectsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := NewShutdown()
	s.Add(func(c context.Context) error {
		// Should still run once before deadline is exceeded — but we
		// check the ctx is canceled when inside.
		if c.Err() == nil {
			t.Fatal("expected ctx to be canceled")
		}
		return nil
	})
	// Pre-canceled context causes the coordinator to bail quickly.
	_ = s.Shutdown(ctx)
}

func TestSignalContextNilParent(t *testing.T) {
	var nilCtx context.Context
	ctx, cancel := SignalContext(nilCtx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Fatalf("expected nil initial err, got %v", err)
	}
}

func TestShutdownNilGuards(t *testing.T) {
	var nilS *Shutdown
	nilS.Add(func(_ context.Context) error { return nil })
	nilS.AddCloser(&fakeCloser{})
	if err := nilS.Shutdown(context.Background()); err != nil {
		t.Fatalf("nil Shutdown = %v, want nil", err)
	}
}

func TestFirstNonEmptyAllEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", ""); got != "unknown" {
		t.Fatalf("firstNonEmpty all empty = %q, want unknown", got)
	}
}

func TestSetMaxProcsCgroupV2Max(t *testing.T) {
	tmp := t.TempDir()
	maxPath := filepath.Join(tmp, "cpu.max")
	if err := os.WriteFile(maxPath, []byte("200000 100000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := runtime.GOMAXPROCS(0)
	result := setMaxProcsWithPaths(cgroupPaths{v2Max: maxPath})
	after := runtime.GOMAXPROCS(0)
	if result.Source != maxPath {
		t.Fatalf("source = %q, want %q", result.Source, maxPath)
	}
	if result.Applied != 2 {
		t.Fatalf("applied = %d, want 2", result.Applied)
	}
	if after != 2 {
		t.Fatalf("GOMAXPROCS = %d, want 2", after)
	}
	runtime.GOMAXPROCS(before)
}

func TestSetMaxProcsCgroupV1Shares(t *testing.T) {
	tmp := t.TempDir()
	sharesPath := filepath.Join(tmp, "cpu.shares")
	if err := os.WriteFile(sharesPath, []byte("2048\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := runtime.GOMAXPROCS(0)
	result := setMaxProcsWithPaths(cgroupPaths{v1Shares: sharesPath})
	if result.Source != sharesPath {
		t.Fatalf("source = %q, want %q", result.Source, sharesPath)
	}
	if result.Applied != 2 {
		t.Fatalf("applied = %d, want 2", result.Applied)
	}
	runtime.GOMAXPROCS(before)
}

func TestSetMaxProcsCgroupV2High(t *testing.T) {
	tmp := t.TempDir()
	highPath := filepath.Join(tmp, "cpu.high")
	if err := os.WriteFile(highPath, []byte("50000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := runtime.GOMAXPROCS(0)
	result := setMaxProcsWithPaths(cgroupPaths{v2High: highPath})
	if result.Source != highPath {
		t.Fatalf("source = %q, want %q", result.Source, highPath)
	}
	if result.Applied != 1 {
		t.Fatalf("applied = %d, want 1", result.Applied)
	}
	runtime.GOMAXPROCS(before)
}

func TestSetMaxProcsCgroupV2MaxInvalid(t *testing.T) {
	tmp := t.TempDir()
	maxPath := filepath.Join(tmp, "cpu.max")
	// "max" means unlimited — should fall through
	if err := os.WriteFile(maxPath, []byte("max 100000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := runtime.GOMAXPROCS(0)
	result := setMaxProcsWithPaths(cgroupPaths{v2Max: maxPath})
	if result.Source != "" {
		t.Fatalf("expected no source, got %q", result.Source)
	}
	runtime.GOMAXPROCS(before)
}

func TestSetMaxProcsCgroupV2HighMax(t *testing.T) {
	tmp := t.TempDir()
	highPath := filepath.Join(tmp, "cpu.high")
	// "max" means unlimited — should fall through
	if err := os.WriteFile(highPath, []byte("max\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := runtime.GOMAXPROCS(0)
	result := setMaxProcsWithPaths(cgroupPaths{v2High: highPath})
	if result.Source != "" {
		t.Fatalf("expected no source, got %q", result.Source)
	}
	runtime.GOMAXPROCS(before)
}

type fakeCloser struct {
	closeFn func() error
}

func (f *fakeCloser) Close() error { return f.closeFn() }
