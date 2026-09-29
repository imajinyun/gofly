package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/observability/trace"
)

// TestRuntimeSemanticFailureModes exercises the public HTTP boundary rather
// than individual middleware helpers. The failure-mode inventory is recorded
// in testdata/goctl-api-semantic/expectations.json#runtimeSemantics:
// authentication, middleware ordering, validation, typed errors, recovery,
// empty success, cancellation, handler deadlines, and downstream deadlines.
func TestRuntimeSemanticFailureModes(t *testing.T) {
	t.Run("JWT rejects missing and invalid credentials before declared middleware", func(t *testing.T) {
		server := newRuntimeSemanticServer(t)
		secret := []byte("runtime-semantic-secret")
		valid, err := auth.SignJWT(auth.JWTClaims{Subject: "catalog"}, secret)
		if err != nil {
			t.Fatalf("SignJWT: %v", err)
		}

		var mu sync.Mutex
		order := make([]string, 0, 2)
		appendOrder := func(name string) {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, name)
		}
		server.AddRoute(Route{
			Method: http.MethodGet,
			Path:   "/protected",
			Handler: func(ctx *Context) {
				appendOrder("handler")
				ctx.String(http.StatusOK, "ok")
			},
		}, WithAuth(auth.JWTValidator(secret, auth.JWTOptions{})), WithMiddlewares(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				appendOrder("service-middleware")
				next.ServeHTTP(w, r)
			})
		}))

		for _, tt := range []struct {
			name          string
			authorization string
		}{
			{name: "missing"},
			{name: "invalid", authorization: auth.BearerValue("not-a-jwt")},
		} {
			t.Run(tt.name, func(t *testing.T) {
				mu.Lock()
				order = nil
				mu.Unlock()
				rec := serveRuntimeSemanticRequest(server, http.MethodGet, "/protected", nil, "auth-"+tt.name, tt.authorization)
				assertRuntimeError(t, rec, http.StatusUnauthorized, coreerrors.CodeUnauthenticated, "", "auth-"+tt.name)
				mu.Lock()
				got := append([]string(nil), order...)
				mu.Unlock()
				if len(got) != 0 {
					t.Fatalf("short-circuited request invoked %v, want no declared middleware or handler", got)
				}
			})
		}

		rec := serveRuntimeSemanticRequest(server, http.MethodGet, "/protected", nil, "auth-valid", auth.BearerValue(valid))
		if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
			t.Fatalf("valid JWT response = %d %q, want 200 ok", rec.Code, rec.Body.String())
		}
		assertRuntimeCorrelation(t, rec, "auth-valid")
		mu.Lock()
		got := append([]string(nil), order...)
		mu.Unlock()
		if want := []string{"service-middleware", "handler"}; !sameStrings(got, want) {
			t.Fatalf("successful middleware order = %v, want %v", got, want)
		}
	})

	t.Run("validation typed errors and panic recovery use stable envelopes", func(t *testing.T) {
		server := newRuntimeSemanticServer(t)
		server.AddRoute(Route{Method: http.MethodPost, Path: "/validate", Handler: func(ctx *Context) {
			var req struct {
				Name string `json:"name" validate:"required"`
			}
			if err := ctx.BindRequest(&req); err != nil {
				ctx.Error(err)
				return
			}
			ctx.JSON(http.StatusOK, req)
		}})
		server.AddRoute(Route{Method: http.MethodGet, Path: "/business-error", Handler: func(ctx *Context) {
			ctx.Error(coreerrors.New(coreerrors.CodeNotFound, "catalog item not found"))
		}})
		server.AddRoute(Route{Method: http.MethodGet, Path: "/panic", Handler: func(*Context) {
			panic("sensitive implementation detail")
		}})

		invalid := serveRuntimeSemanticRequest(server, http.MethodPost, "/validate", []byte(`{"name":`), "validation-id", "")
		assertRuntimeError(t, invalid, http.StatusBadRequest, coreerrors.CodeInvalidArgument, "", "validation-id")

		business := serveRuntimeSemanticRequest(server, http.MethodGet, "/business-error", nil, "business-id", "")
		assertRuntimeError(t, business, http.StatusNotFound, coreerrors.CodeNotFound, "catalog item not found", "business-id")

		panicResponse := serveRuntimeSemanticRequest(server, http.MethodGet, "/panic", nil, "panic-id", "")
		assertRuntimeError(t, panicResponse, http.StatusInternalServerError, coreerrors.CodeInternal, "internal server error", "panic-id")
		if got := panicResponse.Body.String(); strings.Contains(got, "sensitive implementation detail") {
			t.Fatalf("panic envelope leaked implementation detail: %s", got)
		}
	})

	t.Run("response-less success remains empty and correlated", func(t *testing.T) {
		server := newRuntimeSemanticServer(t)
		server.AddRoute(Route{Method: http.MethodDelete, Path: "/items/{id}", Handler: func(ctx *Context) {
			ctx.Response.WriteHeader(http.StatusNoContent)
		}})
		rec := serveRuntimeSemanticRequest(server, http.MethodDelete, "/items/item-1", nil, "delete-id", "")
		if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Fatalf("response-less success = %d %q, want 204 empty", rec.Code, rec.Body.String())
		}
		assertRuntimeCorrelation(t, rec, "delete-id")
	})

	t.Run("request cancellation reaches handler context", func(t *testing.T) {
		server := newRuntimeSemanticServer(t)
		observed := make(chan error, 1)
		server.AddRoute(Route{Method: http.MethodGet, Path: "/cancel", Handler: func(ctx *Context) {
			observed <- ctx.Request.Context().Err()
			ctx.String(http.StatusOK, "canceled")
		}})

		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/cancel", nil).WithContext(ctx)
		req.Header.Set(RequestIDHeader, "cancel-id")
		cancel()
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		assertRuntimeError(t, rec, coreerrors.HTTPStatus(coreerrors.CodeCanceled), coreerrors.CodeCanceled, "request canceled", "cancel-id")
		select {
		case err := <-observed:
			if err != context.Canceled {
				t.Fatalf("handler context error = %v, want context.Canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("handler did not observe request cancellation")
		}
	})

	t.Run("handler deadline returns stable timeout envelope and cancels context", func(t *testing.T) {
		server := newRuntimeSemanticServer(t)
		observed := make(chan error, 1)
		server.AddRoute(Route{Method: http.MethodGet, Path: "/deadline", Handler: func(ctx *Context) {
			<-ctx.Request.Context().Done()
			observed <- ctx.Request.Context().Err()
			ctx.String(http.StatusOK, "late")
		}}, WithTimeout(20*time.Millisecond))

		rec := serveRuntimeSemanticRequest(server, http.MethodGet, "/deadline", nil, "deadline-id", "")
		assertRuntimeError(t, rec, http.StatusGatewayTimeout, coreerrors.CodeDeadlineExceeded, "request timeout", "deadline-id")
		select {
		case err := <-observed:
			if err != context.DeadlineExceeded {
				t.Fatalf("handler deadline error = %v, want context.DeadlineExceeded", err)
			}
		case <-time.After(time.Second):
			t.Fatal("handler did not observe request deadline")
		}
	})

	t.Run("downstream timeout cancels the downstream context and preserves envelope", func(t *testing.T) {
		downstreamCanceled := make(chan error, 1)
		downstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
			downstreamCanceled <- r.Context().Err()
		}))
		t.Cleanup(downstream.Close)

		client := MustNewClient(downstream.URL, WithClientTimeout(20*time.Millisecond))
		server := newRuntimeSemanticServer(t)
		server.AddRoute(Route{Method: http.MethodGet, Path: "/downstream", Handler: func(ctx *Context) {
			resp, err := client.Get(ctx.Request.Context(), "/slow")
			if resp != nil {
				closeResponseBody(resp)
			}
			if err != nil {
				ctx.Error(err)
				return
			}
			ctx.String(http.StatusOK, "unexpected downstream success")
		}})

		rec := serveRuntimeSemanticRequest(server, http.MethodGet, "/downstream", nil, "downstream-id", "")
		assertRuntimeError(t, rec, http.StatusGatewayTimeout, coreerrors.CodeDeadlineExceeded, "", "downstream-id")
		select {
		case err := <-downstreamCanceled:
			if err != context.Canceled {
				t.Fatalf("downstream context error = %v, want context.Canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("downstream did not observe client cancellation")
		}
	})
}

func newRuntimeSemanticServer(t *testing.T) *Server {
	t.Helper()
	return MustNewServer(Config{Name: "runtime-semantic", Preset: PresetStandard, Timeout: time.Second})
}

func serveRuntimeSemanticRequest(server *Server, method, path string, body []byte, requestID, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytesReader(body))
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(RequestIDHeader, requestID)
	if authorization != "" {
		req.Header.Set(auth.AuthorizationHeader, authorization)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	return rec
}

func assertRuntimeError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode coreerrors.Code, wantText, wantRequestID string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	assertRuntimeCorrelation(t, rec, wantRequestID)
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want stable JSON envelope", got)
	}
	var envelope ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rec.Body.String())
	}
	if envelope.Code != wantCode || envelope.Status != wantStatus || envelope.Text == "" || envelope.Message != envelope.Text {
		t.Fatalf("error envelope = %#v, want code=%q status=%d and stable text/message", envelope, wantCode, wantStatus)
	}
	if wantText != "" && envelope.Text != wantText {
		t.Fatalf("error text = %q, want %q", envelope.Text, wantText)
	}
}

func assertRuntimeCorrelation(t *testing.T, rec *httptest.ResponseRecorder, wantRequestID string) {
	t.Helper()
	if got := rec.Header().Get(RequestIDHeader); got == "" || (wantRequestID != "" && got != wantRequestID) {
		t.Fatalf("response request id = %q, want %q", got, wantRequestID)
	}
	if got := rec.Header().Get(trace.TraceParentHeader); got == "" {
		t.Fatalf("response %s header is empty", trace.TraceParentHeader)
	}
}

func bytesReader(body []byte) *bytes.Reader { return bytes.NewReader(body) }

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
