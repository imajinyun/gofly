// Command ai-governed-service demonstrates the runtime state that AI agents can
// query through the REST admin control-plane before checking for drift.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/gofly/gofly/app"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/rest"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "expected" {
		if err := json.NewEncoder(os.Stdout).Encode(expectedControlPlaneContract()); err != nil {
			slog.Error("write expected contract", "error", err)
			os.Exit(1)
		}
		return
	}
	server := buildAIGovernedServer(8200, "ai-token")
	slog.Info("ai-governed-service starting", "addr", ":8200")
	if err := app.Run(context.Background(), []app.Server{server}); err != nil {
		slog.Error("ai-governed-service stopped", "error", err)
		os.Exit(1)
	}
}

func buildAIGovernedServer(port int, token string) *rest.Server {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "ai-rest-default",
		Transport: governance.TransportREST,
		Service:   "ai-governed-service",
		Method:    http.MethodGet,
		Path:      "/v1/state",
		Policy: governance.Policy{
			Timeout:   2 * time.Second,
			RateLimit: governance.RateLimitPolicy{Rate: 50, Burst: 50},
			Headers:   map[string]string{"X-Gofly-Governed": "true"},
		},
	})
	server := rest.MustNewServer(rest.Config{
		Name:    "ai-governed-service",
		Host:    "0.0.0.0",
		Port:    port,
		Timeout: 3 * time.Second,
		Admin:   rest.AdminConfig{Enabled: true, PathPrefix: "/admin", Token: token, Audit: true},
		Middlewares: rest.MiddlewaresConfig{
			Recover:   true,
			Trace:     true,
			Log:       true,
			Metrics:   true,
			Health:    true,
			RequestID: true,
		},
	}, rest.WithGovernanceRuleSet(rules))
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/v1/state",
		Summary: "AI-verifiable runtime state",
		Tags:    []string{"ai", "governance"},
		Handler: func(c *rest.Context) {
			c.JSON(http.StatusOK, map[string]any{
				"service":      "ai-governed-service",
				"governed":     true,
				"contractHash": expectedControlPlaneContract()["contractHash"],
				"requestId":    c.RequestID(),
			})
		},
	})
	server.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "AI governed service", Version: "1.0.0"})
	return server
}

func expectedControlPlaneContract() map[string]any {
	return map[string]any{
		"service":      "ai-governed-service",
		"adminPath":    "/admin/control-plane",
		"contractHash": "ai-governed-service:v1:rest-governed",
		"assertions": []string{
			"metadata.name == ai-governed-service",
			"configs.admin.enabled == true",
			"policies include ai-rest-default",
		},
	}
}
