package generator

const goModTemplate = `module {{.Module}}

go 1.26

require github.com/imajinyun/gofly v0.0.0
{{.ReplaceBlock}}
`

const muxOTelSinkFeatureTemplate = `// Package muxotelsink registers the generated application's custom mux OTel log sink.
package muxotelsink

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/imajinyun/gofly/rpc"
)

const Name = "{{.MuxOTelSinkName}}"

type provider struct{}

type profile struct {
	Endpoint  string ` + "`json:\"endpoint\"`" + `
	BatchSize int    ` + "`json:\"batchSize\"`" + `
	Timeout   string ` + "`json:\"timeout\"`" + `
}

func (provider) RPCMuxOTelLogProfileSchema() json.RawMessage {
	return json.RawMessage(` + "`" + `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"required":["endpoint","batchSize","timeout"],"properties":{"endpoint":{"type":"string","format":"uri","pattern":"^https://"},"batchSize":{"type":"integer","minimum":1,"maximum":1000},"timeout":{"type":"string","pattern":"^[0-9]+(ms|s)$"}}}` + "`" + `)
}

func (provider) ValidateRPCMuxOTelLogProfile(profile string) error {
	_, err := decodeProfile(profile)
	return err
}

func decodeProfile(raw string) (profile, error) {
	var cfg profile
	if err := rpc.DecodeRPCMuxOTelLogProfile(raw, &cfg); err != nil {
		return profile{}, err
	}
	endpoint, err := url.ParseRequestURI(cfg.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return profile{}, fmt.Errorf("endpoint must be an absolute https URL")
	}
	if cfg.BatchSize < 1 || cfg.BatchSize > 1000 {
		return profile{}, fmt.Errorf("batchSize must be between 1 and 1000")
	}
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil || timeout < time.Millisecond || timeout > 30*time.Second {
		return profile{}, fmt.Errorf("timeout must be between 1ms and 30s")
	}
	return cfg, nil
}

func (provider) NewRPCMuxOTelLogExporter(raw string) rpc.RPCMuxOTelLogExporter {
	profile, err := decodeProfile(raw)
	if err != nil {
		return nil
	}
	return rpc.RPCMuxOTelLogExporterFunc(func(ctx context.Context, record rpc.RPCMuxDiagnosisEventOTelLogRecord) {
		export(ctx, profile, record)
	})
}

func export(context.Context, profile, rpc.RPCMuxDiagnosisEventOTelLogRecord) {}

func init() {
	rpc.RegisterRPCMuxOTelLogSinkProvider(Name, provider{})
}
`

const muxOTelSinkFeatureTestTemplate = `package muxotelsink

import (
	"encoding/json"
	"testing"
)

func TestProviderProfileSchemaAndValidation(t *testing.T) {
	schema := (provider{}).RPCMuxOTelLogProfileSchema()
	if !json.Valid(schema) {
		t.Fatalf("profile schema is invalid JSON: %s", schema)
	}
	tests := []struct {
		name    string
		profile string
		wantErr bool
	}{
		{name: "valid", profile: ` + "`" + `{"endpoint":"https://telemetry.example.com/v1/logs","batchSize":64,"timeout":"2s"}` + "`" + `},
		{name: "unknown field", profile: ` + "`" + `{"endpoint":"https://telemetry.example.com/v1/logs","batchSize":64,"timeout":"2s","token":"secret"}` + "`" + `, wantErr: true},
		{name: "insecure endpoint", profile: ` + "`" + `{"endpoint":"http://telemetry.example.com/v1/logs","batchSize":64,"timeout":"2s"}` + "`" + `, wantErr: true},
		{name: "batch too large", profile: ` + "`" + `{"endpoint":"https://telemetry.example.com/v1/logs","batchSize":1001,"timeout":"2s"}` + "`" + `, wantErr: true},
		{name: "timeout too short", profile: ` + "`" + `{"endpoint":"https://telemetry.example.com/v1/logs","batchSize":64,"timeout":"1us"}` + "`" + `, wantErr: true},
		{name: "timeout too long", profile: ` + "`" + `{"endpoint":"https://telemetry.example.com/v1/logs","batchSize":64,"timeout":"31s"}` + "`" + `, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (provider{}).ValidateRPCMuxOTelLogProfile(test.profile)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateRPCMuxOTelLogProfile() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
`

const mainTemplate = `package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/config"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/proc"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
{{.FeatureImports}}

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
	muxSinkSet, err := c.RPC.Mux.Log.OTelCompatible.NewSinkSet()
	if err != nil {
		slog.Error("setup mux diagnosis sink set", "error", err)
		return
	}
	defer func() { _ = muxSinkSet.Close() }()
	rpcOptions = append(rpcOptions, c.RPC.Mux.ServerOptionsWithSinkSet(muxSinkSet)...)
	var muxServer *rpc.ExperimentalMuxServer
	if c.RPC.Mux.Enabled {
		configureMux := func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			if !c.RPC.Mux.Probe {
				return nil
			}
			return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("generated:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		}
		if c.RPC.Mux.Candidate.Enabled {
			muxServer, err = rpc.NewExperimentalMuxCandidateServer(c.RPC.Mux.Addr, configureMux, c.RPC.Mux.CandidateServerConfig())
		} else {
			muxServer, err = rpc.NewExperimentalMuxServer(c.RPC.Mux.Addr, configureMux)
		}
		if err != nil {
			slog.Error("setup mux rpc", "error", err)
			return
		}
		rpcOptions = append(rpcOptions, rpc.WithExperimentalMuxServerAdapter(muxServer))
	}
	rpcServer := rpc.NewServer(rpcOptions...)
	if err := rpcServer.RegisterService(apprpc.GreeterService(svcCtx), nil); err != nil {
		slog.Error("register rpc", "error", err)
		return
	}
	servers := []app.Server{httpServer, rpcServer}
	if muxServer != nil {
		servers = append(servers, muxServer)
	}
	if c.Admin.Enabled {
		servers = append(servers, appadmin.NewServer(c.Admin.Addr, c.Admin.PathPrefix, rpcServer, appadmin.WithControlPlaneSnapshot(func(ctx context.Context) (controlplane.Snapshot, error) {
			return c.ControlPlaneSnapshotWithDiscovery(ctx, registry)
		})))
	}
	governanceManager.StartAsync(ctx, func(err error) { slog.Warn("governance manager stopped", "error", err) })
	go func() {
		if err := config.Watch[appconfig.Config](ctx, configPath, 2*time.Second, func(next appconfig.Config) {
			diffPlan, err := next.RPC.Mux.Log.OTelCompatible.DiffSinkSet(ctx, muxSinkSet)
			if err != nil {
				slog.Warn("plan mux diagnosis sink set reload", "error", err)
				return
			}
			if err := next.RPC.Mux.Log.OTelCompatible.ReloadSinkSet(ctx, muxSinkSet); err != nil {
				slog.Warn("reload mux diagnosis sink set", "error", err)
				return
			}
			if next.RPC.Mux.Log.EventExportEnabled() {
				rpcServer.UpdateMuxDiagnosisEventExporter(muxSinkSet, next.RPC.Mux.Log.Filter())
				svcCtx.UpdateRPCMuxDiagnosisExporters(muxSinkSet, next.RPC.Mux.Log.Filter())
			} else {
				rpcServer.UpdateMuxDiagnosisEventExporter(nil, rpc.RPCMuxDiagnosisFilter{})
				svcCtx.UpdateRPCMuxDiagnosisExporters(nil, rpc.RPCMuxDiagnosisFilter{})
			}
			svcCtx.UpdateConfig(next)
			slog.Info("config reloaded", "rest_host", next.Rest.Host, "rest_port", next.Rest.Port, "rpc_addr", next.RPC.Addr, "mux_sink_diff_plan", diffPlan)
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

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/config"
	"github.com/imajinyun/gofly/core/proc"
	"github.com/imajinyun/gofly/rest"
{{.FeatureImports}}

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

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/config"
	"github.com/imajinyun/gofly/core/proc"
	"github.com/imajinyun/gofly/rest"
{{.FeatureImports}}

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
  "service": {"name": "{{.Name}}", "mode": "dev", "environment": "development", "startupTimeout": 5000000000, "shutdownTimeout": 10000000000, "log": {"level": "info", "format": "json", "trace": true}, "trace": {"enabled": true, "serviceName": "{{.Name}}", "sampleRatio": 1}, "metrics": {"enabled": true}, "profile": {"enabled": false, "addr": "127.0.0.1:6060", "pathPrefix": "/debug/pprof"}, "health": {"timeout": 1000000000}, "governance": {{.ServiceGovernanceFullJSON}}},
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
      "timeoutConfig": {{.RestTimeoutConfigJSON}},
      "breaker": true,
      "breakerConfig": {{.RestBreakerConfigJSON}},
      "rateLimit": true,
      "rateLimitConfig": {{.RestRateLimitConfigJSON}},
      "adaptiveRateLimit": true,
      "adaptiveLimitConfig": {{.RestAdaptiveLimitConfigJSON}},
      "maxConcurrency": true,
      "maxConcurrencyConfig": {{.RestMaxConcurrencyConfigJSON}},
      "securityHeaders": {"contentSecurityPolicy": "default-src 'self'", "frameOptions": "DENY", "contentTypeOptions": "nosniff", "referrerPolicy": "no-referrer", "permissionsPolicy": "geolocation=()", "hsts": "max-age=31536000; includeSubDomains"},
      "logRedaction": {"headers": ["Authorization", "Cookie", "Set-Cookie"], "queries": ["token", "access_token"]},
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
    "rules": {{.GovernanceRulesJSON}}
  },
  "rpc": {
    "addr": ":8081",
    "advertise": "http://127.0.0.1:8081",
    "mux": {"enabled": false, "probe": false, "addr": "127.0.0.1:8082", "endpoints": [], "idleTimeout": 60000000000, "maxOpenRetries": 1, "openRetryReasons": ["dial_failure", "pool_exhausted"], "healthBackoffMultiplier": 2, "healthMaxCooldown": 30000000000, "trace": {"enabled": false, "annotateStreams": false}, "log": {"enabled": false, "diagnosis": false, "exportEvents": false, "eventFamily": "", "event": "", "endpoint": "", "connectionId": "", "otelCompatible": {"enabled": false, "sink": "slog", "profile": ""}}, "tls": {"enabled": false, "certFile": "", "keyFile": "", "caFile": "", "serverName": ""}, "mtls": {"enabled": false, "clientCAFile": "", "clientCertFile": "", "clientKeyFile": ""}, "alpn": {"enabled": false, "protocol": "gofly-mux/experimental-v1"}, "candidate": {"enabled": false, "protocol": "gofly-mux/experimental-v1", "dialTimeout": 30000000000, "keepAlive": 30000000000, "handshakeTimeout": 10000000000, "keepaliveInterval": 30000000000, "keepaliveIdle": 90000000000, "writeTimeout": 0, "creditWaitTimeout": 0, "maxFrameBytes": 4194304, "maxMessageBytes": 67108864, "maxConcurrentStreams": 128, "receiveQueueSize": 16, "connectionWindow": 16, "fragmentStreamWindowUpdatePolicy": "per_fragment", "fragmentConnectionWindowUpdatePolicy": "per_fragment", "fragmentStreamWindowRefillRatio": 1, "fragmentConnectionWindowRefillRatio": 1, "fragmentMaxDeferredFragments": 0, "fragmentWindowPolicyRiskMode": "diagnose", "payloadCodec": "identity", "frameCodec": "binary", "allowLegacyDowngrade": false, "tls": {}}}
  }
}
`

const minimalConfigTemplate = `{
  "environment": "development",
  "service": {"name": "{{.Name}}", "mode": "dev", "environment": "development", "startupTimeout": 5000000000, "shutdownTimeout": 10000000000, "log": {"level": "info", "format": "json"}, "metrics": {"enabled": true}, "trace": {"enabled": true, "sampler": "always_on"}, "health": {"timeout": 1000000000}, "governance": {{.ServiceGovernanceMinimalJSON}}},
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
      "timeout": true,
      "timeoutConfig": {{.RestTimeoutConfigJSON}},
      "breaker": true,
      "breakerConfig": {{.RestBreakerConfigJSON}},
      "rateLimit": true,
      "rateLimitConfig": {{.RestRateLimitConfigJSON}},
      "adaptiveRateLimit": true,
      "adaptiveLimitConfig": {{.RestAdaptiveLimitConfigJSON}},
      "maxConcurrency": true,
      "maxConcurrencyConfig": {{.RestMaxConcurrencyConfigJSON}},
      "metrics": true,
      "health": true,
      "requestId": true
    }
  }
}
`

const governanceTemplate = `{{.GovernanceRulesJSON}}
`

const discoveryRegistryTemplate = `package discovery

import (
	"context"
	"fmt"
	"os"
	"strings"

	corediscovery "github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/discovery/consul"
	"github.com/imajinyun/gofly/core/discovery/etcdv3"

	appconfig "{{.Module}}/internal/config"
)

type closeFunc func(context.Context) error

func NewRegistry(ctx context.Context, cfg appconfig.DiscoveryConfig) (corediscovery.Registry, closeFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	switch cfg.ProviderName() {
	case "memory":
		return corediscovery.NewMemoryRegistry(), noopClose, nil
	case "consul":
		registry, err := consul.New(consul.Config{Address: cfg.Address, Token: envValue(cfg.TokenEnv), TTL: cfg.RegistryTTL()})
		if err != nil {
			return nil, nil, fmt.Errorf("create consul discovery registry: %w", err)
		}
		return registry, registry.Close, nil
	case "etcdv3":
		registry, err := etcdv3.New(etcdv3.Config{
			Endpoints:   cfg.ResolvedEndpoints(),
			Prefix:      cfg.Prefix,
			DialTimeout: cfg.DialTimeoutDuration(),
			TTL:         cfg.RegistryTTL(),
			Username:    envValue(cfg.UsernameEnv),
			Password:    envValue(cfg.PasswordEnv),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("create etcdv3 discovery registry: %w", err)
		}
		return registry, registry.Close, nil
	default:
		return nil, nil, fmt.Errorf("unsupported discovery provider %q", cfg.Provider)
	}
}

func noopClose(context.Context) error { return nil }

func envValue(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return os.Getenv(name)
}
`

const adminServerTemplate = `package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/rpc"
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
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/rpc"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	appconfig "{{.Module}}/internal/config"
	apprpc "{{.Module}}/internal/rpc"
	"{{.Module}}/internal/svc"
)

func TestAdminDiagnostics(t *testing.T) {
	cfg := appconfig.Config{RPC: appconfig.RPCConfig{Mux: appconfig.RPCMuxConfig{Enabled: true, Probe: true, IdleTimeout: time.Nanosecond, MaxOpenRetries: 1, OpenRetryReasons: []string{"dial_failure", "pool_exhausted"}, HealthBackoffMultiplier: 2, HealthMaxCooldown: 30 * time.Second, Trace: appconfig.RPCMuxTraceConfig{Enabled: true, AnnotateStreams: true}, Log: appconfig.RPCMuxLogConfig{Enabled: true, Diagnosis: true, ExportEvents: true, EventFamily: "retry", Event: "open-before-retry"}, Candidate: appconfig.RPCMuxCandidateConfig{Enabled: true, Protocol: "gofly-mux/generated-candidate-test", KeepaliveInterval: time.Hour, KeepaliveIdle: 2 * time.Hour, MaxFrameBytes: 256, MaxMessageBytes: 1024, MaxConcurrentStreams: 8, ReceiveQueueSize: 2, ConnectionWindow: 3, FragmentStreamWindowUpdatePolicy: "on_receive", FragmentConnectionWindowUpdatePolicy: "on_receive", FragmentStreamWindowRefillRatio: 0.5, FragmentConnectionWindowRefillRatio: 0.25, FragmentMaxDeferredFragments: 2, FragmentWindowPolicyRiskMode: "warn", PayloadCodec: "identity", FrameCodec: "binary"}}}}
	clientConn, serverConn := net.Pipe()
	muxClient := rpc.NewExperimentalMuxClientAdapter(clientConn)
	muxServer := rpc.NewExperimentalMuxServerAdapter(serverConn)
	defer muxClient.Close()
	defer muxServer.Close()
	if err := muxServer.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("generated:"), msg.Payload...)}); err != nil {
			return err
		}
		return stream.Close(ctx, "ok")
	}); err != nil {
		t.Fatal(err)
	}
	muxCtx, stopMux := context.WithCancel(context.Background())
	defer stopMux()
	muxDone := make(chan error, 1)
	go func() {
		muxDone <- muxServer.Serve(muxCtx)
	}()
	muxStream, err := muxClient.OpenStream(context.Background(), "greeter/Watch")
	if err != nil {
		t.Fatal(err)
	}
	if err := muxStream.Send(context.Background(), rpc.Message{Payload: []byte("probe")}); err != nil {
		t.Fatal(err)
	}
	got, err := muxStream.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Payload) != "generated:probe" {
		t.Fatalf("mux payload = %q, want generated probe response", got.Payload)
	}
	if _, err := muxStream.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("mux terminal receive = %v, want EOF", err)
	}

	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.RPC.Mux.Endpoints = []string{"tcp://"+tcpListener.Addr().String()}
	tcpCtx, stopTCP := context.WithCancel(context.Background())
	defer stopTCP()
	tcpDone := make(chan error, 1)
	go func() {
		tcpDone <- rpc.ServeExperimentalMuxListener(tcpCtx, tcpListener, func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("manager:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		})
	}()
	manager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver(cfg.RPC.Mux.Endpoints...),
		rpc.WithExperimentalMuxConnectionManagerIdleTimeout(cfg.RPC.Mux.IdleTimeout),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(cfg.RPC.Mux.MaxOpenRetries),
		rpc.WithExperimentalMuxConnectionManagerOpenRetryReasons(cfg.RPC.Mux.OpenRetryReasons...),
	)
	if err != nil {
		t.Fatal(err)
	}
	muxRPCClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer muxRPCClient.Close()
	managerStream, err := muxRPCClient.MuxStream(context.Background(), "greeter/Watch")
	if err != nil {
		t.Fatal(err)
	}
	if err := managerStream.Send(context.Background(), rpc.Message{Payload: []byte("probe")}); err != nil {
		t.Fatal(err)
	}
	managerResponse, err := managerStream.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(managerResponse.Payload) != "manager:probe" {
		t.Fatalf("manager mux payload = %q, want manager probe response", managerResponse.Payload)
	}
	if _, err := managerStream.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("manager mux terminal receive = %v, want EOF", err)
	}
	if diagnosis := muxRPCClient.RuntimeSnapshot().Diagnosis.Mux.Manager; !diagnosis.Enabled || len(diagnosis.Endpoints) != 1 {
		t.Fatalf("manager diagnosis = %+v, want generated mux manager evidence", diagnosis)
	}
	time.Sleep(time.Millisecond)
	if err := manager.CloseIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopTCP()
	if err := <-tcpDone; err != nil {
		t.Fatalf("tcp mux server stopped with error: %v", err)
	}

	candidateListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	candidateCtx, stopCandidate := context.WithCancel(context.Background())
	defer stopCandidate()
	candidateDone := make(chan error, 1)
	candidateServerCfg := cfg.RPC.Mux.CandidateServerConfig()
	candidateClientCfg := cfg.RPC.Mux.CandidateClientConfig()
	go func() {
		candidateDone <- rpc.ServeExperimentalMuxCandidateListener(candidateCtx, candidateListener, func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("candidate:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		}, candidateServerCfg)
	}()
	candidateManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver("tcp://"+candidateListener.Addr().String()),
		rpc.WithExperimentalMuxConnectionManagerCandidateConfig(candidateClientCfg),
	)
	if err != nil {
		t.Fatal(err)
	}
	candidateClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxConnectionManager(candidateManager))
	if err != nil {
		t.Fatal(err)
	}
	defer candidateClient.Close()
	candidateStream, err := candidateClient.MuxStream(context.Background(), "greeter/Watch")
	if err != nil {
		t.Fatal(err)
	}
	if err := candidateStream.Send(context.Background(), rpc.Message{Payload: []byte("probe")}); err != nil {
		t.Fatal(err)
	}
	candidateResponse, err := candidateStream.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(candidateResponse.Payload) != "candidate:probe" {
		t.Fatalf("candidate mux payload = %q, want candidate probe response", candidateResponse.Payload)
	}
	if _, err := candidateStream.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("candidate mux terminal receive = %v, want EOF", err)
	}
	if diagnosis := candidateClient.RuntimeSnapshot().Diagnosis.Mux.Manager; !diagnosis.Candidate.Enabled || diagnosis.Candidate.Protocol != "gofly-mux/generated-candidate-test" || diagnosis.Candidate.FrameCodec != "binary" || diagnosis.Candidate.FragmentStreamWindowUpdatePolicy != "on_receive" || diagnosis.Candidate.FragmentConnectionWindowUpdatePolicy != "on_receive" || diagnosis.Candidate.FragmentStreamWindowRefillRatio != 0.5 || diagnosis.Candidate.FragmentConnectionWindowRefillRatio != 0.25 || diagnosis.Candidate.FragmentMaxDeferredFragments != 2 || diagnosis.Candidate.FragmentWindowPolicyRiskMode != "warn" || !diagnosis.Candidate.FragmentWindowPolicyRiskWarning || !diagnosis.Candidate.FragmentWindowPolicyRisk || diagnosis.Candidate.FragmentEstimatedMaxFragments <= diagnosis.Candidate.ConnectionWindow || len(diagnosis.Endpoints) != 1 || !diagnosis.Endpoints[0].Adapter.Candidate.Enabled || diagnosis.Endpoints[0].Adapter.Transport.ConnectionWindow != 3 || diagnosis.Endpoints[0].Adapter.Transport.FragmentStreamWindowUpdatePolicy != "on_receive" || diagnosis.Endpoints[0].Adapter.Transport.FragmentConnectionWindowUpdatePolicy != "on_receive" || diagnosis.Endpoints[0].Adapter.Transport.FragmentStreamWindowRefillRatio != 0.5 || diagnosis.Endpoints[0].Adapter.Transport.FragmentConnectionWindowRefillRatio != 0.25 || diagnosis.Endpoints[0].Adapter.Transport.FragmentMaxDeferredFragments != 2 || diagnosis.Endpoints[0].Adapter.Transport.FragmentWindowPolicyRiskMode != "warn" || !diagnosis.Endpoints[0].Adapter.Transport.FragmentWindowPolicyRisk || diagnosis.Endpoints[0].Adapter.Transport.FragmentWindowPolicyRiskReason == "" {
		t.Fatalf("candidate manager diagnosis = %+v, want generated candidate mux evidence", diagnosis)
	}
	if err := candidateManager.Drain(context.Background(), "generated_shutdown"); err != nil {
		t.Fatal(err)
	}
	if diagnosis := candidateClient.RuntimeSnapshot().Diagnosis.Mux.Manager; len(diagnosis.Endpoints) != 1 || diagnosis.Endpoints[0].Adapter.Transport.GoAwayFramesOut != 1 {
		t.Fatalf("candidate drain diagnosis = %+v, want generated candidate GOAWAY evidence", diagnosis)
	}
	stopCandidate()
	if err := <-candidateDone; err != nil {
		t.Fatalf("candidate mux server stopped with error: %v", err)
	}

	negotiationListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	negotiationCtx, stopNegotiation := context.WithCancel(context.Background())
	defer stopNegotiation()
	negotiationDone := make(chan error, 1)
	negotiationServerCfg := cfg.RPC.Mux.CandidateServerConfig()
	negotiationServerCfg.FrameCodec = "json"
	go func() {
		negotiationDone <- rpc.ServeExperimentalMuxCandidateListener(negotiationCtx, negotiationListener, func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				return stream.Close(ctx, "unexpected")
			})
		}, negotiationServerCfg)
	}()
	negotiationManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver("tcp://"+negotiationListener.Addr().String()),
		rpc.WithExperimentalMuxConnectionManagerCandidateConfig(cfg.RPC.Mux.CandidateClientConfig()),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	negotiationClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxConnectionManager(negotiationManager))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := negotiationClient.MuxStream(context.Background(), "greeter/Watch"); err == nil || !strings.Contains(err.Error(), "frame codec") {
		t.Fatalf("negotiation mux stream = %v, want frame policy mismatch", err)
	}
	negotiationRec := httptest.NewRecorder()
	negotiationClient.DiagnosisHandler().ServeHTTP(negotiationRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?eventFamily=negotiation&event=frame-policy-mismatch", nil))
	if negotiationRec.Code != http.StatusOK {
		t.Fatalf("negotiation diagnosis status = %d body=%q", negotiationRec.Code, negotiationRec.Body.String())
	}
	var negotiationDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(negotiationRec.Body).Decode(&negotiationDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !negotiationDiagnosis.Matched ||
		negotiationDiagnosis.Diagnosis.Mux.Negotiation.Failures != 1 ||
		negotiationDiagnosis.Diagnosis.Mux.Negotiation.FramePolicyMismatch != 1 ||
		negotiationDiagnosis.Diagnosis.Mux.Negotiation.LastEvent != "frame_policy_mismatch" ||
		len(negotiationDiagnosis.Diagnosis.Mux.Events) != 1 ||
		negotiationDiagnosis.Diagnosis.Mux.Events[0].Event != "frame_policy_mismatch" {
		t.Fatalf("negotiation diagnosis = %+v, want generated frame policy summary evidence", negotiationDiagnosis)
	}
	if err := negotiationClient.Close(); err != nil {
		t.Fatal(err)
	}
	stopNegotiation()
	if err := <-negotiationDone; err != nil {
		t.Fatalf("negotiation mux server stopped with error: %v", err)
	}

	tlsDir := t.TempDir()
	tlsCA, tlsCAKey := generatedRPCTLSCA(t, tlsDir)
	tlsCAFile := filepath.Join(tlsDir, "ca.crt")
	tlsServerCert, tlsServerKey := generatedRPCTLSLeaf(t, tlsDir, "server", tlsCA, tlsCAKey)
	tlsClientCert, tlsClientKey := generatedRPCTLSLeaf(t, tlsDir, "client", tlsCA, tlsCAKey)
	tlsCfg := cfg
	tlsCfg.RPC.Mux.TLS = appconfig.RPCMuxTLSConfig{
		Enabled:    true,
		CertFile:   tlsServerCert,
		KeyFile:    tlsServerKey,
		CAFile:     tlsCAFile,
		ServerName: "svc",
	}
	tlsCfg.RPC.Mux.MutualTLS = appconfig.RPCMuxMutualTLSConfig{
		Enabled:        true,
		ClientCAFile:   tlsCAFile,
		ClientCertFile: tlsClientCert,
		ClientKeyFile:  tlsClientKey,
	}
	tlsCfg.RPC.Mux.ALPN = appconfig.RPCMuxALPNConfig{Enabled: true, Protocol: "gofly-mux/generated-mtls-test"}
	tlsCfg.RPC.Mux.Trace = appconfig.RPCMuxTraceConfig{Enabled: true, AnnotateStreams: true}
	tlsCfg.RPC.Mux.Log = appconfig.RPCMuxLogConfig{Enabled: true, Diagnosis: true, ExportEvents: true, EventFamily: "flow-control", Event: "fragment-window-refill"}
	tlsCfg.RPC.Mux.Log.OTelCompatible = appconfig.RPCMuxOTelCompatibleLogConfig{Enabled: true, Sink: "slog", Profile: "generated-mtls-refill"}
	tlsCandidate := tlsCfg.RPC.Mux.CandidateClientConfig()
	if tlsCandidate.Protocol != "gofly-mux/generated-mtls-test" ||
		tlsCandidate.TLS.CAFile != tlsCAFile ||
		tlsCandidate.TLS.CertFile != tlsClientCert ||
		tlsCandidate.TLS.KeyFile != tlsClientKey ||
		tlsCandidate.TLS.ServerName != "svc" {
		t.Fatalf("generated mux client TLS config = %+v, want client mTLS + ALPN profile", tlsCandidate)
	}
	tlsServerCandidate := tlsCfg.RPC.Mux.CandidateServerConfig()
	if tlsServerCandidate.Protocol != "gofly-mux/generated-mtls-test" ||
		tlsServerCandidate.TLS.CertFile != tlsServerCert ||
		tlsServerCandidate.TLS.KeyFile != tlsServerKey ||
		tlsServerCandidate.TLS.ClientCAFile != tlsCAFile {
		t.Fatalf("generated mux server TLS config = %+v, want server mTLS + ALPN profile", tlsServerCandidate)
	}
	mtlsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mtlsCtx, stopMTLS := context.WithCancel(context.Background())
	defer stopMTLS()
	mtlsDone := make(chan error, 1)
	go func() {
		mtlsDone <- rpc.ServeExperimentalMuxCandidateListener(mtlsCtx, mtlsListener, func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("mtls:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		}, tlsServerCandidate)
	}()
	mtlsManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver("tcp://"+mtlsListener.Addr().String()),
		rpc.WithExperimentalMuxConnectionManagerCandidateConfig(tlsCandidate),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	mtlsRecorder := tracetest.NewSpanRecorder()
	mtlsProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(mtlsRecorder))
	defer func() { _ = mtlsProvider.Shutdown(context.Background()) }()
	mtlsTraceCtx, mtlsSpan := mtlsProvider.Tracer("generated-rpc-admin-smoke").Start(context.Background(), "mux-mtls-success", oteltrace.WithSpanKind(oteltrace.SpanKindClient))
	var mtlsLogBuf bytes.Buffer
	previousMTLSLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&mtlsLogBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousMTLSLogger)
	mtlsClientOptions := append(tlsCfg.RPC.Mux.ClientOptions(),
		rpc.WithExperimentalMuxConnectionManager(mtlsManager),
	)
	mtlsClient, err := rpc.NewClient("http://unused", mtlsClientOptions...)
	if err != nil {
		t.Fatal(err)
	}
	mtlsStream, err := mtlsClient.MuxStream(mtlsTraceCtx, "greeter/Watch")
	if err != nil {
		t.Fatal(err)
	}
	mtlsSpan.End()
	mtlsPayload := []byte(strings.Repeat("probe-", 50))
	mtlsIOCtx, cancelMTLSIO := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelMTLSIO()
	if err := mtlsStream.Send(mtlsIOCtx, rpc.Message{Payload: mtlsPayload}); err != nil {
		t.Fatal(err)
	}
	mtlsResponse, err := mtlsStream.Receive(mtlsIOCtx)
	if err != nil {
		t.Fatal(err)
	}
	if string(mtlsResponse.Payload) != "mtls:"+string(mtlsPayload) {
		t.Fatalf("mTLS mux payload = %q, want mtls probe response", mtlsResponse.Payload)
	}
	if _, err := mtlsStream.Receive(mtlsIOCtx); !errors.Is(err, io.EOF) {
		t.Fatalf("mTLS mux terminal receive = %v, want EOF", err)
	}
	mtlsRec := httptest.NewRecorder()
	mtlsClient.DiagnosisHandler().ServeHTTP(mtlsRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis", nil))
	if mtlsRec.Code != http.StatusOK {
		t.Fatalf("mTLS diagnosis status = %d body=%q", mtlsRec.Code, mtlsRec.Body.String())
	}
	var mtlsDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(mtlsRec.Body).Decode(&mtlsDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.TLS ||
		!mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.MutualTLS ||
		mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.NegotiatedProtocol != "gofly-mux/generated-mtls-test" ||
		len(mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints) != 1 ||
		!mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Candidate.TLS ||
		!mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Candidate.MutualTLS ||
		mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Candidate.NegotiatedProtocol != "gofly-mux/generated-mtls-test" ||
		mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.OpenedStreams != 1 ||
		mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.ClosedStreams != 1 ||
		mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.ActiveStreams != 0 {
		t.Fatalf("mTLS diagnosis = %+v, want generated TLS/mTLS negotiated lifecycle evidence", mtlsDiagnosis.Diagnosis.Mux.Manager)
	}
	refillRec := httptest.NewRecorder()
	mtlsClient.DiagnosisHandler().ServeHTTP(refillRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?flowControlEvent=fragment-window-refill&eventFamily=flow-control&event=fragment-window-refill", nil))
	if refillRec.Code != http.StatusOK {
		t.Fatalf("mTLS refill diagnosis status = %d body=%q", refillRec.Code, refillRec.Body.String())
	}
	var refillDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(refillRec.Body).Decode(&refillDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !refillDiagnosis.Matched ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.Refills < 1 ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.StreamWindowRefillRatio != 0.5 ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.ConnectionWindowRefillRatio != 0.25 ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.MaxDeferredFragments != 2 ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.LastFlowControlEvent != "fragment_window_refill" ||
		len(refillDiagnosis.Diagnosis.Mux.Manager.RefillProfiles) != 1 ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfiles[0].ConnectionID == "" ||
		len(refillDiagnosis.Diagnosis.Mux.Events) == 0 ||
		refillDiagnosis.Diagnosis.Mux.Events[0].Event != "fragment_window_refill" {
		t.Fatalf("mTLS refill diagnosis = %+v, want generated refillProfile admin evidence", refillDiagnosis.Diagnosis.Mux.Manager)
	}
	mtlsRefillTraceCtx, mtlsRefillSpan := mtlsProvider.Tracer("generated-rpc-admin-smoke").Start(context.Background(), "mux-refill-profile-diagnosis", oteltrace.WithSpanKind(oteltrace.SpanKindInternal))
	mtlsClient.ObserveMuxDiagnosis(mtlsRefillTraceCtx, refillDiagnosis)
	mtlsRefillSpan.End()
	if err := mtlsClient.Close(); err != nil {
		t.Fatal(err)
	}
	mtlsTraceSpans := mtlsRecorder.Ended()
	if len(mtlsTraceSpans) != 2 {
		t.Fatalf("mTLS trace spans = %d, want generated mux mTLS and refillProfile spans", len(mtlsTraceSpans))
	}
	mtlsTraceAttrs := generatedTraceAttributeMap(mtlsTraceSpans[0].Attributes())
	if !mtlsTraceAttrs["rpc.mux.candidate.tls"].AsBool() ||
		!mtlsTraceAttrs["rpc.mux.candidate.mutual_tls"].AsBool() ||
		mtlsTraceAttrs["rpc.mux.candidate.negotiated_protocol"].AsString() != "gofly-mux/generated-mtls-test" ||
		mtlsTraceAttrs["rpc.mux.candidate.protocol"].AsString() != "gofly-mux/generated-mtls-test" {
		t.Fatalf("mTLS trace attributes = %+v, want generated TLS/mTLS negotiated protocol", mtlsTraceAttrs)
	}
	mtlsRefillTraceAttrs := generatedTraceAttributeMap(mtlsTraceSpans[1].Attributes())
	if mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.refills.count"].AsInt64() < 1 ||
		mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.stream_window_refill_ratio"].AsFloat64() != 0.5 ||
		mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.connection_window_refill_ratio"].AsFloat64() != 0.25 ||
		mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.max_deferred_fragments"].AsInt64() != 2 ||
		mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.last_flow_control_event"].AsString() != "fragment_window_refill" ||
		mtlsRefillTraceAttrs["rpc.mux.event.flow_control.count"].AsInt64() < 1 {
		t.Fatalf("mTLS refill trace attributes = %+v, want generated refillProfile OTel attributes", mtlsRefillTraceAttrs)
	}
	mtlsLogLine := mtlsLogBuf.String()
	for _, want := range []string{
		"\"msg\":\"rpc mux stream diagnosis\"",
		"\"tls\":true",
		"\"mutual_tls\":true",
		"\"negotiated_protocol\":\"gofly-mux/generated-mtls-test\"",
		"\"candidate_protocol\":\"gofly-mux/generated-mtls-test\"",
		"\"refill_profile_stream_window_refill_ratio\":0.5",
		"\"refill_profile_connection_window_refill_ratio\":0.25",
		"\"refill_profile_max_deferred_fragments\":2",
		"\"refill_profile_last_flow_control_event\":\"fragment_window_refill\"",
		"\"msg\":\"rpc mux runtime event\"",
		"\"event_family\":\"flow_control\"",
		"\"event\":\"fragment_window_refill\"",
		"\"connection_id\":\"",
		"\"pool_slot\":1",
		"\"msg\":\"rpc mux otel log event\"",
		"\"otel_log_name\":\"rpc.mux.diagnosis_event\"",
		"\"otel_log_severity\":\"WARN\"",
		"\"otel_log_profile\":\"generated-mtls-refill\"",
		"\"rpc_mux_event_family\":\"flow_control\"",
		"\"rpc_mux_event_name\":\"fragment_window_refill\"",
		"\"rpc_mux_connection_id\":\"",
		"\"rpc_mux_pool_slot\":1",
	} {
		if !strings.Contains(mtlsLogLine, want) {
			t.Fatalf("mTLS mux diagnosis log missing %s:\n%s", want, mtlsLogLine)
		}
	}
	if err := mtlsManager.Close(); err != nil {
		t.Fatal(err)
	}
	stopMTLS()
	if err := <-mtlsDone; err != nil {
		t.Fatalf("mTLS mux server stopped with error: %v", err)
	}

	tlsServer, err := rpc.NewExperimentalMuxCandidateServer("127.0.0.1:0", func(adapter *rpc.ExperimentalMuxServerAdapter) error {
		return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
			return stream.Close(ctx, "unexpected")
		})
	}, tlsServerCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := tlsServer.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tlsServer.Shutdown(context.Background()) }()
	tlsNoClientCert := tlsCandidate
	tlsNoClientCert.TLS.CertFile = ""
	tlsNoClientCert.TLS.KeyFile = ""
	tlsFailureManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver("tcp://"+tlsServer.Addr()),
		rpc.WithExperimentalMuxConnectionManagerCandidateConfig(tlsNoClientCert),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	tlsFailureClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxConnectionManager(tlsFailureManager))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tlsFailureClient.MuxStream(context.Background(), "greeter/Watch"); err == nil {
		t.Fatal("generated mux TLS stream without client certificate succeeded, want tls_failure")
	}
	tlsFailureRec := httptest.NewRecorder()
	tlsFailureClient.DiagnosisHandler().ServeHTTP(tlsFailureRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?eventFamily=negotiation&event=tls-failure", nil))
	if tlsFailureRec.Code != http.StatusOK {
		t.Fatalf("TLS failure diagnosis status = %d body=%q", tlsFailureRec.Code, tlsFailureRec.Body.String())
	}
	var tlsFailureDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(tlsFailureRec.Body).Decode(&tlsFailureDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !tlsFailureDiagnosis.Matched ||
		tlsFailureDiagnosis.Diagnosis.Mux.Negotiation.TLSFailure != 1 ||
		tlsFailureDiagnosis.Diagnosis.Mux.Negotiation.LastEvent != "tls_failure" ||
		len(tlsFailureDiagnosis.Diagnosis.Mux.Events) != 1 ||
		tlsFailureDiagnosis.Diagnosis.Mux.Events[0].Event != "tls_failure" {
		t.Fatalf("TLS failure diagnosis = %+v, want generated tls_failure admin evidence", tlsFailureDiagnosis)
	}
	if err := tlsFailureClient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tlsFailureManager.Close(); err != nil {
		t.Fatal(err)
	}

	alpnTLSCfg, err := (tlsCfg.RPC.Mux.CandidateServerConfig().TLS).ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	alpnListener, err := tls.Listen("tcp", "127.0.0.1:0", alpnTLSCfg)
	if err != nil {
		t.Fatal(err)
	}
	alpnCtx, stopALPN := context.WithCancel(context.Background())
	defer stopALPN()
	alpnDone := make(chan error, 1)
	go func() {
		for {
			conn, err := alpnListener.Accept()
			if err != nil {
				select {
				case <-alpnCtx.Done():
					alpnDone <- nil
				default:
					alpnDone <- err
				}
				return
			}
			if tlsConn, ok := conn.(*tls.Conn); ok {
				_ = tlsConn.Handshake()
			}
			_ = conn.Close()
		}
	}()
	alpnManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver("tcp://"+alpnListener.Addr().String()),
		rpc.WithExperimentalMuxConnectionManagerCandidateConfig(tlsCandidate),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	alpnClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxConnectionManager(alpnManager))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alpnClient.MuxStream(context.Background(), "greeter/Watch"); err == nil || !strings.Contains(err.Error(), "negotiated protocol") {
		t.Fatalf("generated mux ALPN stream = %v, want alpn_mismatch", err)
	}
	alpnRec := httptest.NewRecorder()
	alpnClient.DiagnosisHandler().ServeHTTP(alpnRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?eventFamily=negotiation&event=alpn-mismatch", nil))
	if alpnRec.Code != http.StatusOK {
		t.Fatalf("ALPN diagnosis status = %d body=%q", alpnRec.Code, alpnRec.Body.String())
	}
	var alpnDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(alpnRec.Body).Decode(&alpnDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !alpnDiagnosis.Matched ||
		alpnDiagnosis.Diagnosis.Mux.Negotiation.ALPNMismatch != 1 ||
		alpnDiagnosis.Diagnosis.Mux.Negotiation.LastEvent != "alpn_mismatch" ||
		len(alpnDiagnosis.Diagnosis.Mux.Events) != 1 ||
		alpnDiagnosis.Diagnosis.Mux.Events[0].Event != "alpn_mismatch" {
		t.Fatalf("ALPN diagnosis = %+v, want generated alpn_mismatch admin evidence", alpnDiagnosis)
	}
	if err := alpnClient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := alpnManager.Close(); err != nil {
		t.Fatal(err)
	}
	stopALPN()
	_ = alpnListener.Close()
	if err := <-alpnDone; err != nil {
		t.Fatalf("ALPN listener stopped with error: %v", err)
	}

	badMuxListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	badMuxEndpoint := "tcp://" + badMuxListener.Addr().String()
	if err := badMuxListener.Close(); err != nil {
		t.Fatal(err)
	}
	retryMuxListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	retryCtx, stopRetryMux := context.WithCancel(context.Background())
	defer stopRetryMux()
	retryDone := make(chan error, 1)
	go func() {
		retryDone <- rpc.ServeExperimentalMuxListener(retryCtx, retryMuxListener, func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			if err := adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("retry:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			}); err != nil {
				return err
			}
			return adapter.RegisterStream("greeter/FailAfterOpen", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				if _, err := stream.Receive(ctx); err != nil {
					return err
				}
				return rpc.NewError(rpc.CodeUnavailable, "generated stream failed after open")
			})
		})
	}()
	retryManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver(badMuxEndpoint, "tcp://"+retryMuxListener.Addr().String()),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(cfg.RPC.Mux.MaxOpenRetries),
		rpc.WithExperimentalMuxConnectionManagerOpenRetryReasons(cfg.RPC.Mux.OpenRetryReasons...),
		rpc.WithExperimentalMuxConnectionManagerHealthBackoffMultiplier(cfg.RPC.Mux.HealthBackoffMultiplier),
		rpc.WithExperimentalMuxConnectionManagerHealthMaxCooldown(cfg.RPC.Mux.HealthMaxCooldown),
		rpc.WithExperimentalMuxConnectionManagerHealthFailureThreshold(1),
		rpc.WithExperimentalMuxConnectionManagerHealthEjectionDuration(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	retryClientOptions := append(cfg.RPC.Mux.ClientOptions(), rpc.WithExperimentalMuxConnectionManager(retryManager))
	retryClient, err := rpc.NewClient("http://unused", retryClientOptions...)
	if err != nil {
		t.Fatal(err)
	}
	defer retryClient.Close()
	runtimeRecorder := tracetest.NewSpanRecorder()
	runtimeProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(runtimeRecorder))
	defer func() { _ = runtimeProvider.Shutdown(context.Background()) }()
	runtimeTraceCtx, runtimeSpan := runtimeProvider.Tracer("generated-rpc-admin-smoke").Start(context.Background(), "mux-runtime-open-before-retry", oteltrace.WithSpanKind(oteltrace.SpanKindClient))
	var runtimeLogBuf bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&runtimeLogBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)
	retryStream, err := retryClient.MuxStream(runtimeTraceCtx, "greeter/Watch")
	if err != nil {
		t.Fatal(err)
	}
	runtimeSpan.End()
	if err := retryStream.Send(context.Background(), rpc.Message{Payload: []byte("probe")}); err != nil {
		t.Fatal(err)
	}
	retryResponse, err := retryStream.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(retryResponse.Payload) != "retry:probe" {
		t.Fatalf("retry mux payload = %q, want retry probe response", retryResponse.Payload)
	}
	if _, err := retryStream.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("retry mux terminal receive = %v, want EOF", err)
	}
	if diagnosis := retryClient.RuntimeSnapshot().Diagnosis.Mux.Manager; diagnosis.OpenRetries != 1 || diagnosis.LastRetriedFrom != badMuxEndpoint || diagnosis.RetryReasons["dial_failure"] != 1 || diagnosis.HealthBackoffMultiplier != 2 || diagnosis.HealthMaxCooldown != 30*time.Second {
		t.Fatalf("retry manager diagnosis = %+v, want generated retry policy evidence", diagnosis)
	}
	if events := retryClient.RuntimeSnapshot().Diagnosis.Mux.Events; len(events) < 2 {
		t.Fatalf("retry mux diagnosis events = %+v, want generated retry/health event evidence", events)
	}
	runtimeTraceSpans := runtimeRecorder.Ended()
	if len(runtimeTraceSpans) != 1 {
		t.Fatalf("runtime trace spans = %d, want generated mux runtime span", len(runtimeTraceSpans))
	}
	runtimeTraceAttrs := generatedTraceAttributeMap(runtimeTraceSpans[0].Attributes())
	if runtimeTraceAttrs["rpc.mux.manager.open_retries.count"].AsInt64() != 1 ||
		runtimeTraceAttrs["rpc.mux.manager.last_retried_from"].AsString() != badMuxEndpoint ||
		runtimeTraceAttrs["rpc.mux.manager.retry_reason.dial_failure.count"].AsInt64() != 1 ||
		runtimeTraceAttrs["rpc.mux.manager.health.reason"].AsString() != "dial_failure" {
		t.Fatalf("runtime mux trace attributes = %+v, want generated open-before retry attributes", runtimeTraceAttrs)
	}
	runtimeLogLine := runtimeLogBuf.String()
	for _, want := range []string{
		"\"msg\":\"rpc mux stream diagnosis\"",
		"\"last_retried_from\":\"" + badMuxEndpoint + "\"",
		"\"health_reason\":\"dial_failure\"",
		"\"msg\":\"rpc mux runtime event\"",
		"\"event_family\":\"retry\"",
		"\"event\":\"open_before_retry\"",
		"\"msg\":\"rpc mux exported event\"",
	} {
		if !strings.Contains(runtimeLogLine, want) {
			t.Fatalf("runtime mux diagnosis log missing %s:\n%s", want, runtimeLogLine)
		}
	}
	failStream, err := retryClient.MuxStream(context.Background(), "greeter/FailAfterOpen")
	if err != nil {
		t.Fatal(err)
	}
	if err := failStream.Send(context.Background(), rpc.Message{Payload: []byte("probe")}); err != nil {
		t.Fatal(err)
	}
	if _, err := failStream.Receive(context.Background()); rpc.CodeOf(err) != rpc.CodeUnavailable {
		t.Fatalf("fail-after-open receive = %v, want CodeUnavailable", err)
	}
	if diagnosis := retryClient.RuntimeSnapshot().Diagnosis.Mux.Manager; diagnosis.OpenRetries != 1 || diagnosis.RetryReasons["open_stream"] != 0 {
		t.Fatalf("fail-after-open diagnosis = %+v, want no post-open retry replay", diagnosis)
	}
	stopRetryMux()
	if err := <-retryDone; err != nil {
		t.Fatalf("retry mux server stopped with error: %v", err)
	}

	flowClientConn, flowServerConn := net.Pipe()
	flowCfg := cfg.RPC.Mux.CandidateConfig()
	flowCfg.Protocol = "gofly-mux/generated-flow-control-test"
	flowCfg.ConnectionWindow = 1
	flowCfg.ReceiveQueueSize = 2
	flowCfg.CreditWaitTimeout = time.Millisecond
	flowClient := rpc.NewExperimentalMuxCandidateClientAdapter(flowClientConn, flowCfg)
	flowServer := rpc.NewExperimentalMuxCandidateServerAdapter(flowServerConn, flowCfg)
	defer flowClient.Close()
	defer flowServer.Close()
	holdFlow := make(chan struct{})
	if err := flowServer.RegisterStream("greeter/Hold", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
		select {
		case <-holdFlow:
		case <-ctx.Done():
			return ctx.Err()
		}
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		return stream.Close(ctx, string(msg.Payload))
	}); err != nil {
		t.Fatal(err)
	}
	flowCtx, stopFlow := context.WithCancel(context.Background())
	defer stopFlow()
	flowDone := make(chan error, 1)
	go func() {
		flowDone <- flowServer.Serve(flowCtx)
	}()
	flowStream, err := flowClient.OpenStream(context.Background(), "greeter/Hold")
	if err != nil {
		t.Fatal(err)
	}
	if err := flowStream.Send(context.Background(), rpc.Message{Payload: []byte("first")}); err != nil {
		t.Fatal(err)
	}
	if err := flowStream.Send(context.Background(), rpc.Message{Payload: []byte("second")}); rpc.CodeOf(err) != rpc.CodeDeadlineExceeded {
		t.Fatalf("flow-control send = %v, want CodeDeadlineExceeded", err)
	}
	if diagnosis := flowClient.DiagnosisSnapshot().FlowControl; diagnosis.CreditWaitTimeouts != 1 || diagnosis.ConnectionWindowExhausted < 1 {
		t.Fatalf("flow-control diagnosis = %+v, want credit timeout and window exhaustion", diagnosis)
	}
	close(holdFlow)
	stopFlow()
	if err := <-flowDone; err != nil {
		t.Fatalf("flow-control mux server stopped with error: %v", err)
	}

	writeTimeoutCfg := cfg.RPC.Mux.CandidateConfig()
	writeTimeoutCfg.Protocol = "gofly-mux/generated-write-timeout-test"
	writeTimeoutCfg.WriteTimeout = time.Millisecond
	writeTimeoutClient := rpc.NewExperimentalMuxCandidateClientAdapter(&generatedTimeoutWriteConn{done: make(chan struct{})}, writeTimeoutCfg)
	writeTimeoutStream, err := writeTimeoutClient.OpenStream(context.Background(), "greeter/Write")
	if err == nil {
		_ = writeTimeoutStream.Close(context.Background(), "unexpected")
		t.Fatal("write-timeout mux stream opened, want timeout")
	}
	if diagnosis := writeTimeoutClient.DiagnosisSnapshot().FlowControl; diagnosis.WriteTimeouts != 1 {
		t.Fatalf("write-timeout diagnosis = %+v, want one write timeout", diagnosis)
	}
	writeTimeoutRPCClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxClientAdapter(writeTimeoutClient))
	if err != nil {
		t.Fatal(err)
	}
	flowDiagnosisRec := httptest.NewRecorder()
	writeTimeoutRPCClient.DiagnosisHandler().ServeHTTP(flowDiagnosisRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?endpoint=http://unused&flowControlEvent=write-timeout&eventFamily=flow-control&event=write-timeout", nil))
	if flowDiagnosisRec.Code != http.StatusOK {
		t.Fatalf("flow-control diagnosis status = %d body=%q", flowDiagnosisRec.Code, flowDiagnosisRec.Body.String())
	}
	var flowDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(flowDiagnosisRec.Body).Decode(&flowDiagnosis); err != nil {
		t.Fatal(err)
	}
	if flowDiagnosis.Endpoint != "http://unused" ||
		flowDiagnosis.FlowControl != "write_timeout" ||
		flowDiagnosis.EventFamily != "flow_control" ||
		flowDiagnosis.Event != "write_timeout" ||
		flowDiagnosis.Diagnosis.Mux.FlowControl.WriteTimeouts != 1 ||
		len(flowDiagnosis.Diagnosis.Mux.FlowControl.Events) != 1 ||
		flowDiagnosis.Diagnosis.Mux.FlowControl.Events[0].Event != "write_timeout" ||
		flowDiagnosis.Diagnosis.Mux.FlowControl.Events[0].Count != 1 {
		t.Fatalf("flow-control diagnosis = %+v, want generated write_timeout event evidence", flowDiagnosis.Diagnosis.Mux.FlowControl)
	}
	if len(flowDiagnosis.Diagnosis.Mux.Events) != 1 ||
		flowDiagnosis.Diagnosis.Mux.Events[0].Family != "flow_control" ||
		flowDiagnosis.Diagnosis.Mux.Events[0].Event != "write_timeout" {
		t.Fatalf("flow-control diagnosis events = %+v, want generated write_timeout mux event evidence", flowDiagnosis.Diagnosis.Mux.Events)
	}
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	traceCtx, span := provider.Tracer("generated-rpc-admin-smoke").Start(context.Background(), "mux-diagnosis", oteltrace.WithSpanKind(oteltrace.SpanKindInternal))
	rpc.AnnotateMuxDiagnosisSpan(traceCtx, flowDiagnosis)
	span.End()
	traceSpans := recorder.Ended()
	if len(traceSpans) != 1 {
		t.Fatalf("trace spans = %d, want generated mux diagnosis span", len(traceSpans))
	}
	traceAttrs := generatedTraceAttributeMap(traceSpans[0].Attributes())
	if traceAttrs["rpc.mux.endpoint"].AsString() != "http://unused" ||
		traceAttrs["rpc.mux.flow_control.event"].AsString() != "write_timeout" ||
		traceAttrs["rpc.mux.flow_control.write_timeout.count"].AsInt64() != 1 {
		t.Fatalf("mux trace attributes = %+v, want generated mux flow-control attributes", traceAttrs)
	}

	connectionDiagnosisRec := httptest.NewRecorder()
	writeTimeoutRPCClient.DiagnosisHandler().ServeHTTP(connectionDiagnosisRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?connectionId=missing&poolSlot=1&flowControlEvent=write-timeout", nil))
	if connectionDiagnosisRec.Code != http.StatusOK {
		t.Fatalf("connection flow-control diagnosis status = %d body=%q", connectionDiagnosisRec.Code, connectionDiagnosisRec.Body.String())
	}
	var connectionDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(connectionDiagnosisRec.Body).Decode(&connectionDiagnosis); err != nil {
		t.Fatal(err)
	}
	if connectionDiagnosis.ConnectionID != "missing" ||
		connectionDiagnosis.PoolSlot != 1 ||
		len(connectionDiagnosis.Diagnosis.Mux.Manager.Endpoints) != 0 {
		t.Fatalf("connection flow-control diagnosis = %+v, want generated connection filter evidence", connectionDiagnosis)
	}
	if err := writeTimeoutRPCClient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeTimeoutClient.Close(); err != nil {
		t.Fatal(err)
	}

	rpcOptions := []rpc.ServerOption{}
	if cfg.RPC.Mux.Enabled && cfg.RPC.Mux.Probe {
		rpcOptions = append(rpcOptions, rpc.WithExperimentalMuxServerAdapter(muxServer))
	}
	rpcServer := rpc.NewServer(rpcOptions...)
	if err := rpcServer.RegisterService(apprpc.GreeterService(svc.NewServiceContext(cfg)), nil); err != nil {
		t.Fatal(err)
	}
	adminServer := NewServer("", "/admin", rpcServer, WithControlPlaneSnapshot(func(ctx context.Context) (controlplane.Snapshot, error) {
		return cfg.ControlPlaneSnapshot(ctx)
	}))
	handler := adminServer.Handler()

	metricsRec := httptest.NewRecorder()
	handler.ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metricsRec.Code != http.StatusOK ||
		!strings.Contains(metricsRec.Body.String(), "gofly_requests_total") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_connections{frame_codec=\"binary\",payload_codec=\"identity\",downgraded=\"false\"} 1") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_drain_total{drain_reason=\"generated_shutdown\",direction=\"out\"} 1") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_active_streams{drain_reason=\"generated_shutdown\",state=\"draining\"} 0") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_flow_control_events_total{event=\"write_timeout\"}") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_flow_control_events_total{event=\"credit_wait_timeout\"}") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_flow_control_events_total{event=\"connection_window_exhausted\"}") {
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

	diagnosisRec := httptest.NewRecorder()
	handler.ServeHTTP(diagnosisRec, httptest.NewRequest(http.MethodGet, "/admin/rpc/admin/diagnosis", nil))
	if diagnosisRec.Code != http.StatusOK {
		t.Fatalf("diagnosis status = %d body=%q", diagnosisRec.Code, diagnosisRec.Body.String())
	}
	var diagnosis rpc.ServerDiagnosisSnapshot
	if err := json.NewDecoder(diagnosisRec.Body).Decode(&diagnosis); err != nil {
		t.Fatal(err)
	}
	if !diagnosis.Mux.Enabled || diagnosis.Mux.Adapter.AcceptedStreams != 1 || diagnosis.Mux.Transport.AcceptedStreams != 1 {
		t.Fatalf("diagnosis mux = %+v, want generated mux probe evidence", diagnosis.Mux)
	}
	stopMux()
	if err := <-muxDone; err != nil {
		t.Fatalf("mux server stopped with error: %v", err)
	}
}

func TestAdminDiagnosticsCustomOTelLogSink(t *testing.T) {
	customRecords := make(chan rpc.RPCMuxDiagnosisEventOTelLogRecord, 4)
	var customProfile string
	cleanup := rpc.RegisterRPCMuxOTelLogSink("otel-test", func(profile string) rpc.RPCMuxOTelLogExporter {
		customProfile = profile
		return rpc.RPCMuxOTelLogExporterFunc(func(_ context.Context, record rpc.RPCMuxDiagnosisEventOTelLogRecord) {
			customRecords <- record
		})
	})
	defer cleanup()

	if !rpc.RPCMuxOTelLogSinkRegistered("otel-test") {
		t.Fatal("custom otel-test sink not found in registry")
	}
	if !rpc.RPCMuxOTelLogSinkRegistered("  OTEL-TEST  ") {
		t.Fatal("custom otel-test sink should be discovered case-insensitively")
	}

	customCfg := appconfig.Config{RPC: appconfig.RPCConfig{Mux: appconfig.RPCMuxConfig{Enabled: true, Log: appconfig.RPCMuxLogConfig{Enabled: true, ExportEvents: true, EventFamily: "flow-control", Event: "fragment-window-refill", OTelCompatible: appconfig.RPCMuxOTelCompatibleLogConfig{Enabled: true, Sink: "otel-test", Profile: "generated-custom-sink", Delivery: appconfig.RPCMuxExporterDeliveryConfig{QueueSize: 4, Timeout: time.Second}}}, Candidate: appconfig.RPCMuxCandidateConfig{Enabled: true, Protocol: "gofly-mux/generated-custom-sink-test", MaxFrameBytes: 256, MaxMessageBytes: 1024, MaxConcurrentStreams: 8, ReceiveQueueSize: 2, ConnectionWindow: 3, FragmentStreamWindowUpdatePolicy: "on_receive", FragmentConnectionWindowUpdatePolicy: "on_receive", FragmentStreamWindowRefillRatio: 0.5, FragmentConnectionWindowRefillRatio: 0.25, FragmentMaxDeferredFragments: 2, FragmentWindowPolicyRiskMode: "warn", PayloadCodec: "identity", FrameCodec: "binary"}}}}
	if err := appconfig.ValidateRPCMuxConfig(customCfg.RPC.Mux); err != nil {
		t.Fatalf("custom otel-test sink should validate: %v", err)
	}

	customClient, err := rpc.NewClient("http://unused", customCfg.RPC.Mux.ClientOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	defer customClient.Close()

	if len(customCfg.RPC.Mux.ServerOptions()) != 1 {
		t.Fatalf("custom sink server options = %d, want one exporter option", len(customCfg.RPC.Mux.ServerOptions()))
	}
	customClient.ObserveMuxDiagnosis(context.Background(), rpc.RPCDiagnosisProbe{
		Target:  "http://unused",
		Method:  "greeter/Watch",
		Matched: true,
		Diagnosis: rpc.RPCDiagnosisSnapshot{Mux: rpc.RPCMuxTransportDiagnosis{
			FlowControl: rpc.RPCMuxFlowControlDiagnosis{
				WriteTimeouts:         1,
				FragmentWindowRefills: 3,
			},
		}},
	})

	if customProfile != "generated-custom-sink" {
		t.Fatalf("custom sink profile = %q, want generated-custom-sink", customProfile)
	}
	foundRefill := false
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for !foundRefill {
		select {
		case record := <-customRecords:
			if record.Event.Family == "flow_control" && record.Event.Event == "fragment_window_refill" {
				foundRefill = true
				if record.Name != "rpc.mux.diagnosis_event" {
					t.Fatalf("custom record name = %q, want rpc.mux.diagnosis_event", record.Name)
				}
				if record.Severity != "WARN" {
					t.Fatalf("custom record severity = %q, want WARN", record.Severity)
				}
			}
		case <-timeout.C:
			t.Fatal("custom otel-test sink received no fragment_window_refill event")
		}
	}
}

type generatedTimeoutWriteConn struct {
	mu       sync.Mutex
	deadline time.Time
	closed   bool
	done     chan struct{}
}

func (c *generatedTimeoutWriteConn) Read([]byte) (int, error) {
	<-c.done
	return 0, net.ErrClosed
}

func (c *generatedTimeoutWriteConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	if !deadline.IsZero() {
		return 0, generatedTimeoutNetError{msg: "write timeout"}
	}
	return len(p), nil
}

func (c *generatedTimeoutWriteConn) Close() error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.done)
	}
	c.mu.Unlock()
	return nil
}

func (c *generatedTimeoutWriteConn) LocalAddr() net.Addr  { return generatedDummyAddr("local") }
func (c *generatedTimeoutWriteConn) RemoteAddr() net.Addr { return generatedDummyAddr("remote") }
func (c *generatedTimeoutWriteConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}
func (c *generatedTimeoutWriteConn) SetReadDeadline(time.Time) error    { return nil }
func (c *generatedTimeoutWriteConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }

type generatedTimeoutNetError struct{ msg string }

func (e generatedTimeoutNetError) Error() string   { return e.msg }
func (e generatedTimeoutNetError) Timeout() bool   { return true }
func (e generatedTimeoutNetError) Temporary() bool { return true }

type generatedDummyAddr string

func (a generatedDummyAddr) Network() string { return string(a) }
func (a generatedDummyAddr) String() string  { return string(a) }

func generatedTraceAttributeMap(attrs []attribute.KeyValue) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value
	}
	return out
}

func generatedRPCTLSCA(t *testing.T, dir string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "generated-rpc-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}
	generatedRPCTestPEM(t, filepath.Join(dir, "ca.crt"), "CERTIFICATE", der)
	return cert, key
}

func generatedRPCTLSLeaf(t *testing.T, dir, name string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"svc"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	certFile = filepath.Join(dir, name+".crt")
	keyFile = filepath.Join(dir, name+".key")
	generatedRPCTestPEM(t, certFile, "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	generatedRPCTestPEM(t, keyFile, "EC PRIVATE KEY", keyDER)
	return certFile, keyFile
}

func generatedRPCTestPEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
`

const apiNewTemplate = `type PingRequest {
  Name string
}

type PingResponse {
  Message string
}

service {{.Name}} {
  @handler Ping
  get /api/v1/ping (PingRequest) returns (PingResponse)
}
`

const rpcNewTemplate = `syntax = "proto3";

package {{.Name}}.v1;

message SayHelloRequest {
  string name = 1;
}

message SayHelloResponse {
  string message = 1;
}

service Greeter {
  rpc SayHello(SayHelloRequest) returns (SayHelloResponse);
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

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/security"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
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
	Mux       RPCMuxConfig ` + "`json:\"mux,omitempty\"`" + `
}

type RPCMuxConfig struct {
	Enabled          bool ` + "`json:\"enabled\"`" + `
	Probe            bool ` + "`json:\"probe\"`" + `
	Addr             string ` + "`json:\"addr\"`" + `
	Endpoints        []string ` + "`json:\"endpoints,omitempty\"`" + `
	IdleTimeout      time.Duration ` + "`json:\"idleTimeout\"`" + `
	MaxOpenRetries   int ` + "`json:\"maxOpenRetries,omitempty\"`" + `
	OpenRetryReasons []string ` + "`json:\"openRetryReasons,omitempty\"`" + `
	HealthBackoffMultiplier int ` + "`json:\"healthBackoffMultiplier,omitempty\"`" + `
	HealthMaxCooldown time.Duration ` + "`json:\"healthMaxCooldown,omitempty\"`" + `
	Trace RPCMuxTraceConfig ` + "`json:\"trace,omitempty\"`" + `
	Log RPCMuxLogConfig ` + "`json:\"log,omitempty\"`" + `
	TLS RPCMuxTLSConfig ` + "`json:\"tls,omitempty\"`" + `
	MutualTLS RPCMuxMutualTLSConfig ` + "`json:\"mtls,omitempty\"`" + `
	ALPN RPCMuxALPNConfig ` + "`json:\"alpn,omitempty\"`" + `
	Candidate RPCMuxCandidateConfig ` + "`json:\"candidate,omitempty\"`" + `
}

type RPCMuxTraceConfig struct {
	Enabled bool ` + "`json:\"enabled\"`" + `
	AnnotateStreams bool ` + "`json:\"annotateStreams\"`" + `
}

type RPCMuxLogConfig struct {
	Enabled bool ` + "`json:\"enabled\"`" + `
	Diagnosis bool ` + "`json:\"diagnosis\"`" + `
	ExportEvents bool ` + "`json:\"exportEvents\"`" + `
	EventFamily string ` + "`json:\"eventFamily,omitempty\"`" + `
	Event string ` + "`json:\"event,omitempty\"`" + `
	Endpoint string ` + "`json:\"endpoint,omitempty\"`" + `
	ConnectionID string ` + "`json:\"connectionId,omitempty\"`" + `
	PoolSlot int ` + "`json:\"poolSlot,omitempty\"`" + `
	OTelCompatible RPCMuxOTelCompatibleLogConfig ` + "`json:\"otelCompatible,omitempty\"`" + `
}

func (c RPCMuxLogConfig) EventExportEnabled() bool {
	return c.Enabled && c.ExportEvents
}

func (c RPCMuxLogConfig) Filter() rpc.RPCMuxDiagnosisFilter {
	return rpc.RPCMuxDiagnosisFilter{
		Endpoint: c.Endpoint,
		ConnectionID: c.ConnectionID,
		PoolSlot: c.PoolSlot,
		EventFamily: c.EventFamily,
		Event: c.Event,
	}
}

type RPCMuxOTelCompatibleLogConfig struct {
	Enabled bool ` + "`json:\"enabled\"`" + `
	Version string ` + "`json:\"version,omitempty\"`" + `
	SchemaVersion string ` + "`json:\"schemaVersion,omitempty\"`" + `
	Sink string ` + "`json:\"sink,omitempty\"`" + `
	Profile string ` + "`json:\"profile,omitempty\"`" + `
	ProfileRef string ` + "`json:\"profileRef,omitempty\"`" + `
	ProfileSchema string ` + "`json:\"profileSchema,omitempty\"`" + `
	ProfileMigration string ` + "`json:\"profileMigration,omitempty\"`" + `
	Delivery RPCMuxExporterDeliveryConfig ` + "`json:\"delivery,omitempty\"`" + `
	Sinks []RPCMuxOTelSinkConfig ` + "`json:\"sinks,omitempty\"`" + `
}

type RPCMuxOTelSinkConfig struct {
	Name string ` + "`json:\"name\"`" + `
	Profile string ` + "`json:\"profile,omitempty\"`" + `
	ProfileRef string ` + "`json:\"profileRef,omitempty\"`" + `
	ProfileSchema string ` + "`json:\"profileSchema,omitempty\"`" + `
	ProfileMigration string ` + "`json:\"profileMigration,omitempty\"`" + `
	Priority int ` + "`json:\"priority,omitempty\"`" + `
	Delivery RPCMuxExporterDeliveryConfig ` + "`json:\"delivery,omitempty\"`" + `
}

type RPCMuxExporterDeliveryConfig struct {
	QueueSize int ` + "`json:\"queueSize,omitempty\"`" + `
	Timeout time.Duration ` + "`json:\"timeout,omitempty\"`" + `
	MaxHungCalls int ` + "`json:\"maxHungCalls,omitempty\"`" + `
	BreakerFailureThreshold int ` + "`json:\"breakerFailureThreshold,omitempty\"`" + `
	BreakerCooldown time.Duration ` + "`json:\"breakerCooldown,omitempty\"`" + `
	ErrorBudget RPCMuxExporterErrorBudgetConfig ` + "`json:\"errorBudget,omitempty\"`" + `
	Isolation RPCMuxSinkIsolationConfig ` + "`json:\"isolation,omitempty\"`" + `
}

type RPCMuxExporterErrorBudgetConfig struct {
	Enabled bool ` + "`json:\"enabled,omitempty\"`" + `
	MinSamples int64 ` + "`json:\"minSamples,omitempty\"`" + `
	BurnRateThreshold float64 ` + "`json:\"burnRateThreshold,omitempty\"`" + `
	RecoveryBurnRateThreshold float64 ` + "`json:\"recoveryBurnRateThreshold,omitempty\"`" + `
	PauseDuration time.Duration ` + "`json:\"pauseDuration,omitempty\"`" + `
}

type RPCMuxSinkIsolationConfig struct {
	Mode string ` + "`json:\"mode,omitempty\"`" + `
	ShutdownTimeout time.Duration ` + "`json:\"shutdownTimeout,omitempty\"`" + `
	MaxMemoryBytes int64 ` + "`json:\"maxMemoryBytes,omitempty\"`" + `
	MaxCPUPercent int ` + "`json:\"maxCpuPercent,omitempty\"`" + `
	AuditFields map[string]string ` + "`json:\"auditFields,omitempty\"`" + `
}

func (c RPCMuxExporterDeliveryConfig) RuntimeConfig() rpc.RPCMuxDiagnosisExporterDeliveryConfig {
	return rpc.RPCMuxDiagnosisExporterDeliveryConfig{
		QueueSize: c.QueueSize,
		Timeout: c.Timeout,
		MaxHungCalls: c.MaxHungCalls,
		BreakerFailureThreshold: c.BreakerFailureThreshold,
		BreakerCooldown: c.BreakerCooldown,
		ErrorBudget: rpc.RPCMuxDiagnosisExporterErrorBudgetConfig{
			Enabled: c.ErrorBudget.Enabled,
			MinSamples: c.ErrorBudget.MinSamples,
			BurnRateThreshold: c.ErrorBudget.BurnRateThreshold,
			RecoveryBurnRateThreshold: c.ErrorBudget.RecoveryBurnRateThreshold,
			PauseDuration: c.ErrorBudget.PauseDuration,
		},
		Isolation: rpc.RPCMuxDiagnosisSinkIsolationConfig{
			Mode: c.Isolation.Mode,
			ShutdownTimeout: c.Isolation.ShutdownTimeout,
			MaxMemoryBytes: c.Isolation.MaxMemoryBytes,
			MaxCPUPercent: c.Isolation.MaxCPUPercent,
			AuditFields: c.Isolation.AuditFields,
		},
	}
}

func (c RPCMuxOTelCompatibleLogConfig) SinkSetConfig() rpc.RPCMuxDiagnosisSinkSetConfig {
	version := strings.TrimSpace(c.Version)
	if version == "" {
		version = "legacy"
	}
	if !c.Enabled {
		return rpc.RPCMuxDiagnosisSinkSetConfig{Version: version, SchemaVersion: strings.TrimSpace(c.SchemaVersion), Secrets: rpc.NewRPCMuxDiagnosisEnvSecretResolver()}
	}
	sinks := make([]rpc.RPCMuxDiagnosisSinkConfig, 0, len(c.Sinks)+1)
	if len(c.Sinks) == 0 {
		name := strings.TrimSpace(c.Sink)
		if name == "" {
			name = "slog"
		}
		sinks = append(sinks, rpc.RPCMuxDiagnosisSinkConfig{
			Name: name,
			Profile: c.Profile,
			ProfileRef: c.ProfileRef,
			ProfileSchema: c.ProfileSchema,
			ProfileMigration: c.ProfileMigration,
			Delivery: c.Delivery.RuntimeConfig(),
		})
	} else {
		for _, sink := range c.Sinks {
			sinks = append(sinks, rpc.RPCMuxDiagnosisSinkConfig{
				Name: sink.Name,
				Profile: sink.Profile,
				ProfileRef: sink.ProfileRef,
				ProfileSchema: sink.ProfileSchema,
				ProfileMigration: sink.ProfileMigration,
				Priority: sink.Priority,
				Delivery: sink.Delivery.RuntimeConfig(),
			})
		}
	}
	return rpc.RPCMuxDiagnosisSinkSetConfig{Version: version, SchemaVersion: strings.TrimSpace(c.SchemaVersion), Sinks: sinks, Secrets: rpc.NewRPCMuxDiagnosisEnvSecretResolver()}
}

func (c RPCMuxOTelCompatibleLogConfig) NewSinkSet() (*rpc.RPCMuxDiagnosisSinkSet, error) {
	return rpc.NewRPCMuxDiagnosisSinkSet(c.SinkSetConfig())
}

func (c RPCMuxOTelCompatibleLogConfig) ReloadSinkSet(ctx context.Context, sinkSet *rpc.RPCMuxDiagnosisSinkSet) error {
	if sinkSet == nil {
		_, err := c.NewSinkSet()
		return err
	}
	return sinkSet.Reload(ctx, c.SinkSetConfig())
}

func (c RPCMuxOTelCompatibleLogConfig) DiffSinkSet(ctx context.Context, sinkSet *rpc.RPCMuxDiagnosisSinkSet) (rpc.RPCMuxDiagnosisSinkSetDiffPlan, error) {
	if sinkSet == nil {
		empty, err := rpc.NewRPCMuxDiagnosisSinkSet(rpc.RPCMuxDiagnosisSinkSetConfig{Version: "legacy", SchemaVersion: strings.TrimSpace(c.SchemaVersion)})
		if err != nil {
			return rpc.RPCMuxDiagnosisSinkSetDiffPlan{}, err
		}
		defer empty.Close()
		return empty.DiffRPCMuxDiagnosisSinkSetConfig(ctx, c.SinkSetConfig())
	}
	return sinkSet.DiffRPCMuxDiagnosisSinkSetConfig(ctx, c.SinkSetConfig())
}

type RPCMuxTLSConfig struct {
	Enabled bool ` + "`json:\"enabled,omitempty\"`" + `
	CertFile string ` + "`json:\"certFile,omitempty\"`" + `
	KeyFile string ` + "`json:\"keyFile,omitempty\"`" + `
	CAFile string ` + "`json:\"caFile,omitempty\"`" + `
	ServerName string ` + "`json:\"serverName,omitempty\"`" + `
	InsecureSkipVerify bool ` + "`json:\"insecureSkipVerify,omitempty\"`" + `
	MinVersion uint16 ` + "`json:\"minVersion,omitempty\"`" + `
}

type RPCMuxMutualTLSConfig struct {
	Enabled bool ` + "`json:\"enabled,omitempty\"`" + `
	ClientCAFile string ` + "`json:\"clientCAFile,omitempty\"`" + `
	ClientCertFile string ` + "`json:\"clientCertFile,omitempty\"`" + `
	ClientKeyFile string ` + "`json:\"clientKeyFile,omitempty\"`" + `
}

type RPCMuxALPNConfig struct {
	Enabled bool ` + "`json:\"enabled,omitempty\"`" + `
	Protocol string ` + "`json:\"protocol,omitempty\"`" + `
}

type RPCMuxCandidateConfig struct {
	Enabled bool ` + "`json:\"enabled\"`" + `
	Protocol string ` + "`json:\"protocol,omitempty\"`" + `
	TLS security.TLSConfig ` + "`json:\"tls,omitempty\"`" + `
	DialTimeout time.Duration ` + "`json:\"dialTimeout,omitempty\"`" + `
	KeepAlive time.Duration ` + "`json:\"keepAlive,omitempty\"`" + `
	HandshakeTimeout time.Duration ` + "`json:\"handshakeTimeout,omitempty\"`" + `
	KeepaliveInterval time.Duration ` + "`json:\"keepaliveInterval,omitempty\"`" + `
	KeepaliveIdle time.Duration ` + "`json:\"keepaliveIdle,omitempty\"`" + `
	WriteTimeout time.Duration ` + "`json:\"writeTimeout,omitempty\"`" + `
	CreditWaitTimeout time.Duration ` + "`json:\"creditWaitTimeout,omitempty\"`" + `
	MaxFrameBytes int64 ` + "`json:\"maxFrameBytes,omitempty\"`" + `
	MaxMessageBytes int64 ` + "`json:\"maxMessageBytes,omitempty\"`" + `
	MaxConcurrentStreams int ` + "`json:\"maxConcurrentStreams,omitempty\"`" + `
	ReceiveQueueSize int ` + "`json:\"receiveQueueSize,omitempty\"`" + `
	ConnectionWindow int ` + "`json:\"connectionWindow,omitempty\"`" + `
	FragmentStreamWindowUpdatePolicy string ` + "`json:\"fragmentStreamWindowUpdatePolicy,omitempty\"`" + `
	FragmentConnectionWindowUpdatePolicy string ` + "`json:\"fragmentConnectionWindowUpdatePolicy,omitempty\"`" + `
	FragmentStreamWindowRefillRatio float64 ` + "`json:\"fragmentStreamWindowRefillRatio,omitempty\"`" + `
	FragmentConnectionWindowRefillRatio float64 ` + "`json:\"fragmentConnectionWindowRefillRatio,omitempty\"`" + `
	FragmentMaxDeferredFragments int ` + "`json:\"fragmentMaxDeferredFragments,omitempty\"`" + `
	FragmentWindowPolicyRiskMode string ` + "`json:\"fragmentWindowPolicyRiskMode,omitempty\"`" + `
	PayloadCodec string ` + "`json:\"payloadCodec,omitempty\"`" + `
	FrameCodec string ` + "`json:\"frameCodec,omitempty\"`" + `
	DrainGrace time.Duration ` + "`json:\"drainGrace,omitempty\"`" + `
	AllowLegacyDowngrade bool ` + "`json:\"allowLegacyDowngrade,omitempty\"`" + `
}

type ResilienceProfile struct {
	Timeout        bool ` + "`json:\"timeout\"`" + `
	RateLimit      bool ` + "`json:\"rateLimit\"`" + `
	Concurrency    bool ` + "`json:\"concurrency\"`" + `
	Breaker        bool ` + "`json:\"breaker\"`" + `
	Retry          bool ` + "`json:\"retry\"`" + `
	AdaptiveLimit  bool ` + "`json:\"adaptiveLimit\"`" + `
	RESTEnabled    bool ` + "`json:\"restEnabled\"`" + `
	RPCEnabled     bool ` + "`json:\"rpcEnabled\"`" + `
	GatewayEnabled bool ` + "`json:\"gatewayEnabled\"`" + `
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

func (c RPCMuxConfig) CandidateConfig() rpc.ExperimentalMuxCandidateConfig {
	return c.CandidateClientConfig()
}

func (c RPCMuxConfig) CandidateServerConfig() rpc.ExperimentalMuxCandidateConfig {
	return c.candidateConfigWithTLS(c.serverTLSConfig())
}

func (c RPCMuxConfig) CandidateClientConfig() rpc.ExperimentalMuxCandidateConfig {
	return c.candidateConfigWithTLS(c.clientTLSConfig())
}

func (c RPCMuxConfig) candidateConfigWithTLS(tlsConfig security.TLSConfig) rpc.ExperimentalMuxCandidateConfig {
	candidate := c.Candidate
	protocol := candidate.Protocol
	if c.ALPN.Enabled && strings.TrimSpace(c.ALPN.Protocol) != "" {
		protocol = strings.TrimSpace(c.ALPN.Protocol)
	}
	return rpc.ExperimentalMuxCandidateConfig{
		Protocol:             protocol,
		TLS:                  tlsConfig,
		DialTimeout:          candidate.DialTimeout,
		KeepAlive:            candidate.KeepAlive,
		HandshakeTimeout:     candidate.HandshakeTimeout,
		KeepaliveInterval:    candidate.KeepaliveInterval,
		KeepaliveIdle:        candidate.KeepaliveIdle,
		WriteTimeout:         candidate.WriteTimeout,
		CreditWaitTimeout:    candidate.CreditWaitTimeout,
		MaxFrameBytes:        candidate.MaxFrameBytes,
		MaxMessageBytes:      candidate.MaxMessageBytes,
		MaxConcurrentStreams: candidate.MaxConcurrentStreams,
		ReceiveQueueSize:     candidate.ReceiveQueueSize,
		ConnectionWindow:     candidate.ConnectionWindow,
		FragmentStreamWindowUpdatePolicy:     candidate.FragmentStreamWindowUpdatePolicy,
		FragmentConnectionWindowUpdatePolicy: candidate.FragmentConnectionWindowUpdatePolicy,
		FragmentStreamWindowRefillRatio:      candidate.FragmentStreamWindowRefillRatio,
		FragmentConnectionWindowRefillRatio:  candidate.FragmentConnectionWindowRefillRatio,
		FragmentMaxDeferredFragments:         candidate.FragmentMaxDeferredFragments,
		FragmentWindowPolicyRiskMode:         candidate.FragmentWindowPolicyRiskMode,
		PayloadCodec:         candidate.PayloadCodec,
		FrameCodec:           candidate.FrameCodec,
		DrainGrace:           candidate.DrainGrace,
		AllowLegacyDowngrade: candidate.AllowLegacyDowngrade,
	}
}

func ValidateRPCMuxConfig(c RPCMuxConfig) error {
	if c.Log.OTelCompatible.Enabled {
		if err := rpc.ValidateRPCMuxDiagnosisSinkSetConfig(c.Log.OTelCompatible.SinkSetConfig()); err != nil {
			return fmt.Errorf("rpc mux otelCompatible sinks: %w", err)
		}
	}
	if !c.Candidate.Enabled {
		return nil
	}
	return errors.Join(
		c.CandidateServerConfig().Validate(),
		c.CandidateClientConfig().Validate(),
	)
}

func (c RPCMuxConfig) CandidateTLSConfig() security.TLSConfig {
	return c.clientTLSConfig()
}

func (c RPCMuxConfig) serverTLSConfig() security.TLSConfig {
	tlsConfig := c.Candidate.TLS
	if c.TLS.Enabled {
		if tlsConfig.CertFile == "" {
			tlsConfig.CertFile = c.TLS.CertFile
		}
		if tlsConfig.KeyFile == "" {
			tlsConfig.KeyFile = c.TLS.KeyFile
		}
		if tlsConfig.CAFile == "" {
			tlsConfig.CAFile = c.TLS.CAFile
		}
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = c.TLS.ServerName
		}
		if !tlsConfig.InsecureSkipVerify {
			tlsConfig.InsecureSkipVerify = c.TLS.InsecureSkipVerify
		}
		if tlsConfig.MinVersion == 0 {
			tlsConfig.MinVersion = c.TLS.MinVersion
		}
	}
	if c.MutualTLS.Enabled {
		if tlsConfig.ClientCAFile == "" {
			tlsConfig.ClientCAFile = c.MutualTLS.ClientCAFile
		}
	}
	return tlsConfig
}

func (c RPCMuxConfig) clientTLSConfig() security.TLSConfig {
	tlsConfig := c.Candidate.TLS
	if c.TLS.Enabled {
		if tlsConfig.CAFile == "" {
			tlsConfig.CAFile = c.TLS.CAFile
		}
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = c.TLS.ServerName
		}
		if !tlsConfig.InsecureSkipVerify {
			tlsConfig.InsecureSkipVerify = c.TLS.InsecureSkipVerify
		}
		if tlsConfig.MinVersion == 0 {
			tlsConfig.MinVersion = c.TLS.MinVersion
		}
	}
	if c.MutualTLS.Enabled {
		if tlsConfig.CertFile == "" {
			tlsConfig.CertFile = c.MutualTLS.ClientCertFile
		}
		if tlsConfig.KeyFile == "" {
			tlsConfig.KeyFile = c.MutualTLS.ClientKeyFile
		}
	}
	return tlsConfig
}

func (c RPCMuxConfig) ClientOptions() []rpc.ClientOption {
	return c.ClientOptionsWithSinkSet(nil)
}

func (c RPCMuxConfig) ClientOptionsWithSinkSet(sinkSet *rpc.RPCMuxDiagnosisSinkSet) []rpc.ClientOption {
	options := make([]rpc.ClientOption, 0, 2)
	if c.Trace.Enabled && c.Trace.AnnotateStreams {
		options = append(options, rpc.WithMuxTraceAnnotation())
	}
	if c.Log.Enabled && c.Log.Diagnosis {
		options = append(options, rpc.WithMuxDiagnosisLogging(nil))
	}
	if c.Log.EventExportEnabled() {
		filter := c.Log.Filter()
		if c.Log.OTelCompatible.Enabled {
			if sinkSet == nil {
				var err error
				sinkSet, err = c.Log.OTelCompatible.NewSinkSet()
				if err != nil {
					return options
				}
			}
			options = append(options, rpc.WithMuxDiagnosisEventExporter(sinkSet, filter))
		} else {
			options = append(options, rpc.WithMuxDiagnosisEventLogging(nil, filter))
		}
	}
	return options
}

func (c RPCMuxConfig) ServerOptions() []rpc.ServerOption {
	return c.ServerOptionsWithSinkSet(nil)
}

func (c RPCMuxConfig) ServerOptionsWithSinkSet(sinkSet *rpc.RPCMuxDiagnosisSinkSet) []rpc.ServerOption {
	if !c.Log.EventExportEnabled() {
		return nil
	}
	filter := c.Log.Filter()
	if c.Log.OTelCompatible.Enabled {
		if sinkSet == nil {
			var err error
			sinkSet, err = c.Log.OTelCompatible.NewSinkSet()
			if err != nil {
				return nil
			}
		}
		return []rpc.ServerOption{rpc.WithServerMuxDiagnosisEventExporter(sinkSet, filter)}
	}
	return []rpc.ServerOption{rpc.WithServerMuxDiagnosisEventLogging(nil, filter)}
}

func (c Config) ResilienceProfile() ResilienceProfile {
	service := c.ServiceConf()
	serviceGovernance := service.Governance
	profile := ResilienceProfile{
		Timeout:       serviceGovernance.Timeout > 0,
		RateLimit:     serviceGovernance.RateLimit.Rate > 0 && serviceGovernance.RateLimit.Burst > 0,
		Concurrency:   serviceGovernance.MaxConcurrency > 0,
		Breaker:       serviceGovernance.Breaker,
		Retry:         serviceGovernance.Retry.Attempts > 0,
		AdaptiveLimit: serviceGovernance.AdaptiveLimit,
		RESTEnabled:   c.Rest.Middlewares.Timeout && c.Rest.Middlewares.RateLimit && c.Rest.Middlewares.MaxConcurrency && c.Rest.Middlewares.Breaker,
		RPCEnabled:    len(service.RPCServerOptions()) > 0 && len(service.RPCClientOptions()) > 0,
	}
	for _, rule := range c.Governance.Rules {
		if rule.Transport == governance.TransportGateway && rule.Policy.Retry.Attempts > 0 && rule.Policy.RateLimit.Rate > 0 && rule.Policy.Concurrency.Limit > 0 && rule.Policy.Breaker.Enabled {
			profile.GatewayEnabled = true
			break
		}
	}
	return profile
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

func (c Config) ControlPlaneSnapshotWithDiscovery(ctx context.Context, registry any) (controlplane.Snapshot, error) {
	contributors := []controlplane.SnapshotContributor{c.ControlPlaneContributor()}
	if source, ok := registry.(controlplane.DiscoverySnapshotSource); ok {
		contributors = append(contributors, controlplane.DiscoveryContributor{Registry: source})
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
	if err := addGeneratedControlPlaneConfig("resilience", cfg.ResilienceProfile()); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("scaffold", cfg.Scaffold); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("discovery", cfg.Discovery.Sanitized()); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("rpcMuxOTelSinks", rpc.RPCMuxOTelLogSinkRegistry()); err != nil {
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
	snapshot.Metadata["generated.project.resilience"] = "timeout,rate,concurrency,breaker,retry"
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

func (c DiscoveryConfig) ResolvedEndpoints() []string {
	if len(c.Endpoints) > 0 {
		return compactDiscoveryEndpoints(c.Endpoints)
	}
	return compactDiscoveryEndpoints(strings.Split(c.Address, ","))
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
	if c.ProviderName() == "etcdv3" && len(c.ResolvedEndpoints()) == 0 {
		return errors.New("discovery endpoints are required for etcdv3")
	}
	return nil
}

func compactDiscoveryEndpoints(endpoints []string) []string {
	out := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint != "" {
			out = append(out, endpoint)
		}
	}
	return out
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
		ValidateRPCMuxConfig(c.RPC.Mux),
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

const configTestHeaderTemplate = `package config

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/rest"
)
`

const rpcMuxConfigValidationTestTemplate = `
func TestRPCMuxConfigValidatesOTelCompatibleSink(t *testing.T) {
	cfg := RPCMuxConfig{Log: RPCMuxLogConfig{
		Enabled:      true,
		ExportEvents: true,
		OTelCompatible: RPCMuxOTelCompatibleLogConfig{
			Enabled: true,
			Sink:    "slog",
			Profile: "generated-mtls-refill",
		},
	}}
	if err := ValidateRPCMuxConfig(cfg); err != nil {
		t.Fatalf("ValidateRPCMuxConfig slog otel-compatible sink: %v", err)
	}

	cfg.Log.OTelCompatible.Sink = ""
	if err := ValidateRPCMuxConfig(cfg); err != nil {
		t.Fatalf("ValidateRPCMuxConfig default otel-compatible sink: %v", err)
	}

	cfg.Log.OTelCompatible.Sink = "unsupported"
	if err := ValidateRPCMuxConfig(cfg); err == nil || !strings.Contains(err.Error(), "otelCompatible sink") {
		t.Fatalf("ValidateRPCMuxConfig unsupported otel-compatible sink = %v, want fail-fast error", err)
	}

	cfg.Log.OTelCompatible = RPCMuxOTelCompatibleLogConfig{
		Enabled: true,
		Version: "generated-v2",
		Sinks: []RPCMuxOTelSinkConfig{
			{Name: "slog", Priority: 20},
			{Name: "slog", Priority: 10},
		},
	}
	if err := ValidateRPCMuxConfig(cfg); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("ValidateRPCMuxConfig duplicate sinks = %v, want duplicate error", err)
	}
}

func TestRPCMuxOTelCompatibleSinkSetHotReload(t *testing.T) {
	t.Setenv("OTEL_PROFILE_JSON", "generated-env-profile")
	cfg := RPCMuxOTelCompatibleLogConfig{
		Enabled: true,
		Version: "generated-v1",
		SchemaVersion: "mux-sinks/v1",
		Sinks: []RPCMuxOTelSinkConfig{{
			Name: "slog",
			ProfileRef: "env://OTEL_PROFILE_JSON",
			ProfileSchema: "slog/v1",
			ProfileMigration: "legacy-to-v1",
			Priority: 10,
			Delivery: RPCMuxExporterDeliveryConfig{
				QueueSize: 2,
				Timeout: time.Second,
				MaxHungCalls: 1,
				BreakerFailureThreshold: 2,
				BreakerCooldown: time.Minute,
				Isolation: RPCMuxSinkIsolationConfig{
					Mode: "isolated_process",
					ShutdownTimeout: time.Second,
					MaxMemoryBytes: 1 << 20,
					MaxCPUPercent: 50,
					AuditFields: map[string]string{"owner": "generated-test"},
				},
				ErrorBudget: RPCMuxExporterErrorBudgetConfig{
					Enabled: true,
					MinSamples: 10,
					BurnRateThreshold: 0.5,
					PauseDuration: time.Minute,
				},
			},
		}},
	}
	sinkSet, err := cfg.NewSinkSet()
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()
	next := cfg
	next.Version = "generated-v2"
	next.SchemaVersion = "mux-sinks/v2"
	next.Sinks[0].Priority = 20
	plan, err := next.DiffSinkSet(context.Background(), sinkSet)
	if err != nil {
		t.Fatalf("DiffSinkSet: %v", err)
	}
	if len(plan.ChangePriority) != 1 || plan.ChangePriority[0] != "slog" || plan.ToSchemaVersion != "mux-sinks/v2" {
		t.Fatalf("diff plan = %+v", plan)
	}
	if err := next.ReloadSinkSet(context.Background(), sinkSet); err != nil {
		t.Fatalf("ReloadSinkSet: %v", err)
	}
	snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
	if snapshot.Version != "generated-v2" || snapshot.SchemaVersion != "mux-sinks/v2" ||
		snapshot.Sinks[0].Priority != 20 || snapshot.Sinks[0].ProfileMigration != "legacy-to-v1" ||
		snapshot.Sinks[0].Delivery.MaxHungCalls != 1 ||
		snapshot.Sinks[0].Delivery.Isolation.Mode != "isolated_process" ||
		snapshot.Sinks[0].Delivery.Isolation.AuditFields["owner"] != "generated-test" {
		t.Fatalf("sink set snapshot = %+v", snapshot)
	}
	if strings.Contains(snapshot.Sinks[0].Name, "generated-env-profile") ||
		strings.Contains(snapshot.Sinks[0].Delivery.LastError, "generated-env-profile") {
		t.Fatalf("sink set snapshot leaked env profile: %+v", snapshot)
	}

	disabled := RPCMuxOTelCompatibleLogConfig{Version: "disabled-v1", SchemaVersion: "mux-sinks/v2"}
	emptySet, err := disabled.NewSinkSet()
	if err != nil {
		t.Fatal(err)
	}
	defer emptySet.Close()
	if err := cfg.ReloadSinkSet(context.Background(), emptySet); err != nil {
		t.Fatalf("activate sink set from disabled config: %v", err)
	}
	if got := emptySet.RPCMuxDiagnosisSinkSetSnapshot(); got.SinkCount != 1 || got.Sinks[0].Name != "slog" {
		t.Fatalf("activated sink set snapshot = %+v", got)
	}
}

func TestRPCMuxConfigValidatesCandidateFragmentWindowRiskMode(t *testing.T) {
	cfg := RPCMuxConfig{Candidate: RPCMuxCandidateConfig{
		Enabled:                              true,
		Protocol:                             "gofly-mux/config-validation-test",
		MaxFrameBytes:                        96,
		MaxMessageBytes:                      2048,
		ReceiveQueueSize:                     2,
		ConnectionWindow:                     3,
		FragmentStreamWindowUpdatePolicy:     "on_receive",
		FragmentConnectionWindowUpdatePolicy: "on_receive",
		FragmentStreamWindowRefillRatio:      0.5,
		FragmentConnectionWindowRefillRatio:  0.25,
		FragmentMaxDeferredFragments:         0,
		FragmentWindowPolicyRiskMode:         "warn",
	}}
	if err := ValidateRPCMuxConfig(cfg); err != nil {
		t.Fatalf("ValidateRPCMuxConfig warn risk mode: %v", err)
	}

	cfg.Candidate.FragmentWindowPolicyRiskMode = "reject"
	if err := ValidateRPCMuxConfig(cfg); err == nil || !strings.Contains(err.Error(), "fragment window policy risk rejected") {
		t.Fatalf("ValidateRPCMuxConfig reject risk mode = %v, want fail-fast policy risk error", err)
	}

	cfg.Candidate.FragmentWindowPolicyRiskMode = "invalid"
	if err := ValidateRPCMuxConfig(cfg); err == nil || !strings.Contains(err.Error(), "risk mode must be diagnose, warn, or reject") {
		t.Fatalf("ValidateRPCMuxConfig invalid risk mode = %v, want invalid mode error", err)
	}

	cfg.Candidate.FragmentWindowPolicyRiskMode = "diagnose"
	cfg.Candidate.FragmentStreamWindowRefillRatio = 1.1
	if err := ValidateRPCMuxConfig(cfg); err == nil || !strings.Contains(err.Error(), "stream window refill ratio") {
		t.Fatalf("ValidateRPCMuxConfig invalid stream refill ratio = %v, want invalid ratio error", err)
	}

	cfg.Candidate.FragmentStreamWindowRefillRatio = 0.5
	cfg.Candidate.FragmentConnectionWindowRefillRatio = -0.1
	if err := ValidateRPCMuxConfig(cfg); err == nil || !strings.Contains(err.Error(), "connection window refill ratio") {
		t.Fatalf("ValidateRPCMuxConfig invalid connection refill ratio = %v, want invalid ratio error", err)
	}

	cfg.Candidate.FragmentConnectionWindowRefillRatio = 0.25
	cfg.Candidate.FragmentMaxDeferredFragments = -1
	if err := ValidateRPCMuxConfig(cfg); err == nil || !strings.Contains(err.Error(), "max deferred fragments") {
		t.Fatalf("ValidateRPCMuxConfig invalid max deferred fragments = %v, want invalid max deferred error", err)
	}

	cfg.Candidate.FragmentMaxDeferredFragments = 2
	cfg.Candidate.FragmentWindowPolicyRiskMode = "reject"
	if err := ValidateRPCMuxConfig(cfg); err == nil || !strings.Contains(err.Error(), "estimated_fragments_exceeds_max_deferred_fragments") {
		t.Fatalf("ValidateRPCMuxConfig reject max deferred risk = %v, want max deferred fail-fast error", err)
	}

	cfg.Candidate.Enabled = false
	if err := ValidateRPCMuxConfig(cfg); err != nil {
		t.Fatalf("ValidateRPCMuxConfig disabled candidate: %v", err)
	}
}
`

const configCommonTestsTemplate = `
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
		Service: serviceConfFixture("hello", app.ServiceGovernance{
			Timeout:        3 * time.Second,
			Breaker:        true,
			Retry:          app.ServiceRetry{Attempts: 2, Backoff: 100 * time.Millisecond},
			RateLimit:      app.ServiceRateLimit{Rate: 100, Burst: 100},
			MaxConcurrency: 64,
			AdaptiveLimit:  true,
		}),
		Scaffold:    ScaffoldConfig{Features: []string{"ecosystem-compat"}},
		Rest: rest.Config{Name: "hello", Host: "127.0.0.1", Port: 8080, Middlewares: rest.MiddlewaresConfig{
			Timeout:        true,
			RateLimit:      true,
			MaxConcurrency: true,
			Breaker:        true,
		}},
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
	var resilience ResilienceProfile
	if err := json.Unmarshal(snapshot.Configs["generated.resilience"], &resilience); err != nil {
		t.Fatalf("decode generated.resilience config: %v", err)
	}
	if !resilience.Timeout || !resilience.RateLimit || !resilience.Concurrency || !resilience.Breaker || !resilience.Retry || !resilience.RESTEnabled {
		t.Fatalf("generated resilience profile = %+v, want timeout/rate/concurrency/breaker/retry REST profile", resilience)
	}
	if string(snapshot.Configs["generated.rest"]) == "" || strings.Contains(string(snapshot.Configs["generated.rest"]), "change-me-admin-token") {
		t.Fatalf("generated.rest config = %s, want sanitized runtime policy without admin token", snapshot.Configs["generated.rest"])
	}
	if len(snapshot.Services) != 1 || snapshot.Services[0].Name != "hello" || len(snapshot.Services[0].Endpoints) != 1 || snapshot.Services[0].Endpoints[0].Metadata["transport"] != "rest" {
		t.Fatalf("snapshot services = %#v, want generated rest endpoint", snapshot.Services)
	}
}

func serviceConfFixture(name string, governance ...app.ServiceGovernance) app.ServiceConf {
	conf := app.ServiceConf{Name: name}
	if len(governance) > 0 {
		conf.Governance = governance[0]
	}
	return conf
}

func boolPtr(v bool) *bool { return &v }
`

const configTestTemplate = configTestHeaderTemplate + configCommonTestsTemplate

const rpcConfigTestTemplate = configTestHeaderTemplate + rpcMuxConfigValidationTestTemplate + configCommonTestsTemplate

const configDiscoveryTestTemplate = `package config

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/rest"
)

func TestDiscoveryConfigDefaultsValidationAndSnapshot(t *testing.T) {
	defaultConfig := DiscoveryConfig{}
	if defaultConfig.ProviderName() != "memory" || defaultConfig.RegistryTTL().String() != "15s" || defaultConfig.DialTimeoutDuration().String() != "5s" {
		t.Fatalf("default discovery config = provider %q ttl %s dial %s", defaultConfig.ProviderName(), defaultConfig.RegistryTTL(), defaultConfig.DialTimeoutDuration())
	}
	if got := defaultConfig.RegisterOptions(); len(got) != 1 {
		t.Fatalf("default register options = %d, want ttl option", len(got))
	}
	resolved := (DiscoveryConfig{Address: " 127.0.0.1:2379, ,127.0.0.2:2379 "}).ResolvedEndpoints()
	if strings.Join(resolved, ",") != "127.0.0.1:2379,127.0.0.2:2379" {
		t.Fatalf("resolved discovery endpoints = %v, want trimmed non-empty endpoints", resolved)
	}
	if err := ValidateDiscoveryConfig(defaultConfig); err != nil {
		t.Fatalf("ValidateDiscoveryConfig default: %v", err)
	}

	if err := ValidateDiscoveryConfig(DiscoveryConfig{Provider: "etcdv3"}); err == nil || !strings.Contains(err.Error(), "endpoints are required") {
		t.Fatalf("ValidateDiscoveryConfig etcdv3 without endpoints = %v, want endpoints error", err)
	}
	if err := ValidateDiscoveryConfig(DiscoveryConfig{Provider: "etcdv3", Endpoints: []string{" ", ""}, Address: " , "}); err == nil || !strings.Contains(err.Error(), "endpoints are required") {
		t.Fatalf("ValidateDiscoveryConfig etcdv3 with blank endpoints = %v, want endpoints error", err)
	}
	if err := ValidateDiscoveryConfig(DiscoveryConfig{Provider: "unsupported"}); err == nil || !strings.Contains(err.Error(), "unsupported discovery provider") {
		t.Fatalf("ValidateDiscoveryConfig unsupported provider = %v", err)
	}
	if err := ValidateDiscoveryConfig(DiscoveryConfig{Provider: "consul", TTL: "bad"}); err == nil || !strings.Contains(err.Error(), "discovery ttl") {
		t.Fatalf("ValidateDiscoveryConfig invalid ttl = %v", err)
	}
}

func TestControlPlaneSnapshotWithDiscoveryIncludesRegistryAndSanitizesDiscovery(t *testing.T) {
	cfg := Config{
		Environment: "development",
		Service:     app.ServiceConf{Name: "hello"},
		Scaffold:    ScaffoldConfig{Features: []string{"ecosystem-compat"}},
		Discovery:   DiscoveryConfig{Provider: "consul", Address: "127.0.0.1:8500", TokenEnv: " CONSUL_HTTP_TOKEN ", UsernameEnv: " ETCD_USER ", PasswordEnv: " ETCD_PASS "},
		Rest:        rest.Config{Name: "hello", Host: "127.0.0.1", Port: 8080},
	}
	registry := discovery.NewMemoryRegistry()
	if _, err := registry.Register(context.Background(), discovery.Instance{ID: "hello-rpc", Service: "greeter", Endpoint: "http://127.0.0.1:8081", Metadata: map[string]string{"transport": "rpc"}}); err != nil {
		t.Fatalf("register discovery instance: %v", err)
	}

	snapshot, err := cfg.ControlPlaneSnapshotWithDiscovery(context.Background(), registry)
	if err != nil {
		t.Fatalf("ControlPlaneSnapshotWithDiscovery: %v", err)
	}
	if snapshot.Checksum == "" || snapshot.Metadata["generated.project.runtime"] != "service,rest,rpc,governance,discovery" {
		t.Fatalf("snapshot checksum/metadata = %q/%#v", snapshot.Checksum, snapshot.Metadata)
	}
	if len(snapshot.Services) != 2 {
		t.Fatalf("snapshot services = %#v, want generated REST service and discovery service", snapshot.Services)
	}
	foundDiscovery := false
	for _, service := range snapshot.Services {
		if service.Name == "greeter" && len(service.Endpoints) == 1 && service.Endpoints[0].Metadata["meta.transport"] == "rpc" {
			foundDiscovery = true
		}
	}
	if !foundDiscovery {
		t.Fatalf("snapshot services = %#v, want discovery registry service", snapshot.Services)
	}

	var discoveryConfig DiscoveryConfig
	if err := json.Unmarshal(snapshot.Configs["generated.discovery"], &discoveryConfig); err != nil {
		t.Fatalf("decode generated.discovery config: %v", err)
	}
	if discoveryConfig.TokenEnv != "CONSUL_HTTP_TOKEN" || discoveryConfig.UsernameEnv != "ETCD_USER" || discoveryConfig.PasswordEnv != "ETCD_PASS" {
		t.Fatalf("sanitized discovery config = %#v, want trimmed secret env names", discoveryConfig)
	}
}
`

const smokeTestTemplate = `package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/rest"
)

func TestGeneratedProductionServiceSmoke(t *testing.T) {
	if os.Getenv("GOFLY_SKIP_GENERATED_SMOKE") == "true" {
		t.Skip("generated service smoke test disabled by GOFLY_SKIP_GENERATED_SMOKE")
	}
	repo := generatedProjectRoot(t)
	restAddr := reserveLocalAddr(t)
	rpcAddr := reserveLocalAddr(t)
	adminAddr := reserveLocalAddr(t)
	rewriteSmokeConfig(t, repo, restAddr, rpcAddr, adminAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/{{.Name}}")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GOFLAGS=-count=1")
	cmd.WaitDelay = 3 * time.Second
	output := strings.Builder{}
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start generated service: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Logf("generated service process did not exit cleanly after kill; service output:\n%s", output.String())
			}
		}
	})

	waitHTTPStatus(t, ctx, "http://"+restAddr+"/healthz", http.StatusOK, &output)
	waitOpenAPI(t, ctx, "http://"+restAddr+"/openapi.json", &output)
	assertInvalidRequestEnvelope(t)
	controlPlane := waitControlPlane(t, ctx, "http://"+adminAddr+"/admin/control-plane", &output)
	metadata, ok := controlPlane["metadata"].(map[string]any)
	if !ok || metadata["generated.project"] != "available" || metadata["generated.project.runtime"] != "service,rest,rpc,governance,discovery" {
		t.Fatalf("control-plane metadata = %#v, want generated project runtime markers", metadata)
	}
	if metadata["generated.project.resilience"] != "timeout,rate,concurrency,breaker,retry" {
		t.Fatalf("control-plane resilience metadata = %#v, want generated resilience marker", metadata)
	}
	assertControlPlaneResilience(t, controlPlane)
}

func generatedProjectRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate smoke test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func reserveLocalAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve local port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

func rewriteSmokeConfig(t *testing.T, repo string, restAddr string, rpcAddr string, adminAddr string) {
	t.Helper()
	path := filepath.Join(repo, "etc", "{{.Name}}.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	restHost, restPort, err := net.SplitHostPort(restAddr)
	if err != nil {
		t.Fatalf("split rest addr: %v", err)
	}
	content := string(data)
	content = strings.Replace(content, ` + "`\"host\": \"127.0.0.1\"`" + `, fmt.Sprintf(` + "`\"host\": %q`" + `, restHost), 1)
	content = strings.Replace(content, ` + "`\"port\": 8080`" + `, ` + "`\"port\": `" + `+restPort, 1)
	content = strings.Replace(content, ` + "`\"addr\": \"127.0.0.1:9090\"`" + `, fmt.Sprintf(` + "`\"addr\": %q`" + `, adminAddr), 1)
	content = strings.Replace(content, ` + "`\"addr\": \":8081\"`" + `, fmt.Sprintf(` + "`\"addr\": %q`" + `, rpcAddr), 1)
	content = strings.Replace(content, ` + "`\"advertise\": \"http://127.0.0.1:8081\"`" + `, fmt.Sprintf(` + "`\"advertise\": %q`" + `, "http://"+rpcAddr), 1)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write generated smoke config: %v", err)
	}
}

func waitHTTPStatus(t *testing.T, ctx context.Context, url string, want int, output *strings.Builder) {
	t.Helper()
	client := http.Client{Timeout: time.Second}
	for ctx.Err() == nil {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == want {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s did not return %d before timeout; service output:\n%s", url, want, output.String())
}

func waitOpenAPI(t *testing.T, ctx context.Context, url string, output *strings.Builder) {
	t.Helper()
	client := http.Client{Timeout: time.Second}
	for ctx.Err() == nil {
		resp, err := client.Get(url)
		if err == nil {
			var doc map[string]any
			decodeErr := json.NewDecoder(resp.Body).Decode(&doc)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil && doc["openapi"] != "" {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s did not return OpenAPI JSON before timeout; service output:\n%s", url, output.String())
}

func assertInvalidRequestEnvelope(t *testing.T) {
	t.Helper()
	rec := httptest.NewRecorder()
	rest.WriteError(rec, coreerrors.New(coreerrors.CodeInvalidArgument, "invalid request"))
	var envelope rest.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode invalid request rest.ErrorResponse: %v", err)
	}
	if rec.Code != http.StatusBadRequest || envelope.Code != coreerrors.CodeInvalidArgument || envelope.Status != http.StatusBadRequest {
		t.Fatalf("invalid request envelope = status %d body %+v, want rest.ErrorResponse invalid_argument", rec.Code, envelope)
	}
}

func waitControlPlane(t *testing.T, ctx context.Context, url string, output *strings.Builder) map[string]any {
	t.Helper()
	client := http.Client{Timeout: time.Second}
	for ctx.Err() == nil {
		resp, err := client.Get(url)
		if err == nil {
			var snapshot map[string]any
			decodeErr := json.NewDecoder(resp.Body).Decode(&snapshot)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil {
				return snapshot
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s did not return a control-plane snapshot before timeout; service output:\n%s", url, output.String())
	return nil
}

func assertControlPlaneResilience(t *testing.T, snapshot map[string]any) {
	t.Helper()
	configs, ok := snapshot["configs"].(map[string]any)
	if !ok {
		t.Fatalf("control-plane configs = %#v, want generated configs", snapshot["configs"])
	}
	resilience, ok := configs["generated.resilience"].(map[string]any)
	if !ok {
		t.Fatalf("generated.resilience config = %#v, want resilience profile", configs["generated.resilience"])
	}
	for _, key := range []string{"timeout", "rateLimit", "concurrency", "breaker", "retry", "adaptiveLimit", "restEnabled", "rpcEnabled", "gatewayEnabled"} {
		if resilience[key] != true {
			t.Fatalf("generated resilience[%s] = %#v in %#v, want true", key, resilience[key], resilience)
		}
	}
}
`

const mqBrokerTemplate = `package mq

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/kv/redis"
	coremq "github.com/imajinyun/gofly/core/mq"
	"github.com/imajinyun/gofly/core/mq/kafka"
	"github.com/imajinyun/gofly/core/mq/rabbitmq"
	"github.com/imajinyun/gofly/core/mq/redisstream"

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

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/rest"
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

type ResilienceProfile struct {
	Timeout        bool ` + "`json:\"timeout\"`" + `
	RateLimit      bool ` + "`json:\"rateLimit\"`" + `
	Concurrency    bool ` + "`json:\"concurrency\"`" + `
	Breaker        bool ` + "`json:\"breaker\"`" + `
	Retry          bool ` + "`json:\"retry\"`" + `
	AdaptiveLimit  bool ` + "`json:\"adaptiveLimit\"`" + `
	RESTEnabled    bool ` + "`json:\"restEnabled\"`" + `
	RPCEnabled     bool ` + "`json:\"rpcEnabled\"`" + `
	GatewayEnabled bool ` + "`json:\"gatewayEnabled\"`" + `
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

func (c Config) ResilienceProfile() ResilienceProfile {
	service := c.ServiceConf()
	governance := service.Governance
	return ResilienceProfile{
		Timeout:       governance.Timeout > 0,
		RateLimit:     governance.RateLimit.Rate > 0 && governance.RateLimit.Burst > 0,
		Concurrency:   governance.MaxConcurrency > 0,
		Breaker:       governance.Breaker,
		Retry:         governance.Retry.Attempts > 0,
		AdaptiveLimit: governance.AdaptiveLimit,
		RESTEnabled:   c.Rest.Middlewares.Timeout && c.Rest.Middlewares.RateLimit && c.Rest.Middlewares.MaxConcurrency && c.Rest.Middlewares.Breaker,
	}
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
	if err := addGeneratedControlPlaneConfig("resilience", cfg.ResilienceProfile()); err != nil {
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
	snapshot.Metadata["generated.project.resilience"] = "timeout,rate,concurrency,breaker,retry"
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
  labels:
    app.kubernetes.io/name: {{.Name}}
    app.kubernetes.io/managed-by: gofly
spec:
  replicas: {{.Replicas}}
{{.RevisionHistory}}  selector:
    matchLabels:
      app: {{.Name}}
  template:
    metadata:
      labels:
        app: {{.Name}}
        app.kubernetes.io/name: {{.Name}}
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
          startupProbe:
            httpGet:
              path: /healthz
              port: http
            failureThreshold: 30
            periodSeconds: 2
---
apiVersion: v1
kind: Service
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/name: {{.Name}}
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
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}-config
  namespace: {{.Namespace}}
data:
{{.Data}}
---
apiVersion: v1
kind: Secret
metadata:
  name: {{.Name}}-secret
  namespace: {{.Namespace}}
type: Opaque
stringData:
  admin-token: change-me-admin-token
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: {{.Name}}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  podSelector:
    matchLabels:
      app: {{.Name}}
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector: {}
      ports:
        - protocol: TCP
          port: {{.Port}}
        - protocol: TCP
          port: {{.RPCPort}}
  egress:
    - to:
        - namespaceSelector: {}
{{.Autoscale}}
`

const helmChartTemplate = `apiVersion: v2
name: {{.Name}}
description: Gofly production service chart for {{.Name}}
type: application
version: 0.1.0
appVersion: "0.1.0"
`

const helmValuesTemplate = `replicaCount: {{.Replicas}}

image:
  repository: {{.Name}}
  tag: latest
  pullPolicy: IfNotPresent

serviceAccount:
  create: true
  name: ""

service:
  type: ClusterIP
  httpPort: {{.Port}}
  rpcPort: {{.RPCPort}}

probes:
  readiness:
    path: /healthz
    initialDelaySeconds: 3
    periodSeconds: 5
  liveness:
    path: /healthz
    initialDelaySeconds: 10
    periodSeconds: 10
  startup:
    path: /healthz
    failureThreshold: 30
    periodSeconds: 2

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi

autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 6
  targetCPUUtilizationPercentage: 80

podDisruptionBudget:
  enabled: true
  minAvailable: 1

networkPolicy:
  enabled: true

config:
  app.json: |
    {}

secret:
  adminToken: change-me-admin-token

serviceMonitor:
  enabled: true
  interval: 30s
  path: /admin/metrics
`

const helmWorkloadTemplate = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.Name}}
  labels:
    app.kubernetes.io/name: {{.Name}}
    app.kubernetes.io/managed-by: Helm
spec:
  replicas: {{.Replicas}}
  selector:
    matchLabels:
      app.kubernetes.io/name: {{.Name}}
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{.Name}}
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/path: /admin/metrics
        prometheus.io/port: "9090"
    spec:
      containers:
        - name: {{.Name}}
          image: {{.Image}}
          ports:
            - name: http
              containerPort: {{.Port}}
            - name: rpc
              containerPort: {{.RPCPort}}
          env:
            - name: GOFLY_ADMIN_TOKEN
              valueFrom:
                secretKeyRef:
                  name: {{.Name}}-secret
                  key: admin-token
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
{{.Resources}}---
apiVersion: v1
kind: Service
metadata:
  name: {{.Name}}
  labels:
    app.kubernetes.io/name: {{.Name}}
spec:
  selector:
    app.kubernetes.io/name: {{.Name}}
  ports:
    - name: http
      port: {{.Port}}
      targetPort: http
    - name: rpc
      port: {{.RPCPort}}
      targetPort: rpc
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}-config
data:
{{.Data}}
---
apiVersion: v1
kind: Secret
metadata:
  name: {{.Name}}-secret
type: Opaque
stringData:
  admin-token: change-me-admin-token
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{.Name}}
  labels:
    app.kubernetes.io/name: {{.Name}}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: {{.Name}}
  endpoints:
    - port: http
      path: /admin/metrics
      interval: 30s
`

const prometheusStackTemplate = `apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/name: {{.Name}}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: {{.Name}}
  endpoints:
    - port: http
      path: /admin/metrics
      interval: 30s
---
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  groups:
    - name: {{.Name}}.slo
      rules:
        - alert: GoflyHighErrorRate
          expr: sum(rate(http_requests_total{service="{{.Name}}",code=~"5.."}[5m])) / sum(rate(http_requests_total{service="{{.Name}}"}[5m])) > 0.05
          for: 10m
          labels:
            severity: warning
          annotations:
            summary: {{.Name}} high 5xx error rate
        - alert: GoflyHighP99Latency
          expr: histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{service="{{.Name}}"}[5m])) by (le)) > 1
          for: 10m
          labels:
            severity: warning
          annotations:
            summary: {{.Name}} p99 latency is above 1s
`

const otelCollectorTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}-otel-collector
  namespace: {{.Namespace}}
data:
  otel-collector-config.yaml: |
    receivers:
      otlp:
        protocols:
          grpc:
          http:
    processors:
      batch:
      resource:
        attributes:
          - key: service.name
            value: {{.Name}}
            action: upsert
    exporters:
      logging:
        verbosity: basic
      otlp:
        endpoint: tempo:4317
        tls:
          insecure: true
    service:
      pipelines:
        traces:
          receivers: [otlp]
          processors: [resource, batch]
          exporters: [logging, otlp]
        metrics:
          receivers: [otlp]
          processors: [resource, batch]
          exporters: [logging]
`

const grafanaDashboardTemplate = `{
  "title": "{{.Name}} / Gofly Production",
  "tags": ["gofly", "{{.Name}}", "production"],
  "timezone": "browser",
  "schemaVersion": 39,
  "version": 1,
  "panels": [
    {"type": "timeseries", "title": "HTTP RPS", "targets": [{"expr": "sum(rate(http_requests_total{service=\"{{.Name}}\"}[5m]))"}]},
    {"type": "timeseries", "title": "HTTP Error Rate", "targets": [{"expr": "sum(rate(http_requests_total{service=\"{{.Name}}\",code=~\"5..\"}[5m])) / sum(rate(http_requests_total{service=\"{{.Name}}\"}[5m]))"}]},
    {"type": "timeseries", "title": "P99 Latency", "targets": [{"expr": "histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{service=\"{{.Name}}\"}[5m])) by (le))"}]},
    {"type": "logs", "title": "Logs by trace_id", "targets": [{"expr": "{service=\"{{.Name}}\"} | json | trace_id != \"\""}]}
  ]
}
`

const logsCorrelationTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}-logs-correlation
  namespace: {{.Namespace}}
data:
  promtail.yaml: |
    pipeline_stages:
      - json:
          expressions:
            trace_id: trace_id
            span_id: span_id
            level: level
            msg: msg
      - labels:
          trace_id:
          span_id:
          level:
      - output:
          source: msg
  loki-derived-fields.yaml: |
    derivedFields:
      - name: TraceID
        matcherRegex: 'trace_id=(\\w+)'
        url: '$${__value.raw}'
        datasourceUid: tempo
`

const productionCheckScriptTemplate = `#!/usr/bin/env sh
set -eu

service_name="{{.Name}}"
config_file="${1:-etc/{{.Name}}.json}"

fail() {
  printf 'production-check failed: %s\n' "$1" >&2
  exit 1
}

[ -f "$config_file" ] || fail "missing config file: $config_file"
[ -f "deploy/k8s/{{.Name}}.yaml" ] || fail "missing k8s production manifest"
[ -f "deploy/helm/values.yaml" ] || fail "missing helm values"
[ -f "deploy/helm/templates/workload.yaml" ] || fail "missing helm workload template"
[ -f "deploy/observability/prometheus.yaml" ] || fail "missing prometheus rules"
[ -f "deploy/observability/otel-collector.yaml" ] || fail "missing otel collector config"
[ -f "deploy/observability/grafana-dashboard.json" ] || fail "missing grafana dashboard"
[ -f "deploy/observability/logs-correlation.yaml" ] || fail "missing log correlation config"

grep -q '"securityHeaders"' "$config_file" || fail "rest security headers are not configured"
grep -q '"tls"' "$config_file" || printf 'production-check warning: tls is expected to be terminated by ingress or configured in rest.tls\n' >&2
grep -q '"admin"' "$config_file" || fail "admin control-plane config is missing"
grep -q 'change-me-admin-token' "$config_file" && fail "replace placeholder admin token before production"
grep -q 'kind: NetworkPolicy' "deploy/k8s/{{.Name}}.yaml" || fail "network policy is missing"
grep -q 'kind: PodDisruptionBudget' "deploy/k8s/{{.Name}}.yaml" || fail "pod disruption budget is missing"
grep -q 'kind: HorizontalPodAutoscaler' "deploy/k8s/{{.Name}}.yaml" || fail "horizontal pod autoscaler is missing"
grep -q 'ServiceMonitor' "deploy/helm/templates/workload.yaml" || fail "helm serviceMonitor is missing"
grep -q 'Logs by trace_id' "deploy/observability/grafana-dashboard.json" || fail "grafana trace log panel is missing"
grep -q 'trace_id' "deploy/observability/logs-correlation.yaml" || fail "log trace correlation is missing"

printf '%s production checklist passed\n' "$service_name"
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

const makefileTemplate = `.PHONY: test race build run production-check

test:
	go test ./...

race:
	go test -race ./...

build:
	go build ./cmd/{{.Name}}

run:
	go run ./cmd/{{.Name}}

production-check:
	sh ./bin/production-check.sh
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

	"github.com/imajinyun/gofly/core/mq"
	"github.com/imajinyun/gofly/rpc"
	"{{.Module}}/internal/config"
)

type RPCMuxDiagnosisClient interface {
	UpdateMuxDiagnosisEventExporter(rpc.RPCMuxDiagnosisEventExporter, rpc.RPCMuxDiagnosisFilter)
}

type ServiceContext struct {
	mu         sync.RWMutex
	Config     config.Config
	MQ         mq.Broker
	rpcClients []RPCMuxDiagnosisClient
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

func (s *ServiceContext) RegisterRPCClient(client RPCMuxDiagnosisClient) func() {
	if s == nil || client == nil {
		return func() {}
	}
	s.mu.Lock()
	s.rpcClients = append(s.rpcClients, client)
	s.mu.Unlock()
	return func() {
		s.UnregisterRPCClient(client)
	}
}

func (s *ServiceContext) UnregisterRPCClient(client RPCMuxDiagnosisClient) {
	if s == nil || client == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, registered := range s.rpcClients {
		if registered == client {
			s.rpcClients = append(s.rpcClients[:index], s.rpcClients[index+1:]...)
			return
		}
	}
}

func (s *ServiceContext) UpdateRPCMuxDiagnosisExporters(exporter rpc.RPCMuxDiagnosisEventExporter, filter rpc.RPCMuxDiagnosisFilter) {
	if s == nil {
		return
	}
	s.mu.RLock()
	clients := append([]RPCMuxDiagnosisClient(nil), s.rpcClients...)
	s.mu.RUnlock()
	for _, client := range clients {
		client.UpdateMuxDiagnosisEventExporter(exporter, filter)
	}
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

	"github.com/imajinyun/gofly/rpc"
	"{{.Module}}/internal/config"
)

type RPCMuxDiagnosisClient interface {
	UpdateMuxDiagnosisEventExporter(rpc.RPCMuxDiagnosisEventExporter, rpc.RPCMuxDiagnosisFilter)
}

type ServiceContext struct {
	mu         sync.RWMutex
	Config     config.Config
	rpcClients []RPCMuxDiagnosisClient
}

func NewServiceContext(c config.Config) *ServiceContext {
	return &ServiceContext{Config: c}
}

func (s *ServiceContext) UpdateConfig(c config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config = c
}

func (s *ServiceContext) RegisterRPCClient(client RPCMuxDiagnosisClient) func() {
	if s == nil || client == nil {
		return func() {}
	}
	s.mu.Lock()
	s.rpcClients = append(s.rpcClients, client)
	s.mu.Unlock()
	return func() {
		s.UnregisterRPCClient(client)
	}
}

func (s *ServiceContext) UnregisterRPCClient(client RPCMuxDiagnosisClient) {
	if s == nil || client == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, registered := range s.rpcClients {
		if registered == client {
			s.rpcClients = append(s.rpcClients[:index], s.rpcClients[index+1:]...)
			return
		}
	}
}

func (s *ServiceContext) UpdateRPCMuxDiagnosisExporters(exporter rpc.RPCMuxDiagnosisEventExporter, filter rpc.RPCMuxDiagnosisFilter) {
	if s == nil {
		return
	}
	s.mu.RLock()
	clients := append([]RPCMuxDiagnosisClient(nil), s.rpcClients...)
	s.mu.RUnlock()
	for _, client := range clients {
		client.UpdateMuxDiagnosisEventExporter(exporter, filter)
	}
}

func (s *ServiceContext) CurrentConfig() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Config
}
`

const goZeroTypesTemplate = `package types

type PingRequest struct {
	Name string ` + "`json:\"name,optional\" form:\"name,optional\"`" + `
}

type PingResponse struct {
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

func (l *PingLogic) Ping(req *types.PingRequest) (*types.PingResponse, error) {
	name := "world"
	if req != nil && strings.TrimSpace(req.Name) != "" {
		name = strings.TrimSpace(req.Name)
	}
	return &types.PingResponse{Message: "hello " + name}, nil
}
`

const goZeroPingHandlerTemplate = `package handler

import (
	"net/http"

	"github.com/imajinyun/gofly/rest"

	"{{.Module}}/internal/logic"
	"{{.Module}}/internal/svc"
	"{{.Module}}/internal/types"
)

func PingHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var req types.PingRequest
		if err := ctx.BindQuery(&req); err != nil {
			ctx.Error(err)
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

	"github.com/imajinyun/gofly/rest"
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
	"github.com/imajinyun/gofly/rest"
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

	"github.com/imajinyun/gofly/rest"
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
			ctx.Error(err)
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

	"github.com/imajinyun/gofly/rest"
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

	"github.com/imajinyun/gofly/rest"
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
			ctx.Error(err)
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
	"github.com/imajinyun/gofly/rest"
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
	"github.com/imajinyun/gofly/rest"
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

	"github.com/imajinyun/gofly/rest"
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

	"github.com/imajinyun/gofly/rpc"
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
	"time"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rpc"
	"{{.Module}}/internal/config"
	"{{.Module}}/internal/svc"
)

func TestGreeterRPCClient(t *testing.T) {
	cfg := config.Config{Service: generatedServiceConfFixture()}
	serviceConf := cfg.ServiceConf()
	server := rpc.NewServer(serviceConf.RPCServerOptions()...)
	svcCtx := svc.NewServiceContext(cfg)
	if err := server.RegisterService(GreeterService(svcCtx), nil); err != nil {
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
	clientOptions := append(serviceConf.RPCClientOptions(),
		rpc.WithResolver(registry.Resolver("greeter")),
		rpc.WithBalancer(rpc.NewHealthBalancer()),
	)
	client, err := rpc.NewClient(httpServer.URL, clientOptions...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	unregisterClient := svcCtx.RegisterRPCClient(client)
	defer unregisterClient()
	records := make(chan rpc.RPCMuxDiagnosisEventRecord, 1)
	svcCtx.UpdateRPCMuxDiagnosisExporters(rpc.RPCMuxDiagnosisEventExporterFunc(func(_ context.Context, record rpc.RPCMuxDiagnosisEventRecord) {
		records <- record
	}), rpc.RPCMuxDiagnosisFilter{EventFamily: "flow_control", Event: "write_timeout"})
	client.ObserveMuxDiagnosis(context.Background(), rpc.RPCDiagnosisProbe{
		Target: httpServer.URL,
		Method: "greeter/Watch",
		Matched: true,
		Diagnosis: rpc.RPCDiagnosisSnapshot{Mux: rpc.RPCMuxTransportDiagnosis{
			FlowControl: rpc.RPCMuxFlowControlDiagnosis{WriteTimeouts: 1},
		}},
	})
	select {
	case record := <-records:
		if record.Event.Event != "write_timeout" {
			t.Fatalf("registered client exporter record = %+v, want write_timeout", record)
		}
	case <-time.After(time.Second):
		t.Fatal("registered client did not receive mux diagnosis exporter update")
	}
	unregisterClient()
	unregisteredRecords := make(chan rpc.RPCMuxDiagnosisEventRecord, 1)
	svcCtx.UpdateRPCMuxDiagnosisExporters(rpc.RPCMuxDiagnosisEventExporterFunc(func(_ context.Context, record rpc.RPCMuxDiagnosisEventRecord) {
		unregisteredRecords <- record
	}), rpc.RPCMuxDiagnosisFilter{EventFamily: "flow_control", Event: "write_timeout"})
	client.ObserveMuxDiagnosis(context.Background(), rpc.RPCDiagnosisProbe{
		Target: httpServer.URL,
		Method: "greeter/Watch",
		Matched: true,
		Diagnosis: rpc.RPCDiagnosisSnapshot{Mux: rpc.RPCMuxTransportDiagnosis{
			FlowControl: rpc.RPCMuxFlowControlDiagnosis{WriteTimeouts: 1},
		}},
	})
	select {
	case record := <-unregisteredRecords:
		t.Fatalf("unregistered client received exporter update: %+v", record)
	default:
	}
	runtimeState := client.PolicyRuntimeSnapshot().State
	if !runtimeState.TimeoutEnforced || runtimeState.EffectiveTimeout != 3*time.Second {
		t.Fatalf("rpc client timeout state = %+v, want generated service timeout", runtimeState)
	}
	if runtimeState.RetryAttempts != 2 || runtimeState.RetryBackoff != 100*time.Millisecond {
		t.Fatalf("rpc client retry state = %+v, want generated service retry profile", runtimeState)
	}
	if !runtimeState.BreakerEnabled || runtimeState.Balancer != rpc.RPCBalancerHealth {
		t.Fatalf("rpc client resilience state = %+v, want breaker/health balancer", runtimeState)
	}
	clientRuntime := client.RuntimeSnapshot()
	if clientRuntime.Middlewares.Unary == 0 || clientRuntime.Middlewares.Stream == 0 {
		t.Fatalf("rpc client middleware state = %+v, want generated governance middleware", clientRuntime.Middlewares)
	}
	if clientRuntime.Transport.Timeout != 30*time.Second {
		t.Fatalf("rpc client transport timeout = %s, want generated transport timeout", clientRuntime.Transport.Timeout)
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

func generatedServiceConfFixture() app.ServiceConf {
	return app.ServiceConf{
		Name:        "hello",
		Mode:        "dev",
		Environment: "development",
		Governance: app.ServiceGovernance{
			Timeout:           3 * time.Second,
			ReadHeaderTimeout: 3 * time.Second,
			Breaker:           true,
			Retry:             app.ServiceRetry{Attempts: 2, Backoff: 100 * time.Millisecond},
			RateLimit:         app.ServiceRateLimit{Rate: 100, Burst: 100},
			MaxConcurrency:    64,
			AdaptiveLimit:     true,
			RPCTimeout:        rpc.RPCTimeoutConfig{Server: 3 * time.Second, Client: 3 * time.Second},
			RPCTransport: rpc.TransportConfig{
				Timeout:               30 * time.Second,
				MaxIdleConns:          200,
				MaxIdleConnsPerHost:   100,
				DialTimeout:           30 * time.Second,
				KeepAlive:             30 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: time.Second,
			},
		},
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

	"github.com/imajinyun/gofly/rest"
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

	"github.com/imajinyun/gofly/rpc"
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
