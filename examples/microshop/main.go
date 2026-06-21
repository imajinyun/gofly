// Command microshop demonstrates a small multi-service topology that exposes
// the same production defaults on every service: health probes, admin
// control-plane snapshots, request IDs, tracing, logging and metrics.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofly/gofly/app"
	"github.com/gofly/gofly/rest"
)

type serviceSpec struct {
	Name        string   `json:"name"`
	Port        int      `json:"port"`
	Description string   `json:"description"`
	Routes      []string `json:"routes"`
}

var microshopServices = []serviceSpec{
	{Name: "gateway", Port: 8100, Description: "public checkout API", Routes: []string{"GET /v1/checkout"}},
	{Name: "users", Port: 8101, Description: "user profile API", Routes: []string{"GET /v1/users/u-1001"}},
	{Name: "orders", Port: 8102, Description: "order lookup API", Routes: []string{"GET /v1/orders/o-1001"}},
	{Name: "inventory", Port: 8103, Description: "inventory availability API", Routes: []string{"GET /v1/inventory/sku-keyboard"}},
	{Name: "payment", Port: 8104, Description: "payment authorization API", Routes: []string{"GET /v1/payments/p-1001"}},
}

func main() {
	mode := "describe"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "describe" {
		if err := json.NewEncoder(os.Stdout).Encode(microshopServices); err != nil {
			slog.Error("write topology", "error", err)
			os.Exit(1)
		}
		return
	}
	spec, ok := serviceByName(mode)
	if !ok {
		slog.Error("unknown service", "service", mode)
		os.Exit(2)
	}
	server := buildMicroshopServer(spec, envInt("PORT", spec.Port))
	slog.Info("microshop service starting", "service", spec.Name, "port", envInt("PORT", spec.Port))
	if err := app.Run(context.Background(), []app.Server{server}); err != nil {
		slog.Error("microshop service stopped", "error", err)
		os.Exit(1)
	}
}

func serviceByName(name string) (serviceSpec, bool) {
	for _, spec := range microshopServices {
		if spec.Name == name {
			return spec, true
		}
	}
	return serviceSpec{}, false
}

func buildMicroshopServer(spec serviceSpec, port int) *rest.Server {
	server := rest.MustNewServer(rest.Config{
		Name:    "microshop-" + spec.Name,
		Host:    "0.0.0.0",
		Port:    port,
		Timeout: 3 * time.Second,
		Admin:   rest.AdminConfig{Enabled: true, PathPrefix: "/admin", Token: "microshop-token", Audit: true},
		Middlewares: rest.MiddlewaresConfig{
			Recover:   true,
			Trace:     true,
			Log:       true,
			Metrics:   true,
			Health:    true,
			RequestID: true,
		},
	})
	server.AddRoute(rest.Route{
		Method:      http.MethodGet,
		Path:        primaryPath(spec),
		Summary:     spec.Description,
		Description: "Microshop example endpoint for " + spec.Name,
		Tags:        []string{"microshop", spec.Name},
		Handler: func(c *rest.Context) {
			c.JSON(http.StatusOK, map[string]any{
				"service":     spec.Name,
				"description": spec.Description,
				"routes":      spec.Routes,
				"requestId":   c.RequestID(),
			})
		},
	})
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/v1/topology",
		Summary: "Microshop topology",
		Tags:    []string{"microshop"},
		Handler: func(c *rest.Context) { c.JSON(http.StatusOK, microshopServices) },
	})
	server.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "microshop " + spec.Name, Version: "1.0.0"})
	return server
}

func primaryPath(spec serviceSpec) string {
	if len(spec.Routes) == 0 {
		return "/v1/" + spec.Name
	}
	parts := strings.Fields(spec.Routes[0])
	if len(parts) != 2 {
		return "/v1/" + spec.Name
	}
	return parts[1]
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		fmt.Fprintf(os.Stderr, "invalid %s=%q, using %d\n", key, value, fallback)
		return fallback
	}
	return parsed
}
