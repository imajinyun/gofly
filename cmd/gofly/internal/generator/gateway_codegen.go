package generator

import (
	"errors"
	"fmt"
	"go/format"
	"path/filepath"
)

type GatewayOptions struct {
	Name   string
	Module string
	Dir    string
}

func GenerateGateway(opts GatewayOptions) error {
	if opts.Name == "" {
		opts.Name = "gateway"
	}
	if opts.Module == "" {
		return errors.New("module is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", opts.Name)
	}
	data := withGeneratedResilienceTemplateData(
		map[string]string{"Name": opts.Name, "Module": opts.Module, "ReplaceBlock": frameworkReplaceBlock("")},
		opts.Name,
	)
	files := map[string]string{
		"go.mod": gatewayGoModTemplate,
		filepath.Join("cmd", opts.Name, "main.go"):            gatewayMainTemplate,
		filepath.Join("etc", opts.Name+".json"):               gatewayConfigTemplate,
		filepath.Join("internal", "config", "config.go"):      gatewayConfigGoTemplate,
		filepath.Join("internal", "config", "config_test.go"): gatewayConfigTestTemplate,
		filepath.Join("internal", "mq", "broker.go"):          mqBrokerTemplate,
		filepath.Join("internal", "routes", "routes.go"):      gatewayRoutesTemplate,
		filepath.Join("internal", "svc", "service.go"):        gatewaySvcTemplate,
	}
	for rel, tmpl := range files {
		path := filepath.Join(opts.Dir, rel)
		content := []byte(render(tmpl, data))
		if filepath.Ext(path) == ".go" {
			formatted, err := format.Source(content)
			if err != nil {
				return fmt.Errorf("format %s: %w", path, err)
			}
			content = formatted
		}
		if err := writeGeneratedFile(path, content); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

const gatewayGoModTemplate = `module {{.Module}}

go 1.26

require github.com/imajinyun/gofly v0.0.0
{{.ReplaceBlock}}
`

const gatewayMainTemplate = `package main

import (
	"context"
	"log/slog"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/config"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/proc"
	"github.com/imajinyun/gofly/gateway"
	"github.com/imajinyun/gofly/rest"

	appconfig "{{.Module}}/internal/config"
	appmq "{{.Module}}/internal/mq"
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
	governanceManager, err := governance.NewManager(c.Governance, governance.WithPlugin(serviceConf.ProductionGovernancePlugin()))
	if err != nil {
		slog.Error("setup governance", "error", err)
		return
	}
	restConf := serviceConf.RESTConfig(c.Rest)
	server := rest.MustNewServer(
		restConf,
		rest.WithGovernanceManager(governanceManager),
	)
	mqBroker, err := appmq.NewBroker(c.MQ, governanceManager)
	if err != nil {
		slog.Error("setup mq", "error", err)
		return
	}
	defer func() { _ = mqBroker.Close(context.Background()) }()
	svcCtx := svc.NewServiceContext(c, mqBroker)
	gatewayConf, err := c.GatewayConfig(ctx)
	if err != nil {
		slog.Error("load gateway config", "error", err)
		return
	}
	gw, err := gateway.NewFromConfig(gatewayConf, nil, gateway.WithGovernanceManager(governanceManager))
	if err != nil {
		slog.Error("setup gateway", "error", err)
		return
	}
	gw.RegisterREST(server)
	if c.GatewayAdmin.Enabled {
		gw.RegisterAdmin(server, c.GatewayAdmin.PathPrefix, c.GatewayAdmin.Token)
	}
	routes.RegisterRoutes(server, svcCtx)
	governanceManager.StartAsync(ctx, func(err error) { slog.Warn("governance manager stopped", "error", err) })
	slog.Info("{{.Name}} gateway starting", "host", restConf.Host, "port", restConf.Port)
	if err := app.Run(ctx, []app.Server{server}, serviceConf.RunOptions()...); err != nil {
		slog.Error("gateway stopped", "error", err)
	}
}
`

const gatewayConfigTemplate = `{
	"environment": "development",
	"service": {"name": "{{.Name}}", "mode": "dev", "environment": "development", "startupTimeout": 5000000000, "shutdownTimeout": 10000000000, "log": {"level": "info", "format": "json", "trace": true}, "metrics": {"enabled": true}, "profile": {"enabled": false, "addr": "127.0.0.1:6060", "pathPrefix": "/debug/pprof"}, "health": {"timeout": 1000000000}},
	"mq": {"enabled": true, "driver": "memory", "service": "{{.Name}}", "trace": true, "log": true, "timeout": 3000000000, "tags": {"component": "gateway-mq"}, "kafka": {"brokers": ["127.0.0.1:9092"], "writeTimeout": 10000000000, "readTimeout": 10000000000}, "rabbitmq": {"url": "amqp://guest:guest@127.0.0.1:5672/", "prefetch": 32}, "redisstream": {"redis": {"addr": "127.0.0.1:6379"}, "blockInterval": 2000000000, "readCount": 16}},
  "rest": {
    "name": "{{.Name}}",
    "host": "127.0.0.1",
    "port": 8080,
    "middlewares": {"recover": true, "trace": true, "log": true, "timeout": true, "timeoutConfig": {{.RestTimeoutConfigJSON}}, "breaker": true, "breakerConfig": {{.RestBreakerConfigJSON}}, "metrics": true, "health": true, "requestId": true}
  },
  "gateway": {
    "timeout": 3000000000,
    "routes": [
      {"name": "api-proxy", "method": "GET", "pathPrefix": "/api", "upstreamPrefix": "/", "targets": ["http://127.0.0.1:8081"], "timeout": 5000000000, "retry": {{.GatewayRetryJSON}}, "breaker": {{.GatewayBreakerJSON}}, "rateLimit": {{.RestRateLimitConfigJSON}}, "concurrency": {{.RestMaxConcurrencyConfigJSON}}},
      {"name": "events-stream", "method": "GET", "pathPrefix": "/events", "upstreamPrefix": "/events", "targets": ["http://127.0.0.1:8081"], "timeout": 5000000000, "retry": {"attempts": 1}, "breaker": {{.GatewayBreakerJSON}}},
      {"name": "websocket-tunnel", "method": "GET", "pathPrefix": "/ws", "upstreamPrefix": "/ws", "targets": ["http://127.0.0.1:8081"], "timeout": 5000000000, "retry": {"attempts": 1}, "breaker": {{.GatewayBreakerJSON}}},
      {"name": "bff-home", "method": "GET", "pathPrefix": "/bff", "targets": ["http://127.0.0.1:8081"], "timeout": 5000000000, "retry": {"attempts": 1}, "breaker": {{.GatewayBreakerJSON}}, "aggregation": {"enabled": true, "steps": [{"name": "profile", "path": "/profile", "required": true}, {"name": "orders", "path": "/orders"}]}}
    ]
  },
  "openapiImports": [
    {"enabled": false, "url": "http://127.0.0.1:8081/openapi.json", "gatewayPrefix": "/contract", "maxBytes": 2097152, "groups": [{"name": "orders", "matchTags": ["orders"], "targets": ["http://127.0.0.1:8081"], "upstreamPrefix": "/orders-api"}]}
  ],
  "governance": {
    "rules": {{.GatewayGovernanceRulesJSON}}
  },
  "gatewayAdmin": {"enabled": true, "pathPrefix": "/_gofly/gateway", "token": ""}
}
`

const gatewayConfigGoTemplate = `package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/gateway"
	"github.com/imajinyun/gofly/rest"
)

type Config struct {
	Environment  string            ` + "`json:\"environment\"`" + `
	Service      app.ServiceConf   ` + "`json:\"service\"`" + `
	MQ           MQConfig          ` + "`json:\"mq\"`" + `
	Rest         rest.Config       ` + "`json:\"rest\"`" + `
	Gateway      gateway.Config    ` + "`json:\"gateway\"`" + `
	OpenAPIImports []OpenAPIImportConfig ` + "`json:\"openapiImports,omitempty\"`" + `
	Governance   governance.Config ` + "`json:\"governance\"`" + `
	GatewayAdmin GatewayAdminConfig ` + "`json:\"gatewayAdmin\"`" + `
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

func (c Config) GatewayConfig(ctx context.Context) (gateway.Config, error) {
	conf := c.Gateway
	for _, item := range c.OpenAPIImports {
		if !item.Enabled {
			continue
		}
		routes, err := gateway.RouteConfigsFromOpenAPIURL(ctx, gateway.OpenAPIURLSource{
			URL: item.URL,
			Headers: item.SourceHeaders,
			MaxBytes: item.MaxBytes,
		}, item.RouteOptions())
		if err != nil {
			return gateway.Config{}, fmt.Errorf("load openapi gateway import %q: %w", item.URL, err)
		}
		conf.Routes = append(conf.Routes, routes...)
	}
	return conf, nil
}

func Validate(c Config) error {
	service := c.ServiceConf()
	if !isProduction(service.Environment) {
		return nil
	}
	var errs []error
	errs = append(errs,
		app.ValidateProductionConfig(service.BootstrapConfig(c.Rest.Name)),
		rest.ValidateProductionConfig(c.Rest),
	)
	if c.GatewayAdmin.Enabled && tokenLooksUnsafe(c.GatewayAdmin.Token) {
		errs = append(errs, fmt.Errorf("production gateway admin requires a non-placeholder token"))
	}
	return errors.Join(errs...)
}

func isProduction(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}

func tokenLooksUnsafe(token string) bool {
	token = strings.TrimSpace(strings.ToLower(token))
	return token == "" || strings.Contains(token, "change-me") || strings.Contains(token, "changeme") || strings.Contains(token, "placeholder")
}

type GatewayAdminConfig struct {
	Enabled    bool   ` + "`json:\"enabled\"`" + `
	PathPrefix string ` + "`json:\"pathPrefix\"`" + `
	Token      string ` + "`json:\"token\"`" + `
}

type OpenAPIImportConfig struct {
	Enabled bool ` + "`json:\"enabled\"`" + `
	URL string ` + "`json:\"url\"`" + `
	SourceHeaders map[string]string ` + "`json:\"sourceHeaders,omitempty\"`" + `
	MaxBytes int64 ` + "`json:\"maxBytes,omitempty\"`" + `
	NamePrefix string ` + "`json:\"namePrefix,omitempty\"`" + `
	GatewayPrefix string ` + "`json:\"gatewayPrefix,omitempty\"`" + `
	UpstreamPrefix string ` + "`json:\"upstreamPrefix,omitempty\"`" + `
	Service string ` + "`json:\"service,omitempty\"`" + `
	Targets []string ` + "`json:\"targets,omitempty\"`" + `
	Timeout time.Duration ` + "`json:\"timeout,omitempty\"`" + `
	MaxBodyBytes int64 ` + "`json:\"maxBodyBytes,omitempty\"`" + `
	PreserveHost bool ` + "`json:\"preserveHost,omitempty\"`" + `
	Retry gateway.RetryPolicy ` + "`json:\"retry,omitempty\"`" + `
	Header gateway.HeaderPolicy ` + "`json:\"header,omitempty\"`" + `
	Breaker gateway.BreakerConfig ` + "`json:\"breaker,omitempty\"`" + `
	RateLimit gateway.RateLimitConfig ` + "`json:\"rateLimit,omitempty\"`" + `
	Concurrency gateway.ConcurrencyConfig ` + "`json:\"concurrency,omitempty\"`" + `
	AllowedHosts []string ` + "`json:\"allowedHosts,omitempty\"`" + `
	Tags map[string]string ` + "`json:\"tags,omitempty\"`" + `
	Headers map[string]string ` + "`json:\"headers,omitempty\"`" + `
	Groups []OpenAPIImportGroupConfig ` + "`json:\"groups,omitempty\"`" + `
}

func (c OpenAPIImportConfig) RouteOptions() gateway.OpenAPIRouteOptions {
	groups := make([]gateway.OpenAPIRouteGroup, 0, len(c.Groups))
	for _, group := range c.Groups {
		groups = append(groups, group.RouteGroup())
	}
	return gateway.OpenAPIRouteOptions{
		NamePrefix: c.NamePrefix,
		GatewayPrefix: c.GatewayPrefix,
		UpstreamPrefix: c.UpstreamPrefix,
		Service: c.Service,
		Targets: append([]string(nil), c.Targets...),
		Timeout: c.Timeout,
		MaxBodyBytes: c.MaxBodyBytes,
		PreserveHost: c.PreserveHost,
		Retry: c.Retry,
		Header: c.Header,
		Breaker: c.Breaker,
		RateLimit: c.RateLimit,
		Concurrency: c.Concurrency,
		AllowedHosts: append([]string(nil), c.AllowedHosts...),
		Tags: cloneStringMap(c.Tags),
		Headers: cloneStringMap(c.Headers),
		Groups: groups,
	}
}

type OpenAPIImportGroupConfig struct {
	Name string ` + "`json:\"name,omitempty\"`" + `
	MatchTags []string ` + "`json:\"matchTags,omitempty\"`" + `
	NamePrefix string ` + "`json:\"namePrefix,omitempty\"`" + `
	GatewayPrefix string ` + "`json:\"gatewayPrefix,omitempty\"`" + `
	UpstreamPrefix string ` + "`json:\"upstreamPrefix,omitempty\"`" + `
	Service string ` + "`json:\"service,omitempty\"`" + `
	Targets []string ` + "`json:\"targets,omitempty\"`" + `
	Headers map[string]string ` + "`json:\"headers,omitempty\"`" + `
}

func (c OpenAPIImportGroupConfig) RouteGroup() gateway.OpenAPIRouteGroup {
	return gateway.OpenAPIRouteGroup{
		Name: c.Name,
		MatchTags: append([]string(nil), c.MatchTags...),
		NamePrefix: c.NamePrefix,
		GatewayPrefix: c.GatewayPrefix,
		UpstreamPrefix: c.UpstreamPrefix,
		Service: c.Service,
		Targets: append([]string(nil), c.Targets...),
		Headers: cloneStringMap(c.Headers),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
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
`

const gatewayConfigTestTemplate = `package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imajinyun/gofly/gateway"
	"github.com/imajinyun/gofly/rest"
)

func TestGatewayConfigLoadsOpenAPIImportProfile(t *testing.T) {
	doc := rest.OpenAPIDocument{
		OpenAPI: "3.0.3",
		Info: rest.OpenAPIInfo{Title: "{{.Name}} upstream", Version: "1.0.0"},
		Paths: map[string]map[string]rest.Operation{
			"/orders/{id}": {
				"get": {
					OperationID: "getOrder",
					Tags: []string{"orders"},
					Responses: map[string]rest.Response{"200": {Description: "OK"}},
				},
			},
		},
	}
	openAPIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi.json" {
			t.Fatalf("openapi request path = %q, want /openapi.json", r.URL.Path)
		}
		if r.Header.Get("X-Contract-Token") != "test-token" {
			t.Fatalf("openapi source token = %q, want test-token", r.Header.Get("X-Contract-Token"))
		}
		if err := json.NewEncoder(w).Encode(doc); err != nil {
			t.Fatalf("encode openapi doc: %v", err)
		}
	}))
	t.Cleanup(openAPIServer.Close)

	c := Config{
		Gateway: gateway.Config{Routes: []gateway.RouteConfig{{
			Name: "static",
			Method: http.MethodGet,
			PathPrefix: "/api",
			Targets: []string{"http://127.0.0.1:8081"},
		}}},
		OpenAPIImports: []OpenAPIImportConfig{{
			Enabled: true,
			URL: openAPIServer.URL + "/openapi.json",
			SourceHeaders: map[string]string{"X-Contract-Token": "test-token"},
			MaxBytes: 1 << 20,
			GatewayPrefix: "/contract",
			Headers: map[string]string{"X-Base": "edge"},
			Groups: []OpenAPIImportGroupConfig{{
				Name: "orders",
				MatchTags: []string{"orders"},
				UpstreamPrefix: "/orders-api",
				Targets: []string{"http://127.0.0.1:8081"},
				Headers: map[string]string{"X-Backend": "orders"},
			}},
		}},
	}
	conf, err := c.GatewayConfig(context.Background())
	if err != nil {
		t.Fatalf("GatewayConfig: %v", err)
	}
	if len(conf.Routes) != 2 {
		t.Fatalf("routes = %#v, want static plus imported route", conf.Routes)
	}
	imported := conf.Routes[1]
	if imported.Name != "orders-getOrder" || imported.Method != http.MethodGet || imported.PathPrefix != "/contract/orders" || imported.UpstreamPrefix != "/orders-api/orders" {
		t.Fatalf("imported route = %#v", imported)
	}
	if imported.Headers["X-Base"] != "edge" || imported.Headers["X-Backend"] != "orders" {
		t.Fatalf("imported headers = %#v", imported.Headers)
	}
	if imported.Targets[0] != "http://127.0.0.1:8081" {
		t.Fatalf("imported targets = %#v", imported.Targets)
	}
}

func TestGatewayConfigSkipsDisabledOpenAPIImportProfile(t *testing.T) {
	c := Config{
		Gateway: gateway.Config{Timeout: time.Second, Routes: []gateway.RouteConfig{{
			Name: "static",
			Method: http.MethodGet,
			PathPrefix: "/api",
			Targets: []string{"http://127.0.0.1:8081"},
		}}},
		OpenAPIImports: []OpenAPIImportConfig{{Enabled: false, URL: "http://127.0.0.1:1/openapi.json"}},
	}
	conf, err := c.GatewayConfig(context.Background())
	if err != nil {
		t.Fatalf("GatewayConfig disabled import: %v", err)
	}
	if conf.Timeout != time.Second || len(conf.Routes) != 1 || conf.Routes[0].Name != "static" {
		t.Fatalf("gateway config = %#v, want unchanged static config", conf)
	}
}
`

const gatewaySvcTemplate = `package svc

import (
	"github.com/imajinyun/gofly/core/mq"
	"{{.Module}}/internal/config"
)

type ServiceContext struct { Config config.Config; MQ mq.Broker }

func NewServiceContext(c config.Config, brokers ...mq.Broker) *ServiceContext {
	var broker mq.Broker
	if len(brokers) > 0 { broker = brokers[0] }
	return &ServiceContext{Config: c, MQ: broker}
}
`

const gatewayRoutesTemplate = `package routes

import (
	"net/http"

	"github.com/imajinyun/gofly/rest"
	"{{.Module}}/internal/svc"
)

func RegisterRoutes(server *rest.Server, svcCtx *svc.ServiceContext) {
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/", Handler: func(ctx *rest.Context) {
		ctx.JSON(http.StatusOK, map[string]string{"service": "{{.Name}}", "status": "ok"})
	}})
}
`
