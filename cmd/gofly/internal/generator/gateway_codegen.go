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
	data["GatewayConfigJSON"] = render(gatewayConfigTemplate, data)
	files := map[string]string{
		"go.mod": gatewayGoModTemplate,
		filepath.Join("cmd", opts.Name, "main.go"):                gatewayMainTemplate,
		filepath.Join("etc", opts.Name+".json"):                   gatewayConfigTemplate,
		filepath.Join("etc", opts.Name+"-profile-candidate.json"): gatewayProfileCandidateTemplate,
		filepath.Join("internal", "config", "config.go"):          gatewayConfigGoTemplate,
		filepath.Join("internal", "config", "config_test.go"):     gatewayConfigTestTemplate,
		filepath.Join("internal", "mq", "broker.go"):              mqBrokerTemplate,
		filepath.Join("internal", "routes", "routes.go"):          gatewayRoutesTemplate,
		filepath.Join("internal", "svc", "service.go"):            gatewaySvcTemplate,
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
	gatewayResolvers, err := c.GatewayResolvers()
	if err != nil {
		slog.Error("load gateway discovery resolvers", "error", err)
		return
	}
	gatewayOptions := append(c.GatewayOptions(), gateway.WithGovernanceManager(governanceManager))
	gw, err := gateway.NewFromConfig(gatewayConf, gatewayResolvers, gatewayOptions...)
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
      {"name": "api-proxy", "method": "GET", "pathPrefix": "/api", "upstreamPrefix": "/", "service": "orders", "targets": ["http://127.0.0.1:8081"], "timeout": 5000000000, "retry": {{.GatewayRetryJSON}}, "breaker": {{.GatewayBreakerJSON}}, "rateLimit": {{.RestRateLimitConfigJSON}}, "concurrency": {{.RestMaxConcurrencyConfigJSON}}},
      {"name": "events-stream", "method": "GET", "pathPrefix": "/events", "upstreamPrefix": "/events", "service": "orders", "targets": ["http://127.0.0.1:8081"], "timeout": 5000000000, "retry": {"attempts": 1}, "breaker": {{.GatewayBreakerJSON}}},
      {"name": "websocket-tunnel", "method": "GET", "pathPrefix": "/ws", "upstreamPrefix": "/ws", "service": "orders", "targets": ["http://127.0.0.1:8081"], "timeout": 5000000000, "retry": {"attempts": 1}, "breaker": {{.GatewayBreakerJSON}}},
      {"name": "bff-home", "method": "GET", "pathPrefix": "/bff", "service": "orders", "targets": ["http://127.0.0.1:8081"], "timeout": 5000000000, "retry": {"attempts": 1}, "breaker": {{.GatewayBreakerJSON}}, "aggregation": {"enabled": true, "shape": {"mode": "flat"}, "steps": [{"name": "profile", "path": "/profile", "required": true, "fallback": {"id": "anonymous"}}, {"name": "orders", "path": "/orders", "timeout": 1000000, "retry": {"attempts": 2, "statuses": [503]}, "fallback": []}]}},
      {"name": "rpc-orders", "method": "POST", "pathPrefix": "/rpc/orders", "service": "orders-rpc", "targets": ["http://127.0.0.1:8082"], "timeout": 5000000000, "retry": {"attempts": 1}, "breaker": {{.GatewayBreakerJSON}}, "transcode": {"enabled": true, "descriptor": "orders.OrderService", "descriptorMethod": "GetOrder", "payload": {"mode": "profile", "mergeBodyObject": true}}}
    ]
  },
  "transcodeProfiles": [
    {"descriptor": "orders.OrderService", "descriptorMethod": "GetOrder", "requestMappings": [{"source": "body.id", "target": "order.id"}, {"source": "body.trace", "target": "meta.trace"}, {"target": "meta.region", "default": "cn"}], "responseMappings": [{"source": "body.id", "target": "data.id"}, {"source": "body.source", "target": "meta.source"}, {"target": "meta.profile", "default": "descriptor"}], "errorMappings": [{"source": "body.code", "target": "error.code"}, {"source": "body.error", "target": "error.message"}, {"source": "body.status", "target": "error.status"}, {"target": "error.source", "default": "rpc"}]}
  ],
  "openapiImports": [
    {"enabled": false, "url": "http://127.0.0.1:8081/openapi.json", "gatewayPrefix": "/contract", "maxBytes": 2097152, "groups": [{"name": "orders", "matchTags": ["orders"], "service": "orders-rpc", "targets": ["http://127.0.0.1:8082"], "upstreamPrefix": "/orders-api", "transcode": {"enabled": true, "descriptor": "orders.OrderService", "methodFromOperationId": true}}]}
  ],
  "gatewayDiscovery": {"failover": false, "services": [{"enabled": false, "service": "orders", "provider": "dns", "host": "orders.service.local", "port": 8081, "scheme": "http", "watchInterval": 5000000000}]},
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
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/gateway"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
)

type Config struct {
	Environment  string            ` + "`json:\"environment\"`" + `
	Service      app.ServiceConf   ` + "`json:\"service\"`" + `
	MQ           MQConfig          ` + "`json:\"mq\"`" + `
	Rest         rest.Config       ` + "`json:\"rest\"`" + `
	Gateway      gateway.Config    ` + "`json:\"gateway\"`" + `
	TranscodeProfiles []gateway.TranscodeProfile ` + "`json:\"transcodeProfiles,omitempty\"`" + `
	OpenAPIImports []OpenAPIImportConfig ` + "`json:\"openapiImports,omitempty\"`" + `
	GatewayDiscovery GatewayDiscoveryConfig ` + "`json:\"gatewayDiscovery,omitempty\"`" + `
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

func (c Config) GatewayOptions() []gateway.Option {
	var opts []gateway.Option
	if c.GatewayDiscovery.Failover {
		opts = append(opts, gateway.WithDiscoveryFailover())
	}
	if len(c.TranscodeProfiles) > 0 {
		opts = append(opts, gateway.WithTranscodeProfiles(c.TranscodeProfiles...))
	}
	return opts
}

func (c Config) GatewayResolvers() (map[string]rpc.Resolver, error) {
	return c.gatewayResolvers(nil)
}

func (c Config) gatewayResolvers(lookupIP func(context.Context, string) ([]net.IP, error)) (map[string]rpc.Resolver, error) {
	resolvers := make(map[string]rpc.Resolver)
	for _, item := range c.GatewayDiscovery.Services {
		if !item.Enabled {
			continue
		}
		service := strings.TrimSpace(item.Service)
		if service == "" {
			return nil, errors.New("gateway discovery service is required")
		}
		provider := strings.ToLower(strings.TrimSpace(item.Provider))
		switch provider {
		case "dns":
			resolver, err := rpc.NewDNSResolver(rpc.DNSResolverConfig{
				Host: item.Host,
				Port: item.Port,
				Scheme: item.Scheme,
				LookupIP: lookupIP,
				WatchInterval: item.WatchInterval,
			})
			if err != nil {
				return nil, fmt.Errorf("create dns gateway resolver for %q: %w", service, err)
			}
			var runtimeResolver rpc.Resolver = resolver
			if c.GatewayDiscovery.Failover {
				failover, err := rpc.NewFailoverResolver(resolver)
				if err != nil {
					return nil, fmt.Errorf("create failover gateway resolver for %q: %w", service, err)
				}
				runtimeResolver = failover
			}
			resolvers[service] = runtimeResolver
		case "static":
			resolvers[service] = rpc.NewStaticResolver(item.Targets...)
		case "nacos":
			static := rpc.NewStaticResolver(item.Targets...)
			if _, err := static.Resolve(context.Background()); err != nil {
				return nil, fmt.Errorf("nacos gateway resolver for %q requires config fallback targets: %w", service, err)
			}
			var runtimeResolver rpc.Resolver = static
			if c.GatewayDiscovery.Failover {
				failover, err := rpc.NewFailoverResolver(static)
				if err != nil {
					return nil, fmt.Errorf("create nacos fallback resolver for %q: %w", service, err)
				}
				runtimeResolver = failover
			}
			resolvers[service] = runtimeResolver
		default:
			return nil, fmt.Errorf("unsupported gateway discovery provider %q", item.Provider)
		}
	}
	if len(resolvers) == 0 {
		return nil, nil
	}
	return resolvers, nil
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

type GatewayDiscoveryConfig struct {
	Failover bool ` + "`json:\"failover,omitempty\"`" + `
	Services []GatewayDiscoveryServiceConfig ` + "`json:\"services,omitempty\"`" + `
}

type GatewayDiscoveryServiceConfig struct {
	Enabled bool ` + "`json:\"enabled,omitempty\"`" + `
	Service string ` + "`json:\"service,omitempty\"`" + `
	Provider string ` + "`json:\"provider,omitempty\"`" + `
	Host string ` + "`json:\"host,omitempty\"`" + `
	Port int ` + "`json:\"port,omitempty\"`" + `
	Scheme string ` + "`json:\"scheme,omitempty\"`" + `
	Targets []string ` + "`json:\"targets,omitempty\"`" + `
	WatchInterval time.Duration ` + "`json:\"watchInterval,omitempty\"`" + `
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
	Transcode OpenAPIImportTranscodeConfig ` + "`json:\"transcode,omitempty\"`" + `
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
		Transcode: c.Transcode.RouteOptions(),
		Groups: groups,
	}
}

type OpenAPIImportTranscodeConfig struct {
	Enabled bool ` + "`json:\"enabled,omitempty\"`" + `
	Descriptor string ` + "`json:\"descriptor,omitempty\"`" + `
	DescriptorMethod string ` + "`json:\"descriptorMethod,omitempty\"`" + `
	Service string ` + "`json:\"service,omitempty\"`" + `
	Method string ` + "`json:\"method,omitempty\"`" + `
	MethodFromOperationID bool ` + "`json:\"methodFromOperationId,omitempty\"`" + `
}

func (c OpenAPIImportTranscodeConfig) RouteOptions() gateway.OpenAPITranscodeOptions {
	return gateway.OpenAPITranscodeOptions{
		Enabled: c.Enabled,
		Descriptor: strings.TrimSpace(c.Descriptor),
		DescriptorMethod: strings.TrimSpace(c.DescriptorMethod),
		Service: strings.TrimSpace(c.Service),
		Method: strings.TrimSpace(c.Method),
		MethodFromOperationID: c.MethodFromOperationID,
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
	Transcode OpenAPIImportTranscodeConfig ` + "`json:\"transcode,omitempty\"`" + `
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
		Transcode: c.Transcode.RouteOptions(),
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

const gatewayProfileCandidateTemplate = `{
  "descriptor": "orders.OrderService",
  "descriptorMethod": "GetOrder",
  "requestMappings": [
    {"source": "body.id", "target": "order.id"},
    {"source": "body.trace", "target": "meta.trace"},
    {"target": "meta.region", "default": "cn"}
  ],
  "responseMappings": [
    {"source": "body.id", "target": "data.id"},
    {"source": "body.source", "target": "meta.source"},
    {"target": "meta.profile", "default": "descriptor"}
  ],
  "errorMappings": [
    {"source": "body.code", "target": "error.code"},
    {"source": "body.error", "target": "error.message"},
    {"source": "body.status", "target": "error.status"},
    {"target": "error.source", "default": "rpc"}
  ]
}
`

const gatewayConfigTestTemplate = `package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/gateway"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
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
				Service: "orders",
				UpstreamPrefix: "/orders-api",
				Targets: []string{"http://127.0.0.1:8081"},
				Headers: map[string]string{"X-Backend": "orders"},
				Transcode: OpenAPIImportTranscodeConfig{
					Enabled: true,
					Descriptor: "orders.OrderService",
					MethodFromOperationID: true,
				},
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
	if imported.Name != "orders-getOrder" || imported.Method != http.MethodGet || imported.PathPrefix != "/contract/orders" || imported.UpstreamPrefix != "/orders-api/orders" || imported.Service != "orders" {
		t.Fatalf("imported route = %#v", imported)
	}
	if imported.Headers["X-Base"] != "edge" || imported.Headers["X-Backend"] != "orders" {
		t.Fatalf("imported headers = %#v", imported.Headers)
	}
	if !imported.Transcode.Enabled || imported.Transcode.Descriptor != "orders.OrderService" || imported.Transcode.DescriptorMethod != "GetOrder" {
		t.Fatalf("imported transcode = %#v, want descriptor-driven GetOrder", imported.Transcode)
	}
	if imported.Targets[0] != "http://127.0.0.1:8081" {
		t.Fatalf("imported targets = %#v", imported.Targets)
	}
}

func TestGatewayGeneratedOpenAPIImportProfileIsRunnable(t *testing.T) {
	doc := rest.OpenAPIDocument{
		OpenAPI: "3.0.3",
		Info: rest.OpenAPIInfo{Title: "{{.Name}} upstream", Version: "1.0.0"},
		Paths: map[string]map[string]rest.Operation{
			"/orders/{id}": {
				"post": {
					OperationID: "getOrder",
					Tags: []string{"orders"},
					Parameters: []rest.Parameter{
						{Name: "id", In: "path", Required: true, Schema: rest.IntegerSchema()},
						{Name: "include_history", In: "query", Required: true, Schema: rest.BooleanSchema()},
						{Name: "tags", In: "query", Schema: rest.ArraySchema(rest.Schema{Type: "integer"})},
					},
					RequestBody: rest.JSONBodySchema(rest.Schema{
						Type: "object",
						Required: []string{"trace", "items"},
						Properties: map[string]rest.Schema{
							"trace": {Type: "string"},
							"items": {Type: "array", Items: &rest.Schema{Type: "object", Required: []string{"sku"}, Properties: map[string]rest.Schema{
								"sku": {Type: "string"},
								"quantity": {Type: "integer"},
							}}},
						},
					}, false),
					Responses: map[string]rest.Response{"200": {Description: "OK"}},
					Extensions: map[string]any{
						"x-gofly-transcode": map[string]any{
							"payloadMappings": []any{
								map[string]any{"source": "path.id", "target": "order.id"},
								map[string]any{"source": "query.include_history", "target": "options.includeHistory"},
								map[string]any{"source": "query.tags", "target": "options.tags"},
								map[string]any{"source": "body.trace", "target": "meta.trace"},
								map[string]any{"source": "body.items", "target": "order.lines"},
								map[string]any{"target": "meta.region", "default": "cn"},
							},
						},
					},
				},
			},
		},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.json":
			if r.Header.Get("X-Contract-Token") != "test-token" {
				t.Fatalf("openapi source token = %q, want test-token", r.Header.Get("X-Contract-Token"))
			}
			if err := json.NewEncoder(w).Encode(doc); err != nil {
				t.Fatalf("encode openapi doc: %v", err)
			}
		case "/orders-api/orders/42":
			if r.Header.Get("X-Base") != "edge" || r.Header.Get("X-Backend") != "orders" {
				t.Fatalf("upstream headers X-Base=%q X-Backend=%q, want generated OpenAPI group headers", r.Header.Get("X-Base"), r.Header.Get("X-Backend"))
			}
			_, _ = fmt.Fprint(w, "order:42")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	rpcServer := rpc.NewServer()
	if err := rpcServer.RegisterService(rpc.ServiceDesc{
		Name: "orders.OrderService",
		Methods: []rpc.MethodDesc{rpc.GenericMethod("GetOrder", func(_ context.Context, raw json.RawMessage) (any, error) {
			var request struct {
				Order struct {
					ID int64 ` + "`json:\"id\"`" + `
					Lines []struct {
						SKU string ` + "`json:\"sku\"`" + `
						Quantity int64 ` + "`json:\"quantity\"`" + `
					} ` + "`json:\"lines\"`" + `
				} ` + "`json:\"order\"`" + `
				Options struct {
					IncludeHistory bool ` + "`json:\"includeHistory\"`" + `
					Tags []int64 ` + "`json:\"tags\"`" + `
				} ` + "`json:\"options\"`" + `
				Meta struct {
					Trace string ` + "`json:\"trace\"`" + `
					Region string ` + "`json:\"region\"`" + `
				} ` + "`json:\"meta\"`" + `
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			return map[string]any{"id": request.Order.ID, "include_history": request.Options.IncludeHistory, "tags": request.Options.Tags, "trace": request.Meta.Trace, "region": request.Meta.Region, "items": request.Order.Lines, "source": "rpc"}, nil
		})},
	}, nil); err != nil {
		t.Fatalf("register generated OpenAPI RPC service: %v", err)
	}
	rpcUpstream := httptest.NewServer(rpcServer)
	t.Cleanup(rpcUpstream.Close)

	c := Config{
		OpenAPIImports: []OpenAPIImportConfig{{
			Enabled: true,
			URL: upstream.URL + "/openapi.json",
			SourceHeaders: map[string]string{"X-Contract-Token": "test-token"},
			MaxBytes: 1 << 20,
			GatewayPrefix: "/contract",
			Headers: map[string]string{"X-Base": "edge"},
			Groups: []OpenAPIImportGroupConfig{{
				Name: "orders",
				MatchTags: []string{"orders"},
				Service: "orders-rpc",
				Targets: []string{rpcUpstream.URL},
				Headers: map[string]string{"X-Backend": "orders"},
				Transcode: OpenAPIImportTranscodeConfig{
					Enabled: true,
					Descriptor: "orders.OrderService",
					MethodFromOperationID: true,
				},
			}},
		}},
	}
	conf, err := c.GatewayConfig(context.Background())
	if err != nil {
		t.Fatalf("GatewayConfig: %v", err)
	}
	gw, err := gateway.NewFromConfig(conf, nil, gateway.WithDescriptors(rpc.Descriptor{Name: "orders.OrderService", Methods: []rpc.MethodDescriptor{{Name: "GetOrder"}}}))
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	rr := httptest.NewRecorder()
	gw.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/contract/orders/42?include_history=true&tags=1,2&tags=3", strings.NewReader("{\"trace\":\"t1\",\"items\":[{\"sku\":\"sku-1\",\"quantity\":2}]}")))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "\"id\":42") || !strings.Contains(rr.Body.String(), "\"include_history\":true") || !strings.Contains(rr.Body.String(), "\"tags\":[1,2,3]") || !strings.Contains(rr.Body.String(), "\"trace\":\"t1\"") || !strings.Contains(rr.Body.String(), "\"region\":\"cn\"") || !strings.Contains(rr.Body.String(), "\"quantity\":2") || !strings.Contains(rr.Body.String(), "\"source\":\"rpc\"") {
		t.Fatalf("generated OpenAPI gateway response = %d %q, want imported RPC transcode success", rr.Code, rr.Body.String())
	}

	missingQuery := httptest.NewRecorder()
	gw.ServeHTTP(missingQuery, httptest.NewRequest(http.MethodPost, "/contract/orders/42", strings.NewReader("{\"trace\":\"t1\",\"items\":[{\"sku\":\"sku-1\",\"quantity\":2}]}")))
	if missingQuery.Code != http.StatusBadRequest || !strings.Contains(missingQuery.Body.String(), "\"code\":\"invalid_argument\"") || !strings.Contains(missingQuery.Body.String(), "include_history is required") {
		t.Fatalf("generated OpenAPI missing query response = %d %q, want required query invalid_argument", missingQuery.Code, missingQuery.Body.String())
	}

	invalid := httptest.NewRecorder()
	gw.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/contract/orders/42?include_history=true", strings.NewReader("{\"trace\":\"t1\",\"items\":[{\"sku\":\"sku-1\",\"quantity\":\"two\"}]}")))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "\"code\":\"invalid_argument\"") || !strings.Contains(invalid.Body.String(), "body.items[0].quantity must be integer") {
		t.Fatalf("generated OpenAPI invalid body response = %d %q, want body schema invalid_argument", invalid.Code, invalid.Body.String())
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

func TestGatewayConfigValidatesTranscodeProfileDiff(t *testing.T) {
	c := Config{
		Gateway: gateway.Config{Routes: []gateway.RouteConfig{{
			Name: "rpc-orders",
			Method: http.MethodPost,
			PathPrefix: "/rpc/orders",
			Targets: []string{"http://127.0.0.1:8082"},
			Transcode: gateway.TranscodeConfig{
				Enabled: true,
				Descriptor: "orders.OrderService",
				DescriptorMethod: "GetOrder",
				Payload: gateway.TranscodePayloadConfig{Mode: "profile", MergeBodyObject: true},
			},
		}}},
		TranscodeProfiles: []gateway.TranscodeProfile{{
			Descriptor: "orders.OrderService",
			DescriptorMethod: "GetOrder",
			RequestMappings: []gateway.TranscodePayloadMapping{{Source: "body.id", Target: "order.id"}},
			ResponseMappings: []gateway.TranscodePayloadMapping{{Source: "body.id", Target: "data.id"}},
			ErrorMappings: []gateway.TranscodePayloadMapping{{Source: "body.code", Target: "error.code"}},
		}},
	}
	gw, err := gateway.NewFromConfig(c.Gateway, nil, append(c.GatewayOptions(), gateway.WithDescriptors(rpc.Descriptor{Name: "orders.OrderService", Methods: []rpc.MethodDescriptor{{Name: "GetOrder"}}}))...)
	if err != nil {
		t.Fatalf("NewFromConfig with generated transcode profile: %v", err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	compatible := gw.ValidateTranscodeProfile(gateway.TranscodeProfile{
		Descriptor: "orders.OrderService",
		DescriptorMethod: "GetOrder",
		RequestMappings: []gateway.TranscodePayloadMapping{
			{Source: "body.id", Target: "order.id"},
			{Source: "body.trace", Target: "meta.trace"},
		},
		ResponseMappings: []gateway.TranscodePayloadMapping{{Source: "body.id", Target: "data.id"}},
		ErrorMappings: []gateway.TranscodePayloadMapping{{Source: "body.code", Target: "error.code"}},
	})
	if !compatible.OK || !compatible.Compatible || len(compatible.Changes) != 1 || compatible.Changes[0].Kind != "add_mapping" {
		t.Fatalf("compatible generated profile report = %+v, want additive compatible diff", compatible)
	}

	breaking := gw.ValidateTranscodeProfile(gateway.TranscodeProfile{
		Descriptor: "orders.OrderService",
		DescriptorMethod: "GetOrder",
		RequestMappings: []gateway.TranscodePayloadMapping{{Source: "body.id", Target: "person.id"}},
	})
	if !breaking.OK || breaking.Compatible || len(breaking.Changes) == 0 || breaking.Changes[0].Severity != "breaking" {
		t.Fatalf("breaking generated profile report = %+v, want breaking diff", breaking)
	}
}

func TestGatewayOptionsEnableDiscoveryFailover(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders/42" {
			t.Fatalf("upstream path = %q, want /orders/42", r.URL.Path)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)
	registry := discovery.NewMemoryRegistry()
	lease, err := registry.Register(context.Background(), discovery.Instance{Service: "orders", ID: "orders-a", Endpoint: upstream.URL})
	if err != nil {
		t.Fatalf("register discovery instance: %v", err)
	}

	c := Config{
		Gateway: gateway.Config{Routes: []gateway.RouteConfig{{
			Name: "orders",
			Method: http.MethodGet,
			PathPrefix: "/api",
			UpstreamPrefix: "",
			Service: "orders",
		}}},
		GatewayDiscovery: GatewayDiscoveryConfig{Failover: true},
	}
	conf, err := c.GatewayConfig(context.Background())
	if err != nil {
		t.Fatalf("GatewayConfig: %v", err)
	}
	opts := append(c.GatewayOptions(), gateway.WithDiscoveryResolvers(map[string]discovery.Resolver{"orders": registry}))
	gw, err := gateway.NewFromConfig(conf, nil, opts...)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	first := httptest.NewRecorder()
	gw.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/orders/42", nil))
	if first.Code != http.StatusOK || first.Body.String() != "ok" {
		t.Fatalf("first gateway response = %d %q, want discovery success", first.Code, first.Body.String())
	}
	if err := lease.Close(context.Background()); err != nil {
		t.Fatalf("close discovery lease: %v", err)
	}
	second := httptest.NewRecorder()
	gw.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/orders/42", nil))
	if second.Code != http.StatusOK || second.Body.String() != "ok" {
		t.Fatalf("second gateway response = %d %q, want failover success", second.Code, second.Body.String())
	}
	snapshot := gw.Snapshot()
	if len(snapshot.Discovery) != 1 || !snapshot.Discovery[0].Stale || snapshot.Discovery[0].Fallbacks == 0 || snapshot.Discovery[0].Error == "" {
		t.Fatalf("discovery snapshot = %+v, want stale failover evidence", snapshot.Discovery)
	}
	if len(snapshot.Discovery[0].Instances) != 1 || snapshot.Discovery[0].Instances[0].Endpoint != upstream.URL {
		t.Fatalf("stale discovery instances = %+v, want last known endpoint", snapshot.Discovery[0].Instances)
	}
}

func TestGatewayDNSDiscoveryConfigBuildsResolverWithFailover(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders/42" {
			t.Fatalf("upstream path = %q, want /orders/42", r.URL.Path)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)
	host, portText, err := net.SplitHostPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split upstream address: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatalf("parse upstream port %q: %v", portText, err)
	}

	c := Config{
		Gateway: gateway.Config{Routes: []gateway.RouteConfig{{
			Name: "orders",
			Method: http.MethodGet,
			PathPrefix: "/api",
			Service: "orders",
		}}},
		GatewayDiscovery: GatewayDiscoveryConfig{
			Failover: true,
			Services: []GatewayDiscoveryServiceConfig{{
				Enabled: true,
				Service: "orders",
				Provider: "dns",
				Host: "orders.service.local",
				Port: port,
				Scheme: "http",
				WatchInterval: time.Millisecond,
			}},
		},
	}
	conf, err := c.GatewayConfig(context.Background())
	if err != nil {
		t.Fatalf("GatewayConfig: %v", err)
	}
	lookupFail := false
	resolvers, err := c.gatewayResolvers(func(context.Context, string) ([]net.IP, error) {
		if lookupFail {
			return nil, fmt.Errorf("dns unavailable")
		}
		return []net.IP{net.ParseIP(host)}, nil
	})
	if err != nil {
		t.Fatalf("GatewayResolvers: %v", err)
	}
	gw, err := gateway.NewFromConfig(conf, resolvers, c.GatewayOptions()...)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	first := httptest.NewRecorder()
	gw.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/orders/42", nil))
	if first.Code != http.StatusOK || first.Body.String() != "ok" {
		t.Fatalf("first DNS gateway response = %d %q", first.Code, first.Body.String())
	}
	lookupFail = true
	second := httptest.NewRecorder()
	gw.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/orders/42", nil))
	if second.Code != http.StatusOK || second.Body.String() != "ok" {
		t.Fatalf("stale DNS gateway response = %d %q", second.Code, second.Body.String())
	}
}

func TestGatewayStaticDiscoveryConfigBuildsResolver(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders/42" {
			t.Fatalf("upstream path = %q, want /orders/42", r.URL.Path)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	c := Config{
		Gateway: gateway.Config{Routes: []gateway.RouteConfig{{
			Name: "orders",
			Method: http.MethodGet,
			PathPrefix: "/api",
			Service: "orders",
		}}},
		GatewayDiscovery: GatewayDiscoveryConfig{
			Services: []GatewayDiscoveryServiceConfig{{
				Enabled: true,
				Service: "orders",
				Provider: "static",
				Targets: []string{upstream.URL},
			}},
		},
	}
	resolvers, err := c.GatewayResolvers()
	if err != nil {
		t.Fatalf("GatewayResolvers: %v", err)
	}
	gw, err := gateway.NewFromConfig(c.Gateway, resolvers, c.GatewayOptions()...)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	rr := httptest.NewRecorder()
	gw.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/orders/42", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
		t.Fatalf("static gateway response = %d %q", rr.Code, rr.Body.String())
	}
	snapshot := gw.Snapshot()
	if len(snapshot.Discovery) != 1 || len(snapshot.Discovery[0].Endpoints) != 1 || snapshot.Discovery[0].Endpoints[0] != upstream.URL {
		t.Fatalf("static discovery snapshot = %+v, want static endpoint", snapshot.Discovery)
	}
}

func TestGatewayNacosDiscoveryConfigUsesStaticFallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders/42" {
			t.Fatalf("upstream path = %q, want /orders/42", r.URL.Path)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	c := Config{
		Gateway: gateway.Config{Routes: []gateway.RouteConfig{{
			Name: "orders",
			Method: http.MethodGet,
			PathPrefix: "/api",
			Service: "orders",
		}}},
		GatewayDiscovery: GatewayDiscoveryConfig{
			Failover: true,
			Services: []GatewayDiscoveryServiceConfig{{
				Enabled: true,
				Service: "orders",
				Provider: "nacos",
				Targets: []string{upstream.URL},
			}},
		},
	}
	resolvers, err := c.GatewayResolvers()
	if err != nil {
		t.Fatalf("GatewayResolvers: %v", err)
	}
	gw, err := gateway.NewFromConfig(c.Gateway, resolvers, c.GatewayOptions()...)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	rr := httptest.NewRecorder()
	gw.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/orders/42", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
		t.Fatalf("nacos fallback gateway response = %d %q", rr.Code, rr.Body.String())
	}
	snapshot := gw.Snapshot()
	if len(snapshot.Discovery) != 1 || snapshot.Discovery[0].Stale || len(snapshot.Discovery[0].Endpoints) != 1 || snapshot.Discovery[0].Endpoints[0] != upstream.URL {
		t.Fatalf("nacos fallback discovery snapshot = %+v, want static fallback endpoint", snapshot.Discovery)
	}

	c.GatewayDiscovery.Services[0].Targets = nil
	if _, err := c.GatewayResolvers(); err == nil {
		t.Fatalf("empty nacos fallback error = %v, want explicit fallback target error", err)
	}
}

func TestGatewayGeneratedDNSDiscoveryProfileIsRunnable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders/42" {
			t.Fatalf("upstream path = %q, want /orders/42", r.URL.Path)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)
	host, portText, err := net.SplitHostPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split upstream address: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatalf("parse upstream port %q: %v", portText, err)
	}

	var c Config
	if err := json.Unmarshal([]byte(` + "`" + `{{.GatewayConfigJSON}}` + "`" + `), &c); err != nil {
		t.Fatalf("decode generated config: %v", err)
	}
	if len(c.Gateway.Routes) == 0 || c.Gateway.Routes[0].Service != "orders" {
		t.Fatalf("generated gateway route service = %#v, want orders service", c.Gateway.Routes)
	}
	if c.GatewayDiscovery.Failover || len(c.GatewayDiscovery.Services) != 1 || c.GatewayDiscovery.Services[0].Enabled {
		t.Fatalf("default gateway discovery = %#v, want disabled opt-in profile", c.GatewayDiscovery)
	}
	c.GatewayDiscovery.Failover = true
	c.GatewayDiscovery.Services[0].Enabled = true
	c.GatewayDiscovery.Services[0].Host = "orders.service.local"
	c.GatewayDiscovery.Services[0].Port = port
	c.GatewayDiscovery.Services[0].Scheme = "http"
	c.GatewayDiscovery.Services[0].WatchInterval = time.Millisecond

	conf, err := c.GatewayConfig(context.Background())
	if err != nil {
		t.Fatalf("GatewayConfig: %v", err)
	}
	resolvers, err := c.gatewayResolvers(func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP(host)}, nil
	})
	if err != nil {
		t.Fatalf("GatewayResolvers: %v", err)
	}
	gw, err := gateway.NewFromConfig(conf, resolvers, c.GatewayOptions()...)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	rr := httptest.NewRecorder()
	gw.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/orders/42", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
		t.Fatalf("generated DNS gateway response = %d %q", rr.Code, rr.Body.String())
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
