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
  "reflection": true,
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
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Name          string          ` + "`json:\"name\"`" + `
	ListenOn      string          ` + "`json:\"listenOn\"`" + `
	Advertise     string          ` + "`json:\"advertise,omitempty\"`" + `
	AdminListenOn string          ` + "`json:\"adminListenOn,omitempty\"`" + `
	Reflection    bool            ` + "`json:\"reflection,omitempty\"`" + `
	Discovery     DiscoveryConfig ` + "`json:\"discovery\"`" + `
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
	return nil
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
}
`

const goZeroRPCSvcTemplate = `package svc

import "{{.Module}}/internal/config"

type ServiceContext struct {
	Config config.Config
}

func NewServiceContext(c config.Config) *ServiceContext {
	return &ServiceContext{Config: c}
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
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"
	"{{.Module}}/internal/config"
	"{{.Module}}/internal/pb"
	"{{.Module}}/internal/svc"
)

func TestGreeterUnarySmoke(t *testing.T) {
	registry := discovery.NewMemoryRegistry()
	grpcServer := flygrpc.NewDefaultServer("127.0.0.1:0", "{{.RPCService}}", nil, nil,
		flygrpc.WithDiscovery(registry, discovery.Instance{ID: "test", Service: "{{.RPCService}}"}),
	)
	pb.RegisterGreeterServer(grpcServer.GRPCServer(), NewGreeterServer(svc.NewServiceContext(config.Config{})))
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
}
`

const goZeroRPCClientTemplate = `package client

import (
	"context"

	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"
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
	opts = append([]flygrpc.ClientOption{flygrpc.WithDiscoveryResolver(resolver, "{{.RPCService}}")}, opts...)
	return NewGreeter(ctx, flygrpc.Target("{{.RPCService}}"), rules, opts...)
}
`

const goZeroRPCMainTemplate = `package main

import (
	"context"
	"log/slog"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/config"
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
	ctx, stop := proc.SignalContext(context.Background())
	defer stop()
	registry, closeRegistry, err := appdiscovery.NewRegistry(ctx, c.Discovery)
	if err != nil {
		slog.Error("setup discovery", "error", err)
		return
	}
	defer func() { _ = closeRegistry(context.Background()) }()
	grpcServer := flygrpc.NewDefaultServer(c.ListenOn, c.Name, nil, nil,
		flygrpc.WithAdminAddr(c.AdminListenOn),
		flygrpc.WithReflection(c.Reflection),
		flygrpc.WithDiscovery(registry, corediscovery.Instance{
			ID: c.Name, Service: c.Name, Endpoint: c.Advertise,
			Metadata: map[string]string{"transport": "grpc"},
		}, corediscovery.WithTTL(c.Discovery.RegistryTTL())),
	)
	stx := svc.NewServiceContext(c)
	pb.RegisterGreeterServer(grpcServer.GRPCServer(), appserver.NewGreeterServer(stx))
	slog.Info("{{.Name}} gRPC starting", "listen_on", c.ListenOn, "admin_listen_on", c.AdminListenOn)
	if err := app.Run(ctx, []app.Server{grpcServer}); err != nil {
		slog.Error("{{.Name}} gRPC stopped", "error", err)
	}
}
`
