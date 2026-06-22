package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofly/gofly/core/auth"
	coreerrors "github.com/gofly/gofly/core/errors"
	"github.com/gofly/gofly/core/observability/metrics"
	"github.com/gofly/gofly/rest"
)

func TestMiddlewareCatalogProductization_BitsUT(t *testing.T) {
	catalog := MiddlewareCatalog()
	if len(catalog) != 19 {
		t.Fatalf("MiddlewareCatalog length = %d, want 19", len(catalog))
	}
	seen := make(map[string]bool, len(catalog))
	for _, item := range catalog {
		if item.Category == "" || item.Name == "" || item.Constructor == "" || item.Docs == "" || item.Example == "" || item.Test == "" || item.ControlPlane == "" || !item.OpenAPIExpose {
			t.Fatalf("catalog item is not fully productized: %#v", item)
		}
		seen[item.Name] = true
	}
	for _, name := range []string{"JWT", "API key", "Basic Auth", "RBAC", "CORS", "CSRF", "Security Headers", "Max Body Bytes", "Request Timeout", "Request ID", "structured access log", "Prometheus metrics", "OpenTelemetry trace", "pprof", "recover", "rate limit", "max concurrency", "circuit breaker", "adaptive limit"} {
		if !seen[name] {
			t.Fatalf("catalog missing %q", name)
		}
	}
}

func TestAuthMiddlewares_BitsUT(t *testing.T) {
	apiKey := APIKeyMiddleware(APIKeyConfig{Keys: map[string]auth.Principal{
		"secret": {Subject: "api-client", Roles: []string{"operator"}},
	}})
	basic := BasicAuthMiddleware(BasicAuthConfig{Users: map[string]BasicAuthUser{
		"demo": {Password: "password", Principal: auth.Principal{Subject: "basic-user"}},
	}})

	apiAccepted := httptest.NewRecorder()
	apiReq := httptest.NewRequest(http.MethodGet, "/", nil)
	apiReq.Header.Set(defaultAPIKeyHeader, "secret")
	apiKey(subjectHandler()).ServeHTTP(apiAccepted, apiReq)
	if apiAccepted.Code != http.StatusOK || !strings.Contains(apiAccepted.Body.String(), "api-client") {
		t.Fatalf("APIKeyMiddleware accepted = %d %s", apiAccepted.Code, apiAccepted.Body.String())
	}

	apiDenied := httptest.NewRecorder()
	apiKey(subjectHandler()).ServeHTTP(apiDenied, httptest.NewRequest(http.MethodGet, "/", nil))
	if apiDenied.Code != http.StatusUnauthorized || !strings.Contains(apiDenied.Body.String(), string(coreerrors.CodeUnauthenticated)) {
		t.Fatalf("APIKeyMiddleware denied = %d %s", apiDenied.Code, apiDenied.Body.String())
	}

	basicAccepted := httptest.NewRecorder()
	basicReq := httptest.NewRequest(http.MethodGet, "/", nil)
	basicReq.SetBasicAuth("demo", "password")
	basic(subjectHandler()).ServeHTTP(basicAccepted, basicReq)
	if basicAccepted.Code != http.StatusOK || !strings.Contains(basicAccepted.Body.String(), "basic-user") {
		t.Fatalf("BasicAuthMiddleware accepted = %d %s", basicAccepted.Code, basicAccepted.Body.String())
	}

	rbacAccepted := httptest.NewRecorder()
	apiReq = httptest.NewRequest(http.MethodGet, "/", nil)
	apiReq.Header.Set(defaultAPIKeyHeader, "secret")
	apiKey(RBACMiddleware(auth.RequireAnyRole("operator"))(okHandler())).ServeHTTP(rbacAccepted, apiReq)
	if rbacAccepted.Code != http.StatusOK {
		t.Fatalf("RBACMiddleware accepted = %d %s", rbacAccepted.Code, rbacAccepted.Body.String())
	}
}

func TestWebSecurityAndObservabilityMiddlewares_BitsUT(t *testing.T) {
	securityHeaders := httptest.NewRecorder()
	SecurityHeadersMiddleware(rest.SecurityHeadersConfig{})(okHandler()).ServeHTTP(securityHeaders, httptest.NewRequest(http.MethodGet, "/", nil))
	if securityHeaders.Header().Get("X-Frame-Options") != "DENY" || securityHeaders.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("SecurityHeadersMiddleware headers = %#v", securityHeaders.Header())
	}

	tooLarge := httptest.NewRecorder()
	MaxBodyBytesMiddleware(4)(okHandler()).ServeHTTP(tooLarge, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345")))
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("MaxBodyBytesMiddleware = %d %s", tooLarge.Code, tooLarge.Body.String())
	}

	timedOut := httptest.NewRecorder()
	RequestTimeoutMiddleware(time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(timedOut, httptest.NewRequest(http.MethodGet, "/", nil))
	if timedOut.Code != http.StatusGatewayTimeout {
		t.Fatalf("RequestTimeoutMiddleware = %d %s", timedOut.Code, timedOut.Body.String())
	}

	requestID := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(rest.RequestIDHeader, "rid")
	RequestIDMiddleware()(okHandler()).ServeHTTP(requestID, req)
	if requestID.Header().Get(rest.RequestIDHeader) != "rid" {
		t.Fatalf("RequestIDMiddleware header = %q", requestID.Header().Get(rest.RequestIDHeader))
	}

	PrometheusMiddleware(metrics.NewRegistry())(okHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	OpenTelemetryMiddleware(OpenTelemetryConfig{Service: "test"})(okHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	StructuredAccessLogMiddleware(rest.LogRedactionConfig{})(okHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestStabilityMiddlewares_BitsUT(t *testing.T) {
	recovered := httptest.NewRecorder()
	RecoverMiddleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })).ServeHTTP(recovered, httptest.NewRequest(http.MethodGet, "/", nil))
	if recovered.Code != http.StatusInternalServerError {
		t.Fatalf("RecoverMiddleware = %d %s", recovered.Code, recovered.Body.String())
	}

	limited := RateLimitMiddleware(RateLimitConfig{Rate: 1, Burst: 1})(okHandler())
	limited.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	second := httptest.NewRecorder()
	limited.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("RateLimitMiddleware second = %d %s", second.Code, second.Body.String())
	}

	maxConcurrency := httptest.NewRecorder()
	MaxConcurrencyMiddleware(1)(okHandler()).ServeHTTP(maxConcurrency, httptest.NewRequest(http.MethodGet, "/", nil))
	if maxConcurrency.Code != http.StatusOK {
		t.Fatalf("MaxConcurrencyMiddleware = %d %s", maxConcurrency.Code, maxConcurrency.Body.String())
	}

	breakerHandler := CircuitBreakerMiddleware(CircuitBreakerConfig{FailureThreshold: 1, OpenTimeout: time.Hour})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	breakerHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	open := httptest.NewRecorder()
	breakerHandler.ServeHTTP(open, httptest.NewRequest(http.MethodGet, "/", nil))
	if open.Code != http.StatusServiceUnavailable {
		t.Fatalf("CircuitBreakerMiddleware open = %d %s", open.Code, open.Body.String())
	}

	adaptive := httptest.NewRecorder()
	AdaptiveLimitMiddleware(AdaptiveLimitConfig{MinLimit: 1, MaxLimit: 2, InitialLimit: 1, MinSamples: 1})(okHandler()).ServeHTTP(adaptive, httptest.NewRequest(http.MethodGet, "/", nil))
	if adaptive.Code != http.StatusOK {
		t.Fatalf("AdaptiveLimitMiddleware = %d %s", adaptive.Code, adaptive.Body.String())
	}
}

func TestPprofAndCatalogHandlers_BitsUT(t *testing.T) {
	srv := rest.MustNewServer(rest.Config{Preset: rest.PresetCustom, Name: "middleware-test"})
	RegisterPprofRoutes(srv, "/debug/pprof")
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/middleware/catalog", Handler: MiddlewareCatalogHandler()})

	pprofRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pprofRec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if pprofRec.Code != http.StatusOK || !strings.Contains(pprofRec.Body.String(), "profile") {
		t.Fatalf("pprof route = %d %s", pprofRec.Code, pprofRec.Body.String())
	}

	catalogRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(catalogRec, httptest.NewRequest(http.MethodGet, "/middleware/catalog", nil))
	if catalogRec.Code != http.StatusOK || !strings.Contains(catalogRec.Body.String(), "JWT") {
		t.Fatalf("catalog route = %d %s", catalogRec.Code, catalogRec.Body.String())
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func subjectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, _ := PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(principal.Subject))
	})
}
