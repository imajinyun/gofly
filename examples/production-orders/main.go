// Command production-orders demonstrates a compact production-style service:
//
//   - REST API accepts order creation requests
//   - RPC service reserves inventory
//   - profile config and memory discovery provide runtime wiring
//   - Docker-backed topology wires SQL outbox, Redis cache, MQ, discovery and OTel endpoints
//   - limiter, retry and breaker protect downstream calls
//   - saga compensates partially completed business steps
//   - outbox relays committed events into an in-memory MQ worker
//   - cache boundaries can be added behind the same request path without
//     changing the REST/RPC contract
//   - observability exposes metrics, health and pprof on an admin port
//
// Run it:
//
//	go run ./examples/production-orders
//
// Then try:
//
//	curl -s -X POST localhost:8090/orders -d '{"sku":"coffee","quantity":2}'
//	curl -s localhost:8091/debug/metrics.json
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/gofly/gofly/app"
	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/config"
	"github.com/gofly/gofly/core/discovery"
	"github.com/gofly/gofly/core/limit"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/mq"
	"github.com/gofly/gofly/core/observability"
	"github.com/gofly/gofly/core/observability/metrics"
	"github.com/gofly/gofly/core/observability/trace"
	"github.com/gofly/gofly/core/outbox"
	"github.com/gofly/gofly/core/retry"
	"github.com/gofly/gofly/core/saga"
	"github.com/gofly/gofly/rest"
	"github.com/gofly/gofly/rpc"
)

type serviceConfig struct {
	REST struct {
		Port int `json:"port"`
	} `json:"rest"`
	RPC struct {
		Port int `json:"port"`
	} `json:"rpc"`
	Admin struct {
		Port int `json:"port"`
	} `json:"admin"`
	Resilience struct {
		RateLimit int `json:"rate_limit"`
		Burst     int `json:"burst"`
	} `json:"resilience"`
}

type topologyMode string

const (
	topologyMemory topologyMode = "memory"
	topologyDocker topologyMode = "docker"
)

type productionTopology struct {
	Mode          topologyMode       `json:"mode"`
	SQLOutbox     string             `json:"sql_outbox"`
	Cache         string             `json:"cache"`
	MQ            []string           `json:"mq"`
	Discovery     []string           `json:"discovery"`
	Observability string             `json:"observability"`
	Evidence      []topologyEvidence `json:"topology_evidence"`
}

type topologyEvidence struct {
	Component    string `json:"component"`
	Mode         string `json:"mode"`
	Endpoint     string `json:"endpoint"`
	Validation   string `json:"validation"`
	FallbackNote string `json:"fallback_note"`
}

type createOrderRequest struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type createOrderResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	TraceID   string `json:"trace_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type reserveRequest struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type reserveResponse struct {
	ReservationID string `json:"reservation_id"`
}

type orderStore struct {
	mu     sync.Mutex
	next   int
	orders map[string]string
}

func newOrderStore() *orderStore {
	return &orderStore{orders: make(map[string]string)}
}

func (s *orderStore) create(sku string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	id := fmt.Sprintf("order-%03d", s.next)
	s.orders[id] = sku
	return id
}

func (s *orderStore) cancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.orders, id)
}

type inventoryService struct{}

func (inventoryService) Reserve(ctx context.Context, req *reserveRequest) (*reserveResponse, error) {
	if req == nil || req.SKU == "" || req.Quantity <= 0 {
		return nil, errors.New("invalid reservation request")
	}
	if req.SKU == "sold-out" {
		return nil, errors.New("inventory unavailable")
	}
	slog.InfoContext(ctx, "inventory reserved", "sku", req.SKU, "quantity", req.Quantity)
	return &reserveResponse{ReservationID: "res-" + req.SKU}, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := mustLoadConfig()
	topology := loadProductionTopology()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	slog.Info("reference app topology",
		"mode", topology.Mode,
		"sql_outbox", topology.SQLOutbox,
		"cache", topology.Cache,
		"mq", topology.MQ,
		"discovery", topology.Discovery,
		"observability", topology.Observability,
	)

	registry := metrics.NewRegistry()
	obs := observability.NewObserve(observability.ObserverConfig{
		Service:  "orders",
		Registry: registry,
		Logger: &observability.LoggerConfig{
			JSON:  true,
			Level: slog.LevelInfo,
		},
		Sampler:    trace.RatioSampler(1.0),
		Pprof:      true,
		ExposeJSON: true,
	})

	discoveryRegistry := discovery.NewMemoryRegistry()
	rpcEndpoint := fmt.Sprintf("http://127.0.0.1:%d", cfg.RPC.Port)
	lease, err := discoveryRegistry.Register(ctx, discovery.Instance{
		Service:  "orders-inventory",
		Endpoint: rpcEndpoint,
		Version:  "v1",
		Zone:     "local",
		Status:   discovery.StatusHealthy,
		Tags:     map[string]string{"component": "inventory"},
	}, discovery.WithTTL(time.Minute))
	if err != nil {
		log.Fatalf("register inventory: %v", err)
	}
	defer lease.Close(context.Background())

	broker := mq.NewMemoryBroker()
	defer broker.Close(context.Background())
	sub, err := broker.Subscribe(ctx, "orders.created", "fulfillment", func(ctx context.Context, msg mq.Message) error {
		slog.InfoContext(ctx, "fulfillment worker received order",
			"key", msg.Key,
			"body", string(msg.Body),
			"trace_id", msg.Headers["trace_id"],
		)
		return nil
	}, mq.WithMaxAttempts(2), mq.WithRetryBackoff(25*time.Millisecond))
	if err != nil {
		log.Fatalf("subscribe fulfillment worker: %v", err)
	}
	defer func() {
		_ = sub.Stop(context.Background())
	}()

	outboxStore := outbox.NewMemoryStore()
	relay := outbox.NewRelay(outboxStore, outbox.BrokerPublisher(mq.AsBroker(broker)), outbox.RelayConfig{
		BatchSize:    10,
		PollInterval: 100 * time.Millisecond,
		Visibility:   time.Second,
		MaxAttempts:  3,
		BaseBackoff:  50 * time.Millisecond,
		MaxBackoff:   time.Second,
		Logger:       logger,
	})

	rpcSrv := rpc.NewServer(rpc.WithAddress(fmt.Sprintf(":%d", cfg.RPC.Port)))
	if err := rpcSrv.RegisterService(inventoryServiceDesc(inventoryService{}), nil); err != nil {
		log.Fatalf("register rpc service: %v", err)
	}

	restSrv := buildRESTServer(cfg, registry, discoveryRegistry, outboxStore, relay, newOrderStore(), topology)

	adminMux := http.NewServeMux()
	obs.Register(adminMux, "/debug")
	adminSrv := &httpServer{
		name:   "orders-admin",
		server: &http.Server{Addr: fmt.Sprintf(":%d", cfg.Admin.Port), Handler: adminMux, ReadHeaderTimeout: 3 * time.Second},
	}

	log.Printf("orders REST :%d, RPC :%d, admin :%d", cfg.REST.Port, cfg.RPC.Port, cfg.Admin.Port)
	if err := app.Run(ctx, []app.Server{rpcSrv, restSrv, adminSrv}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("orders service stopped: %v", err)
	}
}

func buildRESTServer(
	cfg serviceConfig,
	registry *metrics.Registry,
	discoveryRegistry discovery.Registry,
	outboxStore *outbox.MemoryStore,
	relay *outbox.Relay,
	orders *orderStore,
	topology productionTopology,
) *rest.Server {
	limiter := limit.New(cfg.Resilience.RateLimit, cfg.Resilience.Burst)
	inventoryBreaker := breaker.New(
		breaker.WithFailureThreshold(2),
		breaker.WithOpenTimeout(500*time.Millisecond),
	)
	retryPolicy := retry.Policy{
		Attempts:    3,
		BackoffFunc: retry.ExponentialBackoff(10*time.Millisecond, 50*time.Millisecond),
		ShouldRetry: func(err error) bool {
			return !errors.Is(err, breaker.ErrOpen)
		},
	}

	srv := rest.MustNewServer(rest.Config{
		Name: "orders",
		Host: "0.0.0.0",
		Port: cfg.REST.Port,
		Admin: rest.AdminConfig{
			Enabled:    true,
			PathPrefix: "/admin",
			Token:      "orders-token",
			Audit:      true,
		},
		Middlewares: rest.MiddlewaresConfig{
			Recover:   true,
			Trace:     true,
			Log:       true,
			Metrics:   true,
			Health:    true,
			RequestID: true,
		},
	})
	srv.Use(rest.MetricsMiddleware(registry))

	srv.AddRoute(rest.Route{
		Method:      http.MethodPost,
		Path:        "/orders",
		Summary:     "Create an order through REST, RPC, saga, outbox and MQ",
		Description: "Demonstrates production-style composition with resilience and observability.",
		Tags:        []string{"orders"},
		RequestBody: &rest.RequestBody{Description: "order input", Required: true, Content: map[string]rest.MediaType{"application/json": {Schema: orderRequestSchema()}}},
		Responses:   map[string]rest.Response{"202": rest.JSONResponse("accepted order", orderResponseSchema())},
		Handler: func(c *rest.Context) {
			if !limiter.Allow() {
				c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
				return
			}
			var req createOrderRequest
			if err := c.Bind(&req); err != nil {
				c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if req.SKU == "" || req.Quantity <= 0 {
				c.JSON(http.StatusBadRequest, map[string]string{"error": "sku and positive quantity are required"})
				return
			}

			response, err := createOrder(c.Request.Context(), req, discoveryRegistry, outboxStore, relay, orders, inventoryBreaker, retryPolicy)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
				return
			}
			c.JSON(http.StatusAccepted, response)
		},
	})
	srv.AddRoute(rest.Route{
		Method:      http.MethodGet,
		Path:        "/topology",
		Summary:     "Show the production reference topology",
		Description: "Returns the active memory or Docker-backed topology contract for this reference app.",
		Tags:        []string{"orders"},
		Responses:   map[string]rest.Response{"200": rest.JSONResponse("production topology", topologySchema())},
		Handler: func(c *rest.Context) {
			c.JSON(http.StatusOK, topology)
		},
	})
	srv.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "production orders", Version: "1.0.0"})
	return srv
}

func createOrder(
	ctx context.Context,
	req createOrderRequest,
	registry discovery.Registry,
	outboxStore *outbox.MemoryStore,
	relay *outbox.Relay,
	orders *orderStore,
	br *breaker.Breaker,
	policy retry.Policy,
) (createOrderResponse, error) {
	span, ok := trace.FromContext(ctx)
	if !ok {
		ctx, span = observability.StartTrace(ctx, "", "orders", trace.ParentBasedSampler(trace.AlwaysSampler()))
	}
	orderID := orders.create(req.SKU)
	var reservationID string

	flow := saga.New().
		Step("reserve inventory", func(ctx context.Context) error {
			return retry.Do(ctx, policy, func() error {
				return br.Do(ctx, func() error {
					resp, err := reserveInventory(ctx, registry, req)
					if err != nil {
						return err
					}
					reservationID = resp.ReservationID
					return nil
				})
			})
		}, func(ctx context.Context) error {
			slog.WarnContext(ctx, "inventory reservation compensated", "order_id", orderID, "reservation_id", reservationID)
			return nil
		}).
		Step("commit order", func(ctx context.Context) error {
			return nil
		}, func(ctx context.Context) error {
			orders.cancel(orderID)
			slog.WarnContext(ctx, "order creation compensated", "order_id", orderID)
			return nil
		}).
		Step("enqueue outbox event", func(ctx context.Context) error {
			_, err := outboxStore.Enqueue(ctx, outbox.Message{
				Topic: "orders.created",
				Key:   orderID,
				Body:  []byte(fmt.Sprintf(`{"id":%q,"sku":%q,"quantity":%d}`, orderID, req.SKU, req.Quantity)),
				Headers: map[string]string{
					"trace_id":   span.TraceID,
					"request_id": metadata.RequestIDFromContext(ctx),
				},
			})
			return err
		}, nil)

	if err := flow.Execute(ctx); err != nil {
		return createOrderResponse{}, err
	}
	if _, err := relay.ProcessBatch(ctx); err != nil {
		return createOrderResponse{}, fmt.Errorf("publish outbox event: %w", err)
	}
	return createOrderResponse{
		ID:        orderID,
		Status:    "accepted",
		TraceID:   span.TraceID,
		RequestID: metadata.RequestIDFromContext(ctx),
	}, nil
}

func reserveInventory(ctx context.Context, registry discovery.Registry, req createOrderRequest) (*reserveResponse, error) {
	instances, err := registry.Resolve(ctx, "orders-inventory")
	if err != nil {
		return nil, fmt.Errorf("resolve inventory: %w", err)
	}
	if len(instances) == 0 {
		return nil, errors.New("inventory service has no healthy instances")
	}
	client, err := rpc.NewClient(instances[0].Endpoint)
	if err != nil {
		return nil, err
	}
	var resp reserveResponse
	err = client.Call(ctx, "examples.orders.Inventory/Reserve", reserveRequest(req), &resp)
	if err != nil {
		return nil, fmt.Errorf("inventory rpc: %w", err)
	}
	return &resp, nil
}

func inventoryServiceDesc(impl inventoryService) rpc.ServiceDesc {
	return rpc.ServiceDesc{
		Name:    "examples.orders.Inventory",
		Version: "v1",
		Methods: []rpc.MethodDesc{{
			Name:       "Reserve",
			NewRequest: func() any { return new(reserveRequest) },
			Metadata:   map[string]string{"request": "reserveRequest", "response": "reserveResponse"},
			Handler: func(ctx context.Context, req any) (any, error) {
				typed, ok := req.(*reserveRequest)
				if !ok {
					return nil, fmt.Errorf("unexpected request type %T", req)
				}
				return impl.Reserve(ctx, typed)
			},
		}},
	}
}

type httpServer struct {
	name   string
	server *http.Server
}

func (s *httpServer) Start() error {
	slog.Info("http server starting", "name", s.name, "addr", s.server.Addr)
	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *httpServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func mustLoadConfig() serviceConfig {
	dir, err := os.MkdirTemp("", "gofly-orders-config-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	writeConfig(filepath.Join(dir, "orders.json"), `{
  "rest": {"port": 8090},
  "rpc": {"port": 8092},
  "admin": {"port": 8091},
  "resilience": {"rate_limit": 20, "burst": 5}
}`)
	provider := config.NewProfileProvider[serviceConfig](config.ProfileOptions{
		Dir:  dir,
		Name: "orders",
	})
	manager, err := config.NewManagerFromProvider(provider, config.WithValidator(func(cfg serviceConfig) error {
		if cfg.REST.Port <= 0 || cfg.RPC.Port <= 0 || cfg.Admin.Port <= 0 {
			return errors.New("all ports must be positive")
		}
		if cfg.Resilience.RateLimit <= 0 || cfg.Resilience.Burst <= 0 {
			return errors.New("resilience rate limit and burst must be positive")
		}
		return nil
	}))
	if err != nil {
		panic(err)
	}
	return manager.Current()
}

func writeConfig(path, body string) {
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		panic(err)
	}
}

func loadProductionTopology() productionTopology {
	mode := topologyMode(os.Getenv("REFERENCE_APP_MODE"))
	if mode == "" {
		mode = topologyMemory
	}
	if mode != topologyDocker {
		return productionTopology{
			Mode:          topologyMemory,
			SQLOutbox:     "memory",
			Cache:         "memory",
			MQ:            []string{"memory"},
			Discovery:     []string{"memory"},
			Observability: "local-admin",
			Evidence: []topologyEvidence{
				{Component: "SQL outbox", Mode: "memory", Endpoint: "memory", Validation: "go test -C examples/production-orders ./...", FallbackNote: "no external SQL dependency in memory mode"},
				{Component: "Redis cache", Mode: "memory", Endpoint: "memory", Validation: "REFERENCE_APP_MODE=memory make reference-app-smoke", FallbackNote: "read-through cache boundary uses in-process state"},
				{Component: "MQ", Mode: "memory", Endpoint: "memory", Validation: "outbox relay publishes orders.created in unit smoke", FallbackNote: "broker adapters are represented by Docker topology evidence"},
				{Component: "Discovery", Mode: "memory", Endpoint: "memory", Validation: "/admin/control-plane reports registered inventory endpoint", FallbackNote: "external discovery is disabled for local smoke"},
				{Component: "OpenTelemetry collector", Mode: "memory", Endpoint: "local-admin", Validation: "/debug/metrics and trace propagation smoke", FallbackNote: "OTLP export is replaced by local admin observability"},
			},
		}
	}
	sql := envOr("ORDER_SQL_DSN", "postgres://orders:orders@postgres:5432/orders?sslmode=disable")
	redisAddr := envOr("REDIS_ADDR", "redis:6379")
	kafka := envOr("KAFKA_BROKERS", "kafka:9092")
	rabbit := envOr("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/")
	redisStream := envOr("REDIS_STREAM_ADDR", "redis:6379")
	consul := envOr("CONSUL_ADDR", "consul:8500")
	etcd := envOr("ETCD_ENDPOINTS", "etcd:2379")
	nacos := envOr("NACOS_ADDR", "nacos:8848")
	otel := envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4318")
	return productionTopology{
		Mode:          topologyDocker,
		SQLOutbox:     sql,
		Cache:         redisAddr,
		MQ:            []string{kafka, rabbit, redisStream},
		Discovery:     []string{consul, etcd, nacos},
		Observability: otel,
		Evidence: []topologyEvidence{
			{Component: "SQL outbox", Mode: "docker", Endpoint: sql, Validation: "compose service postgres plus /topology sql_outbox", FallbackNote: "static topology contract is verified when Docker is unavailable"},
			{Component: "Redis cache", Mode: "docker", Endpoint: redisAddr, Validation: "compose service redis plus /topology cache", FallbackNote: "memory cache remains the local fallback"},
			{Component: "Kafka", Mode: "docker", Endpoint: kafka, Validation: "compose service kafka plus /topology mq", FallbackNote: "memory broker remains the local fallback"},
			{Component: "RabbitMQ", Mode: "docker", Endpoint: rabbit, Validation: "compose service rabbitmq plus /topology mq", FallbackNote: "memory broker remains the local fallback"},
			{Component: "Redis Stream", Mode: "docker", Endpoint: redisStream, Validation: "compose service redis plus /topology mq", FallbackNote: "memory broker remains the local fallback"},
			{Component: "Consul", Mode: "docker", Endpoint: consul, Validation: "compose service consul plus /topology discovery", FallbackNote: "memory discovery remains the local fallback"},
			{Component: "etcd", Mode: "docker", Endpoint: etcd, Validation: "compose service etcd plus /topology discovery", FallbackNote: "memory discovery remains the local fallback"},
			{Component: "Nacos", Mode: "docker", Endpoint: nacos, Validation: "compose service nacos plus /topology discovery", FallbackNote: "memory discovery remains the local fallback"},
			{Component: "OpenTelemetry collector", Mode: "docker", Endpoint: otel, Validation: "compose service otel-collector plus /topology observability", FallbackNote: "local admin observability remains the local fallback"},
		},
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func orderRequestSchema() *rest.Schema {
	return &rest.Schema{
		Type: "object",
		Properties: map[string]rest.Schema{
			"sku":      {Type: "string"},
			"quantity": {Type: "integer"},
		},
		Required: []string{"sku", "quantity"},
	}
}

func topologySchema() rest.Schema {
	return rest.Schema{
		Type: "object",
		Properties: map[string]rest.Schema{
			"mode":          {Type: "string"},
			"sql_outbox":    {Type: "string"},
			"cache":         {Type: "string"},
			"mq":            {Type: "array", Items: &rest.Schema{Type: "string"}},
			"discovery":     {Type: "array", Items: &rest.Schema{Type: "string"}},
			"observability": {Type: "string"},
			"topology_evidence": {Type: "array", Items: &rest.Schema{Type: "object", Properties: map[string]rest.Schema{
				"component":     {Type: "string"},
				"mode":          {Type: "string"},
				"endpoint":      {Type: "string"},
				"validation":    {Type: "string"},
				"fallback_note": {Type: "string"},
			}}},
		},
		Required: []string{"mode", "sql_outbox", "cache", "mq", "discovery", "observability", "topology_evidence"},
	}
}

func orderResponseSchema() rest.Schema {
	return rest.Schema{
		Type: "object",
		Properties: map[string]rest.Schema{
			"id":         {Type: "string"},
			"status":     {Type: "string"},
			"trace_id":   {Type: "string"},
			"request_id": {Type: "string"},
		},
		Required: []string{"id", "status"},
	}
}
