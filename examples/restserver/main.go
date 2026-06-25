// Command restserver is a minimal, runnable gofly REST service.
//
// It demonstrates the everyday wiring most services need:
//
//   - a REST server with the standard middleware stack enabled,
//   - JSON request binding and responses,
//   - liveness/readiness/startup probes (/healthz, /readyz, /startupz),
//   - Prometheus metrics (/metrics),
//   - an OpenAPI 3.0 contract (/openapi.json) plus Swagger UI (/docs),
//   - graceful shutdown on SIGINT/SIGTERM.
//
// Run it:
//
//	go run ./examples/restserver
//
// Then try:
//
//	curl localhost:8080/healthz
//	curl localhost:8080/users/42
//	curl -XPOST localhost:8080/users -d '{"name":"ada"}'
//	open  http://localhost:8080/docs
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/imajinyun/gofly/rest"
)

type createUserRequest struct {
	Name string `json:"name"`
}

type userResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func main() {
	srv := newRESTServer()

	// Serve until interrupted, then shut down gracefully.
	go func() {
		log.Println("listening on :8080 — try http://localhost:8080/docs")
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
	log.Println("stopped")
}

func newRESTServer() *rest.Server {
	srv := rest.MustNewServer(rest.Config{
		Name: "restserver-demo",
		Host: "0.0.0.0",
		Port: 8080,
		Middlewares: rest.MiddlewaresConfig{
			Recover:   true,
			Log:       true,
			Metrics:   true,
			Health:    true, // mounts /healthz, /readyz, /startupz, /metrics
			RequestID: true,
		},
	})

	// GET /users/{id}
	srv.AddRoute(rest.Route{
		Method:    http.MethodGet,
		Path:      "/users/{id}",
		Summary:   "Fetch a user by id",
		Tags:      []string{"users"},
		Responses: map[string]rest.Response{"200": rest.JSONResponse("the user", userSchema())},
		Handler: func(c *rest.Context) {
			c.JSON(http.StatusOK, userResponse{ID: c.PathValue("id"), Name: "demo"})
		},
	})

	// POST /users
	srv.AddRoute(rest.Route{
		Method:      http.MethodPost,
		Path:        "/users",
		Summary:     "Create a user",
		Tags:        []string{"users"},
		RequestBody: rest.JSONBodySchema(createUserSchema(), true),
		Responses:   map[string]rest.Response{"201": rest.JSONResponse("created", userSchema())},
		Handler: func(c *rest.Context) {
			var req createUserRequest
			if err := c.Bind(&req); err != nil {
				c.Error(err)
				return
			}
			c.JSON(http.StatusCreated, userResponse{ID: "generated", Name: req.Name})
		},
	})

	// Expose the contract and Swagger UI after all routes are registered.
	srv.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "restserver demo", Version: "1.0.0"})
	return srv
}

func userSchema() rest.Schema {
	return rest.Schema{
		Type: "object",
		Properties: map[string]rest.Schema{
			"id":   {Type: "string"},
			"name": {Type: "string"},
		},
		Required: []string{"id", "name"},
	}
}

func createUserSchema() rest.Schema {
	return rest.Schema{
		Type:       "object",
		Properties: map[string]rest.Schema{"name": {Type: "string"}},
		Required:   []string{"name"},
	}
}
