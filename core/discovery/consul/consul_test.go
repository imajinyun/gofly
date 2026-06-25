package consul

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	consulapi "github.com/hashicorp/consul/api"

	"github.com/imajinyun/gofly/core/discovery"
)

func TestConfigDefaultsAndNewWithClientValidation(t *testing.T) {
	cfg := (Config{}).withDefaults()
	if cfg.TTL != 15*time.Second || cfg.DeregisterAfter != time.Minute {
		t.Fatalf("defaults = ttl %s deregister %s, want 15s/1m", cfg.TTL, cfg.DeregisterAfter)
	}
	if _, err := NewWithClient(nil, Config{}); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("NewWithClient(nil) error = %v, want client is nil", err)
	}
}

func TestNewUsesAddressTokenAndDefaults(t *testing.T) {
	var tokenSeen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenSeen = r.Header.Get("X-Consul-Token")
		if r.URL.Path != "/v1/health/service/users" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Consul-Index", "1")
		if err := json.NewEncoder(w).Encode([]*consulapi.ServiceEntry{
			{Service: &consulapi.AgentService{ID: "users-a", Address: "10.0.0.1", Port: 8080}},
		}); err != nil {
			t.Fatalf("encode health entries: %v", err)
		}
	}))
	defer server.Close()

	r, err := New(Config{Address: server.URL, Token: "secret-token"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if r.cfg.TTL != 15*time.Second || r.cfg.DeregisterAfter != time.Minute {
		t.Fatalf("registry cfg = %#v, want defaults applied", r.cfg)
	}
	got, err := r.Resolve(context.Background(), "users")
	if err != nil {
		t.Fatalf("Resolve through New client error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "users-a" {
		t.Fatalf("Resolve through New client = %#v, want users-a", got)
	}
	if tokenSeen != "secret-token" {
		t.Fatalf("X-Consul-Token = %q, want secret-token", tokenSeen)
	}
}

func TestSplitEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		host     string
		port     int
		wantErr  bool
	}{
		{name: "scheme", endpoint: "http://127.0.0.1:8080", host: "127.0.0.1", port: 8080},
		{name: "host port", endpoint: "127.0.0.1:9090", host: "127.0.0.1", port: 9090},
		{name: "host only", endpoint: "127.0.0.1", host: "127.0.0.1", port: 0},
		{name: "bad port", endpoint: "127.0.0.1:http", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := splitEndpoint(tt.endpoint)
			if tt.wantErr {
				if err == nil {
					t.Fatal("splitEndpoint error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("splitEndpoint error = %v", err)
			}
			if host != tt.host || port != tt.port {
				t.Fatalf("splitEndpoint = %q/%d, want %q/%d", host, port, tt.host, tt.port)
			}
		})
	}
}

func TestTagsRoundTrip(t *testing.T) {
	instance := discovery.Instance{
		ID:       "users-1",
		Service:  "users",
		Endpoint: "10.0.0.1:8080",
		Version:  "v1",
		Zone:     "az-a",
		Tags:     map[string]string{"canary": "true"},
		Metadata: map[string]string{"owner": "team-a"},
	}
	tags := tagsToSlice(instance)
	for _, want := range []string{"version=v1", "zone=az-a", "canary=true"} {
		if !contains(tags, want) {
			t.Fatalf("tags %v missing %q", tags, want)
		}
	}

	entry := &consulapi.ServiceEntry{Service: &consulapi.AgentService{
		ID:      instance.ID,
		Address: "10.0.0.1",
		Port:    8080,
		Tags:    tags,
		Meta:    instance.Metadata,
	}}
	got := fromEntry("users", entry)
	if got.ID != instance.ID || got.Endpoint != instance.Endpoint || got.Version != instance.Version || got.Zone != instance.Zone {
		t.Fatalf("fromEntry = %#v, want core fields from instance", got)
	}
	if !reflect.DeepEqual(got.Tags, instance.Tags) || !reflect.DeepEqual(got.Metadata, instance.Metadata) {
		t.Fatalf("fromEntry tags/meta = %#v/%#v, want %#v/%#v", got.Tags, got.Metadata, instance.Tags, instance.Metadata)
	}
}

func TestFilterAppliesResolveOptions(t *testing.T) {
	r := &Registry{}
	entries := []*consulapi.ServiceEntry{
		{Service: &consulapi.AgentService{ID: "users-a", Address: "10.0.0.1", Port: 8080, Tags: []string{"version=v1", "zone=az-a", "canary=true"}}},
		{Service: &consulapi.AgentService{ID: "users-b", Address: "10.0.0.2", Port: 8080, Tags: []string{"version=v2", "zone=az-b", "canary=false"}}},
		{Service: &consulapi.AgentService{ID: "users-empty", Address: "", Port: 0, Tags: []string{"version=v1"}}},
	}

	got, err := r.filter(t.Context(), "users", entries, []discovery.ResolveOption{discovery.WithVersion("v1"), discovery.WithTag("canary", "true")})
	if err != nil {
		t.Fatalf("filter error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "users-a" || got[0].Endpoint != "10.0.0.1:8080" {
		t.Fatalf("filter result = %#v, want users-a only", got)
	}
}

func TestNormalizeTrimsVersionAndZone(t *testing.T) {
	got := normalize(discovery.Instance{
		Service:  " users ",
		Endpoint: " 10.0.0.1:8080/ ",
		ID:       " ",
		Version:  " v1 ",
		Zone:     " az-a ",
	})
	if got.Service != "users" || got.Endpoint != "10.0.0.1:8080" || got.ID != "users-10.0.0.1:8080" {
		t.Fatalf("normalize core fields = %#v", got)
	}
	if got.Version != "v1" || got.Zone != "az-a" {
		t.Fatalf("normalize version/zone = %#v, want trimmed values", got)
	}
}

func TestRegisterAndLeaseCloseUseConsulAgentAPI(t *testing.T) {
	var mu sync.Mutex
	var registered *consulapi.AgentServiceRegistration
	var ttlUpdates []string
	var deregistered []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/agent/service/register":
			var reg consulapi.AgentServiceRegistration
			if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
				t.Fatalf("decode service registration: %v", err)
			}
			mu.Lock()
			registered = &reg
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/v1/agent/check/pass/"):
			mu.Lock()
			ttlUpdates = append(ttlUpdates, strings.TrimPrefix(r.URL.Path, "/v1/agent/check/pass/"))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/v1/agent/check/update/"):
			mu.Lock()
			ttlUpdates = append(ttlUpdates, strings.TrimPrefix(r.URL.Path, "/v1/agent/check/update/"))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/v1/agent/service/deregister/"):
			mu.Lock()
			deregistered = append(deregistered, strings.TrimPrefix(r.URL.Path, "/v1/agent/service/deregister/"))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := newConsulTestRegistry(t, server.URL, Config{TTL: time.Hour, DeregisterAfter: time.Hour})
	lease, err := r.Register(context.Background(), discovery.Instance{
		Service:  " users ",
		Endpoint: "10.0.0.1:8080/",
		Version:  " v1 ",
		Zone:     " az-a ",
		Tags:     map[string]string{"canary": "true"},
	})
	if err != nil {
		t.Fatalf("Register error = %v", err)
	}
	if err := lease.Close(context.Background()); err != nil {
		t.Fatalf("lease Close error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if registered == nil {
		t.Fatal("service registration was not sent")
	}
	if registered.ID != "users-10.0.0.1:8080" || registered.Name != "users" || registered.Address != "10.0.0.1" || registered.Port != 8080 {
		t.Fatalf("registration = %#v, want normalized service endpoint", registered)
	}
	for _, want := range []string{"version=v1", "zone=az-a", "canary=true"} {
		if !contains(registered.Tags, want) {
			t.Fatalf("registration tags = %#v, missing %q", registered.Tags, want)
		}
	}
	if len(ttlUpdates) == 0 || !strings.Contains(ttlUpdates[0], "service:users-10.0.0.1:8080") {
		t.Fatalf("ttl updates = %#v, want initial service check pass", ttlUpdates)
	}
	if !reflect.DeepEqual(deregistered, []string{"users-10.0.0.1:8080"}) {
		t.Fatalf("deregistered = %#v, want normalized id", deregistered)
	}
}

func TestRegisterValidationAndAgentError(t *testing.T) {
	r := &Registry{}
	_, err := r.Register(context.Background(), discovery.Instance{Endpoint: "127.0.0.1:8080"})
	if err == nil || !strings.Contains(err.Error(), "service and endpoint") {
		t.Fatalf("Register without service error = %v, want service and endpoint", err)
	}

	_, err = r.Register(context.Background(), discovery.Instance{Service: "users"})
	if err == nil || !strings.Contains(err.Error(), "service and endpoint") {
		t.Fatalf("Register without endpoint error = %v, want service and endpoint", err)
	}

	_, err = r.Register(context.Background(), discovery.Instance{Service: "users", Endpoint: "127.0.0.1:http"})
	if err == nil || !strings.Contains(err.Error(), "invalid endpoint port") {
		t.Fatalf("Register bad endpoint error = %v, want invalid endpoint port", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/agent/service/register" {
			http.Error(w, "register failed", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	r = newConsulTestRegistry(t, server.URL, Config{})
	_, err = r.Register(context.Background(), discovery.Instance{Service: "users", Endpoint: "127.0.0.1:8080"})
	if err == nil || !strings.Contains(err.Error(), "consul: register") {
		t.Fatalf("Register agent error = %v, want wrapped register error", err)
	}
}

func TestResolveReadsHealthServiceAndFilters(t *testing.T) {
	entries := []*consulapi.ServiceEntry{
		{Service: &consulapi.AgentService{ID: "users-a", Address: "10.0.0.1", Port: 8080, Tags: []string{"version=v1", "zone=az-a", "canary=true"}}},
		{Service: &consulapi.AgentService{ID: "users-b", Address: "10.0.0.2", Port: 8080, Tags: []string{"version=v2", "zone=az-b", "canary=false"}}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health/service/users" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Consul-Index", "7")
		if err := json.NewEncoder(w).Encode(entries); err != nil {
			t.Fatalf("encode health entries: %v", err)
		}
	}))
	defer server.Close()

	r := newConsulTestRegistry(t, server.URL, Config{})
	got, err := r.Resolve(context.Background(), " users ", discovery.WithVersion(" v1 "), discovery.WithTag("canary", "true"))
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "users-a" || got[0].Endpoint != "10.0.0.1:8080" {
		t.Fatalf("Resolve result = %#v, want filtered users-a", got)
	}
}

func TestWatchEmitsSnapshotAndBlockingUpdate(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health/service/users" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		requests++
		requestNumber := requests
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Consul-Index", strconv.Itoa(requestNumber))
		entry := &consulapi.ServiceEntry{Service: &consulapi.AgentService{
			ID:      "users-a",
			Address: "10.0.0.1",
			Port:    8080,
			Tags:    []string{"version=v1", "canary=true"},
		}}
		if requestNumber > 1 {
			entry.Service.ID = "users-b"
			entry.Service.Address = "10.0.0.2"
		}
		if err := json.NewEncoder(w).Encode([]*consulapi.ServiceEntry{entry}); err != nil {
			t.Fatalf("encode health entries: %v", err)
		}
	}))
	defer server.Close()

	r := newConsulTestRegistry(t, server.URL, Config{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, err := r.Watch(ctx, " users ", discovery.WithTag("canary", "true"))
	if err != nil {
		t.Fatalf("Watch error = %v", err)
	}

	snapshot := readConsulEvent(t, ch)
	if snapshot.Type != discovery.EventSnapshot || snapshot.Service != "users" || len(snapshot.Instances) != 1 || snapshot.Instances[0].ID != "users-a" {
		t.Fatalf("snapshot = %#v, want users-a snapshot", snapshot)
	}
	update := readConsulEvent(t, ch)
	if update.Type != discovery.EventRegistered || update.Service != "users" || len(update.Instances) != 1 || update.Instances[0].ID != "users-b" {
		t.Fatalf("update = %#v, want users-b registered update", update)
	}
	cancel()
}

func TestWatchValidationAndBlockingWatchErrorRecovery(t *testing.T) {
	r := &Registry{}
	if _, err := r.Watch(context.Background(), " "); err == nil || !strings.Contains(err.Error(), "service name is required") {
		t.Fatalf("Watch empty service error = %v, want service name required", err)
	}

	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health/service/users" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		requests++
		requestNumber := requests
		mu.Unlock()
		if requestNumber == 2 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Consul-Index", strconv.Itoa(requestNumber))
		if err := json.NewEncoder(w).Encode([]*consulapi.ServiceEntry{
			{Service: &consulapi.AgentService{ID: "users-a", Address: "10.0.0.1", Port: 8080}},
		}); err != nil {
			t.Fatalf("encode health entries: %v", err)
		}
	}))
	defer server.Close()

	r = newConsulTestRegistry(t, server.URL, Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	ch, err := r.Watch(ctx, "users")
	if err != nil {
		t.Fatalf("Watch error = %v", err)
	}
	_ = readConsulEvent(t, ch)
	for range ch {
	}
}

func TestSleepContextBoundaries(t *testing.T) {
	if !sleepContext(context.Background(), 0) {
		t.Fatal("sleepContext zero duration = false, want true")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepContext(ctx, time.Hour) {
		t.Fatal("sleepContext canceled = true, want false")
	}
}

func TestRegistryCloseAndLeaseBoundaries(t *testing.T) {
	var mu sync.Mutex
	var deregistered []string
	var ttlUpdates int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/agent/service/deregister/"):
			mu.Lock()
			deregistered = append(deregistered, strings.TrimPrefix(r.URL.Path, "/v1/agent/service/deregister/"))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/v1/agent/check/pass/") || strings.HasPrefix(r.URL.Path, "/v1/agent/check/update/"):
			mu.Lock()
			ttlUpdates++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := newConsulTestRegistry(t, server.URL, Config{TTL: time.Hour})
	l1 := &lease{registry: r, instance: discovery.Instance{ID: "users-a"}, checkID: "service:users-a", ttl: time.Hour, expires: time.Now().Add(time.Hour)}
	l2 := &lease{registry: r, instance: discovery.Instance{ID: "users-b"}, checkID: "service:users-b", ttl: time.Hour}
	r.leases = []*lease{l1, l2}

	if got := (*lease)(nil).Instance(); got.ID != "" {
		t.Fatalf("nil lease Instance = %#v, want zero", got)
	}
	if got := (*lease)(nil).ExpiresAt(); !got.IsZero() {
		t.Fatalf("nil lease ExpiresAt = %s, want zero", got)
	}
	if err := (*lease)(nil).KeepAlive(context.Background()); err != nil {
		t.Fatalf("nil lease KeepAlive error = %v, want nil", err)
	}
	if err := (*lease)(nil).Close(context.Background()); err != nil {
		t.Fatalf("nil lease Close error = %v, want nil", err)
	}

	if l1.Instance().ID != "users-a" {
		t.Fatalf("lease Instance = %#v, want users-a", l1.Instance())
	}
	expiresBefore := l1.ExpiresAt()
	if err := l1.KeepAlive(context.Background()); err != nil {
		t.Fatalf("KeepAlive error = %v", err)
	}
	if !l1.ExpiresAt().After(expiresBefore) {
		t.Fatalf("ExpiresAt after KeepAlive = %s, want after %s", l1.ExpiresAt(), expiresBefore)
	}

	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Registry Close error = %v", err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Registry second Close error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(deregistered, []string{"users-a", "users-b"}) {
		t.Fatalf("deregistered = %#v, want users-a/users-b once", deregistered)
	}
	if ttlUpdates == 0 {
		t.Fatal("ttlUpdates = 0, want KeepAlive to update TTL")
	}
}

func newConsulTestRegistry(t *testing.T, address string, cfg Config) *Registry {
	t.Helper()
	apiCfg := consulapi.DefaultConfig()
	apiCfg.Address = address
	client, err := consulapi.NewClient(apiCfg)
	if err != nil {
		t.Fatalf("new consul client: %v", err)
	}
	r, err := NewWithClient(client, cfg)
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	return r
}

func readConsulEvent(t *testing.T, ch <-chan discovery.Event) discovery.Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("event channel closed before event")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for consul event")
	}
	return discovery.Event{}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
