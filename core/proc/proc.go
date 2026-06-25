// Package proc provides low-level process primitives used by gofly
// applications: signal-aware contexts, CPU-quota-aware GOMAXPROCS,
// build-info embedding, and a generic Shutdown coordinator.
package proc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ------------------------------------------------------------------
// signal-aware context
// ------------------------------------------------------------------

// SignalContext returns a context that is canceled on SIGINT or SIGTERM.
// The caller must invoke the returned cancel function to release resources.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// ------------------------------------------------------------------
// GOMAXPROCS — container-aware CPU quota detection
// ------------------------------------------------------------------

const (
	// cgroup v1 paths (Kubernetes / Docker on older hosts)
	cgroupV1CPUShares = "/sys/fs/cgroup/cpu/cpu.shares"
	cgroupV1CPUQuota  = "/sys/fs/cgroup/cpu/cpu.cfs_quota_us"
	cgroupV1CPUPeriod = "/sys/fs/cgroup/cpu/cpu.cfs_period_us"

	// cgroup v2 paths (systemd unified hierarchy)
	cgroupV2Max  = "/sys/fs/cgroup/cpu.max"
	cgroupV2High = "/sys/fs/cgroup/cpu.high"
)

// MaxProcsResult describes the GOMAXPROCS decision made by SetMaxProcs.
type MaxProcsResult struct {
	Queried  []string `json:"queried"`            // files / knobs inspected
	Detected float64  `json:"detected,omitempty"` // detected CPU quota (cores)
	Applied  int      `json:"applied"`            // value applied as GOMAXPROCS
	Reason   string   `json:"reason"`             // human-readable reason
	Source   string   `json:"source,omitempty"`   // file that produced the value
}

// SetMaxProcs adjusts runtime.GOMAXPROCS based on the cgroup CPU quota
// observed at the well-known Kubernetes / Docker paths. A minimum of 1
// and a maximum of the physical CPU count are enforced. It returns a
// MaxProcsResult describing the decision so it can be logged.
//
// If no cgroup quota is detected (bare-metal or non-containerized host),
// GOMAXPROCS is left untouched and the result's Reason reflects this.
// cgroupPaths groups the cgroup files inspected by SetMaxProcs.
type cgroupPaths struct {
	v2Max    string
	v1Quota  string
	v1Period string
	v1Shares string
	v2High   string
}

var defaultCgroupPaths = cgroupPaths{
	v2Max:    cgroupV2Max,
	v1Quota:  cgroupV1CPUQuota,
	v1Period: cgroupV1CPUPeriod,
	v1Shares: cgroupV1CPUShares,
	v2High:   cgroupV2High,
}

func setMaxProcsWithPaths(paths cgroupPaths) MaxProcsResult {
	result := MaxProcsResult{Applied: runtime.GOMAXPROCS(0)}

	phys := runtime.NumCPU()
	result.Reason = fmt.Sprintf("physical CPU count = %d; no cgroup quota detected", phys)
	result.Queried = []string{paths.v2Max, paths.v1Quota, paths.v1Quota + "+" + paths.v1Period}

	// ---- cgroup v2 cpu.max: "<quota> <period>" ----
	if data, err := os.ReadFile(paths.v2Max); err == nil {
		parts := strings.Fields(strings.TrimSpace(string(data)))
		if len(parts) >= 2 && parts[0] != "max" {
			quota, errQ := strconv.ParseInt(parts[0], 10, 64)
			period, errP := strconv.ParseInt(parts[1], 10, 64)
			if errQ == nil && errP == nil && period > 0 && quota > 0 {
				cores := float64(quota) / float64(period)
				applied := clampProcs(cores, phys)
				runtime.GOMAXPROCS(applied)
				return MaxProcsResult{
					Queried:  []string{paths.v2Max},
					Detected: cores,
					Applied:  applied,
					Source:   paths.v2Max,
					Reason:   fmt.Sprintf("cgroup v2 cpu.max: %d/%d us = %.2f cores → GOMAXPROCS=%d", quota, period, cores, applied),
				}
			}
		}
	}

	// ---- cgroup v1 cpu.cfs_quota_us + cpu.cfs_period_us ----
	if quota, err := readInt64(paths.v1Quota); err == nil && quota > 0 {
		period, err := readInt64(paths.v1Period)
		if err == nil && period > 0 {
			cores := float64(quota) / float64(period)
			applied := clampProcs(cores, phys)
			runtime.GOMAXPROCS(applied)
			return MaxProcsResult{
				Queried:  []string{paths.v1Quota, paths.v1Period},
				Detected: cores,
				Applied:  applied,
				Source:   paths.v1Quota,
				Reason:   fmt.Sprintf("cgroup v1 cfs_quota_us/cfs_period_us: %d/%d = %.2f cores → GOMAXPROCS=%d", quota, period, cores, applied),
			}
		}
	}

	// ---- cgroup v1 cpu.shares (soft quota): 1 share = 1/1024 of a CPU ----
	if shares, err := readInt64(paths.v1Shares); err == nil && shares > 0 {
		cores := float64(shares) / 1024.0
		applied := clampProcs(cores, phys)
		runtime.GOMAXPROCS(applied)
		return MaxProcsResult{
			Queried:  []string{paths.v1Shares},
			Detected: cores,
			Applied:  applied,
			Source:   paths.v1Shares,
			Reason:   fmt.Sprintf("cgroup v1 cpu.shares: %d → %.2f cores → GOMAXPROCS=%d", shares, cores, applied),
		}
	}

	// ---- cgroup v2 cpu.high (best-effort throttle) ----
	if data, err := os.ReadFile(paths.v2High); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" && v != "max" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				// cpu.high is in microseconds — interpret as a 100ms period default
				cores := float64(n) / 100000.0
				applied := clampProcs(cores, phys)
				runtime.GOMAXPROCS(applied)
				return MaxProcsResult{
					Queried:  []string{paths.v2High},
					Detected: cores,
					Applied:  applied,
					Source:   paths.v2High,
					Reason:   fmt.Sprintf("cgroup v2 cpu.high: %d us / 100ms period ≈ %.2f cores → GOMAXPROCS=%d", n, cores, applied),
				}
			}
		}
	}

	return result
}

// SetMaxProcs adjusts runtime.GOMAXPROCS based on the cgroup CPU quota
// observed at the well-known Kubernetes / Docker paths. A minimum of 1
// and a maximum of the physical CPU count are enforced. It returns a
// MaxProcsResult describing the decision so it can be logged.
//
// If no cgroup quota is detected (bare-metal or non-containerized host),
// GOMAXPROCS is left untouched and the result's Reason reflects this.
func SetMaxProcs() MaxProcsResult {
	return setMaxProcsWithPaths(defaultCgroupPaths)
}

func clampProcs(cores float64, phys int) int {
	if cores <= 0 {
		return phys
	}
	n := int(cores)
	if cores >= 0.5 && n == 0 {
		n = 1 // always round up a sub-core share
	}
	if n < 1 {
		n = 1
	}
	if n > phys {
		n = phys
	}
	return n
}

func readInt64(path string) (int64, error) {
	// #nosec G304 -- callers pass fixed cgroup/procfs control paths, not request-derived file names.
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}

// ------------------------------------------------------------------
// BuildInfo — version / commit / built-at plumbing
// ------------------------------------------------------------------

// BuildInfo describes the binary being executed. Fields are typically
// injected at build time via `-ldflags "-X 'github.com/imajinyun/gofly/core/proc.Version=v1.0.0'"`.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"builtAt"`
	GoOS      string `json:"goos"`
	GoArch    string `json:"goarch"`
	GoVersion string `json:"goVersion"`
}

// Values overridable at build time via -ldflags.
var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = "unknown"
)

// ReadBuildInfo returns the current BuildInfo. It is safe to call from
// multiple goroutines and always returns a fresh copy.
func ReadBuildInfo() BuildInfo {
	return BuildInfo{
		Version:   firstNonEmpty(Version, os.Getenv("GOFLY_VERSION")),
		Commit:    firstNonEmpty(Commit, os.Getenv("GOFLY_COMMIT")),
		BuiltAt:   firstNonEmpty(BuiltAt, os.Getenv("GOFLY_BUILT_AT")),
		GoOS:      runtime.GOOS,
		GoArch:    runtime.GOARCH,
		GoVersion: runtime.Version(),
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return "unknown"
}

// ------------------------------------------------------------------
// Shutdown — a tiny ordered-coordinator used by app.Bootstrap
// ------------------------------------------------------------------

// Shutdown coordinates a list of cleanup functions executed in LIFO
// order. Each registered function is invoked exactly once. Safe for
// concurrent use.
type Shutdown struct {
	mu       sync.Mutex
	fns      []func(context.Context) error
	executed atomic.Bool
}

// NewShutdown returns an empty Shutdown coordinator.
func NewShutdown() *Shutdown {
	return &Shutdown{}
}

// Add registers a cleanup function. Later calls are executed first (LIFO).
// The returned context.CancelFunc provides a convenience adapter for APIs
// expecting a plain CancelFunc.
func (s *Shutdown) Add(fns ...func(ctx context.Context) error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, fn := range fns {
		if fn != nil {
			s.fns = append(s.fns, fn)
		}
	}
}

// AddCloser registers any value that implements a Close() error method
// (e.g. *http.Server, db.Conn, *os.File). The close call is invoked with
// a best-effort context (deadline-aware) at shutdown time.
func (s *Shutdown) AddCloser(closers ...interface{ Close() error }) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range closers {
		if c == nil {
			continue
		}
		c := c
		s.fns = append(s.fns, func(_ context.Context) error {
			return c.Close()
		})
	}
}

// CancelFunc adapter for libraries that only expect context.CancelFunc.
func (s *Shutdown) CancelFunc() context.CancelFunc {
	return func() { _ = s.Shutdown(context.Background()) }
}

// Shutdown runs all registered cleanup functions in reverse registration
// order. It returns the first error encountered (subsequent errors are
// joined via errors.Join). After the first call, subsequent calls are
// no-ops and return nil.
func (s *Shutdown) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if !s.executed.CompareAndSwap(false, true) {
		return nil
	}
	s.mu.Lock()
	fns := s.fns
	s.fns = nil
	s.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	var firstErr error
	for i := len(fns) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			firstErr = errors.Join(firstErr, fmt.Errorf("shutdown: context: %w", err))
			break
		}
		if err := fns[i](ctx); err != nil {
			firstErr = errors.Join(firstErr, err)
		}
	}
	return firstErr
}
