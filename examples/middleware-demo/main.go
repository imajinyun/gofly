// Command middleware-demo runs focused examples for each reusable middleware in examples/middlewares.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/observability/metrics"
	middleware "github.com/imajinyun/gofly/examples/middlewares"
	"github.com/imajinyun/gofly/rest"
)

var (
	demoJWTSecret     = []byte("middleware-demo-jwt-secret-32bytes")
	demoCSRFSecret    = []byte("middleware-demo-csrf-secret-32byt")
	demoSessionSecret = []byte("middleware-demo-session-secret-32b")
)

type validationRequest struct {
	Name  string `json:"name" validate:"required,min=3"`
	Count int    `json:"count" validate:"required,min=1,max=10"`
}

func main() {
	srv := newMiddlewareDemoServer()
	go func() {
		log.Println("listening on :8086 — try /jwt, /cors, /csrf/token, /session, /otel, /metrics, /sse, /ws, /validation")
		if err := srv.Start(); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	if err := srv.Shutdown(context.Background()); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}
}

func newMiddlewareDemoServer() *rest.Server {
	registry := metrics.NewRegistry()
	csrfConfig := rest.CSRFConfig{Secret: demoCSRFSecret, CookieName: "middleware_demo_csrf", HeaderName: "X-CSRF-Token", TTL: time.Hour, SameSite: http.SameSiteLaxMode}
	sessionConfig := middleware.SessionConfig{Secret: demoSessionSecret, CookieName: "middleware_demo_session", HeaderName: "X-Demo-Session-Id", DefaultID: "demo-session", TTL: time.Hour, SameSite: http.SameSiteLaxMode}
	webSocketManager := rest.NewWebSocketManager()
	apiKeyMiddleware := middleware.APIKeyMiddleware(middleware.APIKeyConfig{Keys: map[string]auth.Principal{
		"demo-api-key": {Subject: "api-client", Roles: []string{"operator"}, Permissions: []string{"examples:read"}},
	}})
	basicAuthMiddleware := middleware.BasicAuthMiddleware(middleware.BasicAuthConfig{Users: map[string]middleware.BasicAuthUser{
		"demo": {Password: "password", Principal: auth.Principal{Subject: "basic-user", Roles: []string{"operator"}}},
	}})

	srv := rest.MustNewServer(rest.Config{
		Preset: rest.PresetCustom,
		Name:   "middleware-demo",
		Host:   "0.0.0.0",
		Port:   8086,
		Middlewares: rest.MiddlewaresConfig{
			Recover: true,
		},
	})

	srv.Use(middleware.RecoverMiddleware())
	srv.Use(middleware.RequestIDMiddleware())
	srv.Use(middleware.StructuredAccessLogMiddleware(rest.LogRedactionConfig{Headers: []string{"X-API-Key"}}))
	srv.Use(middleware.SecurityHeadersMiddleware(rest.SecurityHeadersConfig{}))
	srv.Use(middleware.MaxBodyBytesMiddleware(1 << 20))
	srv.Use(middleware.RequestTimeoutMiddleware(time.Second))
	srv.Use(middleware.CORSMiddleware(rest.CORSConfig{AllowOrigins: []string{"https://app.example.com"}}))
	srv.Use(middleware.SessionMiddleware(sessionConfig))
	srv.Use(middleware.OpenTelemetryMiddleware(middleware.OpenTelemetryConfig{Service: "middleware-demo"}))
	srv.Use(middleware.PrometheusMiddleware(registry))
	middleware.RegisterPprofRoutes(srv, "/debug/pprof", apiKeyMiddleware)

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/jwt/token", Summary: "JWT token example", Handler: func(c *rest.Context) {
		token, err := middleware.SignJWT(demoJWTSecret, "demo-user", time.Hour, auth.JWTOptions{Issuer: "middleware-demo", Audience: "examples"}, map[string]any{"roles": []string{"operator"}})
		if err != nil {
			c.Error(coreerrors.Wrap(coreerrors.CodeInternal, "sign jwt", err))
			return
		}
		c.JSON(http.StatusOK, map[string]string{"token": token})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/jwt", Summary: "JWT protected example", Middlewares: []rest.Middleware{middleware.JWTMiddleware(middleware.JWTConfig{Secret: demoJWTSecret, Issuer: "middleware-demo", Audience: "examples"})}, Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"subject": auth.SubjectFromContext(c.Request.Context())})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/apikey", Summary: "API key protected example", Middlewares: []rest.Middleware{apiKeyMiddleware}, Handler: func(c *rest.Context) {
		principal, _ := middleware.PrincipalFromContext(c.Request.Context())
		c.JSON(http.StatusOK, map[string]string{"subject": principal.Subject})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/basic", Summary: "Basic Auth protected example", Middlewares: []rest.Middleware{basicAuthMiddleware}, Handler: func(c *rest.Context) {
		principal, _ := middleware.PrincipalFromContext(c.Request.Context())
		c.JSON(http.StatusOK, map[string]string{"subject": principal.Subject})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/rbac", Summary: "RBAC protected example", Middlewares: []rest.Middleware{apiKeyMiddleware, middleware.RBACMiddleware(auth.RequireAnyRole("operator"))}, Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"rbac": "ok"})
	}})

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/cors", Summary: "CORS example", Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"cors": "ok"})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/security-headers", Summary: "security headers example", Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"security_headers": "ok"})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodPost, Path: "/max-body", Summary: "max body bytes example", Middlewares: []rest.Middleware{middleware.MaxBodyBytesMiddleware(8)}, Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"max_body": "ok"})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/timeout", Summary: "request timeout example", Middlewares: []rest.Middleware{middleware.RequestTimeoutMiddleware(10 * time.Millisecond)}, Handler: func(c *rest.Context) {
		time.Sleep(25 * time.Millisecond)
		c.JSON(http.StatusOK, map[string]string{"timeout": "missed"})
	}})

	csrfMiddleware := middleware.CSRFMiddleware(csrfConfig)
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/csrf/token", Summary: "CSRF token example", Middlewares: []rest.Middleware{csrfMiddleware}, Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"header": csrfConfig.HeaderName})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodPost, Path: "/csrf", Summary: "CSRF protected example", Middlewares: []rest.Middleware{csrfMiddleware}, Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"csrf": "ok"})
	}})

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/session", Summary: "signed session example", Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"session_id": middleware.SessionID(c.Request, sessionConfig)})
	}})

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/otel", Summary: "OpenTelemetry trace propagation example", Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"traceparent": c.Response.Header().Get("Traceparent")})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/request-id", Summary: "request ID example", Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"request_id": c.Response.Header().Get(rest.RequestIDHeader)})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/access-log", Summary: "structured access log example", Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"access_log": "emitted"})
	}})

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/prometheus", Summary: "Prometheus counted endpoint", Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"metrics": "recorded"})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/metrics", Summary: "Prometheus scrape endpoint", Handler: middleware.PrometheusMetricsHandler(registry)})

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/sse", Summary: "SSE stream example", Handler: func(c *rest.Context) {
		events := make(chan rest.SSEEvent, 1)
		events <- rest.SSEEvent{ID: "1", Event: "ready", Data: `{"status":"ok"}`}
		close(events)
		if err := middleware.SSEStream(c, events); err != nil {
			c.Error(coreerrors.Wrap(coreerrors.CodeInternal, "write sse", err))
		}
	}})

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/ws", Summary: "WebSocket echo example", Handler: func(c *rest.Context) {
		_ = middleware.WebSocketEcho(c, middleware.WebSocketConfig{Manager: webSocketManager, MaxMessageBytes: 64 * 1024, ReadTimeout: 30 * time.Second})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/ws/stats", Summary: "WebSocket stats example", Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, webSocketManager.Snapshot())
	}})

	srv.AddRoute(rest.Route{Method: http.MethodPost, Path: "/validation", Summary: "request validation example", Handler: middleware.RequestValidationHandler(func(c *rest.Context, req validationRequest) {
		c.JSON(http.StatusOK, map[string]any{"name": req.Name, "count": req.Count})
	})})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/panic", Summary: "recover example", Handler: func(c *rest.Context) {
		panic("middleware demo panic")
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/rate-limit", Summary: "rate limit example", Middlewares: []rest.Middleware{middleware.RateLimitMiddleware(middleware.RateLimitConfig{Rate: 1, Burst: 1})}, Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"rate_limit": "ok"})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/max-concurrency", Summary: "max concurrency example", Middlewares: []rest.Middleware{middleware.MaxConcurrencyMiddleware(1)}, Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"max_concurrency": "ok"})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/breaker", Summary: "circuit breaker example", Middlewares: []rest.Middleware{middleware.CircuitBreakerMiddleware(middleware.CircuitBreakerConfig{FailureThreshold: 1, OpenTimeout: time.Hour})}, Handler: func(c *rest.Context) {
		c.Response.WriteHeader(http.StatusInternalServerError)
		_, _ = c.Response.Write([]byte(`{"breaker":"failed"}`))
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/adaptive-limit", Summary: "adaptive limit example", Middlewares: []rest.Middleware{middleware.AdaptiveLimitMiddleware(middleware.AdaptiveLimitConfig{MinLimit: 1, MaxLimit: 2, InitialLimit: 1, MinSamples: 1})}, Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, map[string]string{"adaptive_limit": "ok"})
	}})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/middleware/catalog", Summary: "middleware productization catalog", Handler: middleware.MiddlewareCatalogHandler()})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/openapi.json", Summary: "middleware demo OpenAPI", Handler: func(c *rest.Context) {
		c.JSON(http.StatusOK, srv.OpenAPI(rest.OpenAPIInfo{Title: "middleware-demo", Version: "v1"}))
	}})

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/matrix", Summary: "middleware demo matrix", Handler: func(c *rest.Context) {
		_ = json.NewEncoder(c.Response).Encode(middleware.MiddlewareCatalog())
	}})
	return srv
}
