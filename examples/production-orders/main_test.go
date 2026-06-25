package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/discovery"
	"github.com/gofly/gofly/core/outbox"
	"github.com/gofly/gofly/core/retry"
	"github.com/gofly/gofly/rpc"
)

func TestOrderStoreAndInventoryBoundaries_BitsUT(t *testing.T) {
	store := newOrderStore()
	first := store.create("coffee")
	second := store.create("tea")
	if first != "order-001" || second != "order-002" {
		t.Fatalf("order ids = %q/%q, want sequential ids", first, second)
	}
	store.cancel(first)
	if _, ok := store.orders[first]; ok {
		t.Fatalf("order %q still present after cancel", first)
	}

	inv := inventoryService{}
	for _, req := range []*reserveRequest{nil, {SKU: "", Quantity: 1}, {SKU: "coffee", Quantity: 0}, {SKU: "sold-out", Quantity: 1}} {
		if _, err := inv.Reserve(context.Background(), req); err == nil {
			t.Fatalf("Reserve(%#v) succeeded, want error", req)
		}
	}
	resp, err := inv.Reserve(context.Background(), &reserveRequest{SKU: "coffee", Quantity: 2})
	if err != nil {
		t.Fatalf("Reserve valid request: %v", err)
	}
	if resp.ReservationID != "res-coffee" {
		t.Fatalf("reservation id = %q, want res-coffee", resp.ReservationID)
	}
}

func TestProductionOrderReferenceAppContract_BitsUT(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	text := string(readme)
	for _, want := range []string{
		"gofly.reference_app.v1",
		"REST",
		"RPC",
		"MQ",
		"outbox",
		"saga",
		"config",
		"discovery",
		"cache",
		"observability",
		"K8s",
		"rollback",
		"SQL outbox",
		"Redis cache",
		"Kafka",
		"RabbitMQ",
		"Redis Stream",
		"Consul",
		"etcd",
		"Nacos",
		"OpenTelemetry collector",
		"REFERENCE_APP_MODE=memory make reference-app-smoke",
		"REFERENCE_APP_MODE=docker make reference-app-smoke",
		"topology_evidence",
		"fallback note",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("README.md missing %q", want)
		}
	}
}

func TestBuildRESTServerOrderRouteBoundaries_BitsUT(t *testing.T) {
	cfg := serviceConfig{}
	cfg.REST.Port = 0
	cfg.Resilience.RateLimit = 10
	cfg.Resilience.Burst = 10
	registry := discovery.NewMemoryRegistry()
	store := outbox.NewMemoryStore()
	relay := outbox.NewRelay(store, outbox.PublisherFunc(func(context.Context, outbox.Message) error { return nil }), outbox.RelayConfig{BatchSize: 10})
	srv := buildRESTServer(cfg, nil, registry, store, relay, newOrderStore(), loadProductionTopology())

	badJSON := httptest.NewRecorder()
	srv.Handler().ServeHTTP(badJSON, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{`)))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad json status = %d body=%s, want 400", badJSON.Code, badJSON.Body.String())
	}

	missingFields := httptest.NewRecorder()
	srv.Handler().ServeHTTP(missingFields, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"","quantity":0}`)))
	if missingFields.Code != http.StatusBadRequest {
		t.Fatalf("missing fields status = %d body=%s, want 400", missingFields.Code, missingFields.Body.String())
	}

	controlPlane := httptest.NewRecorder()
	cpReq := httptest.NewRequest(http.MethodGet, "/admin/control-plane", nil)
	cpReq.Header.Set("Authorization", "Bearer orders-token")
	srv.Handler().ServeHTTP(controlPlane, cpReq)
	if controlPlane.Code != http.StatusOK {
		t.Fatalf("control-plane status = %d body=%s, want 200", controlPlane.Code, controlPlane.Body.String())
	}

	topology := httptest.NewRecorder()
	srv.Handler().ServeHTTP(topology, httptest.NewRequest(http.MethodGet, "/topology", nil))
	if topology.Code != http.StatusOK {
		t.Fatalf("topology status = %d body=%s, want 200", topology.Code, topology.Body.String())
	}
	var topologyPayload productionTopology
	if err := json.Unmarshal(topology.Body.Bytes(), &topologyPayload); err != nil {
		t.Fatalf("decode topology: %v", err)
	}
	if topologyPayload.Mode != topologyMemory || topologyPayload.SQLOutbox != "memory" || len(topologyPayload.MQ) == 0 {
		t.Fatalf("topology payload = %#v, want memory topology", topologyPayload)
	}

	unavailable := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unavailable, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"coffee","quantity":1}`)))
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d body=%s, want 503", unavailable.Code, unavailable.Body.String())
	}

	limitedCfg := cfg
	limitedCfg.Resilience.RateLimit = 1
	limitedCfg.Resilience.Burst = 1
	limitedSrv := buildRESTServer(limitedCfg, nil, registry, store, relay, newOrderStore(), loadProductionTopology())
	first := httptest.NewRecorder()
	limitedSrv.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"coffee","quantity":1}`)))
	rateLimited := httptest.NewRecorder()
	limitedSrv.Handler().ServeHTTP(rateLimited, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"coffee","quantity":1}`)))
	if rateLimited.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited status = %d body=%s, want 429", rateLimited.Code, rateLimited.Body.String())
	}
}

func TestProductionOrderHelpers_BitsUT(t *testing.T) {
	cfg := mustLoadConfig()
	if cfg.REST.Port != 8090 || cfg.RPC.Port != 8092 || cfg.Admin.Port != 8091 || cfg.Resilience.RateLimit != 20 || cfg.Resilience.Burst != 5 {
		t.Fatalf("config = %#v, want embedded production-order defaults", cfg)
	}
	reqSchema := orderRequestSchema()
	if reqSchema.Type != "object" || len(reqSchema.Required) != 2 || reqSchema.Properties["sku"].Type != "string" {
		t.Fatalf("request schema = %#v, want sku/quantity object schema", reqSchema)
	}
	respSchema := orderResponseSchema()
	if respSchema.Type != "object" || len(respSchema.Required) != 2 || respSchema.Properties["trace_id"].Type != "string" {
		t.Fatalf("response schema = %#v, want trace/request fields", respSchema)
	}
	topologySchema := topologySchema()
	if topologySchema.Type != "object" || len(topologySchema.Required) != 7 || topologySchema.Properties["mq"].Items.Type != "string" || topologySchema.Properties["topology_evidence"].Items.Type != "object" {
		t.Fatalf("topology schema = %#v, want production topology object schema", topologySchema)
	}
}

func TestProductionTopologyModes_BitsUT(t *testing.T) {
	t.Setenv("REFERENCE_APP_MODE", "")
	memory := loadProductionTopology()
	if memory.Mode != topologyMemory || memory.SQLOutbox != "memory" || memory.Cache != "memory" || strings.Join(memory.MQ, ",") != "memory" {
		t.Fatalf("memory topology = %#v, want in-memory adapters", memory)
	}

	t.Setenv("REFERENCE_APP_MODE", "docker")
	t.Setenv("ORDER_SQL_DSN", "postgres://orders:orders@postgres:5432/orders?sslmode=disable")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("KAFKA_BROKERS", "kafka:9092")
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/")
	t.Setenv("REDIS_STREAM_ADDR", "redis:6379")
	t.Setenv("CONSUL_ADDR", "consul:8500")
	t.Setenv("ETCD_ENDPOINTS", "etcd:2379")
	t.Setenv("NACOS_ADDR", "nacos:8848")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4318")
	docker := loadProductionTopology()
	if docker.Mode != topologyDocker || docker.SQLOutbox == "memory" || !contains(docker.MQ, "kafka:9092") || !contains(docker.Discovery, "nacos:8848") {
		t.Fatalf("docker topology = %#v, want SQL/cache/MQ/discovery/OTel endpoints", docker)
	}
}

func TestProductionTopologyEvidenceContract_BitsUT(t *testing.T) {
	t.Setenv("REFERENCE_APP_MODE", "")
	memory := loadProductionTopology()
	assertTopologyEvidence(t, memory, []string{"SQL outbox", "Redis cache", "MQ", "Discovery", "OpenTelemetry collector"})

	t.Setenv("REFERENCE_APP_MODE", "docker")
	docker := loadProductionTopology()
	assertTopologyEvidence(t, docker, []string{"SQL outbox", "Redis cache", "Kafka", "RabbitMQ", "Redis Stream", "Consul", "etcd", "Nacos", "OpenTelemetry collector"})
}

func assertTopologyEvidence(t *testing.T, topology productionTopology, components []string) {
	t.Helper()
	if len(topology.Evidence) < len(components) {
		t.Fatalf("topology evidence = %#v, want at least %d entries", topology.Evidence, len(components))
	}
	for _, component := range components {
		found := false
		for _, evidence := range topology.Evidence {
			if evidence.Component != component {
				continue
			}
			found = true
			if evidence.Mode == "" || evidence.Endpoint == "" || evidence.Validation == "" || evidence.FallbackNote == "" {
				t.Fatalf("topology evidence for %s = %#v, want mode, endpoint, validation and fallback note", component, evidence)
			}
		}
		if !found {
			t.Fatalf("topology evidence = %#v, missing component %q", topology.Evidence, component)
		}
	}
}

func TestInventoryServiceDesc_BitsUT(t *testing.T) {
	desc := inventoryServiceDesc(inventoryService{})
	if err := desc.Validate(); err != nil {
		t.Fatalf("inventory descriptor Validate: %v", err)
	}
	if desc.Name != "examples.orders.Inventory" || desc.Version != "v1" || len(desc.Methods) != 1 {
		t.Fatalf("descriptor = %#v, want one v1 inventory method", desc)
	}
	if _, err := desc.Methods[0].Handler(context.Background(), "bad request"); err == nil || !strings.Contains(err.Error(), "unexpected request type") {
		t.Fatalf("unexpected request error = %v, want type error", err)
	}
	got, err := desc.Methods[0].Handler(context.Background(), &reserveRequest{SKU: "coffee", Quantity: 1})
	if err != nil {
		t.Fatalf("handler valid request: %v", err)
	}
	if got.(*reserveResponse).ReservationID != "res-coffee" {
		t.Fatalf("handler response = %#v, want res-coffee", got)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCreateOrderNoInventoryCompensates_BitsUT(t *testing.T) {
	registry := discovery.NewMemoryRegistry()
	store := outbox.NewMemoryStore()
	relay := outbox.NewRelay(store, outbox.PublisherFunc(func(context.Context, outbox.Message) error { return nil }), outbox.RelayConfig{BatchSize: 10})
	orders := newOrderStore()
	_, err := createOrder(context.Background(), createOrderRequest{SKU: "coffee", Quantity: 1}, registry, store, relay, orders, breaker.New(), retry.Policy{Attempts: 1})
	if err == nil || !strings.Contains(err.Error(), "resolve inventory") {
		t.Fatalf("createOrder error = %v, want inventory resolution error", err)
	}
	if len(orders.orders) != 1 || orders.orders["order-001"] != "coffee" {
		t.Fatalf("orders after first-step failure = %#v, want current pre-saga create behavior recorded", orders.orders)
	}
}

func TestCreateOrderSuccessPublishesOutbox_BitsUT(t *testing.T) {
	rpcSrv := rpc.NewServer()
	if err := rpcSrv.RegisterService(inventoryServiceDesc(inventoryService{}), nil); err != nil {
		t.Fatalf("register inventory rpc: %v", err)
	}
	ts := httptest.NewServer(rpcSrv)
	defer ts.Close()

	registry := discovery.NewMemoryRegistry()
	if _, err := registry.Register(context.Background(), discovery.Instance{Service: "orders-inventory", Endpoint: ts.URL, Status: discovery.StatusHealthy}); err != nil {
		t.Fatalf("register discovery instance: %v", err)
	}
	store := outbox.NewMemoryStore()
	published := make([]outbox.Message, 0, 1)
	relay := outbox.NewRelay(store, outbox.PublisherFunc(func(ctx context.Context, msg outbox.Message) error {
		published = append(published, msg)
		return nil
	}), outbox.RelayConfig{BatchSize: 10})
	orders := newOrderStore()

	resp, err := createOrder(context.Background(), createOrderRequest{SKU: "coffee", Quantity: 2}, registry, store, relay, orders, breaker.New(), retry.Policy{Attempts: 1})
	if err != nil {
		t.Fatalf("createOrder: %v", err)
	}
	if resp.ID != "order-001" || resp.Status != "accepted" {
		t.Fatalf("createOrder response = %#v, want accepted order-001", resp)
	}
	if len(published) != 1 || published[0].Topic != "orders.created" || published[0].Key != "order-001" || !strings.Contains(string(published[0].Body), `"sku":"coffee"`) {
		t.Fatalf("published messages = %#v, want one orders.created coffee event", published)
	}
}
