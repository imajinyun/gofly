package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/kv"
)

func TestIdempotencyMiddlewareReplaysStoredResponse(t *testing.T) {
	var calls atomic.Int64
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/orders", Handler: func(ctx *Context) {
		ctx.Response.Header().Set("X-Order", "created")
		ctx.String(http.StatusCreated, fmt.Sprintf("call-%d", calls.Add(1)))
	}}, WithMiddlewares(IdempotencyMiddleware(IdempotencyConfig{TTL: time.Minute})))

	first := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"a"}`))
	first.Header.Set(DefaultIdempotencyHeader, "idem-1")
	firstRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusCreated || firstRec.Body.String() != "call-1" {
		t.Fatalf("first response = %d/%q", firstRec.Code, firstRec.Body.String())
	}

	second := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"a"}`))
	second.Header.Set(DefaultIdempotencyHeader, "idem-1")
	secondRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusCreated || secondRec.Body.String() != "call-1" || secondRec.Header().Get("X-Idempotency-Replayed") != "true" {
		t.Fatalf("replayed response = %d/%q headers=%v", secondRec.Code, secondRec.Body.String(), secondRec.Header())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

func TestIdempotencyMiddlewareRejectsConflictingFingerprint(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/orders", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, "ok")
	}}, WithMiddlewares(IdempotencyMiddleware(IdempotencyConfig{TTL: time.Minute})))
	first := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"a"}`))
	first.Header.Set(DefaultIdempotencyHeader, "idem-2")
	s.Handler().ServeHTTP(httptest.NewRecorder(), first)
	conflict := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"b"}`))
	conflict.Header.Set(DefaultIdempotencyHeader, "idem-2")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, conflict)
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409", rec.Code)
	}
}

func TestIdempotencyMiddlewareDoesNotStoreFailuresByDefault(t *testing.T) {
	var calls atomic.Int64
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/orders", Handler: func(ctx *Context) {
		if calls.Add(1) == 1 {
			ctx.String(http.StatusInternalServerError, "boom")
			return
		}
		ctx.String(http.StatusOK, "ok")
	}}, WithMiddlewares(IdempotencyMiddleware(IdempotencyConfig{TTL: time.Minute})))
	for i, want := range []int{http.StatusInternalServerError, http.StatusOK} {
		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"a"}`))
		req.Header.Set(DefaultIdempotencyHeader, "idem-3")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d status = %d, want %d", i+1, rec.Code, want)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

func TestIdempotencyMiddlewareKVStoreReplaysAcrossInstances(t *testing.T) {
	// A shared kv.Store stands in for Redis; two independent middleware chains
	// (simulating two replicas) must share idempotent responses through it.
	shared := kv.NewMemoryStore()
	newServer := func(calls *atomic.Int64) *Server {
		s := MustNewServer(Config{})
		s.AddRoute(Route{Method: http.MethodPost, Path: "/orders", Handler: func(ctx *Context) {
			ctx.Response.Header().Set("X-Order", "created")
			ctx.String(http.StatusCreated, fmt.Sprintf("call-%d", calls.Add(1)))
		}}, WithMiddlewares(IdempotencyMiddleware(IdempotencyConfig{
			TTL:   time.Minute,
			Store: NewKVIdempotencyStore(shared),
		})))
		return s
	}

	var callsA, callsB atomic.Int64
	serverA, serverB := newServer(&callsA), newServer(&callsB)

	first := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"a"}`))
	first.Header.Set(DefaultIdempotencyHeader, "idem-kv")
	firstRec := httptest.NewRecorder()
	serverA.Handler().ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusCreated || firstRec.Body.String() != "call-1" {
		t.Fatalf("first response = %d/%q", firstRec.Code, firstRec.Body.String())
	}

	// Replayed by a different replica reading from the shared store.
	second := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"a"}`))
	second.Header.Set(DefaultIdempotencyHeader, "idem-kv")
	secondRec := httptest.NewRecorder()
	serverB.Handler().ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusCreated || secondRec.Body.String() != "call-1" ||
		secondRec.Header().Get("X-Idempotency-Replayed") != "true" || secondRec.Header().Get("X-Order") != "created" {
		t.Fatalf("replayed response = %d/%q headers=%v", secondRec.Code, secondRec.Body.String(), secondRec.Header())
	}
	if callsB.Load() != 0 {
		t.Fatalf("second replica handler calls = %d, want 0", callsB.Load())
	}

	// Conflicting fingerprint on the shared key is rejected.
	conflict := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"b"}`))
	conflict.Header.Set(DefaultIdempotencyHeader, "idem-kv")
	conflictRec := httptest.NewRecorder()
	serverB.Handler().ServeHTTP(conflictRec, conflict)
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409", conflictRec.Code)
	}
}

func TestIdempotencyMiddlewareBodyLimitAndNoKeyBypass(t *testing.T) {
	var calls atomic.Int64
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/orders", Handler: func(ctx *Context) {
		_, _ = io.ReadAll(ctx.Request.Body)
		calls.Add(1)
		ctx.String(http.StatusOK, "ok")
	}}, WithMiddlewares(IdempotencyMiddleware(IdempotencyConfig{MaxBodyBytes: 4})))
	noKey := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader("large-body"))
	noKeyRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(noKeyRec, noKey)
	if noKeyRec.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("no-key bypass = %d calls=%d", noKeyRec.Code, calls.Load())
	}
	tooLarge := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader("large-body"))
	tooLarge.Header.Set(DefaultIdempotencyHeader, "idem-4")
	tooLargeRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(tooLargeRec, tooLarge)
	if tooLargeRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too-large status = %d, want 413", tooLargeRec.Code)
	}
}

func TestIdempotencyHelpersBoundaries_BitsUT(t *testing.T) {
	noBodyReq := httptest.NewRequest(http.MethodPost, "/orders", nil)
	noBodyReq.Body = nil
	body, err := readIdempotencyBody(noBodyReq, 4)
	if err != nil || body != nil {
		t.Fatalf("readIdempotencyBody nil body = %#v, %v; want nil, nil", body, err)
	}

	tooLargeReq := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader("12345"))
	if _, err := readIdempotencyBody(tooLargeReq, 4); err != errIdempotencyBodyTooLarge {
		t.Fatalf("readIdempotencyBody too large error = %v, want errIdempotencyBodyTooLarge", err)
	}

	rec := newCaptureResponseWriter()
	rec.Header().Set("X-Test", "first")
	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusAccepted)
	n, err := rec.Write([]byte("created"))
	if err != nil || n != len("created") {
		t.Fatalf("capture Write = %d, %v", n, err)
	}
	if rec.status != http.StatusCreated || rec.body.String() != "created" || rec.Header().Get("X-Test") != "first" {
		t.Fatalf("capture recorder = status %d body %q header %q", rec.status, rec.body.String(), rec.Header().Get("X-Test"))
	}

	rec2 := newCaptureResponseWriter()
	if _, err := rec2.Write([]byte("ok")); err != nil {
		t.Fatalf("capture Write without status: %v", err)
	}
	if rec2.status != http.StatusOK {
		t.Fatalf("capture Write default status = %d, want 200", rec2.status)
	}
}

func TestKVIdempotencyStoreCompleteBoundaries_BitsUT(t *testing.T) {
	store := kv.NewMemoryStore()
	idem := NewKVIdempotencyStore(store)
	ctx := context.Background()

	if err := idem.Complete(ctx, "missing", "fp", StoredResponse{}, false, time.Minute); err != nil {
		t.Fatalf("Complete missing non-keep = %v, want nil", err)
	}
	resp := StoredResponse{Status: http.StatusCreated, Header: http.Header{"X-Test": []string{"ok"}}, Body: []byte("created")}
	if err := idem.Complete(ctx, "key", "fp", resp, true, time.Minute); err != nil {
		t.Fatalf("Complete keep = %v", err)
	}
	data, err := store.Get(ctx, "key")
	if err != nil {
		t.Fatalf("stored response Get = %v", err)
	}
	var record idempotencyRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("stored response JSON = %v", err)
	}
	if !record.Ready || record.Fingerprint != "fp" || record.Status != http.StatusCreated || string(record.Body) != "created" || record.Header.Get("X-Test") != "ok" {
		t.Fatalf("stored record = %#v, want ready created response", record)
	}

	if err := idem.Complete(ctx, "key", "fp", resp, false, time.Minute); err != nil {
		t.Fatalf("Complete delete = %v", err)
	}
	if _, err := store.Get(ctx, "key"); err != kv.ErrNotFound {
		t.Fatalf("deleted response Get error = %v, want kv.ErrNotFound", err)
	}
}
