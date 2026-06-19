// Package observability provides request tracing, metrics recording, structured
// logging and profiling endpoints for gofly services.
package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofly/gofly/core/metrics"
	"github.com/gofly/gofly/core/trace"
)

// ObserverConfig bundles all observability knobs for a service. The zero value
// produces sensible defaults: Default metrics registry, text slog, no
// sampling, pprof exposed under the /debug/pprof/ prefix.
type ObserverConfig struct {
	Service    string
	Registry   *metrics.Registry
	Logger     *LoggerConfig
	Sampler    trace.Sampler
	Pprof      bool
	ExposeJSON bool
}

// LoggerConfig controls how the service emits structured logs. If zero, the
// default is a human-readable text logger at Info level writing to stderr.
type LoggerConfig struct {
	Level     slog.Level
	JSON      bool
	AddSource bool
	// SetGlobal installs this logger as slog.Default() when non-nil LoggerConfig
	// is passed to NewObserve. Disabled by default to avoid surprising side
	// effects; enable explicitly when you want free-form slog calls to share the
	// same destination and format.
	SetGlobal bool
	Sampling  *LogSamplingConfig
	Output    io.Writer
	// Rotate optionally enables size-based log file rotation. When set,
	// Output is ignored and writes go through the rotating file.
	Rotate *RotateConfig
}

// LogSamplingConfig drops repeated similar messages at high throughput to
// keep the log volume bounded. A message is identified by level+message.
// The first First messages of each key pass through; thereafter one message
// every Thereafter-th is kept, the rest are dropped.
type LogSamplingConfig struct {
	First      int
	Thereafter int
}

// RotateConfig enables size-based log rotation. One primary log file plus
// MaxBackups rotated copies are kept on disk, each capped at MaxBytes.
type RotateConfig struct {
	Filename   string
	MaxBytes   int64
	MaxBackups int
	MaxAgeDays int
	LocalTime  bool
}

// Observe wires together metrics, logging, tracing, and (optionally) pprof
// diagnostics behind a single http.Handler. Mount it on an internal admin
// port or under /debug on your main server. This is the "single object"
// companion to the more granular Observer/Operation API defined in
// observability.go; pick whichever fits your wiring style.
type Observe struct {
	service  string
	registry *metrics.Registry
	logger   *slog.Logger
	handler  *observeHandler
	sampler  trace.Sampler
}

// NewObserve builds and returns an Observe, installing its logger as
// slog.Default so free-form slog calls share the same formatting.
func NewObserve(cfg ObserverConfig) *Observe {
	if cfg.Registry == nil {
		cfg.Registry = metrics.Default
	}
	logger := newLogger(cfg.Logger)
	if cfg.Logger != nil && cfg.Logger.SetGlobal {
		slog.SetDefault(logger)
	}

	return &Observe{
		service:  cfg.Service,
		registry: cfg.Registry,
		logger:   logger,
		sampler:  cfg.Sampler,
		handler:  newObserveHandler(cfg.Service, cfg.Registry, cfg.Pprof, cfg.ExposeJSON),
	}
}

// Logger returns the configured structured logger.
func (o *Observe) Logger() *slog.Logger { return o.logger }

// Registry returns the metrics registry wired to the observer.
func (o *Observe) Registry() *metrics.Registry { return o.registry }

// Handler exposes diagnostics endpoints (metrics, json, pprof when enabled)
// as a single http.Handler. Safe to use with http.ServeMux by prefix.
func (o *Observe) Handler() http.Handler { return o.handler }

// Register attaches the observer's handler onto an existing mux using the
// given prefix (typically "/debug"). Empty prefix is treated as "/".
func (o *Observe) Register(mux *http.ServeMux, prefix string) {
	if prefix == "" {
		prefix = "/"
	}
	if prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}
	mux.Handle(prefix, http.StripPrefix(strings.TrimSuffix(prefix, "/"), o.handler))
}

// observeHandler dispatches /metrics, /metrics.json, /healthz /readyz /startupz,
// and /pprof/* (when enabled). It is intentionally lightweight and does not
// go through the main server's governance middleware.
type observeHandler struct {
	service    string
	registry   *metrics.Registry
	mux        *http.ServeMux
	startedAt  time.Time
	pprof      bool
	exposeJSON bool
}

func newObserveHandler(service string, registry *metrics.Registry, pprofEnabled, exposeJSON bool) *observeHandler {
	if registry == nil {
		registry = metrics.Default
	}
	h := &observeHandler{
		service:    service,
		registry:   registry,
		mux:        http.NewServeMux(),
		startedAt:  time.Now(),
		pprof:      pprofEnabled,
		exposeJSON: exposeJSON,
	}
	h.mux.HandleFunc("/metrics", h.servePrometheus)
	h.mux.HandleFunc("/metrics.json", h.serveJSON)
	h.mux.HandleFunc("/healthz", h.serveCheck)
	h.mux.HandleFunc("/readyz", h.serveCheck)
	h.mux.HandleFunc("/startupz", h.serveCheck)
	if pprofEnabled {
		h.mux.HandleFunc("/pprof/", pprof.Index)
		h.mux.HandleFunc("/pprof/cmdline", pprof.Cmdline)
		h.mux.HandleFunc("/pprof/profile", pprof.Profile)
		h.mux.HandleFunc("/pprof/symbol", pprof.Symbol)
		h.mux.HandleFunc("/pprof/trace", pprof.Trace)
	}
	return h
}

func (h *observeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *observeHandler) servePrometheus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if err := h.registry.WritePrometheus(w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *observeHandler) serveJSON(w http.ResponseWriter, r *http.Request) {
	if !h.exposeJSON {
		http.NotFound(w, r)
		return
	}
	snap := h.registry.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}

func (h *observeHandler) serveCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"service":   h.service,
		"startedAt": h.startedAt.UTC().Format(time.RFC3339),
		"uptime":    time.Since(h.startedAt).String(),
		"check":     strings.TrimPrefix(r.URL.Path, "/"),
	})
}

// ---- newLogger: slog builder with JSON/text/rotation/sampling ------------

func newLogger(cfg *LoggerConfig) *slog.Logger {
	if cfg == nil {
		cfg = &LoggerConfig{}
	}
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}
	if cfg.Rotate != nil && cfg.Rotate.Filename != "" {
		out = &rotateWriter{cfg: cfg.Rotate, mu: sync.Mutex{}, out: nil}
		if err := out.(*rotateWriter).open(); err != nil {
			// Fall back to stderr if the file can't be opened; still
			// log the failure so operators notice.
			fmt.Fprintf(os.Stderr, "observability: could not open log file %q: %v\n", cfg.Rotate.Filename, err)
			out = os.Stderr
		}
	}
	level := cfg.Level
	if level == 0 {
		level = slog.LevelInfo
	}
	var h slog.Handler
	opts := &slog.HandlerOptions{Level: level, AddSource: cfg.AddSource}
	if cfg.JSON {
		h = slog.NewJSONHandler(out, opts)
	} else {
		h = slog.NewTextHandler(out, opts)
	}
	if cfg.Sampling != nil && cfg.Sampling.First > 0 && cfg.Sampling.Thereafter > 1 {
		h = &samplingHandler{base: h, first: cfg.Sampling.First, thereafter: cfg.Sampling.Thereafter}
	}
	return slog.New(h)
}

// samplingHandler reduces log volume by keeping the first N messages of each
// (level, message) key and one out of every M thereafter. It does NOT drop
// errors (LevelError and above) to aid on-call debugging.
type samplingHandler struct {
	base       slog.Handler
	first      int
	thereafter int
	mu         sync.Mutex
	counts     map[string]int
}

func (s *samplingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return s.base.Enabled(ctx, level)
}

func (s *samplingHandler) Handle(ctx context.Context, rec slog.Record) error {
	if rec.Level >= slog.LevelError {
		return s.base.Handle(ctx, rec)
	}
	key := rec.Level.String() + ":" + rec.Message
	s.mu.Lock()
	if s.counts == nil {
		s.counts = make(map[string]int)
	}
	n := s.counts[key] + 1
	s.counts[key] = n
	s.mu.Unlock()
	if n <= s.first || (n-s.first)%s.thereafter == 0 {
		return s.base.Handle(ctx, rec)
	}
	return nil
}

func (s *samplingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &samplingHandler{base: s.base.WithAttrs(attrs), first: s.first, thereafter: s.thereafter, counts: s.counts}
}

func (s *samplingHandler) WithGroup(name string) slog.Handler {
	return &samplingHandler{base: s.base.WithGroup(name), first: s.first, thereafter: s.thereafter, counts: s.counts}
}

// rotateWriter is a minimal size-based rotating writer. Keeping it self-
// contained avoids pulling a third-party dependency into the core module.
type rotateWriter struct {
	cfg     *RotateConfig
	mu      sync.Mutex
	out     *os.File
	current int64
}

func (r *rotateWriter) open() error {
	f, err := os.OpenFile(r.cfg.Filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err == nil {
		r.current = info.Size()
	}
	r.out = f
	return nil
}

func (r *rotateWriter) Write(p []byte) (int, error) {
	if r.out == nil {
		return 0, os.ErrClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cfg.MaxBytes > 0 && r.current+int64(len(p)) > r.cfg.MaxBytes {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.out.Write(p)
	r.current += int64(n)
	return n, err
}

func (r *rotateWriter) rotate() error {
	if err := r.out.Close(); err != nil {
		return err
	}
	ts := time.Now()
	if !r.cfg.LocalTime {
		ts = ts.UTC()
	}
	backup := r.cfg.Filename + "." + ts.Format("2006-01-02T15-04-05.000")
	if err := os.Rename(r.cfg.Filename, backup); err != nil {
		// Re-open even on rename failure to keep logging.
		_ = r.reopen()
		return err
	}
	r.pruneBackups()
	return r.reopen()
}

func (r *rotateWriter) reopen() error {
	f, err := os.OpenFile(r.cfg.Filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	r.out = f
	r.current = 0
	return nil
}

func (r *rotateWriter) pruneBackups() {
	if r.cfg.MaxBackups <= 0 {
		return
	}
	dir := filepath.Dir(r.cfg.Filename)
	base := filepath.Base(r.cfg.Filename) + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type backup struct {
		name string
		mod  time.Time
	}
	var files []backup
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, base) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if r.cfg.MaxAgeDays > 0 && time.Since(info.ModTime()) > time.Duration(r.cfg.MaxAgeDays)*24*time.Hour {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		files = append(files, backup{name: name, mod: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for i := r.cfg.MaxBackups; i < len(files); i++ {
		_ = os.Remove(filepath.Join(dir, files[i].name))
	}
}
