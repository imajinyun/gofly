// Command config-discovery demonstrates profile-based configuration layering
// and in-memory service discovery without requiring an external config center.
//
// Run it:
//
//	go run ./examples/config-discovery
//
// Expected output includes the merged service port, active feature flag, and the
// resolved healthy instance endpoint.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/imajinyun/gofly/core/config"
	"github.com/imajinyun/gofly/core/discovery"
)

type appConfig struct {
	Service struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	} `json:"service"`
	Feature struct {
		Payments bool `json:"payments"`
	} `json:"feature"`
}

func main() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "gofly-config-discovery-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	writeConfig(filepath.Join(dir, "app.json"), `{
  "service": {"name": "orders", "port": 8080},
  "feature": {"payments": false}
}`)
	writeConfig(filepath.Join(dir, "app.dev.json"), `{
  "service": {"port": 18080}
}`)

	provider := config.NewProfileProvider[appConfig](config.ProfileOptions{
		Dir:     dir,
		Name:    "app",
		Profile: "dev",
	})
	manager, err := config.NewManagerFromProvider(provider, config.WithValidator(func(cfg appConfig) error {
		if cfg.Service.Name == "" || cfg.Service.Port <= 0 {
			return fmt.Errorf("service name and port are required")
		}
		return nil
	}))
	if err != nil {
		panic(err)
	}

	cfg := manager.Current()
	fmt.Printf("config service=%s port=%d payments=%v\n", cfg.Service.Name, cfg.Service.Port, cfg.Feature.Payments)

	registry := discovery.NewMemoryRegistry()
	lease, err := registry.Register(ctx, discovery.Instance{
		Service:  cfg.Service.Name,
		Endpoint: fmt.Sprintf("http://127.0.0.1:%d", cfg.Service.Port),
		Version:  "v1",
		Zone:     "local",
		Status:   discovery.StatusHealthy,
		Tags:     map[string]string{"profile": "dev"},
	}, discovery.WithTTL(time.Minute))
	if err != nil {
		panic(err)
	}
	defer lease.Close(ctx)

	instances, err := registry.Resolve(ctx, "orders", discovery.WithTag("profile", "dev"), discovery.WithZone("local"))
	if err != nil {
		panic(err)
	}
	for _, instance := range instances {
		fmt.Printf("resolved service=%s endpoint=%s version=%s\n", instance.Service, instance.Endpoint, instance.Version)
	}
}

func writeConfig(path, body string) {
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		panic(err)
	}
}
