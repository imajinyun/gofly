package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coretrace "github.com/gofly/gofly/core/trace"
)

func TestNewLoggerDefaultsAndFormats(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewLogger(&buf, LogConfig{Level: "debug", Format: "json"})
	if err != nil {
		t.Fatalf("NewLogger error = %v", err)
	}
	logger.Debug("hello")
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("missing log message: %s", buf.String())
	}

	buf.Reset()
	logger, err = NewLogger(&buf, LogConfig{Level: "warn", Format: "text"})
	if err != nil {
		t.Fatalf("NewLogger text error = %v", err)
	}
	logger.Warn("world")
	if !strings.Contains(buf.String(), "world") {
		t.Fatalf("missing text log message: %s", buf.String())
	}
}

func TestNewLoggerRejectsBadLevelAndFormat(t *testing.T) {
	if _, err := NewLogger(nil, LogConfig{Level: "nope"}); err == nil {
		t.Fatal("bad level should error")
	}
	if _, err := NewLogger(nil, LogConfig{Format: "xml"}); err == nil {
		t.Fatal("bad format should error")
	}
}

func TestTraceHandlerAddsTraceAttrs(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := traceHandler{next: inner}

	ctx := coretrace.NewContext(context.Background(), coretrace.SpanContext{
		TraceID: "abc123",
		SpanID:  "def456",
		Sampled: true,
	})
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	if err := h.Handle(ctx, record); err != nil {
		t.Fatalf("Handle error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"trace_id":"abc123"`) {
		t.Fatalf("missing trace_id in: %s", out)
	}
	if !strings.Contains(out, `"span_id":"def456"`) {
		t.Fatalf("missing span_id in: %s", out)
	}
	if !strings.Contains(out, `"trace_sampled":true`) {
		t.Fatalf("missing trace_sampled in: %s", out)
	}
}

func TestTraceHandlerEnabledDelegates(t *testing.T) {
	inner := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn})
	h := traceHandler{next: inner}
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug should be disabled when inner level is warn")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("error should be enabled when inner level is warn")
	}
}

func TestTraceHandlerWithAttrsAndGroup(t *testing.T) {
	inner := slog.NewJSONHandler(io.Discard, nil)
	h := traceHandler{next: inner}
	h2 := h.WithAttrs([]slog.Attr{slog.String("k", "v")})
	if _, ok := h2.(traceHandler); !ok {
		t.Fatalf("WithAttrs should return traceHandler, got %T", h2)
	}
	h3 := h.WithGroup("g")
	if _, ok := h3.(traceHandler); !ok {
		t.Fatalf("WithGroup should return traceHandler, got %T", h3)
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"DEBUG", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
	}
	for _, tt := range tests {
		got, err := parseLogLevel(tt.input)
		if err != nil {
			t.Fatalf("parseLogLevel(%q) error = %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("parseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
	if _, err := parseLogLevel("fatal"); err == nil {
		t.Fatal("fatal should be rejected")
	}
}

func TestLevelHandlerGetAndPut(t *testing.T) {
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelInfo)
	h := LevelHandler(lv)

	// GET
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["level"] != "info" {
		t.Fatalf("level = %q, want info", body["level"])
	}

	// PUT JSON
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"level":"debug"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", rec.Code)
	}
	if lv.Level() != slog.LevelDebug {
		t.Fatalf("level after PUT = %v, want debug", lv.Level())
	}

	// PUT form
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/?level=warn", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", rec.Code)
	}
	if lv.Level() != slog.LevelWarn {
		t.Fatalf("level after POST = %v, want warn", lv.Level())
	}

	// bad method
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", rec.Code)
	}

	// nil levelVar
	hNil := LevelHandler(nil)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	hNil.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("nil levelVar status = %d, want 501", rec.Code)
	}
}

func TestLevelHandlerRejectsBadLevel(t *testing.T) {
	lv := new(slog.LevelVar)
	h := LevelHandler(lv)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"level":"nope"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad level status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty level status = %d, want 200 (defaults to info)", rec.Code)
	}
}
