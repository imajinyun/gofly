package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/observability/metrics"
	"github.com/gofly/gofly/core/observability/trace"
)

func TestStartTraceAddsTraceMetadata(t *testing.T) {
	ctx, sc := StartTrace(context.Background(), "", "checkout", trace.AlwaysSampler())
	if sc.TraceID == "" || sc.SpanID == "" {
		t.Fatalf("trace context = %#v, want trace and span ids", sc)
	}
	md, ok := metadata.FromContext(ctx)
	if !ok {
		t.Fatal("metadata missing from traced context")
	}
	if got := md.Get(trace.TraceIDKey); got != sc.TraceID {
		t.Fatalf("trace id metadata = %q, want %q", got, sc.TraceID)
	}
	if got := md.Get("service"); got != "checkout" {
		t.Fatalf("service metadata = %q, want checkout", got)
	}
}

func TestOperationEndRecordsMetricsAndLogs(t *testing.T) {
	var buf bytes.Buffer
	registry := metrics.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	observer := New(Config{Service: "checkout", Registry: registry, Logger: logger})
	ctx := metadata.Append(context.Background(), metadata.RequestIDKey, "req-1")
	ctx, _ = StartTrace(ctx, "", "checkout", trace.AlwaysSampler())

	op := observer.Start("POST /orders", "method", http.MethodPost)
	time.Sleep(time.Nanosecond)
	op.End(ctx, http.StatusServiceUnavailable, errors.New("backend unavailable"), "request failed")

	snapshot := registry.Snapshot()
	if snapshot.Requests != 1 || snapshot.Errors != 1 || snapshot.InFlight != 0 {
		t.Fatalf("snapshot = %#v, want one failed completed request", snapshot)
	}
	if !strings.Contains(buf.String(), "request failed") || !strings.Contains(buf.String(), "req-1") || !strings.Contains(buf.String(), "trace_id") {
		t.Fatalf("log output = %q, want message, request id and trace id", buf.String())
	}
}

func TestShouldLogSamplesSuccessButAlwaysLogsServerErrors(t *testing.T) {
	ctx := context.Background()
	if ShouldLog(ctx, trace.NeverSampler(), http.StatusOK) {
		t.Fatal("successful request should respect sampler")
	}
	if !ShouldLog(ctx, trace.NeverSampler(), http.StatusInternalServerError) {
		t.Fatal("server error should ignore sampler")
	}
}

func TestObserveHandlerServesHealthMetricsAndJSON(t *testing.T) {
	registry := metrics.NewRegistry()
	registry.Observe("GET /orders", http.StatusCreated, 3*time.Millisecond)
	observe := NewObserve(ObserverConfig{Service: "checkout", Registry: registry, ExposeJSON: true})

	health := httptest.NewRecorder()
	observe.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(health.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["status"] != "ok" || body["service"] != "checkout" || body["check"] != "healthz" {
		t.Fatalf("health body = %#v", body)
	}

	metricsJSON := httptest.NewRecorder()
	observe.Handler().ServeHTTP(metricsJSON, httptest.NewRequest(http.MethodGet, "/metrics.json", nil))
	if metricsJSON.Code != http.StatusOK || !strings.Contains(metricsJSON.Body.String(), `"requests"`) {
		t.Fatalf("metrics json status/body = %d/%q", metricsJSON.Code, metricsJSON.Body.String())
	}

	prom := httptest.NewRecorder()
	observe.Handler().ServeHTTP(prom, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if prom.Code != http.StatusOK || !strings.Contains(prom.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("prometheus status/content-type = %d/%q", prom.Code, prom.Header().Get("Content-Type"))
	}
}

func TestSamplingHandlerDropsRepeatedInfoButKeepsErrors(t *testing.T) {
	var buf bytes.Buffer
	handler := &samplingHandler{base: slog.NewTextHandler(&buf, nil), first: 1, thereafter: 3}
	logger := slog.New(handler)
	for i := 0; i < 5; i++ {
		logger.Info("same message")
	}
	logger.Error("same message")
	out := buf.String()
	if got := strings.Count(out, "level=INFO"); got != 2 {
		t.Fatalf("info logs = %d, want first and every third thereafter; output=%q", got, out)
	}
	if got := strings.Count(out, "level=ERROR"); got != 1 {
		t.Fatalf("error logs = %d, want errors always kept; output=%q", got, out)
	}
}

func TestObserveRegisterAndAccessors(t *testing.T) {
	observe := NewObserve(ObserverConfig{Service: "svc"})
	if observe.Logger() == nil {
		t.Fatal("Logger should not be nil")
	}
	if observe.Registry() == nil {
		t.Fatal("Registry should not be nil")
	}
	if observe.Handler() == nil {
		t.Fatal("Handler should not be nil")
	}

	mux := http.NewServeMux()
	observe.Register(mux, "/debug")
	observe.Register(mux, "")
	observe.Register(mux, "/admin")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("register healthz status = %d", rec.Code)
	}
}

func TestObserveHandlerJSONDisabled(t *testing.T) {
	observe := NewObserve(ObserverConfig{Service: "svc", ExposeJSON: false})
	rec := httptest.NewRecorder()
	observe.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics.json", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("json disabled status = %d, want 404", rec.Code)
	}
}

func TestObserveHandlerPprofBoundary_BitsUT(t *testing.T) {
	enabled := NewObserve(ObserverConfig{Service: "svc", Pprof: true})
	rec := httptest.NewRecorder()
	enabled.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pprof/", nil))
	if rec.Code == http.StatusNotFound {
		t.Fatalf("pprof enabled status = %d, want registered handler", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "profiles") && !strings.Contains(body, "goroutine") {
		t.Fatalf("pprof body = %q, want pprof index", body)
	}

	disabled := NewObserve(ObserverConfig{Service: "svc", Pprof: false})
	disabledRec := httptest.NewRecorder()
	disabled.Handler().ServeHTTP(disabledRec, httptest.NewRequest(http.MethodGet, "/pprof/", nil))
	if disabledRec.Code != http.StatusNotFound {
		t.Fatalf("pprof disabled status = %d, want 404", disabledRec.Code)
	}
}

func TestSamplingHandlerWithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, nil)
	h := &samplingHandler{base: base, first: 1, thereafter: 2}
	h2 := h.WithAttrs([]slog.Attr{slog.String("k", "v")})
	if h2 == h {
		t.Fatal("WithAttrs should return new handler")
	}
	h3 := h.WithGroup("g")
	if h3 == h {
		t.Fatal("WithGroup should return new handler")
	}
}

func TestRotateWriterWriteNilOut(t *testing.T) {
	rw := &rotateWriter{cfg: &RotateConfig{Filename: "/tmp/test.log"}}
	if _, err := rw.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write nil out error = %v, want ErrClosed", err)
	}
}

func TestNewLoggerRotateFallback(t *testing.T) {
	// Use a directory path as Filename to force open failure.
	cfg := &LoggerConfig{
		Rotate: &RotateConfig{Filename: t.TempDir()},
	}
	logger := newLogger(cfg)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewLoggerJSONSetGlobalBoundary_BitsUT(t *testing.T) {
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	var buf bytes.Buffer
	logger := newLogger(&LoggerConfig{JSON: true, SetGlobal: true, Output: &buf})
	if logger == nil {
		t.Fatal("newLogger returned nil")
	}
	slog.SetDefault(logger)
	slog.Info("global-test")
	out := buf.String()
	if !strings.Contains(out, `"msg":"global-test"`) {
		t.Fatalf("json logger output = %q, want global-test msg", out)
	}
}

func TestRecordNilRegistry(t *testing.T) {
	// Should not panic and should use metrics.Default
	Record(nil, "op", http.StatusOK, time.Millisecond)
}

func TestNilObserverStart(t *testing.T) {
	var nilO *Observer
	op := nilO.Start("op")
	if op == nil {
		t.Fatal("nil observer Start should return non-nil operation")
	}
	// Ending should not panic
	op.End(context.Background(), http.StatusOK, nil, "done")
}

func TestNilOperationEnd(t *testing.T) {
	var nilOp *Operation
	nilOp.End(context.Background(), http.StatusOK, nil, "done")
}

func TestOperationEndIdempotent(t *testing.T) {
	var buf bytes.Buffer
	registry := metrics.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	observer := New(Config{Service: "svc", Registry: registry, Logger: logger})
	op := observer.Start("op")
	op.End(context.Background(), http.StatusOK, nil, "done")
	buf.Reset()
	op.End(context.Background(), http.StatusOK, nil, "done")
	if buf.Len() != 0 {
		t.Fatal("second End should produce no log output")
	}
}

func TestRotateWriterRotatesAndPrunesBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	rw := &rotateWriter{cfg: &RotateConfig{Filename: path, MaxBytes: 4, MaxBackups: 1}}
	if err := rw.open(); err != nil {
		t.Fatalf("open rotate writer: %v", err)
	}
	defer rw.out.Close()

	if _, err := rw.Write([]byte("abcd")); err != nil {
		t.Fatalf("write initial log: %v", err)
	}
	if _, err := rw.Write([]byte("efgh")); err != nil {
		t.Fatalf("write rotated log: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read current log: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat current log: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("current log permissions = %o, want 0600", got)
	}
	if string(data) != "efgh" {
		t.Fatalf("current log = %q, want rotated second payload", string(data))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	backups := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "app.log.") {
			backups++
		}
	}
	if backups != 1 {
		t.Fatalf("backup count = %d, want 1", backups)
	}
}

func TestRotateWriterPrunesBackupsByAge_BitsUT(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	stale := filepath.Join(dir, "app.log.2000-01-01T00-00-00.000")
	keep := filepath.Join(dir, "not-a-backup")
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatalf("write stale backup: %v", err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes stale backup: %v", err)
	}
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write non-backup: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "app.log.dir"), 0o700); err != nil {
		t.Fatalf("mkdir backup-like dir: %v", err)
	}
	rw := &rotateWriter{cfg: &RotateConfig{Filename: path, MaxAgeDays: 1, MaxBackups: 10}}
	rw.pruneBackups()
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale backup stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("non-backup file stat error = %v", err)
	}
}
