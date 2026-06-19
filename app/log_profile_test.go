package app

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewLoggerAndSetDefaultLogger(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewLogger(&buf, LogConfig{Level: "debug", Format: "json"})
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	logger.DebugContext(context.Background(), "boot", "service", "gofly")
	if got := buf.String(); !strings.Contains(got, "boot") || !strings.Contains(got, "gofly") {
		t.Fatalf("log output = %q, want json debug record", got)
	}

	buf.Reset()
	if err := SetDefaultLogger(&buf, LogConfig{Level: "info", Format: "text"}); err != nil {
		t.Fatalf("SetDefaultLogger: %v", err)
	}
	slog.Info("ready", "service", "gofly")
	if got := buf.String(); !strings.Contains(got, "ready") || !strings.Contains(got, "gofly") {
		t.Fatalf("default log output = %q, want text info record", got)
	}
}

func TestNewLoggerRejectsInvalidConfig(t *testing.T) {
	if _, err := NewLogger(nil, LogConfig{Level: "verbose"}); err == nil {
		t.Fatal("NewLogger invalid level succeeded, want error")
	}
	if _, err := NewLogger(nil, LogConfig{Format: "xml"}); err == nil {
		t.Fatal("NewLogger invalid format succeeded, want error")
	}
}

func TestLeveledLoggerDynamicLevel(t *testing.T) {
	var buf bytes.Buffer
	logger, levelVar, err := NewLeveledLogger(&buf, LogConfig{Level: "info", Format: "json"})
	if err != nil {
		t.Fatalf("NewLeveledLogger: %v", err)
	}
	logger.DebugContext(context.Background(), "hidden")
	if buf.Len() != 0 {
		t.Fatalf("debug log emitted at info level: %q", buf.String())
	}
	levelVar.Set(slog.LevelDebug)
	logger.DebugContext(context.Background(), "visible")
	if !strings.Contains(buf.String(), "visible") {
		t.Fatalf("debug log not emitted after level change: %q", buf.String())
	}
}

func TestLevelHandlerGetAndUpdate(t *testing.T) {
	_, levelVar, err := NewLeveledLogger(&bytes.Buffer{}, LogConfig{Level: "info", Format: "json"})
	if err != nil {
		t.Fatalf("NewLeveledLogger: %v", err)
	}
	handler := LevelHandler(levelVar)

	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/loglevel", nil))
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), "info") {
		t.Fatalf("GET level = %d body %q", getRec.Code, getRec.Body.String())
	}

	putReq := httptest.NewRequest(http.MethodPut, "/loglevel", strings.NewReader(`{"level":"debug"}`))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT level status = %d body %q", putRec.Code, putRec.Body.String())
	}
	if levelVar.Level() != slog.LevelDebug {
		t.Fatalf("level after update = %v, want debug", levelVar.Level())
	}

	badReq := httptest.NewRequest(http.MethodPut, "/loglevel?level=verbose", nil)
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid level status = %d, want 400", badRec.Code)
	}
}

func TestSetDefaultLeveledLogger(t *testing.T) {
	var buf bytes.Buffer
	levelVar, err := SetDefaultLeveledLogger(&buf, LogConfig{Level: "warn", Format: "json"})
	if err != nil {
		t.Fatalf("SetDefaultLeveledLogger: %v", err)
	}
	slog.Info("hidden")
	if buf.Len() != 0 {
		t.Fatalf("info log emitted at warn level: %q", buf.String())
	}
	levelVar.Set(slog.LevelInfo)
	slog.Info("visible")
	if !strings.Contains(buf.String(), "visible") {
		t.Fatalf("default logger did not use dynamic level: %q", buf.String())
	}
}

func TestProfileServerHandlerAndDefaults(t *testing.T) {
	server := NewProfileServer(ProfileConfig{Enabled: true})
	if server.conf.Addr != "127.0.0.1:6060" || server.conf.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("profile config = %+v, want default addr and timeout", server.conf)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pprof index status = %d, want 200", rec.Code)
	}
}

func TestProfileServerDisabledHandlerAndRemoteProtection(t *testing.T) {
	disabled := NewProfileServer(ProfileConfig{Enabled: false})
	rec := httptest.NewRecorder()
	disabled.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled pprof status = %d, want 404", rec.Code)
	}

	server := NewProfileServer(ProfileConfig{Enabled: true, AllowRemote: true})
	remote := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	remote.RemoteAddr = "203.0.113.10:12345"
	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, remote)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("remote pprof without token status = %d, want 401", unauthorized.Code)
	}

	secured := NewProfileServer(ProfileConfig{Enabled: true, AllowRemote: true, Token: "secret"})
	allowed := httptest.NewRecorder()
	remote.Header.Set("Authorization", "Bearer secret")
	secured.Handler().ServeHTTP(allowed, remote)
	if allowed.Code != http.StatusOK {
		t.Fatalf("remote pprof with token status = %d, want 200", allowed.Code)
	}
}

func TestProfileServerCustomPrefixAndDisabledStart(t *testing.T) {
	server := NewProfileServer(ProfileConfig{Enabled: false, PathPrefix: "internal/pprof"})
	if err := server.Start(); err != nil {
		t.Fatalf("disabled Start: %v", err)
	}
	rec := httptest.NewRecorder()
	server.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/pprof/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("custom pprof internal handler status = %d, want 200", rec.Code)
	}
}

func TestProfileServerExtraHandlerSharesSecurity(t *testing.T) {
	server := NewProfileServer(
		ProfileConfig{Enabled: true, AllowRemote: true, Token: "secret"},
		WithProfileHandler("debug/loglevel", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})),
	)
	remote := httptest.NewRequest(http.MethodGet, "/debug/loglevel", nil)
	remote.RemoteAddr = "203.0.113.10:12345"
	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, remote)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("extra handler without token status = %d, want 401", unauthorized.Code)
	}

	remote.Header.Set("Authorization", "Bearer secret")
	allowed := httptest.NewRecorder()
	server.Handler().ServeHTTP(allowed, remote)
	if allowed.Code != http.StatusOK || allowed.Body.String() != "ok" {
		t.Fatalf("extra handler with token status = %d body %q, want 200 ok", allowed.Code, allowed.Body.String())
	}
}
