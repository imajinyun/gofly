package spi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/rpc/endpoint"
	"github.com/imajinyun/gofly/spi"
)

type exampleConfig struct {
	Addr string
}

type exampleConfigSource struct {
	value exampleConfig
}

func (s exampleConfigSource) Load(ctx context.Context) (exampleConfig, error) {
	if err := ctx.Err(); err != nil {
		return exampleConfig{}, err
	}
	return s.value, nil
}

func ExampleConfigSource() {
	var source spi.ConfigSource[exampleConfig] = exampleConfigSource{value: exampleConfig{Addr: ":8080"}}
	conf, _ := source.Load(context.Background())
	fmt.Println(conf.Addr)
	// Output: :8080
}

type exampleLease struct {
	instance discovery.Instance
}

func (l exampleLease) KeepAlive(ctx context.Context) error { return ctx.Err() }
func (l exampleLease) Close(ctx context.Context) error     { return ctx.Err() }
func (l exampleLease) Instance() discovery.Instance        { return l.instance }
func (l exampleLease) ExpiresAt() time.Time                { return time.Time{} }

type exampleDiscoveryProvider struct {
	instances []discovery.Instance
}

func (p *exampleDiscoveryProvider) Register(ctx context.Context, instance discovery.Instance, _ ...discovery.RegisterOption) (discovery.Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.instances = append(p.instances, instance)
	return exampleLease{instance: instance}, nil
}

func (p *exampleDiscoveryProvider) Deregister(ctx context.Context, instance discovery.Instance) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	out := p.instances[:0]
	for _, existing := range p.instances {
		if existing.Service != instance.Service || existing.Endpoint != instance.Endpoint {
			out = append(out, existing)
		}
	}
	p.instances = out
	return nil
}

func (p *exampleDiscoveryProvider) Resolve(ctx context.Context, service string, _ ...discovery.ResolveOption) ([]discovery.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]discovery.Instance, 0, len(p.instances))
	for _, instance := range p.instances {
		if instance.Service == service {
			out = append(out, instance)
		}
	}
	return out, nil
}

func (p *exampleDiscoveryProvider) Watch(ctx context.Context, service string, opts ...discovery.ResolveOption) (<-chan discovery.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(chan discovery.Event, 1)
	instances, err := p.Resolve(ctx, service, opts...)
	if err != nil {
		return nil, err
	}
	out <- discovery.Event{Type: discovery.EventSnapshot, Service: service, Instances: instances, At: time.Now().UTC()}
	close(out)
	return out, nil
}

func ExampleDiscoveryProvider() {
	var provider spi.DiscoveryProvider = &exampleDiscoveryProvider{}
	_, _ = provider.Register(context.Background(), discovery.Instance{Service: "orders", Endpoint: "127.0.0.1:8080"})
	instances, _ := provider.Resolve(context.Background(), "orders")
	fmt.Println(instances[0].Endpoint)
	// Output: 127.0.0.1:8080
}

type exampleGovernanceProvider struct {
	rules []governance.Rule
}

func (p exampleGovernanceProvider) Load(ctx context.Context) ([]governance.Rule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]governance.Rule(nil), p.rules...), nil
}

func ExampleGovernanceProvider() {
	var provider spi.GovernanceProvider = exampleGovernanceProvider{rules: []governance.Rule{{Name: "orders-timeout", Transport: governance.TransportREST}}}
	rules, _ := provider.Load(context.Background())
	fmt.Println(rules[0].Name)
	// Output: orders-timeout
}

type exampleControlPlaneContributor struct{}

func (exampleControlPlaneContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot.Metadata == nil {
		snapshot.Metadata = map[string]string{}
	}
	snapshot.Metadata["extension"] = "example"
	return nil
}

func ExampleControlPlaneContributor() {
	var contributor spi.ControlPlaneContributor = exampleControlPlaneContributor{}
	snapshot := controlplane.Snapshot{}
	_ = contributor.ContributeSnapshot(context.Background(), &snapshot)
	fmt.Println(snapshot.Metadata["extension"])
	// Output: example
}

func ExampleHTTPMiddleware() {
	var middleware spi.HTTPMiddleware = spi.HTTPMiddlewareFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("x-extension", "example")
			next.ServeHTTP(w, r)
		})
	})

	recorder := httptest.NewRecorder()
	handler := middleware.WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	fmt.Println(recorder.Header().Get("x-extension"))
	// Output: example
}

func ExampleRPCInterceptor() {
	var interceptor spi.RPCInterceptor = spi.RPCInterceptorFunc(func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			resp, err := next(ctx, req)
			if err != nil {
				return nil, err
			}
			return fmt.Sprintf("%s:intercepted", resp), nil
		}
	})

	wrapped := interceptor.WrapRPC(func(ctx context.Context, req any) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return req, nil
	})
	resp, _ := wrapped(context.Background(), "orders")
	fmt.Println(resp)
	// Output: orders:intercepted
}

type exampleGeneratorPlugin struct{}

func (exampleGeneratorPlugin) Name() string { return "example-generator" }

func (exampleGeneratorPlugin) Manifest() spi.GeneratorManifest {
	return spi.GeneratorManifest{
		Name:               "example-generator",
		Version:            "v0.1.0",
		CompatibleVersions: []string{spi.ProtocolVersion},
		Capabilities:       []string{"generate:file"},
		Permissions:        []string{"filesystem:write-relative"},
		RequiresDryRun:     true,
	}
}

func (exampleGeneratorPlugin) Generate(ctx context.Context, req spi.GeneratorRequest) (spi.GeneratorResponse, error) {
	if err := ctx.Err(); err != nil {
		return spi.GeneratorResponse{}, err
	}
	return spi.GeneratorResponse{
		ProtocolVersion: req.ProtocolVersion,
		Files:           []spi.GeneratorFile{{Path: "internal/example/plugin.txt", Content: req.Service}},
	}, nil
}

func ExampleGeneratorPlugin() {
	var plugin spi.GeneratorPlugin = exampleGeneratorPlugin{}
	resp, _ := plugin.Generate(context.Background(), spi.GeneratorRequest{ProtocolVersion: spi.ProtocolVersion, Service: "orders"})
	fmt.Println(plugin.Manifest().Name, resp.Files[0].Path, resp.Files[0].Content)
	// Output: example-generator internal/example/plugin.txt orders
}
