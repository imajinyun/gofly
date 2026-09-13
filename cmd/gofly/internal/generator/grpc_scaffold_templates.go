package generator

const goZeroRPCNewTemplate = `syntax = "proto3";

package {{.Name}};

option go_package = "{{.Module}}/internal/pb;pb";

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

const goZeroRPCConfigTemplate = `{
  "name": "{{.RPCService}}",
  "listenOn": "0.0.0.0:8081",
  "advertise": "",
  "adminListenOn": "127.0.0.1:9090",
  "environment": "development",
  "reflection": false,
  "ruleFile": "etc/governance.json",
  "ruleWatch": false,
  "adminTokenEnv": "",
  "authTokenEnv": "",
  "tls": {},
  "telemetry": {"enabled": false},
  "logJSON": true,
  "loadBalancing": {"policy": "gofly_p2c_ewma"},
  "rules": [{"name": "greeter-timeout", "transport": "rpc", "method": "SayHello", "policy": {"timeout": 2000000000}}],
  "discovery": {
    "provider": "memory",
    "ttl": "15s",
    "prefix": "/gofly/services",
    "dialTimeout": "5s"
  }
}
`

const goZeroRPCConfigGoTemplate = `package config

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/observability/trace"
	"github.com/imajinyun/gofly/core/security"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"
)

type Config struct {
	Name          string          ` + "`json:\"name\"`" + `
	ListenOn      string          ` + "`json:\"listenOn\"`" + `
	Advertise     string          ` + "`json:\"advertise,omitempty\"`" + `
	AdminListenOn string          ` + "`json:\"adminListenOn,omitempty\"`" + `
	Reflection    bool            ` + "`json:\"reflection,omitempty\"`" + `
	Discovery      DiscoveryConfig      ` + "`json:\"discovery\"`" + `
	LoadBalancing  LoadBalancingConfig  ` + "`json:\"loadBalancing,omitempty\"`" + `
	Rules         []governance.Rule ` + "`json:\"rules,omitempty\"`" + `
	Environment   string ` + "`json:\"environment,omitempty\"`" + `
	RuleFile      string ` + "`json:\"ruleFile,omitempty\"`" + `
	RuleWatch     bool ` + "`json:\"ruleWatch,omitempty\"`" + `
	AdminTokenEnv string ` + "`json:\"adminTokenEnv,omitempty\"`" + `
	AuthTokenEnv  string ` + "`json:\"authTokenEnv,omitempty\"`" + `
	TLS           security.TLSConfig ` + "`json:\"tls,omitempty\"`" + `
	Telemetry     trace.AgentConfig ` + "`json:\"telemetry,omitempty\"`" + `
	LogJSON       bool ` + "`json:\"logJSON,omitempty\"`" + `
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

type LoadBalancingConfig struct {
	Policy string ` + "`json:\"policy,omitempty\"`" + `
}

func Validate(c Config) error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("rpc service name is required")
	}
	if strings.TrimSpace(c.ListenOn) == "" {
		return errors.New("rpc listen address is required")
	}
	if c.Discovery.ProviderName() == "etcdv3" && len(c.Discovery.ResolvedEndpoints()) == 0 {
		return errors.New("discovery endpoints are required for etcdv3")
	}
	if c.Discovery.ProviderName() != "memory" && strings.TrimSpace(c.Advertise) == "" {
		return errors.New("rpc advertise address is required for external discovery")
	}
	if c.Environment != "" && c.Environment != "development" && c.Environment != "production" {
		return errors.New("environment must be development or production")
	}
	if (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") || (c.TLS.ClientCAFile != "" && !c.TLS.Enabled()) {
		return errors.New("TLS requires both certFile and keyFile")
	}
	if c.Environment == "production" && (!c.TLS.Enabled() || c.Reflection) {
		return errors.New("production requires TLS and disabled reflection")
	}
	if c.RuleWatch && c.RuleFile == "" { return errors.New("ruleWatch requires ruleFile") }
	if _, err := c.LoadBalancing.ResolverOption(); err != nil { return err }
	for _, name := range []string{c.AdminTokenEnv, c.AuthTokenEnv} {
		if name != "" && strings.TrimSpace(os.Getenv(name)) == "" { return errors.New("configured token environment variable is empty") }
	}
	if c.AuthTokenEnv != "" && !c.TLS.Enabled() { return errors.New("RPC token authentication requires TLS") }
	if c.AdminListenOn != "" {
		host, _, err := net.SplitHostPort(c.AdminListenOn)
		if err != nil { return errors.New("invalid admin listen address") }
		if (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) && c.AdminTokenEnv == "" {
			return errors.New("non-loopback admin requires adminTokenEnv and a protected network")
		}
	}
	return governance.ValidateRules(c.Rules...)
}

func (c LoadBalancingConfig) PolicyName() string {
	policy := strings.ToLower(strings.TrimSpace(c.Policy))
	if policy == "" { return flygrpc.P2CEWMABalancerName }
	return policy
}

func (c LoadBalancingConfig) ResolverOption() (flygrpc.ResolverOption, error) {
	switch policy := c.PolicyName(); policy {
	case "round_robin":
		return flygrpc.WithRoundRobinResolver(), nil
	case flygrpc.P2CEWMABalancerName:
		return flygrpc.WithP2CEWMAResolver(), nil
	case flygrpc.ConsistentHashBalancerName:
		return flygrpc.WithConsistentHashResolver(), nil
	default:
		return nil, errors.New("unsupported gRPC load-balancing policy " + policy)
	}
}

func ResolveConfigPath(name string) string {
	for _, path := range []string{"config.json", filepath.Join("etc", strings.TrimSpace(name)+".json")} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join("etc", strings.TrimSpace(name)+".json")
}

func (c DiscoveryConfig) ProviderName() string {
	provider := strings.ToLower(strings.TrimSpace(c.Provider))
	if provider == "" {
		return "memory"
	}
	return provider
}

func (c DiscoveryConfig) ResolvedEndpoints() []string {
	out := make([]string, 0, len(c.Endpoints)+1)
	values := append(append([]string(nil), c.Endpoints...), strings.Split(c.Address, ",")...)
	for _, endpoint := range values {
		if endpoint = strings.TrimSpace(endpoint); endpoint != "" {
			out = append(out, endpoint)
		}
	}
	return out
}

func (c DiscoveryConfig) RegistryTTL() time.Duration {
	return parseDuration(c.TTL, 15*time.Second)
}

func (c DiscoveryConfig) DialTimeoutDuration() time.Duration {
	return parseDuration(c.DialTimeout, 5*time.Second)
}

func parseDuration(value string, fallback time.Duration) time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
`

const goZeroRPCDiscoveryTemplate = discoveryRegistryTemplate

const goZeroRPCConfigTestTemplate = `package config

import "testing"

func TestValidate(t *testing.T) {
	cfg := Config{Name: "{{.RPCService}}", ListenOn: "127.0.0.1:8081", Discovery: DiscoveryConfig{Provider: "memory"}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := cfg.Discovery.RegistryTTL().String(); got != "15s" {
		t.Fatalf("registry TTL = %s, want 15s", got)
	}
	if got := cfg.LoadBalancing.PolicyName(); got != "gofly_p2c_ewma" {
		t.Fatalf("default load-balancing policy = %q, want gofly_p2c_ewma", got)
	}
	for _, policy := range []string{"round_robin", "gofly_p2c_ewma", "gofly_consistent_hash"} {
		t.Run("load balancing "+policy, func(t *testing.T) {
			configured := cfg
			configured.LoadBalancing.Policy = policy
			if err := Validate(configured); err != nil { t.Fatalf("Validate: %v", err) }
			if option, err := configured.LoadBalancing.ResolverOption(); err != nil || option == nil { t.Fatalf("ResolverOption = %v, %v", option, err) }
		})
	}
	for _, name := range []string{"production plaintext", "partial TLS", "public admin", "missing token", "watch without file", "unknown environment"} {
		t.Run(name, func(t *testing.T) {
			invalid := cfg
			switch name {
			case "production plaintext": invalid.Environment = "production"
			case "partial TLS": invalid.TLS.CertFile = "cert.pem"
			case "public admin": invalid.AdminListenOn = "0.0.0.0:9090"
			case "missing token": invalid.AuthTokenEnv = "GOFLY_TEST_MISSING_TOKEN"; t.Setenv(invalid.AuthTokenEnv, "")
			case "watch without file": invalid.RuleWatch = true
			case "unknown environment": invalid.Environment = "prod"
			}
			if err := Validate(invalid); err == nil { t.Fatal("unsafe config accepted") }
		})
	}
	invalid := cfg
	invalid.LoadBalancing.Policy = "least_request"
	if err := Validate(invalid); err == nil { t.Fatal("unsupported load-balancing policy accepted") }
}
`

const goZeroRPCProductionCheckGoTemplate = `//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

type productionConfig struct {
	Environment string ` + "`json:\"environment\"`" + `
	Reflection bool ` + "`json:\"reflection\"`" + `
	RuleFile string ` + "`json:\"ruleFile\"`" + `
	AdminListenOn string ` + "`json:\"adminListenOn\"`" + `
	AdminTokenEnv string ` + "`json:\"adminTokenEnv\"`" + `
	TLS struct { CertFile string ` + "`json:\"certFile\"`" + `; KeyFile string ` + "`json:\"keyFile\"`" + ` } ` + "`json:\"tls\"`" + `
}

func main() {
	if len(os.Args) != 2 { fail("usage: go run ./internal/config/production_check.go <config>") }
	data, err := os.ReadFile(os.Args[1])
	if err != nil { fail("read config: %v", err) }
	var cfg productionConfig
	if err := json.Unmarshal(data, &cfg); err != nil { fail("decode config json: %v", err) }
	if cfg.Environment != "production" { fail("environment must be production") }
	if cfg.Reflection { fail("reflection must be disabled in production") }
	if strings.TrimSpace(cfg.TLS.CertFile) == "" || strings.TrimSpace(cfg.TLS.KeyFile) == "" { fail("TLS certFile and keyFile are required") }
	if strings.TrimSpace(cfg.RuleFile) == "" { fail("ruleFile is required for restart recovery") }
	ruleFile := filepath.Clean(cfg.RuleFile)
	if filepath.IsAbs(ruleFile) || ruleFile == ".." || strings.HasPrefix(ruleFile, ".."+string(filepath.Separator)) { fail("ruleFile must stay inside the project") }
	ruleInfo, err := os.Lstat(ruleFile)
	if err != nil { fail("ruleFile: %v", err) }
	if !ruleInfo.Mode().IsRegular() { fail("ruleFile must be a regular file") }
	for current := ruleFile; current != "."; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil { fail("ruleFile: %v", err) }
		if info.Mode()&os.ModeSymlink != 0 { fail("ruleFile path must not contain symlinks") }
	}
	host, _, err := net.SplitHostPort(cfg.AdminListenOn)
	if err != nil { fail("invalid admin listen address") }
	if ip := net.ParseIP(host); (ip == nil || !ip.IsLoopback()) && strings.TrimSpace(cfg.AdminTokenEnv) == "" { fail("non-loopback admin requires adminTokenEnv") }
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "production-check failed: "+format+"\n", args...)
	os.Exit(1)
}
`

const goZeroRPCProductionCheckScriptTemplate = `#!/usr/bin/env sh
set -eu

service_name="{{.Name}}"
config_file="${1:-etc/{{.Name}}.json}"

[ -f "$config_file" ] || { printf 'production-check failed: missing config file: %s\n' "$config_file" >&2; exit 1; }
[ -f etc/governance.json ] || { printf 'production-check failed: missing governance rule file\n' >&2; exit 1; }
go run ./internal/config/production_check.go "$config_file"
go test ./internal/config -run '^TestGovernanceRuleRestartRecovery$' -count=1
printf '%s gRPC production checklist passed\n' "$service_name"
`

const goZeroRPCGovernanceRecoveryTestTemplate = `package config

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/imajinyun/gofly/core/governance"
)

func TestGovernanceRuleRestartRecovery(t *testing.T) {
	source := filepath.Join("..", "..", "etc", "governance.json")
	baseline, err := os.ReadFile(source)
	if err != nil { t.Fatal(err) }
	ruleFile := filepath.Join(t.TempDir(), "governance.json")
	if err := os.WriteFile(ruleFile, baseline, 0o600); err != nil { t.Fatal(err) }
	load := func() *governance.Manager {
		manager, err := governance.NewManager(governance.Config{RuleFile: ruleFile})
		if err != nil { t.Fatal(err) }
		if err := manager.Reload(t.Context()); err != nil { t.Fatal(err) }
		return manager
	}
	original := load().RuleSet().Snapshot()
	changed := []governance.Rule{{Name: "restart-proof", Transport: governance.TransportRPC, Method: "SayHello", Policy: governance.Policy{Timeout: 123}}}
	writer := load()
	persist := func(manager *governance.Manager, rules []governance.Rule) {
		body, err := json.Marshal(map[string]any{"persist": true, "rules": rules})
		if err != nil { t.Fatal(err) }
		recorder := httptest.NewRecorder()
		governance.NewAdmin(nil, nil, governance.WithAdminManager(manager)).ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/rules", bytes.NewReader(body)))
		if recorder.Code != http.StatusOK { t.Fatalf("persist status=%d body=%s", recorder.Code, recorder.Body.String()) }
	}
	persist(writer, changed)
	restarted := load().RuleSet().Snapshot()
	if len(restarted) != 1 || restarted[0].Name != "restart-proof" { t.Fatalf("restart rules = %+v", restarted) }
	persist(load(), original)
	rolledBack := load().RuleSet().Snapshot()
	if len(rolledBack) != len(original) { t.Fatalf("rollback rules = %+v, want %+v", rolledBack, original) }
	for index := range original {
		if rolledBack[index].Name != original[index].Name { t.Fatalf("rollback rule %d = %+v, want %+v", index, rolledBack[index], original[index]) }
	}
}
`

const goZeroRPCSvcTemplate = `package svc

import (
	"github.com/imajinyun/gofly/core/governance"
	"{{.Module}}/internal/config"
)

type ServiceContext struct {
	Config config.Config
	Rules *governance.RuleSet
}

func NewServiceContext(c config.Config) *ServiceContext {
	return &ServiceContext{Config: c, Rules: governance.NewRuleSet(c.Rules...)}
}
`

const goZeroRPCLogicTemplate = `package logic

import (
	"context"
	"strings"

	"{{.Module}}/internal/pb"
	"{{.Module}}/internal/svc"
)

type SayHelloLogic struct {
	ctx context.Context
	stx *svc.ServiceContext
}

func NewSayHelloLogic(ctx context.Context, stx *svc.ServiceContext) *SayHelloLogic {
	return &SayHelloLogic{ctx: ctx, stx: stx}
}

func (l *SayHelloLogic) SayHello(req *pb.SayHelloRequest) (*pb.SayHelloResponse, error) {
	name := "world"
	if req != nil && strings.TrimSpace(req.GetName()) != "" {
		name = strings.TrimSpace(req.GetName())
	}
	return &pb.SayHelloResponse{Message: "hello " + name}, nil
}
`

const goZeroRPCServerTemplate = `package server

import (
	"context"

	"{{.Module}}/internal/logic"
	"{{.Module}}/internal/pb"
	"{{.Module}}/internal/svc"
)

type GreeterServer struct {
	pb.UnimplementedGreeterServer
	stx *svc.ServiceContext
}

func NewGreeterServer(stx *svc.ServiceContext) *GreeterServer {
	return &GreeterServer{stx: stx}
}

func (s *GreeterServer) SayHello(ctx context.Context, req *pb.SayHelloRequest) (*pb.SayHelloResponse, error) {
	return logic.NewSayHelloLogic(ctx, s.stx).SayHello(req)
}
`

const goZeroRPCServerTestTemplate = `package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"
	"{{.Module}}/internal/config"
	"{{.Module}}/internal/pb"
	"{{.Module}}/internal/svc"
)

func TestGreeterUnarySmoke(t *testing.T) {
	registry := discovery.NewMemoryRegistry()
	stx := svc.NewServiceContext(config.Config{})
	grpcServer := flygrpc.NewDefaultServer("127.0.0.1:0", "{{.RPCService}}", stx.Rules, nil,
		flygrpc.WithDiscovery(registry, discovery.Instance{ID: "test", Service: "{{.RPCService}}"}),
	)
	pb.RegisterGreeterServer(grpcServer.GRPCServer(), NewGreeterServer(stx))
	started := make(chan error, 1)
	go func() { started <- grpcServer.Start() }()
	t.Cleanup(func() {
		_ = grpcServer.Shutdown(context.Background())
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Error("timed out waiting for gRPC server shutdown")
		}
	})

	deadline := time.Now().Add(time.Second)
	var endpoint string
	for time.Now().Before(deadline) {
		instances, err := registry.Resolve(context.Background(), "{{.RPCService}}")
		if err == nil && len(instances) == 1 {
			endpoint = instances[0].Endpoint
			break
		}
		time.Sleep(time.Millisecond)
	}
	if endpoint == "" {
		t.Fatal("gRPC service was not registered")
	}
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		t.Fatalf("registered endpoint = %q: %v", endpoint, err)
	}
	conn, err := flygrpc.NewDefaultClient(context.Background(), endpoint, "{{.RPCService}}", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := pb.NewGreeterClient(conn.Conn()).SayHello(ctx, &pb.SayHelloRequest{Name: "gofly"})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetMessage() != "hello gofly" {
		t.Fatalf("message = %q, want hello gofly", response.GetMessage())
	}
	stx.Rules.Replace(governance.Rule{Method: "SayHello", Policy: governance.Policy{RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1}}})
	if _, err := pb.NewGreeterClient(conn.Conn()).SayHello(ctx, &pb.SayHelloRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := pb.NewGreeterClient(conn.Conn()).SayHello(ctx, &pb.SayHelloRequest{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("updated governance rule = %v, want ResourceExhausted", err)
	}
}
`

const goZeroRPCClientTemplate = `package client

import (
	"context"

	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"
	"{{.Module}}/internal/config"
	"{{.Module}}/internal/pb"
)

func NewGreeter(ctx context.Context, target string, rules *governance.RuleSet, opts ...flygrpc.ClientOption) (pb.GreeterClient, *flygrpc.ClientConn, error) {
	conn, err := flygrpc.NewDefaultClient(ctx, target, "{{.RPCService}}", rules, nil, opts...)
	if err != nil {
		return nil, nil, err
	}
	return pb.NewGreeterClient(conn.Conn()), conn, nil
}

func NewDiscoveredGreeter(ctx context.Context, resolver discovery.Resolver, rules *governance.RuleSet, opts ...flygrpc.ClientOption) (pb.GreeterClient, *flygrpc.ClientConn, error) {
	opts = append([]flygrpc.ClientOption{flygrpc.WithDiscoveryResolverOptions(resolver, "{{.RPCService}}", []flygrpc.ResolverOption{flygrpc.WithP2CEWMAResolver()})}, opts...)
	return NewGreeter(ctx, flygrpc.Target("{{.RPCService}}"), rules, opts...)
}

func NewConfiguredGreeter(ctx context.Context, resolver discovery.Resolver, c config.Config, rules *governance.RuleSet, opts ...flygrpc.ClientOption) (pb.GreeterClient, *flygrpc.ClientConn, error) {
	resolverOption, err := c.LoadBalancing.ResolverOption()
	if err != nil {
		return nil, nil, err
	}
	opts = append([]flygrpc.ClientOption{flygrpc.WithDiscoveryResolverOptions(resolver, "{{.RPCService}}", []flygrpc.ResolverOption{resolverOption})}, opts...)
	return NewGreeter(ctx, flygrpc.Target("{{.RPCService}}"), rules, opts...)
}
`

const goZeroRPCMainTemplate = `package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/core/config"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/observability/trace"
	"github.com/imajinyun/gofly/core/security"
	corediscovery "github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/proc"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"

	appconfig "{{.Module}}/internal/config"
	appdiscovery "{{.Module}}/internal/discovery"
	"{{.Module}}/internal/pb"
	appserver "{{.Module}}/internal/server"
	"{{.Module}}/internal/svc"
)

func main() {
	var c appconfig.Config
	configPath := appconfig.ResolveConfigPath("{{.Name}}")
	if err := config.Load(configPath, &c, config.WithEnvExpansion(), config.WithStrictFields(), config.WithLoadValidator(appconfig.Validate)); err != nil {
		slog.Error("load config", "error", err)
		return
	}
	if c.LogJSON { slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil))) }
	ctx, stop := proc.SignalContext(context.Background())
	defer stop()
	c.Telemetry.ServiceName = c.Name
	traceAgent, err := trace.StartAgent(ctx, c.Telemetry)
	if err != nil { slog.Error("setup telemetry", "error", err); return }
	defer func() { _ = traceAgent.Shutdown(context.Background()) }()
	registry, closeRegistry, err := appdiscovery.NewRegistry(ctx, c.Discovery)
	if err != nil {
		slog.Error("setup discovery", "error", err)
		return
	}
	defer func() { _ = closeRegistry(context.Background()) }()
	stx := svc.NewServiceContext(c)
	manager, err := governance.NewManager(governance.Config{RuleFile: c.RuleFile, Watch: c.RuleWatch}, governance.WithRuleSet(stx.Rules))
	if err != nil { slog.Error("setup governance", "error", err); return }
	if c.RuleFile != "" {
		if err := manager.Reload(ctx); err != nil { slog.Error("load governance", "error", err); return }
	}
	if c.RuleWatch {
		watchCtx, cancelWatch := context.WithCancel(ctx)
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			if err := manager.Start(watchCtx); err != nil && watchCtx.Err() == nil { slog.Error("watch governance", "error", err); stop() }
		}()
		defer func() { cancelWatch(); <-watchDone }()
	}
	var authOptions []flygrpc.ServerOption
	if c.AuthTokenEnv != "" {
		authConfig := flygrpc.AuthConfig{Validator: auth.StaticTokenValidator(os.Getenv(c.AuthTokenEnv), c.Name), RequireAuthentication: true}
		authOptions = append(authOptions, flygrpc.WithUnaryServerInterceptors(flygrpc.AuthUnaryServerInterceptor(authConfig)), flygrpc.WithStreamServerInterceptors(flygrpc.AuthStreamServerInterceptor(authConfig)))
	}
	if c.AdminTokenEnv != "" {
		token := os.Getenv(c.AdminTokenEnv)
		authOptions = append(authOptions, flygrpc.WithAdminAuthorization(func(r *http.Request) bool { return security.AuthorizeBearerOrLocal(r, token) }))
	}
	serverOptions := append(authOptions,
		flygrpc.WithGovernanceManager(manager),
		flygrpc.WithServerTLS(c.TLS),
		flygrpc.WithAdminAddr(c.AdminListenOn),
		flygrpc.WithReflection(c.Reflection),
		flygrpc.WithDiscovery(registry, corediscovery.Instance{
			ID: c.Name, Service: c.Name, Endpoint: c.Advertise,
			Metadata: map[string]string{"transport": "grpc"},
		}, corediscovery.WithTTL(c.Discovery.RegistryTTL())),
	)
	grpcServer := flygrpc.NewDefaultServer(c.ListenOn, c.Name, stx.Rules, nil, serverOptions...)
	pb.RegisterGreeterServer(grpcServer.GRPCServer(), appserver.NewGreeterServer(stx))
	slog.Info("{{.Name}} gRPC starting", "listen_on", c.ListenOn, "admin_listen_on", c.AdminListenOn)
	if err := app.Run(ctx, []app.Server{grpcServer}); err != nil {
		slog.Error("{{.Name}} gRPC stopped", "error", err)
	}
}
`
