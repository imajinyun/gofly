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
  "etcd": {},
  "clients": {},
  "loadBalancing": {"policy": "gofly_p2c_ewma"},
  "adaptiveLimit": {"enabled": true, "minLimit": 16, "maxLimit": 256, "initialLimit": 64, "cpuThresholdPermille": 800, "window": 10000000000, "targetLatency": 100000000, "targetErrorRatio": 0.05, "minSamples": 20},
  "rules": {{.RPCMethodRulesJSON}},
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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/observability/trace"
	"github.com/imajinyun/gofly/core/security"
	corerpc "github.com/imajinyun/gofly/rpc"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"

	"google.golang.org/grpc/keepalive"
)

type Config struct {
	Name          string          ` + "`json:\"name\"`" + `
	ListenOn      string          ` + "`json:\"listenOn\"`" + `
	Advertise     string          ` + "`json:\"advertise,omitempty\"`" + `
	AdminListenOn string          ` + "`json:\"adminListenOn,omitempty\"`" + `
	Reflection    bool            ` + "`json:\"reflection,omitempty\"`" + `
	Discovery      DiscoveryConfig      ` + "`json:\"discovery\"`" + `
	LoadBalancing  LoadBalancingConfig       ` + "`json:\"loadBalancing,omitempty\"`" + `
	AdaptiveLimit  flygrpc.AdaptiveLimitConfig ` + "`json:\"adaptiveLimit,omitempty\"`" + `
	Rules         []governance.Rule ` + "`json:\"rules,omitempty\"`" + `
	Environment   string ` + "`json:\"environment,omitempty\"`" + `
	RuleFile      string ` + "`json:\"ruleFile,omitempty\"`" + `
	RuleWatch     bool ` + "`json:\"ruleWatch,omitempty\"`" + `
	AdminTokenEnv string ` + "`json:\"adminTokenEnv,omitempty\"`" + `
	AuthTokenEnv  string ` + "`json:\"authTokenEnv,omitempty\"`" + `
	TLS           security.TLSConfig ` + "`json:\"tls,omitempty\"`" + `
	Telemetry     trace.AgentConfig ` + "`json:\"telemetry,omitempty\"`" + `
	LogJSON       bool ` + "`json:\"logJSON,omitempty\"`" + `
	Etcd          EtcdConfig ` + "`json:\"etcd,omitempty\"`" + `
	Clients       map[string]RPCClientConfig ` + "`json:\"clients,omitempty\"`" + `
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

// EtcdConfig carries the migration-critical subset of go-zero's EtcdConf.
// Generated ServiceContext wiring constructs a read-only resolver for this
// zRPC-compatible endpoint layout; direct constructors may inject one.
type EtcdConfig struct {
	Hosts              []string ` + "`json:\"hosts,omitempty\"`" + `
	Key                string   ` + "`json:\"key,omitempty\"`" + `
	ID                 int64    ` + "`json:\"id,omitempty\"`" + `
	User               string   ` + "`json:\"user,omitempty\"`" + `
	Pass               string   ` + "`json:\"pass,omitempty\"`" + `
	CertFile           string   ` + "`json:\"certFile,omitempty\"`" + `
	CertKeyFile        string   ` + "`json:\"certKeyFile,omitempty\"`" + `
	CACertFile         string   ` + "`json:\"caCertFile,omitempty\"`" + `
	InsecureSkipVerify bool     ` + "`json:\"insecureSkipVerify,omitempty\"`" + `
}

func (c EtcdConfig) ResolvedHosts() []string {
	return compactRPCClientEndpoints(c.Hosts)
}

func (c EtcdConfig) TLSConfig() security.TLSConfig {
	return security.TLSConfig{
		CertFile: c.CertFile, KeyFile: c.CertKeyFile, CAFile: c.CACertFile,
		InsecureSkipVerify: c.InsecureSkipVerify,
	}
}

func (c EtcdConfig) Enabled() bool {
	return len(c.ResolvedHosts()) > 0 || strings.TrimSpace(c.Key) != ""
}

func (c EtcdConfig) Validate() error {
	if !c.Enabled() { return nil }
	if len(c.ResolvedHosts()) == 0 || strings.TrimSpace(c.Key) == "" {
		return errors.New("etcd requires hosts and key")
	}
	if c.ID < 0 { return errors.New("etcd id must be non-negative") }
	if (strings.TrimSpace(c.User) == "") != (strings.TrimSpace(c.Pass) == "") {
		return errors.New("etcd credentials require both user and pass")
	}
	if err := validateRPCClientTLS(c.TLSConfig()); err != nil { return fmt.Errorf("etcd TLS: %w", err) }
	return nil
}

// RPCClientConfig is the generated-project compatibility adapter for the
// migration-critical fields in zrpc.RpcClientConf. Timeout is in milliseconds.
type RPCClientConfig struct {
	Endpoints                []string           ` + "`json:\"endpoints,omitempty\"`" + `
	Target                   string             ` + "`json:\"target,omitempty\"`" + `
	Etcd                     EtcdConfig         ` + "`json:\"etcd,omitempty\"`" + `
	App                      string             ` + "`json:\"app,omitempty\"`" + `
	Token                    string             ` + "`json:\"token,omitempty\"`" + `
	NonBlock                 bool               ` + "`json:\"nonBlock,omitempty\"`" + `
	Timeout                  int64              ` + "`json:\"timeout,omitempty\"`" + `
	KeepaliveTime            time.Duration      ` + "`json:\"keepaliveTime,omitempty\"`" + `
	BalancerName             string             ` + "`json:\"balancerName,omitempty\"`" + `
	TLS                      security.TLSConfig ` + "`json:\"tls,omitempty\"`" + `
	AllowInsecureCredentials bool               ` + "`json:\"allowInsecureCredentials,omitempty\"`" + `
}

func (c *RPCClientConfig) UnmarshalJSON(data []byte) error {
	type plain RPCClientConfig
	value := plain{NonBlock: true, Timeout: 2000}
	if err := json.Unmarshal(data, &value); err != nil { return err }
	*c = RPCClientConfig(value)
	return nil
}

func (c *RPCClientConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain RPCClientConfig
	value := plain{NonBlock: true, Timeout: 2000}
	if err := unmarshal(&value); err != nil { return err }
	*c = RPCClientConfig(value)
	return nil
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
	if err := c.Etcd.Validate(); err != nil { return fmt.Errorf("rpc server: %w", err) }
	if c.Etcd.Enabled() && strings.TrimSpace(c.Advertise) == "" {
		return errors.New("rpc advertise address is required for zrpc etcd registration")
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
	if err := c.AdaptiveLimit.Validate(); err != nil { return err }
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
	clientNames := make([]string, 0, len(c.Clients))
	for name := range c.Clients { clientNames = append(clientNames, name) }
	sort.Strings(clientNames)
	for _, name := range clientNames {
		if err := c.Clients[name].Validate(name); err != nil { return fmt.Errorf("rpc client %q: %w", name, err) }
	}
	return governance.ValidateRules(c.Rules...)
}

func (c LoadBalancingConfig) PolicyName() string {
	policy := strings.ToLower(strings.TrimSpace(c.Policy))
	if policy == "" || policy == "p2c_ewma" { return flygrpc.P2CEWMABalancerName }
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

// TargetAndOptions maps zRPC-style client configuration onto gofly's native
// client and resolver options. Endpoints take precedence over Target, which
// takes precedence over Etcd, matching zrpc.RpcClientConf.BuildTarget.
func (c RPCClientConfig) TargetAndOptions(resolver discovery.Resolver, defaultService string) (string, []flygrpc.ClientOption, error) {
	if err := c.Validate(defaultService); err != nil { return "", nil, err }
	service := strings.TrimSpace(defaultService)
	resolverOption, err := (LoadBalancingConfig{Policy: c.BalancerName}).ResolverOption()
	if err != nil {
		return "", nil, err
	}
	var target string
	var options []flygrpc.ClientOption
	if endpoints := compactRPCClientEndpoints(c.Endpoints); len(endpoints) > 0 {
		target = flygrpc.Target(service)
		options = append(options, flygrpc.WithStaticResolver(service, endpoints, resolverOption))
	} else if direct := strings.TrimSpace(c.Target); direct != "" {
		target = direct
		options = append(options, flygrpc.WithBalancerName((LoadBalancingConfig{Policy: c.BalancerName}).PolicyName()))
	} else {
		key := strings.TrimSpace(c.Etcd.Key)
		hosts := compactRPCClientEndpoints(c.Etcd.Hosts)
		if key == "" || len(hosts) == 0 {
			return "", nil, errors.New("rpc client requires endpoints, target, or etcd hosts and key")
		}
		if resolver == nil {
			return "", nil, errors.New("rpc client etcd config requires a gofly discovery resolver")
		}
		target = flygrpc.Target(service)
		options = append(options, flygrpc.WithServiceResolver(service, corerpc.NewDiscoveryResolver(resolver, key), resolverOption))
	}
	if (strings.TrimSpace(c.App) == "") != (strings.TrimSpace(c.Token) == "") {
		return "", nil, errors.New("rpc client credentials require both app and token")
	}
	if err := validateRPCClientTLS(c.TLS); err != nil {
		return "", nil, err
	}
	if rpcClientTLSConfigured(c.TLS) {
		options = append(options, flygrpc.WithClientTLS(c.TLS))
	}
	if strings.TrimSpace(c.App) != "" {
		if !rpcClientTLSConfigured(c.TLS) && !c.AllowInsecureCredentials {
			return "", nil, errors.New("rpc client app/token credentials require TLS or explicit allowInsecureCredentials")
		}
		options = append(options, flygrpc.WithAppTokenCredentials(flygrpc.AppTokenCredentials{
			App: strings.TrimSpace(c.App), Token: strings.TrimSpace(c.Token), AllowInsecure: c.AllowInsecureCredentials,
		}))
	}
	if c.Timeout > 0 {
		timeout := time.Duration(c.Timeout) * time.Millisecond
		options = append(options, flygrpc.WithClientCallTimeout(timeout))
	}
	if c.KeepaliveTime > 0 {
		options = append(options, flygrpc.WithClientKeepalive(keepalive.ClientParameters{Time: c.KeepaliveTime}))
	}
	if !c.NonBlock {
		options = append(options, flygrpc.WithWaitForReady())
	}
	return target, options, nil
}

func (c RPCClientConfig) Validate(defaultService string) error {
	if strings.TrimSpace(defaultService) == "" { return errors.New("rpc client service is required") }
	if _, err := (LoadBalancingConfig{Policy: c.BalancerName}).ResolverOption(); err != nil { return err }
	if len(compactRPCClientEndpoints(c.Endpoints)) == 0 && strings.TrimSpace(c.Target) == "" &&
		(strings.TrimSpace(c.Etcd.Key) == "" || len(compactRPCClientEndpoints(c.Etcd.Hosts)) == 0) {
		return errors.New("rpc client requires endpoints, target, or etcd hosts and key")
	}
	if (strings.TrimSpace(c.App) == "") != (strings.TrimSpace(c.Token) == "") {
		return errors.New("rpc client credentials require both app and token")
	}
	if err := validateRPCClientTLS(c.TLS); err != nil { return err }
	if strings.TrimSpace(c.App) != "" && !rpcClientTLSConfigured(c.TLS) && !c.AllowInsecureCredentials {
		return errors.New("rpc client app/token credentials require TLS or explicit allowInsecureCredentials")
	}
	if c.Timeout < 0 { return errors.New("rpc client timeout must be non-negative") }
	if c.KeepaliveTime < 0 { return errors.New("rpc client keepalive time must be non-negative") }
	if c.RequiresEtcdResolver() {
		if (strings.TrimSpace(c.Etcd.User) == "") != (strings.TrimSpace(c.Etcd.Pass) == "") {
			return errors.New("rpc client etcd credentials require both user and pass")
		}
		if err := validateRPCClientTLS(c.Etcd.TLSConfig()); err != nil {
			return fmt.Errorf("rpc client etcd TLS: %w", err)
		}
	}
	return nil
}

func (c RPCClientConfig) RequiresEtcdResolver() bool {
	return len(compactRPCClientEndpoints(c.Endpoints)) == 0 && strings.TrimSpace(c.Target) == "" &&
		strings.TrimSpace(c.Etcd.Key) != "" && len(c.Etcd.ResolvedHosts()) > 0
}

func compactRPCClientEndpoints(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if value == "" { continue }
		if _, ok := seen[value]; ok { continue }
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func validateRPCClientTLS(c security.TLSConfig) error {
	if strings.TrimSpace(c.ClientCAFile) != "" {
		return errors.New("rpc client TLS does not accept server-only clientCAFile")
	}
	if (strings.TrimSpace(c.CertFile) == "") != (strings.TrimSpace(c.KeyFile) == "") {
		return errors.New("rpc client TLS requires both certFile and keyFile")
	}
	return nil
}

func rpcClientTLSConfigured(c security.TLSConfig) bool {
	return c.Enabled() || strings.TrimSpace(c.CAFile) != "" || strings.TrimSpace(c.ServerName) != "" || c.InsecureSkipVerify
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

const goZeroRPCDiscoveryTemplate = discoveryRegistryTemplate + `

// NewZRPCResolver connects to the etcd cluster declared by a zRPC-style
// client config and reads its raw key/value endpoint layout. It is separate
// from NewRegistry because gofly's native registry stores structured Instance
// JSON under a different namespace contract.
func NewZRPCResolver(ctx context.Context, cfg appconfig.EtcdConfig) (corediscovery.Resolver, closeFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	tlsConfig, err := cfg.TLSConfig().ClientTLSConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("configure zrpc etcd TLS: %w", err)
	}
	resolver, err := etcdv3.NewZRPCResolver(etcdv3.Config{
		Endpoints: cfg.ResolvedHosts(), Username: cfg.User, Password: cfg.Pass, TLS: tlsConfig,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create zrpc etcd resolver: %w", err)
	}
	return resolver, resolver.Close, nil
}

// NewZRPCRegistrar publishes the raw endpoint layout consumed by go-zero zRPC.
// It is explicit and separate from NewRegistry so native gofly registration
// retains its structured Instance JSON contract.
func NewZRPCRegistrar(ctx context.Context, cfg appconfig.EtcdConfig) (corediscovery.Registrar, closeFunc, error) {
	if ctx == nil { ctx = context.Background() }
	if err := ctx.Err(); err != nil { return nil, nil, err }
	if err := cfg.Validate(); err != nil { return nil, nil, err }
	tlsConfig, err := cfg.TLSConfig().ClientTLSConfig()
	if err != nil { return nil, nil, fmt.Errorf("configure zrpc etcd TLS: %w", err) }
	registrar, err := etcdv3.NewZRPCRegistrar(etcdv3.Config{
		Endpoints: cfg.ResolvedHosts(), Username: cfg.User, Password: cfg.Pass,
		TLS: tlsConfig, RegistrationID: cfg.ID,
	})
	if err != nil { return nil, nil, fmt.Errorf("create zrpc etcd registrar: %w", err) }
	return registrar, registrar.Close, nil
}
`

const goZeroRPCConfigTestTemplate = `package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/core/discovery"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"
)

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
	if limiter, err := cfg.AdaptiveLimit.NewLimiter(nil); err != nil || limiter != nil {
		t.Fatalf("absent adaptive config limiter=%v err=%v", limiter, err)
	}
	cfg.AdaptiveLimit = flygrpc.DefaultAdaptiveLimitConfig()
	if limiter, err := cfg.AdaptiveLimit.NewLimiter(func() int { return 0 }); err != nil || limiter == nil {
		t.Fatalf("generated adaptive limiter=%v err=%v", limiter, err)
	}
	for _, policy := range []string{"round_robin", "gofly_p2c_ewma", "gofly_consistent_hash"} {
		t.Run("load balancing "+policy, func(t *testing.T) {
			configured := cfg
			configured.LoadBalancing.Policy = policy
			if err := Validate(configured); err != nil { t.Fatalf("Validate: %v", err) }
			if option, err := configured.LoadBalancing.ResolverOption(); err != nil || option == nil { t.Fatalf("ResolverOption = %v, %v", option, err) }
		})
	}
	for _, name := range []string{"production plaintext", "partial TLS", "public admin", "missing token", "watch without file", "unknown environment", "invalid client", "partial server etcd", "server etcd without advertise", "negative server etcd id"} {
		t.Run(name, func(t *testing.T) {
			invalid := cfg
			switch name {
			case "production plaintext": invalid.Environment = "production"
			case "partial TLS": invalid.TLS.CertFile = "cert.pem"
			case "public admin": invalid.AdminListenOn = "0.0.0.0:9090"
			case "missing token": invalid.AuthTokenEnv = "GOFLY_TEST_MISSING_TOKEN"; t.Setenv(invalid.AuthTokenEnv, "")
			case "watch without file": invalid.RuleWatch = true
			case "unknown environment": invalid.Environment = "prod"
			case "invalid client": invalid.Clients = map[string]RPCClientConfig{"orders.v1.Orders": {}}
			case "partial server etcd": invalid.Etcd.Hosts = []string{"127.0.0.1:2379"}
			case "server etcd without advertise": invalid.Etcd = EtcdConfig{Hosts: []string{"127.0.0.1:2379"}, Key: "greeter.rpc"}
			case "negative server etcd id": invalid.Advertise = "127.0.0.1:8081"; invalid.Etcd = EtcdConfig{Hosts: []string{"127.0.0.1:2379"}, Key: "greeter.rpc", ID: -1}
			}
			if err := Validate(invalid); err == nil { t.Fatal("unsafe config accepted") }
		})
	}
	invalid := cfg
	invalid.LoadBalancing.Policy = "least_request"
	if err := Validate(invalid); err == nil { t.Fatal("unsupported load-balancing policy accepted") }
	invalid = cfg
	invalid.AdaptiveLimit.InitialLimit = invalid.AdaptiveLimit.MaxLimit + 1
	if err := Validate(invalid); err == nil { t.Fatal("invalid adaptive limit accepted") }
	for _, client := range []RPCClientConfig{
		{Etcd: EtcdConfig{Hosts: []string{"127.0.0.1:2379"}, Key: "greeter.rpc", User: "user"}},
		{Etcd: EtcdConfig{Hosts: []string{"127.0.0.1:2379"}, Key: "greeter.rpc", CertFile: "client.crt"}},
	} {
		invalid = cfg
		invalid.Clients = map[string]RPCClientConfig{"greeter.Greeter": client}
		if err := Validate(invalid); err == nil { t.Fatal("invalid etcd client security config accepted") }
	}
}

func TestRPCClientConfigMigration(t *testing.T) {
	var defaults RPCClientConfig
	if err := json.Unmarshal([]byte(` + "`{\"target\":\"dns:///greeter\"}`" + `), &defaults); err != nil { t.Fatal(err) }
	if !defaults.NonBlock || defaults.Timeout != 2000 { t.Fatalf("zRPC defaults = %+v, want nonBlock=true timeout=2000", defaults) }
	precedence := RPCClientConfig{Endpoints: []string{"127.0.0.1:8081"}, Target: "ignored:8081", Etcd: EtcdConfig{Hosts: []string{"127.0.0.1:2379"}, Key: "greeter.rpc"}}
	if precedence.RequiresEtcdResolver() { t.Fatal("endpoint precedence should not create an etcd resolver") }
	etcdOnly := RPCClientConfig{Etcd: EtcdConfig{Hosts: []string{" 127.0.0.1:2379 ", "127.0.0.1:2379"}, Key: "greeter.rpc"}}
	if !etcdOnly.RequiresEtcdResolver() || len(etcdOnly.Etcd.ResolvedHosts()) != 1 { t.Fatalf("etcd resolver selection = %+v", etcdOnly) }
	tests := []struct {
		name string
		cfg RPCClientConfig
		resolver discovery.Resolver
		wantTarget string
		wantErr string
	}{
		{name: "endpoints precede target", cfg: RPCClientConfig{Endpoints: []string{"127.0.0.1:8081", "127.0.0.1:8082"}, Target: "ignored:8081", NonBlock: true}, wantTarget: flygrpc.Target("greeter.Greeter")},
		{name: "direct target", cfg: RPCClientConfig{Target: "dns:///greeter", NonBlock: true}, wantTarget: "dns:///greeter"},
		{name: "etcd bridge", cfg: RPCClientConfig{Etcd: EtcdConfig{Hosts: []string{"127.0.0.1:2379"}, Key: "greeter.rpc"}, NonBlock: true}, resolver: discovery.NewMemoryRegistry(), wantTarget: flygrpc.Target("greeter.Greeter")},
		{name: "etcd requires resolver", cfg: RPCClientConfig{Etcd: EtcdConfig{Hosts: []string{"127.0.0.1:2379"}, Key: "greeter.rpc"}}, wantErr: "requires a gofly discovery resolver"},
		{name: "partial credentials", cfg: RPCClientConfig{Target: "127.0.0.1:8081", App: "app"}, wantErr: "both app and token"},
		{name: "credentials require transport security", cfg: RPCClientConfig{Target: "127.0.0.1:8081", App: "app", Token: "token"}, wantErr: "require TLS"},
		{name: "explicit insecure credential migration", cfg: RPCClientConfig{Target: "127.0.0.1:8081", App: "app", Token: "token", AllowInsecureCredentials: true, NonBlock: true}, wantTarget: "127.0.0.1:8081"},
		{name: "unsupported balancer", cfg: RPCClientConfig{Target: "127.0.0.1:8081", BalancerName: "least_request"}, wantErr: "unsupported gRPC load-balancing policy"},
		{name: "missing destination", cfg: RPCClientConfig{}, wantErr: "requires endpoints, target, or etcd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, options, err := tt.cfg.TargetAndOptions(tt.resolver, "greeter.Greeter")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) { t.Fatalf("TargetAndOptions error = %v, want %q", err, tt.wantErr) }
				return
			}
			if err != nil { t.Fatal(err) }
			if target != tt.wantTarget { t.Fatalf("target = %q, want %q", target, tt.wantTarget) }
			if len(options) == 0 { t.Fatal("client mapping produced no options") }
		})
	}
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
	AdaptiveLimit struct {
		Enabled bool ` + "`json:\"enabled\"`" + `
		MinLimit int ` + "`json:\"minLimit\"`" + `
		MaxLimit int ` + "`json:\"maxLimit\"`" + `
		InitialLimit int ` + "`json:\"initialLimit\"`" + `
		CPUThresholdPermille int ` + "`json:\"cpuThresholdPermille\"`" + `
		Window int64 ` + "`json:\"window\"`" + `
		TargetLatency int64 ` + "`json:\"targetLatency\"`" + `
		TargetErrorRatio float64 ` + "`json:\"targetErrorRatio\"`" + `
		MinSamples int64 ` + "`json:\"minSamples\"`" + `
	} ` + "`json:\"adaptiveLimit\"`" + `
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
	if !cfg.AdaptiveLimit.Enabled { fail("adaptiveLimit must be enabled in production") }
	adaptive := cfg.AdaptiveLimit
	if adaptive.MinLimit < 1 || adaptive.InitialLimit < adaptive.MinLimit || adaptive.MaxLimit < adaptive.InitialLimit { fail("adaptiveLimit requires 1 <= minLimit <= initialLimit <= maxLimit") }
	if adaptive.CPUThresholdPermille < 1 || adaptive.CPUThresholdPermille > 1000 { fail("adaptiveLimit cpuThresholdPermille must be between 1 and 1000") }
	if adaptive.Window <= 0 || adaptive.TargetLatency <= 0 || adaptive.MinSamples <= 0 || adaptive.TargetErrorRatio < 0 || adaptive.TargetErrorRatio > 1 { fail("adaptiveLimit thresholds must be valid") }
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
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	corediscovery "github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"
	"{{.Module}}/internal/config"
	appdiscovery "{{.Module}}/internal/discovery"
)

type rpcClientResource struct {
	conn *flygrpc.ClientConn
}

type rpcResolverResource struct {
	resolver corediscovery.Resolver
	close func(context.Context) error
}

type rpcResolverKey struct {
	hosts string
	user string
	pass string
	certFile string
	certKeyFile string
	caCertFile string
	insecureSkipVerify bool
}

type ServiceContext struct {
	Config config.Config
	Rules *governance.RuleSet
	mu sync.RWMutex
	rpcClients map[string]rpcClientResource
	rpcResolvers map[rpcResolverKey]rpcResolverResource
}

func NewServiceContext(c config.Config) *ServiceContext {
	rules := governance.MergeRules(config.DefaultRPCMethodRules(), c.Rules)
	return &ServiceContext{
		Config: c, Rules: governance.NewRuleSet(rules...),
		rpcClients: make(map[string]rpcClientResource), rpcResolvers: make(map[rpcResolverKey]rpcResolverResource),
	}
}

func (s *ServiceContext) InitRPCClients(ctx context.Context, resolver corediscovery.Resolver) error {
	names := make([]string, 0, len(s.Config.Clients))
	for name := range s.Config.Clients { names = append(names, name) }
	sort.Strings(names)
	next := make(map[string]rpcClientResource, len(names))
	nextResolvers := make(map[rpcResolverKey]rpcResolverResource)
	for _, name := range names {
		cfg := s.Config.Clients[name]
		clientResolver := resolver
		if cfg.RequiresEtcdResolver() {
			key := newRPCResolverKey(cfg.Etcd)
			resource, ok := nextResolvers[key]
			if !ok {
				var err error
				resource.resolver, resource.close, err = appdiscovery.NewZRPCResolver(ctx, cfg.Etcd)
				if err != nil { return errors.Join(fmt.Errorf("initialize rpc client %q discovery: %w", name, err), closeRPCResources(next, nextResolvers)) }
				nextResolvers[key] = resource
			}
			clientResolver = resource.resolver
		}
		target, options, err := cfg.TargetAndOptions(clientResolver, name)
		if err != nil { return errors.Join(fmt.Errorf("initialize rpc client %q: %w", name, err), closeRPCResources(next, nextResolvers)) }
		conn, err := flygrpc.NewDefaultClient(ctx, target, name, s.Rules, nil, options...)
		if err != nil { return errors.Join(fmt.Errorf("initialize rpc client %q: %w", name, err), closeRPCResources(next, nextResolvers)) }
		next[name] = rpcClientResource{conn: conn}
	}
	s.mu.Lock()
	previous, previousResolvers := s.rpcClients, s.rpcResolvers
	s.rpcClients = next
	s.rpcResolvers = nextResolvers
	s.mu.Unlock()
	return closeRPCResources(previous, previousResolvers)
}

func (s *ServiceContext) RPCClient(name string) (*flygrpc.ClientConn, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resource, ok := s.rpcClients[name]
	return resource.conn, ok
}

func (s *ServiceContext) Close() error {
	s.mu.Lock()
	clients, resolvers := s.rpcClients, s.rpcResolvers
	s.rpcClients = make(map[string]rpcClientResource)
	s.rpcResolvers = make(map[rpcResolverKey]rpcResolverResource)
	s.mu.Unlock()
	return closeRPCResources(clients, resolvers)
}

func newRPCResolverKey(c config.EtcdConfig) rpcResolverKey {
	hosts := c.ResolvedHosts()
	sort.Strings(hosts)
	return rpcResolverKey{
		hosts: strings.Join(hosts, "\x00"), user: c.User, pass: c.Pass,
		certFile: c.CertFile, certKeyFile: c.CertKeyFile, caCertFile: c.CACertFile,
		insecureSkipVerify: c.InsecureSkipVerify,
	}
}

func closeRPCResources(clients map[string]rpcClientResource, resolvers map[rpcResolverKey]rpcResolverResource) error {
	names := make([]string, 0, len(clients))
	for name := range clients { names = append(names, name) }
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var err error
	for _, name := range names {
		resource := clients[name]
		if closeErr := resource.conn.Close(); closeErr != nil { err = errors.Join(err, fmt.Errorf("close rpc client %q: %w", name, closeErr)) }
	}
	for _, resource := range resolvers {
		if resource.close != nil {
			if closeErr := resource.close(context.Background()); closeErr != nil { err = errors.Join(err, fmt.Errorf("close rpc client discovery: %w", closeErr)) }
		}
	}
	return err
}
`

const goZeroRPCMethodDefaultsTemplate = `// Code generated by gofly. DO NOT EDIT.

package config

import (
	"time"

	"github.com/imajinyun/gofly/core/governance"
)

func DefaultRPCMethodRules() []governance.Rule {
	return []governance.Rule{{
		Name: "{{.RPCMethodRuleName}}", Priority: -1000,
		Transport: governance.TransportRPC, Service: "{{.RPCService}}", Method: "SayHello",
		Policy: governance.Policy{Timeout: 2 * time.Second},
	}}
}

func DefaultRPCMethodGovernance() governance.Plugin {
	return governance.NewPlugin("descriptor-rpc-method-defaults", DefaultRPCMethodRules()...)
}
`

const goZeroRPCLogicTemplate = `package greeter

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

const goZeroRPCServerTemplate = `package rpc

import (
	"context"

	appgreeter "{{.Module}}/internal/app/greeter"
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

func DiscoveryAliases() []string {
	return []string{"{{.RPCService}}"}
}

func (s *GreeterServer) SayHello(ctx context.Context, req *pb.SayHelloRequest) (*pb.SayHelloResponse, error) {
	return appgreeter.NewSayHelloLogic(ctx, s.stx).SayHello(req)
}
`

const goZeroRPCServerTestTemplate = `package rpc

import (
	"context"
	"net"
	"strings"
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
	aliasLease, err := registry.Register(t.Context(), discovery.Instance{ID: "zrpc-key", Service: "/rpc/greeter", Endpoint: endpoint})
	if err != nil { t.Fatal(err) }
	defer aliasLease.Close(context.Background())
	client, conn, err := NewConfiguredGreeter(
		context.Background(),
		registry,
		config.RPCClientConfig{Etcd: config.EtcdConfig{Hosts: []string{"memory"}, Key: "/rpc/greeter"}},
		nil,
		flygrpc.WithWaitForReady(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := client.SayHello(ctx, &pb.SayHelloRequest{Name: "gofly"})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetMessage() != "hello gofly" {
		t.Fatalf("message = %q, want hello gofly", response.GetMessage())
	}
	for _, policy := range []string{"round_robin", flygrpc.P2CEWMABalancerName, flygrpc.ConsistentHashBalancerName} {
		t.Run("configured load balancing "+policy, func(t *testing.T) {
			configured := config.RPCClientConfig{Etcd: config.EtcdConfig{Hosts: []string{"memory"}, Key: "/rpc/greeter"}, BalancerName: policy}
			configuredClient, configuredConn, err := NewConfiguredGreeter(
				t.Context(),
				registry,
				configured,
				nil,
				flygrpc.WithWaitForReady(),
			)
			if err != nil {
				t.Fatalf("NewConfiguredGreeter policy %q: %v", policy, err)
			}
			defer configuredConn.Close()
			callCtx := t.Context()
			if policy == flygrpc.ConsistentHashBalancerName {
				if _, err := configuredClient.SayHello(callCtx, &pb.SayHelloRequest{Name: policy}); err == nil || !strings.Contains(err.Error(), "consistent hash key is required") {
					t.Fatalf("consistent-hash call without key = %v, want missing-key failure", err)
				}
				callCtx = flygrpc.WithHashKey(callCtx, "tenant-42")
			}
			response, err := configuredClient.SayHello(callCtx, &pb.SayHelloRequest{Name: policy})
			if err != nil {
				t.Fatalf("SayHello policy %q: %v", policy, err)
			}
			if response.GetMessage() != "hello "+policy {
				t.Fatalf("policy %q message = %q", policy, response.GetMessage())
			}
		})
	}
	invalid := config.RPCClientConfig{Target: "127.0.0.1:1", NonBlock: true, BalancerName: "least_request"}
	if _, _, err := NewConfiguredGreeter(t.Context(), registry, invalid, nil); err == nil {
		t.Fatal("unsupported configured load-balancing policy was accepted")
	}
	stx.Config.Clients = map[string]config.RPCClientConfig{
		"{{.RPCService}}": {Endpoints: []string{endpoint}, NonBlock: true},
	}
	if err := stx.InitRPCClients(t.Context(), registry); err != nil {
		t.Fatalf("InitRPCClients: %v", err)
	}
	managedConn, ok := stx.RPCClient("{{.RPCService}}")
	if !ok { t.Fatal("configured RPC client was not published") }
	managedClient := pb.NewGreeterClient(managedConn.Conn())
	if response, err := managedClient.SayHello(t.Context(), &pb.SayHelloRequest{Name: "managed"}); err != nil || response.GetMessage() != "hello managed" {
		t.Fatalf("managed client response=%v err=%v", response, err)
	}
	if err := stx.Close(); err != nil { t.Fatalf("close service context: %v", err) }
	if _, ok := stx.RPCClient("{{.RPCService}}"); ok { t.Fatal("closed RPC client remained published") }
	stx.Config.Clients = map[string]config.RPCClientConfig{
		"a.valid": {Target: "passthrough:///bufnet", NonBlock: true},
		"b.invalid": {},
	}
	if err := stx.InitRPCClients(t.Context(), registry); err == nil || !strings.Contains(err.Error(), "b.invalid") {
		t.Fatalf("partial initialization error = %v, want b.invalid", err)
	}
	if _, ok := stx.RPCClient("a.valid"); ok { t.Fatal("partial RPC client was published") }
	stx.Rules.Replace(governance.Rule{Method: "SayHello", Policy: governance.Policy{RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1}}})
	if _, err := client.SayHello(ctx, &pb.SayHelloRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SayHello(ctx, &pb.SayHelloRequest{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("updated governance rule = %v, want ResourceExhausted", err)
	}
}
`

const goZeroRPCClientTemplate = `package rpc

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

func NewConfiguredGreeter(ctx context.Context, resolver discovery.Resolver, c config.RPCClientConfig, rules *governance.RuleSet, opts ...flygrpc.ClientOption) (pb.GreeterClient, *flygrpc.ClientConn, error) {
	target, configured, err := c.TargetAndOptions(resolver, "{{.RPCService}}")
	if err != nil {
		return nil, nil, err
	}
	opts = append(configured, opts...)
	return NewGreeter(ctx, target, rules, opts...)
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
	"github.com/imajinyun/gofly/core/limit"
	"github.com/imajinyun/gofly/core/observability/trace"
	"github.com/imajinyun/gofly/core/security"
	corediscovery "github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/proc"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"

	appconfig "{{.Module}}/internal/config"
	appdiscovery "{{.Module}}/internal/discovery"
	"{{.Module}}/internal/pb"
	apprpc "{{.Module}}/internal/api/grpc"
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
	serverRegistrar := corediscovery.Registrar(registry)
	serverService := c.Name
	serverAliases := apprpc.DiscoveryAliases()
	if c.Etcd.Enabled() {
		zrpcRegistrar, closeZRPCRegistrar, err := appdiscovery.NewZRPCRegistrar(ctx, c.Etcd)
		if err != nil { slog.Error("setup zrpc etcd registration", "error", err); return }
		defer func() { _ = closeZRPCRegistrar(context.Background()) }()
		serverRegistrar = zrpcRegistrar
		serverService = c.Etcd.Key
		serverAliases = nil
	}
	stx := svc.NewServiceContext(c)
	if err := stx.InitRPCClients(ctx, registry); err != nil { slog.Error("setup RPC clients", "error", err); return }
	defer func() {
		if err := stx.Close(); err != nil { slog.Error("close RPC clients", "error", err) }
	}()
	manager, err := governance.NewManager(governance.Config{RuleFile: c.RuleFile, Watch: c.RuleWatch}, governance.WithRuleSet(stx.Rules), governance.WithPlugin(appconfig.DefaultRPCMethodGovernance()))
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
	cpuReader := limit.NewRuntimeCPUReader()
	adaptiveLimiter, err := c.AdaptiveLimit.NewLimiter(cpuReader.Permille)
	if err != nil { slog.Error("configure adaptive admission", "error", err); return }
	serverOptions := append(authOptions,
		flygrpc.WithGovernanceManager(manager),
		flygrpc.WithServerTLS(c.TLS),
		flygrpc.WithAdminAddr(c.AdminListenOn),
		flygrpc.WithReflection(c.Reflection),
		flygrpc.WithDiscovery(serverRegistrar, corediscovery.Instance{
			ID: c.Name, Service: serverService, Endpoint: c.Advertise,
			Metadata: map[string]string{"transport": "grpc"},
		}, corediscovery.WithTTL(c.Discovery.RegistryTTL())),
		flygrpc.WithDiscoveryAliases(serverAliases...),
	)
	if adaptiveLimiter != nil { serverOptions = append(serverOptions, flygrpc.WithAdaptiveLimiter(adaptiveLimiter)) }
	grpcServer := flygrpc.NewDefaultServer(c.ListenOn, c.Name, stx.Rules, nil, serverOptions...)
	pb.RegisterGreeterServer(grpcServer.GRPCServer(), apprpc.NewGreeterServer(stx))
	slog.Info("{{.Name}} gRPC starting", "listen_on", c.ListenOn, "admin_listen_on", c.AdminListenOn)
	if err := app.Run(ctx, []app.Server{grpcServer}); err != nil {
		slog.Error("{{.Name}} gRPC stopped", "error", err)
	}
}
`
