package authz

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
)

const testJWTSecret = "gosky-test-jwt-secret-that-is-at-least-32-bytes"

func TestAuthorizerHTTPContract(t *testing.T) {
	authorizer := newTestAuthorizer(t)
	server := rest.MustNewServer(rest.Config{DisableDefaultMiddlewares: true})
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/api/v1/projects/{id}",
		Handler: func(ctx *rest.Context) { ctx.JSON(http.StatusOK, map[string]string{"id": ctx.PathValue("id")}) },
	}, rest.WithAuth(authorizer.JWTValidator()), rest.WithMiddlewares(authorizer.Middleware()))

	tests := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "missing bearer token", wantStatus: http.StatusUnauthorized},
		{name: "invalid bearer token", token: "not-a-jwt", wantStatus: http.StatusUnauthorized},
		{name: "missing tenant claim", token: signedToken(t, "alice", ""), wantStatus: http.StatusForbidden},
		{name: "same tenant permitted role", token: signedToken(t, "alice", "tenant:acme"), wantStatus: http.StatusOK},
		{name: "other tenant role cannot cross tenant", token: signedToken(t, "alice", "tenant:other"), wantStatus: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/project-123", nil)
			if tt.token != "" {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(tt.token))
			}
			// A caller-controlled tenant header must never select the policy domain.
			req.Header.Set("X-Tenant-ID", "tenant:acme")
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestNewFileAuthorizerRejectsInvalidInputs(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "model.conf")
	policyPath := filepath.Join(dir, "policy.csv")
	if err := os.WriteFile(modelPath, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("p, viewer, tenant:acme, /api/v1/projects/:id, GET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileAuthorizer(modelPath, policyPath, []byte(testJWTSecret)); err == nil {
		t.Fatal("NewFileAuthorizer accepted an invalid model")
	}
	if _, err := NewFileAuthorizer("", policyPath, []byte(testJWTSecret)); err == nil {
		t.Fatal("NewFileAuthorizer accepted an empty model path")
	}
	if _, err := NewFileAuthorizer(modelPath, policyPath, nil); err == nil {
		t.Fatal("NewFileAuthorizer accepted an empty JWT secret")
	}
}

func TestAuthorizerRPCContract(t *testing.T) {
	authorizer := newTestAuthorizer(t)
	protected := authorizer.RPCMiddleware("rpc:greeter/SayHello")(func(ctx context.Context, _ any) (any, error) {
		principal, ok := auth.FromContext(ctx)
		if !ok || principal.Subject != "alice" {
			t.Fatalf("authorized RPC handler principal = %#v, %t; want alice", principal, ok)
		}
		return "ok", nil
	})

	tests := []struct {
		name     string
		token    string
		tenantMD string
		wantCode rpc.Code
	}{
		{name: "missing bearer token", wantCode: rpc.CodeUnauthenticated},
		{name: "invalid bearer token", token: "not-a-jwt", wantCode: rpc.CodeUnauthenticated},
		{name: "missing tenant claim", token: signedToken(t, "alice", ""), wantCode: rpc.CodePermissionDenied},
		{name: "same tenant permitted role", token: signedToken(t, "alice", "tenant:acme"), wantCode: rpc.CodeOK},
		{name: "metadata tenant cannot cross tenant", token: signedToken(t, "alice", "tenant:other"), tenantMD: "tenant:acme", wantCode: rpc.CodePermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.token != "" || tt.tenantMD != "" {
				ctx = metadata.NewContext(ctx, metadata.MD{
					auth.MetadataKey: auth.BearerValue(tt.token),
					"tenant_id":      tt.tenantMD,
				})
			}
			response, err := protected(ctx, nil)
			if got := rpc.CodeOf(err); got != tt.wantCode {
				t.Fatalf("RPC code = %s, want %s; error=%v", got, tt.wantCode, err)
			}
			if tt.wantCode == rpc.CodeOK && response != "ok" {
				t.Fatalf("response = %#v, want ok", response)
			}
		})
	}
}

func TestAuthorizerMuxStreamContract(t *testing.T) {
	authorizer := newTestAuthorizer(t)
	clientConn, serverConn := net.Pipe()
	client := rpc.NewExperimentalMuxClientAdapter(clientConn)
	server := rpc.NewExperimentalMuxServerAdapter(serverConn)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	var handlerCalls atomic.Int32
	if err := server.RegisterStreamWithMiddlewares("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
		handlerCalls.Add(1)
		principal, ok := auth.FromContext(ctx)
		if !ok || principal.Subject != "alice" {
			return rpc.NewError(rpc.CodeInternal, "missing authorized stream principal")
		}
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("stream:"), msg.Payload...)}); err != nil {
			return err
		}
		return stream.Close(ctx, "ok")
	}, authorizer.MuxStreamMiddleware("rpc:greeter/Watch")); err != nil {
		t.Fatalf("RegisterStreamWithMiddlewares: %v", err)
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(serveCtx) }()

	tests := []struct {
		name     string
		token    string
		tenantMD string
		wantCode rpc.Code
	}{
		{name: "missing bearer token", wantCode: rpc.CodeUnauthenticated},
		{name: "invalid bearer token", token: "not-a-jwt", wantCode: rpc.CodeUnauthenticated},
		{name: "missing tenant claim", token: signedToken(t, "alice", ""), wantCode: rpc.CodePermissionDenied},
		{name: "same tenant permitted role", token: signedToken(t, "alice", "tenant:acme"), wantCode: rpc.CodeOK},
		{name: "metadata tenant cannot cross tenant", token: signedToken(t, "alice", "tenant:other"), tenantMD: "tenant:acme", wantCode: rpc.CodePermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.token != "" || tt.tenantMD != "" {
				ctx = metadata.NewContext(ctx, metadata.MD{
					auth.MetadataKey: auth.BearerValue(tt.token),
					"tenant_id":      tt.tenantMD,
				})
			}
			stream, err := client.OpenStream(ctx, "greeter/Watch")
			if err != nil {
				t.Fatalf("OpenStream: %v", err)
			}
			if tt.wantCode != rpc.CodeOK {
				if _, err := stream.Receive(muxStreamTestTimeout(t)); rpc.CodeOf(err) != tt.wantCode {
					t.Fatalf("mux stream code = %s, want %s; error=%v", rpc.CodeOf(err), tt.wantCode, err)
				}
				return
			}
			if err := stream.Send(ctx, rpc.Message{Payload: []byte("hello")}); err != nil {
				t.Fatalf("Send: %v", err)
			}
			response, err := stream.Receive(muxStreamTestTimeout(t))
			if err != nil || string(response.Payload) != "stream:hello" {
				t.Fatalf("stream response = %#v, err=%v; want stream:hello", response, err)
			}
			if _, err := stream.Receive(muxStreamTestTimeout(t)); !errors.Is(err, io.EOF) {
				t.Fatalf("terminal stream receive = %v, want EOF", err)
			}
		})
	}
	if got := handlerCalls.Load(); got != 1 {
		t.Fatalf("authorized stream handler calls = %d, want 1", got)
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve returned error after cancel: %v", err)
	}
}

func newTestAuthorizer(t *testing.T) *Authorizer {
	t.Helper()
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "casbin_model.conf")
	policyPath := filepath.Join(dir, "casbin_policy.csv")
	if err := os.WriteFile(modelPath, []byte(defaultModel), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("g, alice, viewer, tenant:acme\np, viewer, tenant:acme, /api/v1/projects/:id, GET\np, viewer, tenant:acme, rpc:greeter/SayHello, CALL\np, viewer, tenant:acme, rpc:greeter/Watch, STREAM\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	authorizer, err := NewFileAuthorizer(modelPath, policyPath, []byte(testJWTSecret))
	if err != nil {
		t.Fatalf("NewFileAuthorizer: %v", err)
	}
	return authorizer
}

func muxStreamTestTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func signedToken(t *testing.T, subject, tenant string) string {
	t.Helper()
	extra := map[string]any{}
	if tenant != "" {
		extra["tenant_id"] = tenant
	}
	token, err := auth.SignJWT(auth.JWTClaims{Subject: subject, ExpiresAt: time.Now().Add(time.Hour).Unix(), Extra: extra}, []byte(testJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return token
}
