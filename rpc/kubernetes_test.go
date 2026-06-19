package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestKubernetesResolverConfigAndNilBoundaries_BitsUT(t *testing.T) {
	if _, err := NewKubernetesResolver(KubernetesResolverConfig{}); err == nil || !strings.Contains(err.Error(), "base url is required") {
		t.Fatalf("empty config error = %v, want base url required", err)
	}
	if _, err := NewKubernetesResolver(KubernetesResolverConfig{BaseURL: "http://kubernetes"}); err == nil || !strings.Contains(err.Error(), "service is required") {
		t.Fatalf("missing service error = %v, want service required", err)
	}
	resolver, err := NewKubernetesResolver(KubernetesResolverConfig{BaseURL: "http://kubernetes/", Service: "svc", Scheme: "https", Port: 9443})
	if err != nil {
		t.Fatalf("NewKubernetesResolver: %v", err)
	}
	if resolver.baseURL != "http://kubernetes" || resolver.namespace != "default" || resolver.scheme != "https" || resolver.port != 9443 || resolver.watchInterval != 5*time.Second {
		t.Fatalf("resolver config = %#v, want normalized defaults", resolver)
	}
	var nilResolver *KubernetesResolver
	if _, err := nilResolver.ResolveInstances(context.Background()); err == nil || !strings.Contains(err.Error(), "resolver is nil") {
		t.Fatalf("nil ResolveInstances error = %v, want resolver is nil", err)
	}
	if _, err := nilResolver.Watch(context.Background()); err == nil || !strings.Contains(err.Error(), "resolver is nil") {
		t.Fatalf("nil Watch error = %v, want resolver is nil", err)
	}
}

func TestKubernetesResolverErrorResponses_BitsUT(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "status error", status: http.StatusForbidden, body: `{}`, want: "status 403"},
		{name: "invalid json", status: http.StatusOK, body: `{`, want: "decode kubernetes endpoints"},
		{name: "no endpoints", status: http.StatusOK, body: `{"subsets":[]}`, want: "no rpc endpoints resolved"},
		{name: "missing named port", status: http.StatusOK, body: `{"subsets":[{"addresses":[{"ip":"10.0.0.1"}],"ports":[{"name":"http","port":8080}]}]}`, want: "no rpc endpoints resolved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()
			resolver, err := NewKubernetesResolver(KubernetesResolverConfig{BaseURL: ts.URL, Service: "greeter", PortName: "grpc"})
			if err != nil {
				t.Fatalf("NewKubernetesResolver: %v", err)
			}
			_, err = resolver.ResolveInstances(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ResolveInstances error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestKubernetesResolverHelpers_BitsUT(t *testing.T) {
	r := &KubernetesResolver{scheme: "grpc", port: 7777}
	if got := r.selectPort(nil); got != 7777 {
		t.Fatalf("select explicit port = %d, want 7777", got)
	}
	r.port = 0
	if got := r.selectPort([]kubernetesEndpointPort{{Name: "first", Port: 1111}}); got != 1111 {
		t.Fatalf("select first port = %d, want 1111", got)
	}
	if got := (&KubernetesResolver{portName: "grpc"}).selectPort([]kubernetesEndpointPort{{Name: "http", Port: 8080}}); got != 0 {
		t.Fatalf("missing named port = %d, want 0", got)
	}
	instances := r.addressInstances([]kubernetesEndpointAddress{{}, {IP: "10.0.0.9"}}, 9090, "healthy")
	if len(instances) != 1 || instances[0].Endpoint != "grpc://10.0.0.9:9090" || instances[0].Status != "healthy" {
		t.Fatalf("address instances = %#v, want one grpc healthy endpoint", instances)
	}
	if !sameEndpoints([]string{"a", "b"}, []string{"a", "b"}) {
		t.Fatal("sameEndpoints equal slices = false, want true")
	}
	if sameEndpoints([]string{"a", "b"}, []string{"b", "a"}) || sameEndpoints([]string{"a"}, []string{"a", "b"}) {
		t.Fatal("sameEndpoints accepted different slices")
	}
}
