package config

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/rest"
)

var (
	_ = os.WriteFile
	_ = filepath.Join
	_ = bytes.NewBuffer
	_ = slog.SetDefault
)

func TestOpenAPIConfigDefaultsAndOverrides(t *testing.T) {
	defaultConfig := Config{Service: serviceConfFixture("hello")}
	if !defaultConfig.OpenAPIEnabled() {
		t.Fatal("OpenAPI should be enabled by default")
	}
	defaultInfo := defaultConfig.OpenAPIInfo()
	if defaultInfo.Title != "hello API" || defaultInfo.Version != "1.0.0" {
		t.Fatalf("default OpenAPI info = %#v, want title hello API and version 1.0.0", defaultInfo)
	}
	if err := ValidateOpenAPIConfig(defaultConfig); err != nil {
		t.Fatalf("ValidateOpenAPIConfig default: %v", err)
	}

	disabled := Config{OpenAPI: OpenAPIConfig{Enabled: boolPtr(false)}}
	if disabled.OpenAPIEnabled() {
		t.Fatal("OpenAPI should be disabled when enabled=false")
	}
	if err := ValidateOpenAPIConfig(disabled); err != nil {
		t.Fatalf("ValidateOpenAPIConfig disabled: %v", err)
	}

	custom := Config{OpenAPI: OpenAPIConfig{Title: "  custom API  ", Version: "  v2  ", Description: "  generated  "}}
	info := custom.OpenAPIInfo()
	if info.Title != "custom API" || info.Version != "v2" || info.Description != "generated" {
		t.Fatalf("custom OpenAPI info = %#v", info)
	}
}

func TestProjectStoreConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    ProjectStoreConfig
		setDSN    bool
		wantError bool
	}{
		{name: "disabled store", config: ProjectStoreConfig{}},
		{name: "enabled requires mysql driver", config: ProjectStoreConfig{Enabled: true, Driver: "postgres", DSNEnv: "GOSKY_MYSQL_DSN"}, wantError: true},
		{name: "enabled requires DSN environment name", config: ProjectStoreConfig{Enabled: true, Driver: "mysql"}, wantError: true},
		{name: "enabled requires DSN value", config: ProjectStoreConfig{Enabled: true, Driver: "mysql", DSNEnv: "GOSKY_MYSQL_DSN"}, wantError: true},
		{name: "enabled mysql store", config: ProjectStoreConfig{Enabled: true, Driver: "mysql", DSNEnv: "GOSKY_MYSQL_DSN"}, setDSN: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOSKY_MYSQL_DSN", "")
			if tt.setDSN {
				t.Setenv("GOSKY_MYSQL_DSN", "gosky:secret@tcp(127.0.0.1:3306)/gosky?parseTime=true")
			}
			err := ValidateProjectStoreConfig(tt.config)
			if tt.wantError {
				if err == nil {
					t.Fatal("ValidateProjectStoreConfig error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateProjectStoreConfig: %v", err)
			}
			storageConfig, err := tt.config.StorageConfig()
			if tt.config.Enabled {
				if err != nil {
					t.Fatalf("StorageConfig: %v", err)
				}
				if storageConfig.Driver != "mysql" || storageConfig.DSN == "" {
					t.Fatalf("StorageConfig = %#v, want mysql config with DSN", storageConfig)
				}
				return
			}
			if err != nil || storageConfig.Driver != "" || storageConfig.DSN != "" {
				t.Fatalf("disabled StorageConfig = %#v, %v", storageConfig, err)
			}
		})
	}
}

func TestControlPlaneSnapshotExposesGeneratedContract(t *testing.T) {
	cfg := Config{
		Environment: "development",
		Service: serviceConfFixture("hello", app.ServiceGovernance{
			Timeout:        3 * time.Second,
			Breaker:        true,
			Retry:          app.ServiceRetry{Attempts: 2, Backoff: 100 * time.Millisecond},
			RateLimit:      app.ServiceRateLimit{Rate: 100, Burst: 100},
			MaxConcurrency: 64,
			AdaptiveLimit:  true,
		}),
		Scaffold: ScaffoldConfig{Features: []string{"ecosystem-compat"}},
		Rest: rest.Config{Name: "hello", Host: "127.0.0.1", Port: 8080, Middlewares: rest.MiddlewaresConfig{
			Timeout:        true,
			RateLimit:      true,
			MaxConcurrency: true,
			Breaker:        true,
		}},
	}
	snapshot, err := cfg.ControlPlaneSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ControlPlaneSnapshot: %v", err)
	}
	if snapshot.Version != controlplane.DefaultSnapshotVersion || snapshot.Checksum == "" {
		t.Fatalf("snapshot version/checksum = %q/%q, want default version and stable checksum", snapshot.Version, snapshot.Checksum)
	}
	if snapshot.Metadata["generated.project"] != "available" || snapshot.Metadata["generated.project.contract"] != "scaffold,runtime-policy,ai-manifest" {
		t.Fatalf("snapshot metadata = %#v, want generated project contract markers", snapshot.Metadata)
	}
	if !json.Valid(snapshot.Configs["generated.rest"]) || !json.Valid(snapshot.Configs["generated.service"]) || !json.Valid(snapshot.Configs["generated.scaffold"]) {
		t.Fatalf("snapshot configs = %#v, want valid generated config blobs", snapshot.Configs)
	}
	var resilience ResilienceProfile
	if err := json.Unmarshal(snapshot.Configs["generated.resilience"], &resilience); err != nil {
		t.Fatalf("decode generated.resilience config: %v", err)
	}
	if !resilience.Timeout || !resilience.RateLimit || !resilience.Concurrency || !resilience.Breaker || !resilience.Retry || !resilience.RESTEnabled {
		t.Fatalf("generated resilience profile = %+v, want timeout/rate/concurrency/breaker/retry REST profile", resilience)
	}
	if string(snapshot.Configs["generated.rest"]) == "" || strings.Contains(string(snapshot.Configs["generated.rest"]), "change-me-admin-token") {
		t.Fatalf("generated.rest config = %s, want sanitized runtime policy without admin token", snapshot.Configs["generated.rest"])
	}
	if len(snapshot.Services) != 1 || snapshot.Services[0].Name != "hello" || len(snapshot.Services[0].Endpoints) != 1 || snapshot.Services[0].Endpoints[0].Metadata["transport"] != "rest" {
		t.Fatalf("snapshot services = %#v, want generated rest endpoint", snapshot.Services)
	}
}

func serviceConfFixture(name string, governance ...app.ServiceGovernance) app.ServiceConf {
	conf := app.ServiceConf{Name: name}
	if len(governance) > 0 {
		conf.Governance = governance[0]
	}
	return conf
}

func boolPtr(v bool) *bool { return &v }
