package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gofly/gofly/core/discovery"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRPCEtcdRegistryValidationNoopAndDoBoundaries_BitsUT(t *testing.T) {
	if _, err := NewEtcdRegistry("", "", nil); err == nil || !strings.Contains(err.Error(), "base url") {
		t.Fatalf("NewEtcdRegistry empty base error = %v, want base url error", err)
	}
	called := false
	registry, err := NewEtcdRegistry("http://etcd/", "", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatalf("NewEtcdRegistry error = %v", err)
	}
	if registry.baseURL != "http://etcd" || registry.prefix != "/gofly/services" || registry.watchInterval != defaultEtcdWatchInterval {
		t.Fatalf("registry defaults = %#v", registry)
	}
	lease, err := registry.Register(context.Background(), discovery.Instance{})
	if err != nil {
		t.Fatalf("Register empty instance error = %v", err)
	}
	if called {
		t.Fatal("Register empty instance called etcd, want noop lease")
	}
	if err := lease.KeepAlive(context.Background()); err != nil {
		t.Fatalf("noop KeepAlive error = %v", err)
	}
	if err := lease.Close(context.Background()); err != nil {
		t.Fatalf("noop Close error = %v", err)
	}
	if !lease.ExpiresAt().IsZero() {
		t.Fatalf("noop ExpiresAt = %s, want zero", lease.ExpiresAt())
	}

	statusRegistry, _ := NewEtcdRegistry("http://etcd", "", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})})
	if err := statusRegistry.Deregister(context.Background(), discovery.Instance{Service: "svc", Endpoint: "http://127.0.0.1"}); err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("Deregister status error = %v, want status 500", err)
	}

	decodeRegistry, _ := NewEtcdRegistry("http://etcd", "", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`not-json`)), Header: make(http.Header)}, nil
	})})
	if _, err := decodeRegistry.Resolve(context.Background(), "svc"); err == nil || !strings.Contains(err.Error(), "decode etcd response") {
		t.Fatalf("Resolve decode error = %v, want decode etcd response", err)
	}

	canceledCalls := 0
	canceledRegistry, _ := NewEtcdRegistry("http://etcd", "", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		canceledCalls++
		return nil, errors.New("unexpected call")
	})})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := canceledRegistry.Deregister(ctx, discovery.Instance{Service: "svc", Endpoint: "http://127.0.0.1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Deregister canceled error = %v, want context.Canceled", err)
	}
	if canceledCalls != 0 {
		t.Fatalf("canceled registry calls = %d, want 0", canceledCalls)
	}
}

func TestRPCEtcdRegistryRegisterResolveDeregisterFakeHTTP_BitsUT(t *testing.T) {
	var paths []string
	var bodies []map[string]string
	instance := discovery.Instance{Service: "orders", Endpoint: "http://127.0.0.1:8080/", Weight: 2, Version: " v1 ", Zone: " az1 ", Status: " healthy ", Tags: map[string]string{"lane": "blue"}, Metadata: map[string]string{"env": "test"}}
	encodedInstance := mustEtcdValue(t, normalizeEtcdDiscoveryInstance(instance))
	legacy := ServiceInstance{Endpoint: "http://127.0.0.2:8080", Weight: 3, Tags: map[string]string{"legacy": "true"}}
	encodedLegacy := mustEtcdValue(t, legacy)

	registry, err := NewEtcdRegistry("http://etcd", "/svc/", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		bodies = append(bodies, body)
		if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/json" {
			return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		}
		if req.URL.Path == "/v3/kv/range" {
			payload := etcdRangeResponse{KVs: []struct {
				Value string `json:"value"`
			}{{Value: encodedInstance}, {Value: encodedLegacy}}}
			data, _ := json.Marshal(payload)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatalf("NewEtcdRegistry error = %v", err)
	}
	lease, err := registry.Register(context.Background(), instance)
	if err != nil {
		t.Fatalf("Register error = %v", err)
	}
	if lease.Instance().Endpoint != "http://127.0.0.1:8080" {
		t.Fatalf("lease instance = %#v, want normalized endpoint", lease.Instance())
	}
	resolved, err := registry.Resolve(context.Background(), "orders")
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if len(resolved) != 2 || resolved[0].Endpoint == "" || resolved[1].Endpoint == "" {
		t.Fatalf("resolved instances = %#v, want discovery and legacy values", resolved)
	}
	if err := registry.Deregister(context.Background(), instance); err != nil {
		t.Fatalf("Deregister error = %v", err)
	}
	if len(paths) != 3 || paths[0] != "/v3/kv/put" || paths[1] != "/v3/kv/range" || paths[2] != "/v3/kv/deleterange" {
		t.Fatalf("paths = %#v, want put/range/deleterange", paths)
	}
	decodedKey, err := base64.StdEncoding.DecodeString(bodies[0]["key"])
	if err != nil || string(decodedKey) != "/svc/orders/http://127.0.0.1:8080" {
		t.Fatalf("register key = %q err=%v", string(decodedKey), err)
	}
}

func TestRPCEtcdPureHelpers_BitsUT(t *testing.T) {
	if got := prefixEnd(""); got != "\x00" {
		t.Fatalf("prefixEnd empty = %q, want nul", got)
	}
	if got := prefixEnd("/svc/"); got != "/svc0" {
		t.Fatalf("prefixEnd = %q, want /svc0", got)
	}
	if _, err := decodeEtcdInstance("orders", "not-base64"); err == nil || !strings.Contains(err.Error(), "decode etcd value") {
		t.Fatalf("decode bad base64 error = %v, want decode etcd value", err)
	}
	if _, err := decodeEtcdInstance("orders", b64("not-json")); err == nil || !strings.Contains(err.Error(), "unmarshal etcd service instance") {
		t.Fatalf("decode bad json error = %v, want unmarshal etcd service instance", err)
	}

	tags := map[string]string{"lane": "blue"}
	metadata := map[string]string{"env": "test"}
	normalized := normalizeEtcdDiscoveryInstance(discovery.Instance{Service: " orders ", Endpoint: " http://127.0.0.1/ ", Weight: -1, Version: " v1 ", Zone: " az1 ", Status: " up ", Tags: tags, Metadata: metadata})
	if normalized.Service != "orders" || normalized.Endpoint != "http://127.0.0.1" || normalized.ID != "http://127.0.0.1" || normalized.Weight != 0 || normalized.Version != "v1" || normalized.Zone != "az1" || normalized.Status != "up" {
		t.Fatalf("normalized instance = %#v", normalized)
	}
	tags["lane"] = "green"
	metadata["env"] = "prod"
	if normalized.Tags["lane"] != "blue" || normalized.Metadata["env"] != "test" {
		t.Fatalf("normalized instance aliases maps: %#v", normalized)
	}

	previous := etcdInstancesByID([]discovery.Instance{{ID: "a", Service: "orders", Endpoint: "a", Metadata: map[string]string{"v": "1"}}})
	nextSame := etcdInstancesByID([]discovery.Instance{{ID: "a", Service: "orders", Endpoint: "a", Metadata: map[string]string{"v": "1"}}})
	if _, _, changed := diffEtcdInstances(previous, nextSame); changed {
		t.Fatal("diffEtcdInstances identical maps changed = true, want false")
	}
	nextChanged := etcdInstancesByID([]discovery.Instance{{ID: "a", Service: "orders", Endpoint: "a", Metadata: map[string]string{"v": "2"}}})
	if event, instance, changed := diffEtcdInstances(previous, nextChanged); !changed || event != discovery.EventRegistered || instance.Metadata["v"] != "2" {
		t.Fatalf("changed diff = %s %#v %v, want registered v=2", event, instance, changed)
	}
	if event, instance, changed := diffEtcdInstances(previous, map[string]discovery.Instance{}); !changed || event != discovery.EventDeregister || instance.ID != "a" {
		t.Fatalf("removed diff = %s %#v %v, want deregister a", event, instance, changed)
	}
}

func mustEtcdValue(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal etcd value: %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}
