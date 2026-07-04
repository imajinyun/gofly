package middleware

import (
	"net/http"

	"github.com/imajinyun/gofly/rest"
)

// CatalogItem describes one production-ready middleware capability and where a
// generated project should expose or document it.
type CatalogItem struct {
	Category      string `json:"category"`
	Name          string `json:"name"`
	Constructor   string `json:"constructor"`
	Docs          string `json:"docs"`
	Example       string `json:"example"`
	Test          string `json:"test"`
	ControlPlane  string `json:"controlPlane"`
	OpenAPIExpose bool   `json:"openapiExpose"`
}

// MiddlewareCatalog returns the Gin/Echo/Fiber-aligned productization matrix.
// It doubles as a lightweight control-plane payload for generated projects.
func MiddlewareCatalog() []CatalogItem {
	return []CatalogItem{
		{Category: "auth", Name: "JWT", Constructor: "JWTMiddleware", Docs: "jwt.go", Example: "/jwt", Test: "TestMiddlewareDemoJWT", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "auth", Name: "API key", Constructor: "APIKeyMiddleware", Docs: "auth.go", Example: "/apikey", Test: "TestMiddlewareDemoAuthMatrix", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "auth", Name: "Basic Auth", Constructor: "BasicAuthMiddleware", Docs: "auth.go", Example: "/basic", Test: "TestMiddlewareDemoAuthMatrix", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "auth", Name: "RBAC", Constructor: "RBACMiddleware", Docs: "auth.go", Example: "/rbac", Test: "TestMiddlewareDemoAuthMatrix", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "web_security", Name: "CORS", Constructor: "CORSMiddleware", Docs: "cors.go", Example: "/cors", Test: "TestMiddlewareDemoCORS", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "web_security", Name: "CSRF", Constructor: "CSRFMiddleware", Docs: "csrf.go", Example: "/csrf", Test: "TestMiddlewareDemoCSRF", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "web_security", Name: "Security Headers", Constructor: "SecurityHeadersMiddleware", Docs: "web_security.go", Example: "/security-headers", Test: "TestMiddlewareDemoWebSecurity", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "web_security", Name: "Max Body Bytes", Constructor: "MaxBodyBytesMiddleware", Docs: "web_security.go", Example: "/max-body", Test: "TestMiddlewareDemoWebSecurity", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "web_security", Name: "Request Timeout", Constructor: "RequestTimeoutMiddleware", Docs: "web_security.go", Example: "/timeout", Test: "TestMiddlewareDemoWebSecurity", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "observability", Name: "Request ID", Constructor: "RequestIDMiddleware", Docs: "observability.go", Example: "/request-id", Test: "TestMiddlewareDemoObservability", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "observability", Name: "structured access log", Constructor: "StructuredAccessLogMiddleware", Docs: "observability.go", Example: "/access-log", Test: "TestMiddlewareDemoObservability", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "observability", Name: "Prometheus metrics", Constructor: "PrometheusMiddleware", Docs: "prometheus.go", Example: "/metrics", Test: "TestMiddlewareDemoPrometheus", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "observability", Name: "OpenTelemetry trace", Constructor: "OpenTelemetryMiddleware", Docs: "opentelemetry.go", Example: "/otel", Test: "TestMiddlewareDemoOpenTelemetry", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "observability", Name: "pprof", Constructor: "RegisterPprofRoutes", Docs: "observability.go", Example: "/debug/pprof/", Test: "TestMiddlewareDemoObservability", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "stability", Name: "recover", Constructor: "RecoverMiddleware", Docs: "stability.go", Example: "/panic", Test: "TestMiddlewareDemoStability", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "stability", Name: "rate limit", Constructor: "RateLimitMiddleware", Docs: "stability.go", Example: "/rate-limit", Test: "TestMiddlewareDemoStability", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "stability", Name: "max concurrency", Constructor: "MaxConcurrencyMiddleware", Docs: "stability.go", Example: "/max-concurrency", Test: "TestMiddlewareDemoStability", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "stability", Name: "circuit breaker", Constructor: "CircuitBreakerMiddleware", Docs: "stability.go", Example: "/breaker", Test: "TestMiddlewareDemoStability", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
		{Category: "stability", Name: "adaptive limit", Constructor: "AdaptiveLimitMiddleware", Docs: "stability.go", Example: "/adaptive-limit", Test: "TestMiddlewareDemoStability", ControlPlane: "/middleware/catalog", OpenAPIExpose: true},
	}
}

// MiddlewareCatalogHandler exposes MiddlewareCatalog as JSON. Register this on
// an internal control-plane route to make the productized middleware inventory
// visible in OpenAPI and runtime diagnostics.
func MiddlewareCatalogHandler() rest.HandlerFunc {
	return func(c *rest.Context) {
		c.JSON(http.StatusOK, MiddlewareCatalog())
	}
}
