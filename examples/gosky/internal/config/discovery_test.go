package config

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/rest"
)

func TestDiscoveryConfigDefaultsValidationAndSnapshot(t *testing.T) {
	defaultConfig := DiscoveryConfig{}
	if defaultConfig.ProviderName() != "memory" || defaultConfig.RegistryTTL().String() != "15s" || defaultConfig.DialTimeoutDuration().String() != "5s" {
		t.Fatalf("default discovery config = provider %q ttl %s dial %s", defaultConfig.ProviderName(), defaultConfig.RegistryTTL(), defaultConfig.DialTimeoutDuration())
	}
	if got := defaultConfig.RegisterOptions(); len(got) != 1 {
		t.Fatalf("default register options = %d, want ttl option", len(got))
	}
	resolved := (DiscoveryConfig{Address: " 127.0.0.1:2379, ,127.0.0.2:2379 "}).ResolvedEndpoints()
	if strings.Join(resolved, ",") != "127.0.0.1:2379,127.0.0.2:2379" {
		t.Fatalf("resolved discovery endpoints = %v, want trimmed non-empty endpoints", resolved)
	}
	if err := ValidateDiscoveryConfig(defaultConfig); err != nil {
		t.Fatalf("ValidateDiscoveryConfig default: %v", err)
	}

	if err := ValidateDiscoveryConfig(DiscoveryConfig{Provider: "etcdv3"}); err == nil || !strings.Contains(err.Error(), "endpoints are required") {
		t.Fatalf("ValidateDiscoveryConfig etcdv3 without endpoints = %v, want endpoints error", err)
	}
	if err := ValidateDiscoveryConfig(DiscoveryConfig{Provider: "etcdv3", Endpoints: []string{" ", ""}, Address: " , "}); err == nil || !strings.Contains(err.Error(), "endpoints are required") {
		t.Fatalf("ValidateDiscoveryConfig etcdv3 with blank endpoints = %v, want endpoints error", err)
	}
	if err := ValidateDiscoveryConfig(DiscoveryConfig{Provider: "unsupported"}); err == nil || !strings.Contains(err.Error(), "unsupported discovery provider") {
		t.Fatalf("ValidateDiscoveryConfig unsupported provider = %v", err)
	}
	if err := ValidateDiscoveryConfig(DiscoveryConfig{Provider: "consul", TTL: "bad"}); err == nil || !strings.Contains(err.Error(), "discovery ttl") {
		t.Fatalf("ValidateDiscoveryConfig invalid ttl = %v", err)
	}
}

func TestControlPlaneSnapshotWithDiscoveryIncludesRegistryAndSanitizesDiscovery(t *testing.T) {
	cfg := Config{
		Environment: "development",
		Service:     app.ServiceConf{Name: "hello"},
		Scaffold:    ScaffoldConfig{Features: []string{"ecosystem-compat"}},
		Discovery:   DiscoveryConfig{Provider: "consul", Address: "127.0.0.1:8500", TokenEnv: " CONSUL_HTTP_TOKEN ", UsernameEnv: " ETCD_USER ", PasswordEnv: " ETCD_PASS "},
		Rest:        rest.Config{Name: "hello", Host: "127.0.0.1", Port: 8080},
	}
	registry := discovery.NewMemoryRegistry()
	if _, err := registry.Register(context.Background(), discovery.Instance{ID: "hello-rpc", Service: "greeter", Endpoint: "http://127.0.0.1:8081", Metadata: map[string]string{"transport": "rpc"}}); err != nil {
		t.Fatalf("register discovery instance: %v", err)
	}

	snapshot, err := cfg.ControlPlaneSnapshotWithDiscovery(context.Background(), registry)
	if err != nil {
		t.Fatalf("ControlPlaneSnapshotWithDiscovery: %v", err)
	}
	if snapshot.Checksum == "" || snapshot.Metadata["generated.project.runtime"] != "service,rest,rpc,governance,discovery" {
		t.Fatalf("snapshot checksum/metadata = %q/%#v", snapshot.Checksum, snapshot.Metadata)
	}
	if len(snapshot.Services) != 2 {
		t.Fatalf("snapshot services = %#v, want generated REST service and discovery service", snapshot.Services)
	}
	foundDiscovery := false
	for _, service := range snapshot.Services {
		if service.Name == "greeter" && len(service.Endpoints) == 1 && service.Endpoints[0].Metadata["meta.transport"] == "rpc" {
			foundDiscovery = true
		}
	}
	if !foundDiscovery {
		t.Fatalf("snapshot services = %#v, want discovery registry service", snapshot.Services)
	}

	var discoveryConfig DiscoveryConfig
	if err := json.Unmarshal(snapshot.Configs["generated.discovery"], &discoveryConfig); err != nil {
		t.Fatalf("decode generated.discovery config: %v", err)
	}
	if discoveryConfig.TokenEnv != "CONSUL_HTTP_TOKEN" || discoveryConfig.UsernameEnv != "ETCD_USER" || discoveryConfig.PasswordEnv != "ETCD_PASS" {
		t.Fatalf("sanitized discovery config = %#v, want trimmed secret env names", discoveryConfig)
	}
}
