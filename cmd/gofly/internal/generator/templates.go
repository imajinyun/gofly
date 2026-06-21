package generator

const goModTemplate = `module {{.Module}}

go 1.26

require github.com/gofly/gofly v0.0.0
{{.ReplaceBlock}}
`

const mainTemplate = `package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/gofly/gofly/app"
	"github.com/gofly/gofly/core/config"
	"github.com/gofly/gofly/core/controlplane"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/proc"
	"github.com/gofly/gofly/rest"
	"github.com/gofly/gofly/rpc"

	appadmin "{{.Module}}/internal/admin"
	appconfig "{{.Module}}/internal/config"
	appdiscovery "{{.Module}}/internal/discovery"
	appmq "{{.Module}}/internal/mq"
	apprpc "{{.Module}}/internal/rpc"
	"{{.Module}}/internal/routes"
	"{{.Module}}/internal/svc"
)

func main() {
	var c appconfig.Config
	configPath := appconfig.ResolveConfigPath("{{.Name}}")
	if err := config.Load(configPath, &c, config.WithEnvExpansion(), config.WithStrictFields(), config.WithLoadValidator(appconfig.Validate)); err != nil {
		slog.Error("load config", "error", err)
		return
	}
	ctx, stop := proc.SignalContext(context.Background())
	defer stop()
	serviceConf := c.ServiceConf()
	bootstrapConf := serviceConf.BootstrapConfig("{{.Name}}")
	shutdown, runtimeState, err := app.BootstrapWithRuntime(ctx, bootstrapConf)
	if err != nil {
		slog.Error("bootstrap", "error", err)
		return
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()
	governanceManager, err := governance.NewManager(c.Governance, governance.WithPlugin(serviceConf.ProductionGovernancePlugin()))
	if err != nil {
		slog.Error("setup governance", "error", err)
		return
	}
	mqBroker, err := appmq.NewBroker(c.MQ, governanceManager)
	if err != nil {
		slog.Error("setup mq", "error", err)
		return
	}
	defer func() { _ = mqBroker.Close(context.Background()) }()
	svcCtx := svc.NewServiceContext(c, mqBroker)
	restConf := serviceConf.RESTConfig(c.Rest)
	httpServer := rest.MustNewServer(
		restConf,
		rest.WithGovernanceManager(governanceManager),
	)
	routes.RegisterRoutes(httpServer, svcCtx)
	if c.OpenAPIEnabled() {
		httpServer.AddOpenAPIRoutes(c.OpenAPIInfo())
	}
	registry, closeRegistry, err := appdiscovery.NewRegistry(ctx, c.Discovery)
	if err != nil {
		slog.Error("setup discovery", "error", err)
		return
	}
	defer func() { _ = closeRegistry(context.Background()) }()
	registrar := rpc.NewDiscoveryRegistrar(registry, c.Discovery.RegisterOptions()...)
	rpcOptions := append(serviceConf.RPCServerOptions(),
		rpc.WithAddress(c.RPC.Addr),
		rpc.WithRegistry(registrar, "greeter", c.RPC.Advertise),
		rpc.WithRegistryTTL(c.Discovery.RegistryTTL()),
		rpc.WithServerGovernanceManager(governanceManager),
	)
	rpcServer := rpc.NewServer(rpcOptions...)
	if err := rpcServer.RegisterService(apprpc.GreeterService(svcCtx), nil); err != nil {
		slog.Error("register rpc", "error", err)
		return
	}
	servers := []app.Server{httpServer, rpcServer}
	if c.Admin.Enabled {
		servers = append(servers, appadmin.NewServer(c.Admin.Addr, c.Admin.PathPrefix, rpcServer, appadmin.WithControlPlaneSnapshot(func(ctx context.Context) (controlplane.Snapshot, error) {
			return c.ControlPlaneSnapshotWithDiscovery(ctx, registry)
		})))
	}
	governanceManager.StartAsync(ctx, func(err error) { slog.Warn("governance manager stopped", "error", err) })
	go func() {
		if err := config.Watch[appconfig.Config](ctx, configPath, 2*time.Second, func(next appconfig.Config) {
			svcCtx.UpdateConfig(next)
			slog.Info("config reloaded", "rest_host", next.Rest.Host, "rest_port", next.Rest.Port, "rpc_addr", next.RPC.Addr)
		}); err != nil && ctx.Err() == nil {
			slog.Warn("watch config", "error", err)
		}
	}()
	slog.Info("{{.Name}} starting", "rest_host", restConf.Host, "rest_port", restConf.Port, "rpc_addr", c.RPC.Addr, "runtime", runtimeState.Snapshot(ctx))
	if err := app.Run(ctx, servers, serviceConf.RunOptions()...); err != nil {
		slog.Error("{{.Name}} stopped", "error", err)
	}
}
`

const minimalMainTemplate = `package main

import (
	"context"
	"log/slog"

	"github.com/gofly/gofly/app"
	"github.com/gofly/gofly/core/config"
	"github.com/gofly/gofly/core/proc"
	"github.com/gofly/gofly/rest"

	appconfig "{{.Module}}/internal/config"
	"{{.Module}}/internal/routes"
	"{{.Module}}/internal/svc"
)

func main() {
	var c appconfig.Config
	configPath := appconfig.ResolveConfigPath("{{.Name}}")
	if err := config.Load(configPath, &c, config.WithEnvExpansion(), config.WithStrictFields(), config.WithLoadValidator(appconfig.Validate)); err != nil {
		slog.Error("load config", "error", err)
		return
	}
	ctx, stop := proc.SignalContext(context.Background())
	defer stop()
	serviceConf := c.ServiceConf()
	shutdown, err := app.Bootstrap(ctx, serviceConf.BootstrapConfig("{{.Name}}"))
	if err != nil {
		slog.Error("bootstrap", "error", err)
		return
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()
	svcCtx := svc.NewServiceContext(c)
	restConf := serviceConf.RESTConfig(c.Rest)
	httpServer := rest.MustNewServer(restConf)
	routes.RegisterRoutes(httpServer, svcCtx)
	if c.OpenAPIEnabled() {
		httpServer.AddOpenAPIRoutes(c.OpenAPIInfo())
	}
	slog.Info("{{.Name}} starting", "rest_host", restConf.Host, "rest_port", restConf.Port)
	if err := app.Run(ctx, []app.Server{httpServer}, serviceConf.RunOptions()...); err != nil {
		slog.Error("{{.Name}} stopped", "error", err)
	}
}
`

const goZeroMainTemplate = `package main

import (
	"context"
	"log/slog"

	"github.com/gofly/gofly/app"
	"github.com/gofly/gofly/core/config"
	"github.com/gofly/gofly/core/proc"
	"github.com/gofly/gofly/rest"

	appconfig "{{.Module}}/internal/config"
	"{{.Module}}/internal/handler"
	"{{.Module}}/internal/svc"
)

func main() {
	var c appconfig.Config
	configPath := appconfig.ResolveConfigPath("{{.Name}}")
	if err := config.Load(configPath, &c, config.WithEnvExpansion(), config.WithStrictFields(), config.WithLoadValidator(appconfig.Validate)); err != nil {
		slog.Error("load config", "error", err)
		return
	}
	ctx, stop := proc.SignalContext(context.Background())
	defer stop()
	serviceConf := c.ServiceConf()
	shutdown, err := app.Bootstrap(ctx, serviceConf.BootstrapConfig("{{.Name}}"))
	if err != nil {
		slog.Error("bootstrap", "error", err)
		return
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()
	svcCtx := svc.NewServiceContext(c)
	restConf := serviceConf.RESTConfig(c.Rest)
	httpServer := rest.MustNewServer(restConf)
	handler.RegisterHandlers(httpServer, svcCtx)
	if c.OpenAPIEnabled() {
		httpServer.AddOpenAPIRoutes(c.OpenAPIInfo())
	}
	slog.Info("{{.Name}} starting", "rest_host", restConf.Host, "rest_port", restConf.Port)
	if err := app.Run(ctx, []app.Server{httpServer}, serviceConf.RunOptions()...); err != nil {
		slog.Error("{{.Name}} stopped", "error", err)
	}
}
`

const configTemplate = `{
  "environment": "development",
  "service": {"name": "{{.Name}}", "mode": "dev", "environment": "development", "startupTimeout": 5000000000, "shutdownTimeout": 10000000000, "log": {"level": "info", "format": "json", "trace": true}, "trace": {"enabled": true, "serviceName": "{{.Name}}", "sampleRatio": 1}, "metrics": {"enabled": true}, "profile": {"enabled": false, "addr": "127.0.0.1:6060", "pathPrefix": "/debug/pprof"}, "health": {"timeout": 1000000000}},
  "scaffold": {"features": ["ecosystem-compat"]},
  "discovery": {"provider": "memory", "ttl": "15s", "prefix": "/gofly/services", "dialTimeout": "5s"},
  "openapi": {"enabled": true, "title": "{{.Name}} API", "version": "1.0.0", "description": "Runtime OpenAPI contract generated by gofly"},
  "rest": {
    "name": "{{.Name}}",
    "host": "127.0.0.1",
    "port": 8080,
    "middlewares": {
      "recover": true,
      "trimStrings": true,
      "trace": true,
      "log": true,
      "timeout": true,
      "timeoutConfig": {"duration": 3000000000, "readHeaderTimeout": 3000000000, "healthTimeout": 1000000000},
      "breaker": true,
      "breakerConfig": {"openTimeout": 5000000000, "window": 10000000000, "buckets": 10, "minRequests": 20, "failureRatio": 0.5},
      "rateLimit": true,
      "rateLimitConfig": {
        "rate": 100,
        "burst": 100
      },
      "metrics": true,
      "health": true,
      "requestId": true
    },
    "admin": {"enabled": true, "pathPrefix": "/admin", "token": "change-me-admin-token"}
  },
	"admin": {"enabled": true, "addr": "127.0.0.1:9090", "pathPrefix": "/admin"},
	"mq": {"enabled": true, "driver": "memory", "service": "{{.Name}}", "trace": true, "log": true, "timeout": 3000000000, "tags": {"component": "mq"}, "kafka": {"brokers": ["127.0.0.1:9092"], "writeTimeout": 10000000000, "readTimeout": 10000000000}, "rabbitmq": {"url": "amqp://guest:guest@127.0.0.1:5672/", "prefetch": 32}, "redisstream": {"redis": {"addr": "127.0.0.1:6379"}, "blockInterval": 2000000000, "readCount": 16}},
  "governance": {
    "ruleFile": "etc/governance.json",
    "watch": true,
    "watchInterval": 2000000000,
    "rules": [
      {"name": "rest-default", "transport": "rest", "service": "{{.Name}}", "policy": {"timeout": 3000000000}},
      {"name": "rpc-default", "transport": "rpc", "service": "greeter", "policy": {"timeout": 3000000000, "retry": {"attempts": 2, "backoff": 100000000}}},
      {"name": "mq-default", "transport": "mq", "service": "{{.Name}}", "policy": {"timeout": 3000000000, "retry": {"attempts": 2, "backoff": 100000000}, "breaker": {"enabled": true, "failureRatio": 0.5, "minRequests": 20}}},
      {"name": "gateway-default", "transport": "gateway", "service": "{{.Name}}", "policy": {"retry": {"attempts": 2, "backoff": 100000000}, "breaker": {"enabled": true, "failureRatio": 0.5, "minRequests": 20}}}
    ]
  },
  "rpc": {
    "addr": ":8081",
    "advertise": "http://127.0.0.1:8081"
  }
}
`

const minimalConfigTemplate = `{
  "environment": "development",
  "service": {"name": "{{.Name}}", "mode": "dev", "environment": "development", "startupTimeout": 5000000000, "shutdownTimeout": 10000000000, "log": {"level": "info", "format": "json"}, "metrics": {"enabled": true}, "trace": {"enabled": true, "sampler": "always_on"}, "health": {"timeout": 1000000000}},
  "scaffold": {"features": ["ecosystem-compat"]},
  "openapi": {"enabled": true, "title": "{{.Name}} API", "version": "1.0.0", "description": "Runtime OpenAPI contract generated by gofly"},
  "rest": {
    "name": "{{.Name}}",
    "host": "127.0.0.1",
    "port": 8080,
    "middlewares": {
      "recover": true,
      "trace": true,
      "log": true,
      "trimStrings": true,
      "metrics": true,
      "health": true,
      "requestId": true
    }
  }
}
`

const governanceTemplate = `[
  {"name": "rest-default", "transport": "rest", "service": "{{.Name}}", "policy": {"timeout": 3000000000}},
  {"name": "rpc-default", "transport": "rpc", "service": "greeter", "policy": {"timeout": 3000000000, "retry": {"attempts": 2, "backoff": 100000000}}},
  {"name": "mq-default", "transport": "mq", "service": "{{.Name}}", "policy": {"timeout": 3000000000, "retry": {"attempts": 2, "backoff": 100000000}, "breaker": {"enabled": true, "failureRatio": 0.5, "minRequests": 20}}},
  {"name": "gateway-default", "transport": "gateway", "service": "{{.Name}}", "policy": {"retry": {"attempts": 2, "backoff": 100000000}, "breaker": {"enabled": true, "failureRatio": 0.5, "minRequests": 20}}}
]
`

const adminServerTemplate = `package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/gofly/gofly/core/controlplane"
	"github.com/gofly/gofly/core/observability/metrics"
	"github.com/gofly/gofly/rpc"
)

const defaultAddr = "127.0.0.1:9090"

type Server struct {
	addr                 string
	pathPrefix           string
	rpcServer            *rpc.HTTPServer
	controlPlaneSnapshot func(context.Context) (controlplane.Snapshot, error)
	server               *http.Server
}

type Option func(*Server)

func WithControlPlaneSnapshot(snapshot func(context.Context) (controlplane.Snapshot, error)) Option {
	return func(s *Server) {
		s.controlPlaneSnapshot = snapshot
	}
}

func NewServer(addr string, pathPrefix string, rpcServer *rpc.HTTPServer, opts ...Option) *Server {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = defaultAddr
	}
	pathPrefix = strings.TrimRight(strings.TrimSpace(pathPrefix), "/")
	s := &Server{addr: addr, pathPrefix: pathPrefix, rpcServer: rpcServer}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

func (s *Server) Start() error {
	s.server = &http.Server{Addr: s.addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.mount(mux, "")
	if s.pathPrefix != "" {
		s.mount(mux, s.pathPrefix)
	}
	return mux
}

func (s *Server) mount(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix+"/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc(prefix+"/metrics", s.serveMetrics)
	mux.HandleFunc(prefix+"/control-plane", s.serveControlPlane)
	mux.HandleFunc(prefix+"/debug/pprof/", pprof.Index)
	mux.HandleFunc(prefix+"/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc(prefix+"/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc(prefix+"/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc(prefix+"/debug/pprof/trace", pprof.Trace)
	mux.Handle(prefix+"/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle(prefix+"/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle(prefix+"/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	mux.Handle(prefix+"/debug/pprof/block", pprof.Handler("block"))
	mux.Handle(prefix+"/debug/pprof/mutex", pprof.Handler("mutex"))
	mux.HandleFunc(prefix+"/rpc/admin/", s.serveRPCAdmin(prefix))
}

func (s *Server) serveMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_ = metrics.Default.WritePrometheus(w)
}

func (s *Server) serveControlPlane(w http.ResponseWriter, r *http.Request) {
	if s.controlPlaneSnapshot == nil {
		http.Error(w, "control-plane snapshot is not configured", http.StatusServiceUnavailable)
		return
	}
	snapshot, err := s.controlPlaneSnapshot(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (s *Server) serveRPCAdmin(prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.rpcServer == nil {
			http.Error(w, "rpc server is not configured", http.StatusServiceUnavailable)
			return
		}
		if prefix != "" {
			r = r.Clone(r.Context())
			r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		}
		s.rpcServer.ServeHTTP(w, r)
	}
}
`

const adminServerTestTemplate = `package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofly/gofly/core/controlplane"
	"github.com/gofly/gofly/rpc"
	appconfig "{{.Module}}/internal/config"
	apprpc "{{.Module}}/internal/rpc"
	"{{.Module}}/internal/svc"
)

func TestAdminDiagnostics(t *testing.T) {
	rpcServer := rpc.NewServer()
	if err := rpcServer.RegisterService(apprpc.GreeterService(svc.NewServiceContext(appconfig.Config{})), nil); err != nil {
		t.Fatal(err)
	}
	adminServer := NewServer("", "/admin", rpcServer, WithControlPlaneSnapshot(func(ctx context.Context) (controlplane.Snapshot, error) {
		return appconfig.Config{}.ControlPlaneSnapshot(ctx)
	}))
	handler := adminServer.Handler()

	metricsRec := httptest.NewRecorder()
	handler.ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metricsRec.Code != http.StatusOK || !strings.Contains(metricsRec.Body.String(), "gofly_requests_total") {
		t.Fatalf("metrics response = %d %q", metricsRec.Code, metricsRec.Body.String())
	}

	pprofRec := httptest.NewRecorder()
	handler.ServeHTTP(pprofRec, httptest.NewRequest(http.MethodGet, "/debug/pprof/goroutine?debug=1", nil))
	if pprofRec.Code != http.StatusOK || !strings.Contains(pprofRec.Body.String(), "goroutine") {
		t.Fatalf("goroutine pprof response = %d %q", pprofRec.Code, pprofRec.Body.String())
	}

	controlPlaneRec := httptest.NewRecorder()
	handler.ServeHTTP(controlPlaneRec, httptest.NewRequest(http.MethodGet, "/admin/control-plane", nil))
	if controlPlaneRec.Code != http.StatusOK {
		t.Fatalf("control-plane status = %d body=%q", controlPlaneRec.Code, controlPlaneRec.Body.String())
	}
	var snapshot controlplane.Snapshot
	if err := json.NewDecoder(controlPlaneRec.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode control-plane snapshot: %v", err)
	}
	if snapshot.Metadata["generated.project"] != "available" {
		t.Fatalf("control-plane snapshot metadata = %#v, want generated project marker", snapshot.Metadata)
	}

	descRec := httptest.NewRecorder()
	handler.ServeHTTP(descRec, httptest.NewRequest(http.MethodGet, "/admin/rpc/admin/descriptors/greeter", nil))
	if descRec.Code != http.StatusOK {
		t.Fatalf("descriptor status = %d body=%q", descRec.Code, descRec.Body.String())
	}
	var descriptor rpc.Descriptor
	if err := json.NewDecoder(descRec.Body).Decode(&descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Name != "greeter" || len(descriptor.Methods) != 1 || descriptor.Methods[0].Name != "SayHello" {
		t.Fatalf("descriptor = %#v, want greeter/SayHello", descriptor)
	}
}
`

const apiNewTemplate = `type PingReq {
  Name string
}

type PingResp {
  Message string
}

service {{.Name}} {
  @handler Ping
  get /api/v1/ping (PingReq) returns (PingResp)
}
`

const rpcNewTemplate = `syntax = "proto3";

package {{.Name}}.v1;

message SayHelloReq {
  string name = 1;
}

message SayHelloResp {
  string message = 1;
}

service Greeter {
  rpc SayHello(SayHelloReq) returns (SayHelloResp);
}
`

const modelTemplateInitTemplate = `CREATE TABLE users (
  id bigint primary key,
  name varchar(64) not null,
  created_at datetime
);
`

const configGoTemplate = `package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofly/gofly/app"
	"github.com/gofly/gofly/core/controlplane"
	"github.com/gofly/gofly/core/discovery"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/rest"
)

type Config struct {
	Environment string ` + "`json:\"environment\"`" + `
	Service app.ServiceConf ` + "`json:\"service\"`" + `
	Scaffold ScaffoldConfig ` + "`json:\"scaffold,omitempty\"`" + `
	Discovery DiscoveryConfig ` + "`json:\"discovery,omitempty\"`" + `
	OpenAPI OpenAPIConfig ` + "`json:\"openapi,omitempty\"`" + `
	Rest    rest.Config       ` + "`json:\"rest\"`" + `
	Admin   AdminConfig       ` + "`json:\"admin\"`" + `
	RPC     RPCConfig         ` + "`json:\"rpc\"`" + `
	MQ      MQConfig ` + "`json:\"mq\"`" + `
	Governance governance.Config ` + "`json:\"governance\"`" + `
}

type ScaffoldConfig struct {
	Features []string ` + "`json:\"features,omitempty\"`" + `
}

type DiscoveryConfig struct {
	Provider    string   ` + "`json:\"provider,omitempty\"`" + `
	Address     string   ` + "`json:\"address,omitempty\"`" + `
	Endpoints   []string ` + "`json:\"endpoints,omitempty\"`" + `
	Prefix      string   ` + "`json:\"prefix,omitempty\"`" + `
	TTL         string   ` + "`json:\"ttl,omitempty\"`" + `
	DialTimeout string   ` + "`json:\"dialTimeout,omitempty\"`" + `
	TokenEnv    string   ` + "`json:\"tokenEnv,omitempty\"`" + `
	UsernameEnv string   ` + "`json:\"usernameEnv,omitempty\"`" + `
	PasswordEnv string   ` + "`json:\"passwordEnv,omitempty\"`" + `
}

type OpenAPIConfig struct {
	Enabled     *bool  ` + "`json:\"enabled,omitempty\"`" + `
	Title       string ` + "`json:\"title,omitempty\"`" + `
	Version     string ` + "`json:\"version,omitempty\"`" + `
	Description string ` + "`json:\"description,omitempty\"`" + `
}

type RPCConfig struct {
	Addr      string ` + "`json:\"addr\"`" + `
	Advertise string ` + "`json:\"advertise\"`" + `
}

type AdminConfig struct {
	Enabled    bool   ` + "`json:\"enabled\"`" + `
	Addr       string ` + "`json:\"addr\"`" + `
	PathPrefix string ` + "`json:\"pathPrefix\"`" + `
}

type MQConfig struct {
	Enabled bool ` + "`json:\"enabled\"`" + `
	Driver string ` + "`json:\"driver\"`" + `
	Service string ` + "`json:\"service\"`" + `
	Trace bool ` + "`json:\"trace\"`" + `
	Log bool ` + "`json:\"log\"`" + `
	Timeout time.Duration ` + "`json:\"timeout\"`" + `
	Tags map[string]string ` + "`json:\"tags\"`" + `
	Kafka MQKafkaConfig ` + "`json:\"kafka\"`" + `
	RabbitMQ MQRabbitMQConfig ` + "`json:\"rabbitmq\"`" + `
	RedisStream MQRedisStreamConfig ` + "`json:\"redisstream\"`" + `
}

type MQKafkaConfig struct {
	Brokers []string ` + "`json:\"brokers\"`" + `
	WriteTimeout time.Duration ` + "`json:\"writeTimeout\"`" + `
	ReadTimeout time.Duration ` + "`json:\"readTimeout\"`" + `
	MinBytes int ` + "`json:\"minBytes\"`" + `
	MaxBytes int ` + "`json:\"maxBytes\"`" + `
}

type MQRabbitMQConfig struct {
	URL string ` + "`json:\"url\"`" + `
	ExchangePrefix string ` + "`json:\"exchangePrefix\"`" + `
	Prefetch int ` + "`json:\"prefetch\"`" + `
}

type MQRedisStreamConfig struct {
	Redis RedisConfig ` + "`json:\"redis\"`" + `
	MaxLen int64 ` + "`json:\"maxLen\"`" + `
	Consumer string ` + "`json:\"consumer\"`" + `
	BlockInterval time.Duration ` + "`json:\"blockInterval\"`" + `
	ReadCount int ` + "`json:\"readCount\"`" + `
}

func ConfigPaths(name string) []string {
	name = strings.TrimSpace(name)
	paths := []string{"config.yaml", "config.yml", "config.toml", "config.json"}
	if name != "" {
		paths = append(paths,
			filepath.Join("etc", name+".yaml"),
			filepath.Join("etc", name+".yml"),
			filepath.Join("etc", name+".toml"),
			filepath.Join("etc", name+".json"),
		)
	}
	return paths
}

func ResolveConfigPath(name string) string {
	for _, path := range ConfigPaths(name) {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if strings.TrimSpace(name) == "" {
		return "config.json"
	}
	return filepath.Join("etc", strings.TrimSpace(name)+".json")
}

type RedisConfig struct {
	Addr string ` + "`json:\"addr\"`" + `
	Password string ` + "`json:\"password\"`" + `
	DB int ` + "`json:\"db\"`" + `
	DialTimeout time.Duration ` + "`json:\"dialTimeout\"`" + `
	Timeout time.Duration ` + "`json:\"timeout\"`" + `
	MaxConns int ` + "`json:\"maxConns\"`" + `
	MaxIdleConns int ` + "`json:\"maxIdleConns\"`" + `
	ConnMaxIdleTime time.Duration ` + "`json:\"connMaxIdleTime\"`" + `
	ConnMaxLifetime time.Duration ` + "`json:\"connMaxLifetime\"`" + `
}

func (c Config) ServiceConf() app.ServiceConf {
	service := c.Service
	if service.Name == "" {
		service.Name = c.Rest.Name
	}
	if service.Environment == "" {
		service.Environment = c.Environment
	}
	return service.WithDefaults(c.Rest.Name)
}

type ControlPlaneContributor struct {
	Config Config
}

func (c Config) ControlPlaneContributor() ControlPlaneContributor {
	return ControlPlaneContributor{Config: c}
}

func (c Config) ControlPlaneSnapshot(ctx context.Context) (controlplane.Snapshot, error) {
	return c.ControlPlaneSnapshotWithDiscovery(ctx, nil)
}

func (c Config) ControlPlaneSnapshotWithDiscovery(ctx context.Context, registry controlplane.DiscoverySnapshotSource) (controlplane.Snapshot, error) {
	contributors := []controlplane.SnapshotContributor{c.ControlPlaneContributor()}
	if registry != nil {
		contributors = append(contributors, controlplane.DiscoveryContributor{Registry: registry})
	}
	provider := controlplane.CompositeProvider{
		Name:         "generated-project",
		Contributors: contributors,
	}
	return provider.Load(ctx)
}

func (c ControlPlaneContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	cfg := c.Config
	service := cfg.ServiceConf()
	addGeneratedControlPlaneConfig := func(name string, value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("marshal generated control-plane config %s: %w", name, err)
		}
		if snapshot.Configs == nil {
			snapshot.Configs = make(map[string]json.RawMessage)
		}
		snapshot.Configs["generated."+name] = data
		return nil
	}
	if err := addGeneratedControlPlaneConfig("service", service); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("scaffold", cfg.Scaffold); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("discovery", cfg.Discovery.Sanitized()); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("openapi", cfg.OpenAPIInfo()); err != nil {
		return err
	}
	restConfig := cfg.Rest
	restConfig.Admin.Token = ""
	if err := addGeneratedControlPlaneConfig("rest", restConfig); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("rpc", cfg.RPC); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("admin", struct {
		Enabled    bool   ` + "`json:\"enabled\"`" + `
		Addr       string ` + "`json:\"addr\"`" + `
		PathPrefix string ` + "`json:\"pathPrefix\"`" + `
	}{Enabled: cfg.Admin.Enabled, Addr: cfg.Admin.Addr, PathPrefix: cfg.Admin.PathPrefix}); err != nil {
		return err
	}
	snapshot.Policies = append(snapshot.Policies, cfg.Governance.Rules...)
	serviceSnapshot := controlplane.ServiceSnapshot{Name: service.Name, Metadata: map[string]string{"source": "generated-project"}}
	if cfg.Rest.Port > 0 {
		host := strings.TrimSpace(cfg.Rest.Host)
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		serviceSnapshot.Endpoints = append(serviceSnapshot.Endpoints, controlplane.EndpointSnapshot{Address: fmt.Sprintf("http://%s:%d", host, cfg.Rest.Port), Metadata: map[string]string{"transport": "rest"}})
	}
	if strings.TrimSpace(cfg.RPC.Advertise) != "" {
		snapshot.Services = append(snapshot.Services, controlplane.ServiceSnapshot{Name: "greeter", Endpoints: []controlplane.EndpointSnapshot{{Address: strings.TrimSpace(cfg.RPC.Advertise), Metadata: map[string]string{"transport": "rpc"}}}, Metadata: map[string]string{"source": "generated-project"}})
	}
	if len(serviceSnapshot.Endpoints) > 0 {
		snapshot.Services = append(snapshot.Services, serviceSnapshot)
	}
	if snapshot.Metadata == nil {
		snapshot.Metadata = make(map[string]string)
	}
	snapshot.Metadata["generated.project"] = "available"
	snapshot.Metadata["generated.project.service"] = service.Name
	snapshot.Metadata["generated.project.features"] = strings.Join(cfg.EffectiveScaffoldFeatures(), ",")
	snapshot.Metadata["generated.project.runtime"] = "service,rest,rpc,governance,discovery"
	snapshot.Metadata["generated.project.contract"] = "scaffold,runtime-policy,ai-manifest"
	return nil
}

func (c DiscoveryConfig) ProviderName() string {
	provider := strings.ToLower(strings.TrimSpace(c.Provider))
	if provider == "" {
		return "memory"
	}
	return provider
}

func (c DiscoveryConfig) RegistryTTL() time.Duration {
	return parseDiscoveryDuration(c.TTL, 15*time.Second)
}

func (c DiscoveryConfig) DialTimeoutDuration() time.Duration {
	return parseDiscoveryDuration(c.DialTimeout, 5*time.Second)
}

func (c DiscoveryConfig) RegisterOptions() []discovery.RegisterOption {
	ttl := c.RegistryTTL()
	if ttl <= 0 {
		return nil
	}
	return []discovery.RegisterOption{discovery.WithTTL(ttl)}
}

func (c DiscoveryConfig) Sanitized() DiscoveryConfig {
	c.TokenEnv = strings.TrimSpace(c.TokenEnv)
	c.UsernameEnv = strings.TrimSpace(c.UsernameEnv)
	c.PasswordEnv = strings.TrimSpace(c.PasswordEnv)
	return c
}

func ValidateDiscoveryConfig(c DiscoveryConfig) error {
	switch c.ProviderName() {
	case "memory", "consul", "etcdv3":
	default:
		return fmt.Errorf("unsupported discovery provider %q", c.Provider)
	}
	if c.TTL != "" {
		if _, err := time.ParseDuration(c.TTL); err != nil {
			return fmt.Errorf("discovery ttl: %w", err)
		}
	}
	if c.DialTimeout != "" {
		if _, err := time.ParseDuration(c.DialTimeout); err != nil {
			return fmt.Errorf("discovery dial timeout: %w", err)
		}
	}
	if c.ProviderName() == "etcdv3" && len(c.Endpoints) == 0 && strings.TrimSpace(c.Address) == "" {
		return errors.New("discovery endpoints are required for etcdv3")
	}
	return nil
}

func parseDiscoveryDuration(value string, fallback time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func (c Config) OpenAPIEnabled() bool {
	return c.OpenAPI.Enabled == nil || *c.OpenAPI.Enabled
}

func (c Config) OpenAPIInfo() rest.OpenAPIInfo {
	info := rest.OpenAPIInfo{
		Title:       strings.TrimSpace(c.OpenAPI.Title),
		Version:     strings.TrimSpace(c.OpenAPI.Version),
		Description: strings.TrimSpace(c.OpenAPI.Description),
	}
	if info.Title == "" {
		info.Title = c.ServiceConf().Name + " API"
	}
	if info.Version == "" {
		info.Version = "1.0.0"
	}
	return info
}

func ValidateOpenAPIConfig(c Config) error {
	if !c.OpenAPIEnabled() {
		return nil
	}
	info := c.OpenAPIInfo()
	if strings.TrimSpace(info.Title) == "" {
		return errors.New("openapi title is required")
	}
	if strings.TrimSpace(info.Version) == "" {
		return errors.New("openapi version is required")
	}
	return nil
}

func (c Config) EffectiveScaffoldFeatures() []string {
	if c.Scaffold.Features == nil {
		return []string{"ecosystem-compat"}
	}
	return NormalizeScaffoldFeatures(c.Scaffold.Features)
}

func RegisteredScaffoldFeatures() []string {
	features := make([]string, 0, len(registeredScaffoldFeatures))
	for name := range registeredScaffoldFeatures {
		features = append(features, name)
	}
	sort.Strings(features)
	return features
}

func ValidateScaffoldFeatures(features []string) error {
	for _, feature := range NormalizeScaffoldFeatures(features) {
		if !registeredScaffoldFeatures[feature] {
			return fmt.Errorf("feature %q is not registered", feature)
		}
	}
	return nil
}

func NormalizeScaffoldFeatures(features []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(features))
	for _, feature := range features {
		feature = strings.TrimSpace(feature)
		if feature == "" {
			continue
		}
		if _, ok := seen[feature]; ok {
			continue
		}
		seen[feature] = struct{}{}
		normalized = append(normalized, feature)
	}
	return normalized
}

var registeredScaffoldFeatures = map[string]bool{
	"ecosystem-compat": true,
	"http-compat":      true,
	"rpc-compat":       true,
}

func Validate(c Config) error {
	if err := errors.Join(
		ValidateScaffoldFeatures(c.Scaffold.Features),
		ValidateDiscoveryConfig(c.Discovery),
		ValidateOpenAPIConfig(c),
	); err != nil {
		return err
	}
	service := c.ServiceConf()
	if !isProduction(service.Environment) {
		return nil
	}
	return errors.Join(
		app.ValidateProductionConfig(service.BootstrapConfig(c.Rest.Name)),
		rest.ValidateProductionConfig(c.Rest),
	)
}

func isProduction(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}
`

const configTestTemplate = `package config

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofly/gofly/app"
	"github.com/gofly/gofly/core/controlplane"
	"github.com/gofly/gofly/rest"
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

func TestControlPlaneSnapshotExposesGeneratedContract(t *testing.T) {
	cfg := Config{
		Environment: "development",
		Service:     serviceConfFixture("hello"),
		Scaffold:    ScaffoldConfig{Features: []string{"ecosystem-compat"}},
		Rest:        rest.Config{Name: "hello", Host: "127.0.0.1", Port: 8080},
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
	if string(snapshot.Configs["generated.rest"]) == "" || strings.Contains(string(snapshot.Configs["generated.rest"]), "change-me-admin-token") {
		t.Fatalf("generated.rest config = %s, want sanitized runtime policy without admin token", snapshot.Configs["generated.rest"])
	}
	if len(snapshot.Services) != 1 || snapshot.Services[0].Name != "hello" || len(snapshot.Services[0].Endpoints) != 1 || snapshot.Services[0].Endpoints[0].Metadata["transport"] != "rest" {
		t.Fatalf("snapshot services = %#v, want generated rest endpoint", snapshot.Services)
	}
}

func serviceConfFixture(name string) app.ServiceConf {
	return app.ServiceConf{Name: name}
}

func boolPtr(v bool) *bool { return &v }
`

const mqBrokerTemplate = `package mq

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/kv/redis"
	coremq "github.com/gofly/gofly/core/mq"
	"github.com/gofly/gofly/core/mq/kafka"
	"github.com/gofly/gofly/core/mq/rabbitmq"
	"github.com/gofly/gofly/core/mq/redisstream"

	"{{.Module}}/internal/config"
)

func NewBroker(cfg config.MQConfig, manager *governance.Manager) (coremq.Broker, error) {
	broker, err := newDriverBroker(cfg)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return broker, nil
	}
	broker, err = coremq.NewGovernanceBroker(
		broker,
		coremq.WithGovernanceService(cfg.Service),
		coremq.WithGovernanceManager(manager),
		coremq.WithGovernanceMetrics(nil),
		coremq.WithGovernanceTrace(cfg.Trace),
		coremq.WithGovernanceLog(cfg.Log),
		coremq.WithGovernanceTimeout(cfg.Timeout),
		coremq.WithGovernanceTags(cfg.Tags),
	)
	if err != nil {
		return nil, fmt.Errorf("setup mq governance: %w", err)
	}
	return broker, nil
}

func newDriverBroker(cfg config.MQConfig) (coremq.Broker, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "", "memory":
		return coremq.AsBroker(coremq.NewMemoryBroker()), nil
	case "kafka":
		return kafka.New(kafka.Options{
			Brokers:      cfg.Kafka.Brokers,
			WriteTimeout: cfg.Kafka.WriteTimeout,
			ReadTimeout:  cfg.Kafka.ReadTimeout,
			MinBytes:     cfg.Kafka.MinBytes,
			MaxBytes:     cfg.Kafka.MaxBytes,
		})
	case "rabbitmq":
		return rabbitmq.New(rabbitmq.Options{
			URL:            cfg.RabbitMQ.URL,
			ExchangePrefix: cfg.RabbitMQ.ExchangePrefix,
			Prefetch:       cfg.RabbitMQ.Prefetch,
		})
	case "redisstream":
		client := redis.New(redis.Config{
			Addr:            cfg.RedisStream.Redis.Addr,
			Password:        cfg.RedisStream.Redis.Password,
			DB:              cfg.RedisStream.Redis.DB,
			DialTimeout:     cfg.RedisStream.Redis.DialTimeout,
			Timeout:         cfg.RedisStream.Redis.Timeout,
			MaxConns:        cfg.RedisStream.Redis.MaxConns,
			MaxIdleConns:    cfg.RedisStream.Redis.MaxIdleConns,
			ConnMaxIdleTime: cfg.RedisStream.Redis.ConnMaxIdleTime,
			ConnMaxLifetime: cfg.RedisStream.Redis.ConnMaxLifetime,
		})
		broker, err := redisstream.New(client, redisstream.Options{
			MaxLen:        cfg.RedisStream.MaxLen,
			Consumer:      cfg.RedisStream.Consumer,
			BlockInterval: cfg.RedisStream.BlockInterval,
			ReadCount:     cfg.RedisStream.ReadCount,
		})
		if err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("create redis stream broker: %w", err)
		}
		return brokerWithCleanup{Broker: broker, cleanup: client.Close}, nil
	default:
		return nil, fmt.Errorf("unsupported mq driver %q", cfg.Driver)
	}
}

type brokerWithCleanup struct {
	coremq.Broker
	cleanup func() error
}

func (b brokerWithCleanup) Close(ctx context.Context) error {
	return errors.Join(b.Broker.Close(ctx), b.cleanup())
}
`

const minimalConfigGoTemplate = `package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gofly/gofly/app"
	"github.com/gofly/gofly/core/controlplane"
	"github.com/gofly/gofly/rest"
)

type Config struct {
	Environment string ` + "`json:\"environment\"`" + `
	Service app.ServiceConf ` + "`json:\"service\"`" + `
	Scaffold ScaffoldConfig ` + "`json:\"scaffold,omitempty\"`" + `
	OpenAPI OpenAPIConfig ` + "`json:\"openapi,omitempty\"`" + `
	Rest rest.Config   ` + "`json:\"rest\"`" + `
}

type ScaffoldConfig struct {
	Features []string ` + "`json:\"features,omitempty\"`" + `
}

type OpenAPIConfig struct {
	Enabled     *bool  ` + "`json:\"enabled,omitempty\"`" + `
	Title       string ` + "`json:\"title,omitempty\"`" + `
	Version     string ` + "`json:\"version,omitempty\"`" + `
	Description string ` + "`json:\"description,omitempty\"`" + `
}

func ConfigPaths(name string) []string {
	name = strings.TrimSpace(name)
	paths := []string{"config.yaml", "config.yml", "config.toml", "config.json"}
	if name != "" {
		paths = append(paths,
			filepath.Join("etc", name+".yaml"),
			filepath.Join("etc", name+".yml"),
			filepath.Join("etc", name+".toml"),
			filepath.Join("etc", name+".json"),
		)
	}
	return paths
}

func ResolveConfigPath(name string) string {
	for _, path := range ConfigPaths(name) {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if strings.TrimSpace(name) == "" {
		return "config.json"
	}
	return filepath.Join("etc", strings.TrimSpace(name)+".json")
}

func (c Config) ServiceConf() app.ServiceConf {
	service := c.Service
	if service.Name == "" {
		service.Name = c.Rest.Name
	}
	if service.Environment == "" {
		service.Environment = c.Environment
	}
	return service.WithDefaults(c.Rest.Name)
}

type ControlPlaneContributor struct {
	Config Config
}

func (c Config) ControlPlaneContributor() ControlPlaneContributor {
	return ControlPlaneContributor{Config: c}
}

func (c Config) ControlPlaneSnapshot(ctx context.Context) (controlplane.Snapshot, error) {
	provider := controlplane.CompositeProvider{
		Name:         "generated-project",
		Contributors: []controlplane.SnapshotContributor{c.ControlPlaneContributor()},
	}
	return provider.Load(ctx)
}

func (c ControlPlaneContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	cfg := c.Config
	service := cfg.ServiceConf()
	addGeneratedControlPlaneConfig := func(name string, value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("marshal generated control-plane config %s: %w", name, err)
		}
		if snapshot.Configs == nil {
			snapshot.Configs = make(map[string]json.RawMessage)
		}
		snapshot.Configs["generated."+name] = data
		return nil
	}
	if err := addGeneratedControlPlaneConfig("service", service); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("scaffold", cfg.Scaffold); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("openapi", cfg.OpenAPIInfo()); err != nil {
		return err
	}
	restConfig := cfg.Rest
	restConfig.Admin.Token = ""
	if err := addGeneratedControlPlaneConfig("rest", restConfig); err != nil {
		return err
	}
	if cfg.Rest.Port > 0 {
		host := strings.TrimSpace(cfg.Rest.Host)
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		snapshot.Services = append(snapshot.Services, controlplane.ServiceSnapshot{Name: service.Name, Endpoints: []controlplane.EndpointSnapshot{{Address: fmt.Sprintf("http://%s:%d", host, cfg.Rest.Port), Metadata: map[string]string{"transport": "rest"}}}, Metadata: map[string]string{"source": "generated-project"}})
	}
	if snapshot.Metadata == nil {
		snapshot.Metadata = make(map[string]string)
	}
	snapshot.Metadata["generated.project"] = "available"
	snapshot.Metadata["generated.project.service"] = service.Name
	snapshot.Metadata["generated.project.features"] = strings.Join(cfg.EffectiveScaffoldFeatures(), ",")
	snapshot.Metadata["generated.project.runtime"] = "service,rest"
	snapshot.Metadata["generated.project.contract"] = "scaffold,runtime-policy,ai-manifest"
	return nil
}

func (c Config) OpenAPIEnabled() bool {
	return c.OpenAPI.Enabled == nil || *c.OpenAPI.Enabled
}

func (c Config) OpenAPIInfo() rest.OpenAPIInfo {
	info := rest.OpenAPIInfo{
		Title:       strings.TrimSpace(c.OpenAPI.Title),
		Version:     strings.TrimSpace(c.OpenAPI.Version),
		Description: strings.TrimSpace(c.OpenAPI.Description),
	}
	if info.Title == "" {
		info.Title = c.ServiceConf().Name + " API"
	}
	if info.Version == "" {
		info.Version = "1.0.0"
	}
	return info
}

func ValidateOpenAPIConfig(c Config) error {
	if !c.OpenAPIEnabled() {
		return nil
	}
	info := c.OpenAPIInfo()
	if strings.TrimSpace(info.Title) == "" {
		return errors.New("openapi title is required")
	}
	if strings.TrimSpace(info.Version) == "" {
		return errors.New("openapi version is required")
	}
	return nil
}

func (c Config) EffectiveScaffoldFeatures() []string {
	if c.Scaffold.Features == nil {
		return []string{"ecosystem-compat"}
	}
	return NormalizeScaffoldFeatures(c.Scaffold.Features)
}

func RegisteredScaffoldFeatures() []string {
	features := make([]string, 0, len(registeredScaffoldFeatures))
	for name := range registeredScaffoldFeatures {
		features = append(features, name)
	}
	sort.Strings(features)
	return features
}

func ValidateScaffoldFeatures(features []string) error {
	for _, feature := range NormalizeScaffoldFeatures(features) {
		if !registeredScaffoldFeatures[feature] {
			return fmt.Errorf("feature %q is not registered", feature)
		}
	}
	return nil
}

func NormalizeScaffoldFeatures(features []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(features))
	for _, feature := range features {
		feature = strings.TrimSpace(feature)
		if feature == "" {
			continue
		}
		if _, ok := seen[feature]; ok {
			continue
		}
		seen[feature] = struct{}{}
		normalized = append(normalized, feature)
	}
	return normalized
}

var registeredScaffoldFeatures = map[string]bool{
	"ecosystem-compat": true,
	"http-compat":      true,
	"rpc-compat":       true,
}

func Validate(c Config) error {
	if err := errors.Join(
		ValidateScaffoldFeatures(c.Scaffold.Features),
		ValidateOpenAPIConfig(c),
	); err != nil {
		return err
	}
	service := c.ServiceConf()
	if !isProduction(service.Environment) {
		return nil
	}
	return errors.Join(
		app.ValidateProductionConfig(service.BootstrapConfig(c.Rest.Name)),
		rest.ValidateProductionConfig(c.Rest),
	)
}

func isProduction(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}
`

const dockerfileTemplate = `FROM golang:{{.GoVersion}} AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/{{.Exe}} {{.GoFile}}

FROM {{.BaseImage}}
WORKDIR /app
COPY --from=builder /out/{{.Exe}} /app/{{.Exe}}
COPY etc /app/etc
ENV TZ={{.Timezone}}
EXPOSE {{.Port}} 8081
USER nonroot:nonroot
ENTRYPOINT ["/app/{{.Exe}}"]
`

const kubeTemplate = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  replicas: {{.Replicas}}
{{.RevisionHistory}}  selector:
    matchLabels:
      app: {{.Name}}
  template:
    metadata:
      labels:
        app: {{.Name}}
    spec:
{{.ServiceAccount}}{{.ImagePullSecrets}}      containers:
        - name: {{.Name}}
          image: {{.Image}}
{{.ImagePullPolicy}}          ports:
            - name: http
              containerPort: {{.Port}}
            - name: rpc
              containerPort: {{.RPCPort}}
{{.Resources}}          readinessProbe:
            httpGet:
              path: /healthz
              port: http
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
---
apiVersion: v1
kind: Service
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
{{.ServiceType}}  selector:
    app: {{.Name}}
  ports:
    - name: http
      port: {{.Port}}
      targetPort: http
{{.NodePort}}    - name: rpc
      port: {{.RPCPort}}
      targetPort: rpc
{{.Autoscale}}
`

const kubeServiceTemplate = `apiVersion: v1
kind: Service
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
{{.ServiceType}}  selector:
    app: {{.Name}}
  ports:
    - name: http
      port: {{.Port}}
      targetPort: http
{{.NodePort}}    - name: rpc
      port: {{.RPCPort}}
      targetPort: rpc
`

const kubeIngressTemplate = `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  rules:
    - host: {{.Host}}
      http:
        paths:
          - path: {{.Path}}
            pathType: Prefix
            backend:
              service:
                name: {{.Name}}
                port:
                  number: {{.Port}}
`

const kubeConfigMapTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
data:
{{.Data}}
`

const kubeJobTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  template:
    metadata:
      labels:
        app: {{.Name}}
    spec:
      restartPolicy: OnFailure
{{.ServiceAccount}}{{.ImagePullSecrets}}      containers:
        - name: {{.Name}}
          image: {{.Image}}
{{.ImagePullPolicy}}{{.Resources}}
`

const makefileTemplate = `.PHONY: test race build run

test:
	go test ./...

race:
	go test -race ./...

build:
	go build ./cmd/{{.Name}}

run:
	go run ./cmd/{{.Name}}
`

const ciWorkflowTemplate = `name: ci

on:
  push:
    branches: [ main, master ]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go test ./...
      - run: go test -race ./...
`

const svcTemplate = `package svc

import (
	"sync"

	"github.com/gofly/gofly/core/mq"
	"{{.Module}}/internal/config"
)

type ServiceContext struct {
	mu     sync.RWMutex
	Config config.Config
	MQ     mq.Broker
}

func NewServiceContext(c config.Config, brokers ...mq.Broker) *ServiceContext {
	var broker mq.Broker
	if len(brokers) > 0 {
		broker = brokers[0]
	}
	return &ServiceContext{Config: c, MQ: broker}
}

func (s *ServiceContext) UpdateConfig(c config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config = c
}

func (s *ServiceContext) CurrentConfig() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Config
}
`

const goZeroSvcTemplate = `package svc

import (
	"sync"

	"{{.Module}}/internal/config"
)

type ServiceContext struct {
	mu     sync.RWMutex
	Config config.Config
}

func NewServiceContext(c config.Config) *ServiceContext {
	return &ServiceContext{Config: c}
}

func (s *ServiceContext) UpdateConfig(c config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config = c
}

func (s *ServiceContext) CurrentConfig() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Config
}
`

const goZeroTypesTemplate = `package types

type PingReq struct {
	Name string ` + "`json:\"name,optional\" form:\"name,optional\"`" + `
}

type PingResp struct {
	Message string ` + "`json:\"message\"`" + `
}
`

const goZeroPingLogicTemplate = `package logic

import (
	"context"
	"strings"

	"{{.Module}}/internal/svc"
	"{{.Module}}/internal/types"
)

type PingLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPingLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PingLogic {
	return &PingLogic{ctx: ctx, svcCtx: svcCtx}
}

func (l *PingLogic) Ping(req *types.PingReq) (*types.PingResp, error) {
	name := "world"
	if req != nil && strings.TrimSpace(req.Name) != "" {
		name = strings.TrimSpace(req.Name)
	}
	return &types.PingResp{Message: "hello " + name}, nil
}
`

const goZeroPingHandlerTemplate = `package handler

import (
	"net/http"

	"github.com/gofly/gofly/rest"

	"{{.Module}}/internal/logic"
	"{{.Module}}/internal/svc"
	"{{.Module}}/internal/types"
)

func PingHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var req types.PingReq
		if err := ctx.BindQuery(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		resp, err := logic.NewPingLogic(ctx.Request.Context(), svcCtx).Ping(&req)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, resp)
	}
}
`

const goZeroRoutesTemplate = `package handler

import (
	"net/http"

	"github.com/gofly/gofly/rest"
	"{{.Module}}/internal/svc"
)

func RegisterHandlers(server *rest.Server, svcCtx *svc.ServiceContext) {
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/ping", Handler: PingHandler(svcCtx)},
	}, rest.WithPrefix("/api/v1"))
}
`

const routesTemplate = `package routes

import (
	"github.com/gofly/gofly/rest"
	"{{.Module}}/internal/api/v1/ping"
	"{{.Module}}/internal/svc"
)

func RegisterRoutes(server *rest.Server, svcCtx *svc.ServiceContext) {
	api := server.Group("/api")
	api.AddRoute(rest.Route{Method: "GET", Path: "/v1/ping", Handler: ping.PingHandler(svcCtx)})
}
`

const routesTestTemplate = `package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofly/gofly/rest"
	"{{.Module}}/internal/config"
	"{{.Module}}/internal/svc"
)

func TestRegisterRoutes(t *testing.T) {
	server := rest.MustNewServer(rest.Config{Middlewares: rest.MiddlewaresConfig{Health: true, Metrics: true}})
	RegisterRoutes(server, svc.NewServiceContext(config.Config{}))
	tests := []struct {
		name string
		path string
	}{
		{name: "ping", path: "/api/v1/ping?name=%20gofly%20"},
		{name: "health", path: "/healthz"},
		{name: "metrics", path: "/metrics"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
		})
	}
}

func TestRegisterRoutesUsesTrimMiddleware(t *testing.T) {
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, svc.NewServiceContext(config.Config{}))
	server.AddRoute(rest.Route{Method: http.MethodPost, Path: "/echo", Handler: func(ctx *rest.Context) {
		var req struct {
			Name string ` + "`json:\"name\"`" + `
		}
		if err := ctx.Bind(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, req)
	}})
	req := httptest.NewRequest(http.MethodPost, "/echo?name=%20gofly%20", strings.NewReader(` + "`{\"name\":\"  gofly  \"}`" + `))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Name string ` + "`json:\"name\"`" + `
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Name != "gofly" {
		t.Fatalf("name = %q, want gofly", resp.Name)
	}
}
`

const trimMiddlewareTemplate = `package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gofly/gofly/rest"
)

func TrimSpaceMiddleware() rest.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trimQuery(r)
			trimBody(r)
			next.ServeHTTP(w, r)
		})
	}
}

func trimQuery(r *http.Request) {
	q := r.URL.Query()
	for key, values := range q {
		for i, value := range values {
			values[i] = strings.TrimSpace(value)
		}
		q[key] = values
	}
	r.URL.RawQuery = q.Encode()
}

func trimBody(r *http.Request) {
	if r.Body == nil || r.Body == http.NoBody {
		return
	}
	data, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return
	}
	data = bytes.TrimSpace(data)
	if isJSON(r.Header.Get("Content-Type")) && len(data) > 0 {
		data = trimJSONBody(data)
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

func isJSON(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "application/json")
}

func trimJSONBody(data []byte) []byte {
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return data
	}
	trimmed, err := json.Marshal(trimJSONValue(payload))
	if err != nil {
		return data
	}
	return trimmed
}

func trimJSONValue(v any) any {
	switch value := v.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		for i, item := range value {
			value[i] = trimJSONValue(item)
		}
		return value
	case map[string]any:
		for key, item := range value {
			value[key] = trimJSONValue(item)
		}
		return value
	default:
		return value
	}
}
`

const trimMiddlewareTestTemplate = `package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofly/gofly/rest"
)

func TestTrimSpaceMiddleware(t *testing.T) {
	server := rest.MustNewServer(rest.Config{})
	server.Use(TrimSpaceMiddleware())
	server.AddRoute(rest.Route{Method: http.MethodPost, Path: "/trim", Handler: func(ctx *rest.Context) {
		var req struct {
			Name   string   ` + "`json:\"name\"`" + `
			Tags   []string ` + "`json:\"tags\"`" + `
			Nested struct {
				Value string ` + "`json:\"value\"`" + `
			} ` + "`json:\"nested\"`" + `
		}
		if err := ctx.Bind(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, map[string]any{
			"query":  ctx.Query("q"),
			"name":   req.Name,
			"tag":    req.Tags[0],
			"nested": req.Nested.Value,
		})
	}})
	req := httptest.NewRequest(http.MethodPost, "/trim?q=%20hello%20", strings.NewReader(` + "` {\"name\":\"  gofly  \",\"tags\":[\"  rpc  \"],\"nested\":{\"value\":\"  ok  \"}} `" + `))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%q", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"query": "hello", "name": "gofly", "tag": "rpc", "nested": "ok"} {
		if got[key] != want {
			t.Fatalf("%s = %q, want %q", key, got[key], want)
		}
	}
}
`

const pingHandlerTemplate = `package ping

import (
	"github.com/gofly/gofly/rest"
	"{{.Module}}/internal/service"
	"{{.Module}}/internal/svc"
)

func PingHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		ctx.JSON(200, service.Ping())
	}
}
`

const handlerGenTemplate = `package {{.Package}}

import (
	"github.com/gofly/gofly/rest"
	"{{.Module}}/internal/svc"
)

func {{.HandlerName}}Handler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		ctx.JSON(200, map[string]string{"message": "{{.Name}}"})
	}
}
`

const middlewareGenTemplate = `package middleware

import (
	"net/http"

	"github.com/gofly/gofly/rest"
)

func {{.MiddlewareName}}() rest.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
`

const pingServiceTemplate = `package service

type PingResponse struct {
	Message string ` + "`json:\"message\"`" + `
}

func Ping() PingResponse {
	return PingResponse{Message: "pong"}
}
`

const pingServiceTestTemplate = `package service

import "testing"

func TestPing(t *testing.T) {
	resp := Ping()
	if resp.Message != "pong" {
		t.Fatalf("Ping().Message = %q, want pong", resp.Message)
	}
}
`

const greeterTemplate = `package rpc

import (
	"context"

	"github.com/gofly/gofly/rpc"
	"{{.Module}}/internal/svc"
)

type SayHelloRequest struct {
	Name string ` + "`json:\"name\"`" + `
}

type SayHelloResponse struct {
	Message string ` + "`json:\"message\"`" + `
}

func GreeterService(svcCtx *svc.ServiceContext) rpc.ServiceDesc {
	return rpc.ServiceDesc{
		Name: "greeter",
		Methods: []rpc.MethodDesc{{
			Name: "SayHello",
			NewRequest: func() any { return new(SayHelloRequest) },
			Request: "SayHelloRequest",
			Response: "SayHelloResponse",
			Handler: func(ctx context.Context, req any) (any, error) {
				in, ok := req.(*SayHelloRequest)
				if !ok || in == nil {
					return nil, rpc.NewError(rpc.CodeInvalidArgument, "unexpected request type for SayHello")
				}
				name := in.Name
				if name == "" {
					name = "world"
				}
				return SayHelloResponse{Message: "hello " + name}, nil
			},
		}},
	}
}
`

const greeterTestTemplate = `package rpc

import (
	"context"
	"testing"

	"{{.Module}}/internal/config"
	"{{.Module}}/internal/svc"
)

func TestGreeterService(t *testing.T) {
	desc := GreeterService(svc.NewServiceContext(config.Config{}))
	resp, err := desc.Methods[0].Handler(context.Background(), &SayHelloRequest{Name: "gofly"})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.(SayHelloResponse).Message
	if got != "hello gofly" {
		t.Fatalf("message = %q, want hello gofly", got)
	}
}
`

const greeterClientTestTemplate = `package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/rpc"
	"{{.Module}}/internal/config"
	"{{.Module}}/internal/svc"
)

func TestGreeterRPCClient(t *testing.T) {
	server := rpc.NewServer()
	if err := server.RegisterService(GreeterService(svc.NewServiceContext(config.Config{})), nil); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	descriptorResp, err := http.Get(httpServer.URL + "/rpc/admin/descriptors/greeter")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = descriptorResp.Body.Close() }()
	if descriptorResp.StatusCode != http.StatusOK {
		t.Fatalf("descriptor status = %d, want %d", descriptorResp.StatusCode, http.StatusOK)
	}
	var descriptor rpc.Descriptor
	if err := json.NewDecoder(descriptorResp.Body).Decode(&descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Name != "greeter" || len(descriptor.Methods) != 1 || descriptor.Methods[0].Name != "SayHello" {
		t.Fatalf("descriptor = %#v, want greeter/SayHello", descriptor)
	}
	descriptorPayload, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	compatResp, err := http.Post(httpServer.URL+"/rpc/admin/descriptors/greeter/compatibility", "application/json", bytes.NewReader(descriptorPayload))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = compatResp.Body.Close() }()
	if compatResp.StatusCode != http.StatusOK {
		t.Fatalf("descriptor compatibility status = %d, want %d", compatResp.StatusCode, http.StatusOK)
	}
	var report rpc.DescriptorCompatibilityReport
	if err := json.NewDecoder(compatResp.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if !report.IsCompatible() {
		t.Fatalf("descriptor compatibility report = %#v, want compatible", report)
	}

	registry := rpc.NewRegistry()
	if err := registry.RegisterService(context.Background(), "greeter", httpServer.URL); err != nil {
		t.Fatal(err)
	}
	client, err := rpc.NewClient(
		httpServer.URL,
		rpc.WithRetry(2),
		rpc.WithResolver(registry.Resolver("greeter")),
		rpc.WithBalancer(rpc.NewHealthBalancer()),
		rpc.WithClientSuite(rpc.ObservabilitySuite("hello", 0)),
	)
	if err != nil {
		t.Fatal(err)
	}
	var resp SayHelloResponse
	ctx := metadata.Append(context.Background(), metadata.RequestIDKey, "test-request-id")
	if err := client.Call(ctx, "greeter/SayHello", SayHelloRequest{Name: "client"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "hello client" {
		t.Fatalf("message = %q, want hello client", resp.Message)
	}
}
`

const goZeroCompatibilityTemplate = `// Package gozero contains small migration adapters for projects that previously
// used go-zero-style HTTP handler signatures. go-zero is a third-party project;
// this package is not endorsed by or affiliated with its maintainers and does
// not include or depend on go-zero source code.
package gozero

import (
	"context"
	"net/http"

	"github.com/gofly/gofly/rest"
)

// Handler is a minimal HTTP handler shape used by migration code that previously
// targeted go-zero/httpx helpers.
type Handler func(http.ResponseWriter, *http.Request)

// FromHandler adapts a migration HTTP handler into a gofly REST handler.
func FromHandler(handler Handler) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		if handler == nil {
			ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "go-zero handler is nil"})
			return
		}
		handler(ctx.Response, ctx.Request)
	}
}

// Middleware is a minimal HTTP middleware shape that can be passed to gofly
// routes after adaptation with FromMiddleware.
type Middleware func(http.HandlerFunc) http.HandlerFunc

// FromMiddleware adapts migration HTTP middleware into gofly REST middleware.
func FromMiddleware(middleware Middleware) rest.Middleware {
	return func(next http.Handler) http.Handler {
		if middleware == nil {
			return next
		}
		return middleware(next.ServeHTTP)
	}
}

// RequestContext returns the request context with a nil-safe fallback for old
// migration code that accepted a request pointer.
func RequestContext(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return r.Context()
}
`

const kitexCompatibilityTemplate = `// Package kitex contains small migration adapters for projects that previously
// used unary endpoint signatures from other RPC ecosystems. Kitex is a
// third-party project;
// this package is not endorsed by or affiliated with its maintainers and does
// not include or depend on Kitex source code.
package kitex

import (
	"context"
	"fmt"
	"strings"

	"github.com/gofly/gofly/rpc"
)

// Endpoint is the minimal unary endpoint shape used by generated migration
// handlers. It keeps migration code independent from third-party RPC runtimes.
type Endpoint func(context.Context, any) (any, error)

// Method binds a migration endpoint to a gofly RPC method descriptor.
func Method(name string, newRequest func() any, endpoint Endpoint, opts ...MethodOption) rpc.MethodDesc {
	desc := rpc.MethodDesc{
		Name:       strings.TrimSpace(name),
		NewRequest: newRequest,
		Handler: func(ctx context.Context, req any) (any, error) {
			if endpoint == nil {
				return nil, fmt.Errorf("kitex endpoint %s is nil", name)
			}
			return endpoint(ctx, req)
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&desc)
		}
	}
	return desc
}

type MethodOption func(*rpc.MethodDesc)

// WithMetadata attaches service metadata to the generated gofly method.
func WithMetadata(metadata map[string]string) MethodOption {
	return func(desc *rpc.MethodDesc) {
		if len(metadata) == 0 {
			return
		}
		desc.Metadata = make(map[string]string, len(metadata))
		for key, value := range metadata {
			desc.Metadata[key] = value
		}
	}
}

// Service assembles a gofly service from migration method descriptors.
func Service(name string, methods ...rpc.MethodDesc) rpc.ServiceDesc {
	return rpc.ServiceDesc{Name: strings.TrimSpace(name), Methods: append([]rpc.MethodDesc(nil), methods...)}
}
`
