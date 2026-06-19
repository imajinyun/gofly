package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func k8sEndpointsServer(t *testing.T, body map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestKubernetesResolverNamedPort(t *testing.T) {
	ts := k8sEndpointsServer(t, map[string]any{
		"subsets": []any{map[string]any{
			"addresses": []any{map[string]any{"ip": "10.0.0.1"}},
			"ports": []any{
				map[string]any{"name": "http", "port": 8080},
				map[string]any{"name": "grpc", "port": 9090},
			},
		}},
	})
	defer ts.Close()
	resolver, err := NewKubernetesResolver(KubernetesResolverConfig{BaseURL: ts.URL, Service: "greeter", PortName: "grpc"})
	if err != nil {
		t.Fatal(err)
	}
	instances, err := resolver.ResolveInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Endpoint != "http://10.0.0.1:9090" {
		t.Fatalf("instances = %#v, want grpc port", instances)
	}
}

func TestKubernetesResolverNotReady(t *testing.T) {
	body := map[string]any{
		"subsets": []any{map[string]any{
			"addresses":         []any{map[string]any{"ip": "10.0.0.1"}},
			"notReadyAddresses": []any{map[string]any{"ip": "10.0.0.2"}},
			"ports":             []any{map[string]any{"port": 8081}},
		}},
	}
	ts := k8sEndpointsServer(t, body)
	defer ts.Close()

	ready, err := NewKubernetesResolver(KubernetesResolverConfig{BaseURL: ts.URL, Service: "greeter"})
	if err != nil {
		t.Fatal(err)
	}
	instances, err := ready.ResolveInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("ready instances = %#v, want 1", instances)
	}

	all, err := NewKubernetesResolver(KubernetesResolverConfig{BaseURL: ts.URL, Service: "greeter", IncludeNotReady: true})
	if err != nil {
		t.Fatal(err)
	}
	instances, err = all.ResolveInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 2 {
		t.Fatalf("all instances = %#v, want 2", instances)
	}
}

func TestKubernetesResolverWatch(t *testing.T) {
	ts := k8sEndpointsServer(t, map[string]any{
		"subsets": []any{map[string]any{
			"addresses": []any{map[string]any{"ip": "10.0.0.1"}},
			"ports":     []any{map[string]any{"port": 8081}},
		}},
	})
	defer ts.Close()
	resolver, err := NewKubernetesResolver(KubernetesResolverConfig{BaseURL: ts.URL, Service: "greeter", WatchInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := resolver.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case endpoints := <-ch:
		if len(endpoints) != 1 || endpoints[0] != "http://10.0.0.1:8081" {
			t.Fatalf("endpoints = %#v", endpoints)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch event")
	}
}
