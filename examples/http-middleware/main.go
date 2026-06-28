// Command http-middleware demonstrates a productized HTTP middleware matrix for
// gofly services: JWT bearer auth, CORS, CSRF, signed sessions, OpenTelemetry
// trace propagation, Prometheus metrics, SSE, WebSocket, and request validation.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/rest"
)

type migrationDXReport struct {
	Ordering           []string                     `json:"ordering"`
	FrameworkMapping   map[string]map[string]string `json:"frameworkMapping"`
	FailureModes       []string                     `json:"failureModes"`
	ProductionDefaults []string                     `json:"productionDefaults"`
	SmokeReferences    []string                     `json:"smokeReferences"`
}

var (
	jwtSecret     = []byte("http-middleware-demo-jwt-secret-32b")
	csrfSecret    = []byte("http-middleware-demo-csrf-secret-32")
	sessionSecret = []byte("http-middleware-demo-session-secret-32")
)

type createOrderRequest struct {
	SKU      string `json:"sku" validate:"required,min=3"`
	Quantity int    `json:"quantity" validate:"required,min=1,max=100"`
}

type orderResponse struct {
	ID        string `json:"id"`
	SKU       string `json:"sku"`
	Quantity  int    `json:"quantity"`
	Subject   string `json:"subject"`
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--describe" {
		if err := json.NewEncoder(os.Stdout).Encode(describeReport()); err != nil {
			log.Fatalf("describe error: %v", err)
		}
		return
	}

	srv := newHTTPMiddlewareServer()
	go func() {
		log.Println("listening on :8085 — try /docs, /events, /orders")
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

func newHTTPMiddlewareServer() *rest.Server {
	csrfConfig := rest.CSRFConfig{Secret: csrfSecret, CookieName: "gofly_demo_csrf", HeaderName: "X-CSRF-Token", TTL: time.Hour, SameSite: http.SameSiteLaxMode}
	corsConfig := rest.CORSConfig{
		AllowOrigins:     []string{"https://app.example.com"},
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowHeaders:     []string{"Authorization", "Content-Type", "X-CSRF-Token"},
		ExposeHeaders:    []string{rest.RequestIDHeader},
		AllowCredentials: true,
		MaxAge:           10 * time.Minute,
	}
	srv := rest.MustNewServer(rest.Config{
		Name: "http-middleware-demo",
		Host: "0.0.0.0",
		Port: 8085,
		Middlewares: rest.MiddlewaresConfig{
			Recover:         true,
			Trace:           true,
			Log:             true,
			Metrics:         true,
			Health:          true,
			RequestID:       true,
			CORS:            &corsConfig,
			SecurityHeaders: &rest.SecurityHeadersConfig{ContentTypeOptions: "nosniff", FrameOptions: "DENY", ReferrerPolicy: "no-referrer"},
		},
	})

	session := sessionMiddleware(sessionSecret)
	jwt := rest.BearerAuthMiddleware(auth.JWTValidator(jwtSecret, auth.JWTOptions{Issuer: "gofly-demo", Audience: "orders"}))
	csrf := rest.CSRFMiddleware(csrfConfig)

	srv.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/token",
		Summary: "Issue a local demo JWT and CSRF cookie",
		Tags:    []string{"middleware"},
		Responses: map[string]rest.Response{
			"200": rest.JSONResponse("demo credentials", tokenSchema()),
		},
		Middlewares: []rest.Middleware{csrf, session},
		Handler: func(c *rest.Context) {
			token, err := demoJWT(time.Now())
			if err != nil {
				c.Error(coreerrors.Wrap(coreerrors.CodeInternal, "create demo token", err))
				return
			}
			c.JSON(http.StatusOK, map[string]string{"token": token, "csrf_header": csrfConfig.HeaderName})
		},
	})

	srv.AddRoute(rest.Route{
		Method:      http.MethodPost,
		Path:        "/orders",
		Summary:     "Create an order with JWT, CORS, CSRF, session and validation",
		Tags:        []string{"middleware"},
		RequestBody: rest.JSONBodySchema(createOrderSchema(), true),
		Responses: map[string]rest.Response{
			"201": rest.JSONResponse("created order", orderSchema()),
			"400": rest.JSONResponse("validation error", errorSchema()),
			"401": rest.JSONResponse("missing or invalid JWT", errorSchema()),
			"403": rest.JSONResponse("missing or invalid CSRF token", errorSchema()),
		},
		Middlewares: []rest.Middleware{csrf, session, jwt},
		Handler: func(c *rest.Context) {
			var req createOrderRequest
			if err := c.Bind(&req); err != nil {
				writeInvalidArgument(c, err)
				return
			}
			c.JSON(http.StatusCreated, orderResponse{
				ID:        "order-001",
				SKU:       req.SKU,
				Quantity:  req.Quantity,
				Subject:   auth.SubjectFromContext(c.Request.Context()),
				SessionID: sessionIDFromRequest(c.Request),
				RequestID: c.RequestID(),
			})
		},
	})

	srv.AddRoute(rest.Route{
		Method:      http.MethodGet,
		Path:        "/events",
		Summary:     "Send one SSE event with trace and request correlation",
		Tags:        []string{"middleware"},
		Responses:   map[string]rest.Response{"200": {Description: "SSE event stream"}},
		Middlewares: []rest.Middleware{session},
		Handler: func(c *rest.Context) {
			if err := c.SSE(rest.SSEEvent{Event: "ready", ID: c.RequestID(), Data: `{"status":"ok"}`}); err != nil {
				c.Error(coreerrors.Wrap(coreerrors.CodeInternal, "write sse", err))
			}
		},
	})

	wsManager := rest.NewWebSocketManager()
	srv.AddRoute(rest.Route{
		Method:      http.MethodGet,
		Path:        "/ws",
		Summary:     "Upgrade to a bounded WebSocket echo stream",
		Tags:        []string{"middleware"},
		Responses:   map[string]rest.Response{"101": {Description: "websocket upgrade"}},
		Middlewares: []rest.Middleware{session},
		Handler: func(c *rest.Context) {
			_ = c.WebSocket(func(ctx context.Context, conn *rest.WebSocketConn) {
				messageType, payload, err := conn.ReadMessage()
				if err == nil {
					_ = conn.WriteMessage(messageType, payload)
				}
			}, rest.WithWebSocketManager(wsManager), rest.WithWebSocketMaxMessageBytes(64*1024), rest.WithWebSocketReadTimeout(30*time.Second))
		},
	})

	srv.AddRoute(rest.Route{
		Method:    http.MethodGet,
		Path:      "/ws/stats",
		Summary:   "Report WebSocket manager statistics",
		Tags:      []string{"middleware"},
		Responses: map[string]rest.Response{"200": rest.JSONResponse("websocket stats", websocketStatsSchema())},
		Handler: func(c *rest.Context) {
			c.JSON(http.StatusOK, wsManager.Snapshot())
		},
	})

	srv.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "http middleware demo", Version: "1.0.0"})
	return srv
}

func demoJWT(now time.Time) (string, error) {
	return auth.SignJWT(auth.JWTClaims{
		Subject:   "demo-user",
		Issuer:    "gofly-demo",
		Audience:  "orders",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Hour).Unix(),
		Extra: map[string]any{
			"roles":       []string{"operator"},
			"permissions": []string{"orders:create"},
		},
	}, jwtSecret)
}

func sessionMiddleware(secret []byte) rest.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sessionID := sessionIDFromRequest(r)
			if sessionID == "" {
				sessionID = "demo-session"
			}
			signed := signSession(sessionID, secret)
			http.SetCookie(w, &http.Cookie{Name: "gofly_demo_session", Value: signed, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 3600})
			r.Header.Set("X-Demo-Session-Id", sessionID)
			next.ServeHTTP(w, r)
		})
	}
}

func sessionIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if sessionID := r.Header.Get("X-Demo-Session-Id"); sessionID != "" {
		return sessionID
	}
	cookie, err := r.Cookie("gofly_demo_session")
	if err != nil {
		return ""
	}
	sessionID, ok := verifySession(cookie.Value, sessionSecret)
	if !ok {
		return ""
	}
	return sessionID
}

func signSession(sessionID string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(sessionID))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(sessionID)) + "." + sig
}

func verifySession(value string, secret []byte) (string, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	sessionID := string(raw)
	want := signSession(sessionID, secret)
	return sessionID, hmac.Equal([]byte(value), []byte(want))
}

func writeInvalidArgument(c *rest.Context, err error) {
	c.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Response.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(c.Response).Encode(rest.ErrorResponse{Code: coreerrors.CodeInvalidArgument, Text: "invalid request", Message: err.Error(), Status: http.StatusBadRequest, Fields: rest.ValidationFailuresOf(err)})
}

func tokenSchema() rest.Schema {
	return rest.Schema{Type: "object", Properties: map[string]rest.Schema{"token": {Type: "string"}, "csrf_header": {Type: "string"}}, Required: []string{"token", "csrf_header"}}
}

func createOrderSchema() rest.Schema {
	return rest.StructSchema(createOrderRequest{})
}

func orderSchema() rest.Schema {
	return rest.Schema{Type: "object", Properties: map[string]rest.Schema{"id": {Type: "string"}, "sku": {Type: "string"}, "quantity": {Type: "integer"}, "subject": {Type: "string"}, "session_id": {Type: "string"}, "request_id": {Type: "string"}}, Required: []string{"id", "sku", "quantity", "subject", "session_id", "request_id"}}
}

func errorSchema() rest.Schema {
	return rest.Schema{Type: "object", Properties: map[string]rest.Schema{"code": {Type: "string"}, "text": {Type: "string"}, "message": {Type: "string"}, "status": {Type: "integer"}, "fields": {Type: "array", Items: &rest.Schema{Type: "object", Properties: map[string]rest.Schema{"field": {Type: "string"}, "rule": {Type: "string"}, "message": {Type: "string"}}, Required: []string{"field", "rule", "message"}}}}, Required: []string{"code", "text", "status"}}
}

func websocketStatsSchema() rest.Schema {
	return rest.Schema{Type: "object", Properties: map[string]rest.Schema{"accepted": {Type: "integer"}, "active": {Type: "integer"}, "closed": {Type: "integer"}, "messagesIn": {Type: "integer"}, "messagesOut": {Type: "integer"}, "bytesIn": {Type: "integer"}, "bytesOut": {Type: "integer"}, "protocolErrors": {Type: "integer"}}}
}

func describe() []string {
	return []string{"JWT", "CORS", "CSRF", "sessions", "OpenTelemetry", "Prometheus", "SSE", "WebSocket", "request validation"}
}

func describeReport() map[string]any {
	return map[string]any{
		"schema":       "gofly.http_middleware_matrix.v1",
		"capabilities": describe(),
		"migrationDX":  migrationDX(),
		"routes": map[string]string{
			"catalog":   "/middleware/catalog",
			"openapi":   "/openapi.json",
			"token":     "/token",
			"orders":    "/orders",
			"events":    "/events",
			"websocket": "/ws",
		},
		"contracts": map[string]any{
			"invalidRequestStatus": 400,
			"schemaOutput":         "openapi",
		},
	}
}

func migrationDX() migrationDXReport {
	return migrationDXReport{
		Ordering: []string{
			"recover",
			"request-id",
			"trace",
			"log",
			"metrics",
			"security-headers",
			"cors",
			"max-body-bytes",
			"timeout",
			"session",
			"csrf",
			"jwt",
			"validation",
			"handler",
			"sse-websocket-bounds",
		},
		FrameworkMapping: map[string]map[string]string{
			"Gin": {
				"auth":          "gin middleware that validates Authorization maps to rest.BearerAuthMiddleware or examples/middlewares.JWTMiddleware",
				"cors":          "gin-contrib/cors settings map to rest.CORSConfig origins, headers, credentials, and max age",
				"csrf":          "gin CSRF or custom double-submit middleware maps to rest.CSRFConfig cookie/header/TTL/SameSite",
				"session":       "gin session store maps to signed HttpOnly session cookies or an injected session middleware before auth",
				"observability": "Gin metrics/tracing middleware maps to MetricsMiddleware, PrometheusMetricsHandler, and TraceMiddleware",
				"realtime":      "Gin SSE/WebSocket handlers map to rest.Context.SSE and rest.Context.WebSocket with explicit bounds",
			},
			"go-zero": {
				"auth":          "go-zero JWT/AuthInterceptor policy maps to rest.BearerAuthMiddleware and route OpenAPI security metadata",
				"cors":          "go-zero rest CORS config maps to rest.CORSConfig and must preserve credentialed preflight behavior",
				"csrf":          "go-zero custom browser-safety middleware maps to rest.CSRFConfig before state-changing handlers",
				"session":       "go-zero custom session middleware maps to signed HttpOnly cookies or an injected session middleware before auth",
				"observability": "go-zero trace/log/prometheus handlers map to TraceMiddleware, LogMiddleware, MetricsMiddleware, and /metrics",
				"realtime":      "go-zero streaming/custom upgrade endpoints map to SSE/WebSocket routes with cancellation and size limits",
			},
		},
		FailureModes: []string{
			"JWT rejects missing or invalid Authorization with a stable unauthenticated error envelope",
			"CORS preflight preserves allowed origins, credential behavior, and exposed request-id headers",
			"CSRF rejects missing, mismatched, or expired double-submit tokens before handlers mutate state",
			"Session cookies stay signed, HttpOnly, SameSite, and do not silently downgrade production Secure policy",
			"Prometheus metrics use bounded route labels and keep /metrics scrape output stable",
			"OpenTelemetry propagates traceparent and keeps request-id correlation visible in responses",
			"SSE keeps text/event-stream headers, event IDs, request cancellation, and trace correlation",
			"WebSocket upgrades enforce max message bytes, read/write timeouts, and manager stats",
		},
		ProductionDefaults: []string{
			"replace demo JWT, CSRF, and session secrets with secret-manager values",
			"use exact CORS origins when credentials are enabled",
			"keep CSRF and session cookies Secure on HTTPS production deployments",
			"run metrics, tracing, request-id, and structured logging before handler-specific middleware",
			"keep SSE and WebSocket routes authenticated when streams expose user data",
			"keep old Gin or go-zero middleware active until smoke gates and OpenAPI/schema evidence pass",
		},
		SmokeReferences: []string{
			"make p1-growth-check",
			"make examples-smoke",
			"make api-example-consistency-check",
			"go -C examples/http-middleware test ./...",
			"go -C examples/middlewares test ./...",
			"go -C examples/http-middleware run . --describe",
		},
	}
}
