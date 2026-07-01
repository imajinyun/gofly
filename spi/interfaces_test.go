package spi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/config"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/kv"
	"github.com/imajinyun/gofly/rpc/endpoint"
)

type spiTestConfig struct {
	Name string
}

var (
	_ ConfigSource[spiTestConfig]      = config.StaticProvider[spiTestConfig]{}
	_ ConfigSource[spiTestConfig]      = config.ProviderFunc[spiTestConfig](nil)
	_ ConfigWatcher[spiTestConfig]     = config.FileProvider[spiTestConfig]{}
	_ DiscoveryProvider                = (*discovery.MemoryRegistry)(nil)
	_ GovernanceProvider               = governance.StaticRuleProvider{}
	_ GovernanceProvider               = governance.RuleProviderFunc(nil)
	_ GovernanceSaver                  = governance.KVRuleProvider{}
	_ ControlPlaneContributor          = controlplane.MetadataContributor{}
	_ HTTPMiddleware                   = HTTPMiddlewareFunc(nil)
	_ RPCInterceptor                   = RPCInterceptorFunc(nil)
	_ endpoint.Middleware              = EndpointMiddleware(nil)
	_ GeneratorPlugin                  = contractGeneratorPlugin{}
	_ controlplane.SnapshotContributor = controlplane.SnapshotContributorFunc(nil)
)

func TestHTTPMiddlewareFuncWrapsHandler(t *testing.T) {
	mw := HTTPMiddlewareFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("x-spi", ProtocolVersion)
			next.ServeHTTP(w, r)
		})
	})

	handler := RESTMiddleware(mw)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("x-spi"); got != ProtocolVersion {
		t.Fatalf("x-spi header = %q, want %q", got, ProtocolVersion)
	}
}

func TestRPCInterceptorFuncWrapsEndpoint(t *testing.T) {
	interceptor := RPCInterceptorFunc(func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			got, err := next(ctx, req)
			if err != nil {
				return nil, err
			}
			return got.(string) + ":wrapped", nil
		}
	})

	wrapped := EndpointMiddleware(interceptor)(func(ctx context.Context, req any) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return req.(string) + ":handled", nil
	})
	got, err := wrapped(context.Background(), "orders")
	if err != nil {
		t.Fatalf("wrapped endpoint: %v", err)
	}
	if got != "orders:handled:wrapped" {
		t.Fatalf("wrapped endpoint = %q, want orders:handled:wrapped", got)
	}
}

func TestMiddlewareAdaptersNilPassThrough(t *testing.T) {
	httpHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	for _, handler := range []http.Handler{
		HTTPMiddlewareFunc(nil).WrapHTTP(httpHandler),
		RESTMiddleware(nil)(httpHandler),
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("nil HTTP adapter status = %d, want %d", recorder.Code, http.StatusAccepted)
		}
	}

	rpcEndpoint := endpoint.Endpoint(func(ctx context.Context, req any) (any, error) {
		return req, ctx.Err()
	})
	for _, wrapped := range []endpoint.Endpoint{
		RPCInterceptorFunc(nil).WrapRPC(rpcEndpoint),
		EndpointMiddleware(nil)(rpcEndpoint),
	} {
		got, err := wrapped(context.Background(), "orders")
		if err != nil || got != "orders" {
			t.Fatalf("nil RPC adapter result = %v/%v, want orders/nil", got, err)
		}
	}
}

func TestStableProviderContractsRemainCompatible(t *testing.T) {
	ctx := context.Background()
	conf, err := config.StaticProvider[spiTestConfig]{Value: spiTestConfig{Name: "orders"}}.Load(ctx)
	if err != nil || conf.Name != "orders" {
		t.Fatalf("config source Load() = %#v/%v, want orders config", conf, err)
	}

	registry := discovery.NewMemoryRegistry()
	lease, err := registry.Register(ctx, discovery.Instance{Service: "orders", Endpoint: "127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("discovery Register: %v", err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	instances, err := registry.Resolve(ctx, "orders")
	if err != nil || len(instances) != 1 {
		t.Fatalf("discovery Resolve() = %#v/%v, want one instance", instances, err)
	}

	rules, err := governance.StaticRuleProvider{Rules: []governance.Rule{{Name: "rest", Transport: governance.TransportREST}}}.Load(ctx)
	if err != nil || len(rules) != 1 || rules[0].Name != "rest" {
		t.Fatalf("governance Load() = %#v/%v, want rest rule", rules, err)
	}
	if err := (GovernanceSaver)(governance.KVRuleProvider{Store: kv.NewMemoryStore(), Key: "rules"}).Save(ctx, rules, time.Minute); err != nil {
		t.Fatalf("governance Save(): %v", err)
	}

	snapshot := controlplane.Snapshot{}
	contributor := ControlPlaneContributor(controlplane.MetadataContributor{Metadata: map[string]string{"service": "orders"}})
	if err := contributor.ContributeSnapshot(ctx, &snapshot); err != nil {
		t.Fatalf("control-plane contributor: %v", err)
	}
	if snapshot.Metadata["service"] != "orders" {
		t.Fatalf("snapshot metadata = %#v, want service=orders", snapshot.Metadata)
	}
}

type contractGeneratorPlugin struct{}

func (contractGeneratorPlugin) Name() string { return "contract" }

func (contractGeneratorPlugin) Manifest() GeneratorManifest {
	return GeneratorManifest{
		Name:               "contract",
		Version:            "v0.1.0",
		CompatibleVersions: []string{ProtocolVersion},
		Capabilities:       []string{"generate:file"},
		Permissions:        []string{"filesystem:write-relative"},
		RequiresDryRun:     true,
	}
}

func (contractGeneratorPlugin) Generate(ctx context.Context, req GeneratorRequest) (GeneratorResponse, error) {
	if err := ctx.Err(); err != nil {
		return GeneratorResponse{}, err
	}
	return GeneratorResponse{
		ProtocolVersion: req.ProtocolVersion,
		Files: []GeneratorFile{{
			Path:    "internal/contract.txt",
			Content: req.Service,
		}},
	}, nil
}

func TestGeneratorPluginContract(t *testing.T) {
	plugin := contractGeneratorPlugin{}
	manifest := plugin.Manifest()
	if manifest.Name != plugin.Name() || !containsString(manifest.CompatibleVersions, ProtocolVersion) {
		t.Fatalf("manifest = %#v, want compatible %s manifest", manifest, ProtocolVersion)
	}

	resp, err := plugin.Generate(context.Background(), GeneratorRequest{ProtocolVersion: ProtocolVersion, Service: "orders"})
	if err != nil {
		t.Fatalf("Generate(): %v", err)
	}
	if resp.ProtocolVersion != ProtocolVersion || len(resp.Files) != 1 || resp.Files[0].Content != "orders" {
		t.Fatalf("Generate() = %#v, want one orders file", resp)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
