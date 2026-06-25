// Command observability demonstrates gofly's full observability loop:
//
//   - REST server with trace, metrics, structured logs, and health probes
//   - RPC client call that propagates trace context
//   - Admin port exposing /metrics, /metrics.json, /healthz, /readyz, /pprof
//   - All requests share the same trace_id, request_id, and service name
//
// Run it:
//
//	go run ./examples/observability
//
// Then try:
//
//	curl localhost:8080/users/42
//	curl localhost:8081/debug/metrics
//	curl localhost:8081/debug/metrics.json
//	curl localhost:8081/debug/healthz
//	curl localhost:8081/debug/readyz
//
// Look for "trace_id" and "request_id" in the service logs to see the
// correlation between REST and RPC spans.
package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/core/observability"
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/core/observability/trace"
	"github.com/imajinyun/gofly/rest"
)

func main() {
	admin, srv := newObservabilityDemo()

	go func() {
		log.Println("admin listening on :8081")
		if err := admin.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("admin server error: %v", err)
		}
	}()

	go func() {
		log.Println("service listening on :8080")
		if err := srv.Start(); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = admin.Shutdown(shutdownCtx)
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}
	log.Println("stopped")
}

func newObservabilityDemo() (*http.Server, *rest.Server) {
	// 1. Create a dedicated metrics registry for this service.
	registry := metrics.NewRegistry()

	// 2. Wire up the unified observability handler on an admin port.
	obs := observability.NewObserve(observability.ObserverConfig{
		Service:  "observability-demo",
		Registry: registry,
		Logger: &observability.LoggerConfig{
			JSON:  true,
			Level: slog.LevelInfo,
		},
		Sampler:    trace.RatioSampler(1.0), // sample 100% for the demo
		Pprof:      true,
		ExposeJSON: true,
	})

	adminMux := http.NewServeMux()
	obs.Register(adminMux, "/debug")
	admin := &http.Server{
		Addr:              ":8081",
		Handler:           adminMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// 3. Start the REST server with the standard observability middleware.
	srv := rest.MustNewServer(rest.Config{
		Name: "observability-demo",
		Host: "0.0.0.0",
		Port: 8080,
		Middlewares: rest.MiddlewaresConfig{
			Recover:   true,
			Log:       true,
			Metrics:   true,
			Health:    true,
			RequestID: true,
		},
	})

	// Inject the custom registry so middleware metrics go into it.
	srv.Use(rest.MetricsMiddleware(registry))

	// GET /users/{id} — simulates a request that calls an internal RPC.
	srv.AddRoute(rest.Route{
		Method:    http.MethodGet,
		Path:      "/users/{id}",
		Summary:   "Fetch a user (with internal RPC trace propagation)",
		Tags:      []string{"users"},
		Responses: map[string]rest.Response{"200": rest.JSONResponse("the user", userSchema())},
		Handler: func(c *rest.Context) {
			id := c.PathValue("id")

			// Simulate an internal RPC call that propagates the trace context.
			rpcResp, rpcErr := callUserRPC(c.Request.Context(), id)

			if rpcErr != nil {
				c.JSON(http.StatusInternalServerError, map[string]string{"error": rpcErr.Error()})
				return
			}
			c.JSON(http.StatusOK, rpcResp)
		},
	})

	// Expose OpenAPI contract and Swagger UI.
	srv.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "observability demo", Version: "1.0.0"})
	return admin, srv
}

// callUserRPC simulates an internal RPC call that reuses the incoming trace
// context. In a real service this would be an rpc.Client call.
func callUserRPC(ctx context.Context, id string) (map[string]any, error) {
	// Ensure trace context is propagated into the RPC layer.
	ctx, sc := observability.StartTrace(ctx, "", "observability-demo", trace.ParentBasedSampler(trace.AlwaysSampler()))

	// Simulate RPC work.
	time.Sleep(2 * time.Millisecond)

	// Log the RPC span with trace attributes so it can be correlated.
	slog.InfoContext(ctx, "rpc call simulated",
		"name", "GetUser",
		"code", 0,
		"duration", 2*time.Millisecond,
		"trace_id", sc.TraceID,
		"span_id", sc.SpanID,
	)

	return map[string]any{
		"id":   id,
		"name": "demo-user",
		"trace_id": func() string {
			if md, ok := metadata.FromContext(ctx); ok {
				return md.Get(trace.TraceIDKey)
			}
			return ""
		}(),
		"request_id": metadata.RequestIDFromContext(ctx),
	}, nil
}

func userSchema() rest.Schema {
	return rest.Schema{
		Type: "object",
		Properties: map[string]rest.Schema{
			"id":         {Type: "string"},
			"name":       {Type: "string"},
			"trace_id":   {Type: "string"},
			"request_id": {Type: "string"},
		},
		Required: []string{"id", "name"},
	}
}
