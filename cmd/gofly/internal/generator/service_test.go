package generator

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gofly/gofly/core/governance"
)

func TestGenerateService(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateService(ServiceOptions{Name: "hello", Module: "example.com/hello", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"go.mod",
		"Dockerfile",
		"Makefile",
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join("cmd", "hello", "main.go"),
		filepath.Join("internal", "config", "config_test.go"),
		filepath.Join("internal", "config", "discovery_test.go"),
		filepath.Join("internal", "routes", "routes.go"),
		filepath.Join("internal", "api", "v1", "ping", "ping.go"),
		filepath.Join("internal", "middleware", "trim.go"),
		filepath.Join("internal", "service", "ping_test.go"),
		filepath.Join("internal", "mq", "broker.go"),
		filepath.Join("internal", "discovery", "registry.go"),
		filepath.Join("internal", "rpc", "greeter.go"),
		filepath.Join("etc", "governance.json"),
	}
	for _, rel := range paths {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected generated file %s: %v", rel, err)
		}
	}
	greeterData, err := os.ReadFile(filepath.Join(dir, "internal", "rpc", "greeter.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(greeterData), "in, ok := req.(*SayHelloRequest)") || !strings.Contains(string(greeterData), `return nil, rpc.NewError(rpc.CodeInvalidArgument, "unexpected request type for SayHello")`) {
		t.Fatalf("greeter.go handler is not defensive:\n%s", greeterData)
	}
	oldPaths := []string{
		filepath.Join("internal", "handler", "routes.go"),
		filepath.Join("internal", "handler", "ping.go"),
		filepath.Join("internal", "handler", "ping_handler.go"),
		filepath.Join("internal", "logic", "ping_logic.go"),
	}
	for _, rel := range oldPaths {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("unexpected legacy generated file %s", rel)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/Users/") || strings.Contains(string(data), "replace github.com/gofly/gofly =>") {
		t.Fatalf("go.mod contains unexpected local replace: %s", data)
	}
	routesData, err := os.ReadFile(filepath.Join(dir, "internal", "routes", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package routes",
		`"example.com/hello/internal/api/v1/ping"`,
		`Path: "/v1/ping"`,
	} {
		if !strings.Contains(string(routesData), want) {
			t.Fatalf("routes.go missing %q:\n%s", want, routesData)
		}
	}
	mainData, err := os.ReadFile(filepath.Join(dir, "cmd", "hello", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"github.com/gofly/gofly/core/governance"`,
		`appadmin "example.com/hello/internal/admin"`,
		`appdiscovery "example.com/hello/internal/discovery"`,
		`appmq "example.com/hello/internal/mq"`,
		"configPath := appconfig.ResolveConfigPath(\"hello\")",
		"config.Load(configPath",
		"config.WithEnvExpansion()",
		"config.WithStrictFields()",
		"config.WithLoadValidator(appconfig.Validate)",
		"app.BootstrapWithRuntime",
		"serviceConf.BootstrapConfig",
		"serviceConf.RunOptions()",
		"governance.NewManager",
		"governance.WithPlugin(serviceConf.ProductionGovernancePlugin())",
		"appmq.NewBroker(c.MQ, governanceManager)",
		"rest.WithGovernanceManager(governanceManager)",
		"if c.OpenAPIEnabled()",
		"httpServer.AddOpenAPIRoutes(c.OpenAPIInfo())",
		"appdiscovery.NewRegistry(ctx, c.Discovery)",
		"rpc.NewDiscoveryRegistrar(registry, c.Discovery.RegisterOptions()...)",
		"rpc.WithRegistryTTL(c.Discovery.RegistryTTL())",
		"c.ControlPlaneSnapshotWithDiscovery(ctx, registry)",
		"rpc.WithServerGovernanceManager(governanceManager)",
		"servers := []app.Server{httpServer, rpcServer}",
		"appadmin.NewServer(c.Admin.Addr, c.Admin.PathPrefix, rpcServer, appadmin.WithControlPlaneSnapshot(func(ctx context.Context) (controlplane.Snapshot, error)",
		"svc.NewServiceContext(c, mqBroker)",
		"config.Watch[appconfig.Config](ctx, configPath",
	} {
		if !strings.Contains(string(mainData), want) {
			t.Fatalf("main.go missing governance wiring %q:\n%s", want, mainData)
		}
	}
	configData, err := os.ReadFile(filepath.Join(dir, "etc", "hello.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"environment": "development"`,
		`"service": {"name": "hello"`,
		`"scaffold": {"features": ["ecosystem-compat"]}`,
		`"discovery": {"provider": "memory", "ttl": "15s", "prefix": "/gofly/services", "dialTimeout": "5s"}`,
		`"openapi": {"enabled": true, "title": "hello API", "version": "1.0.0"`,
		`"startupTimeout": 5000000000`,
		`"shutdownTimeout": 10000000000`,
		`"timeoutConfig": {"duration": 3000000000`,
		`"breakerConfig": {"openTimeout": 5000000000`,
		`"token": "change-me-admin-token"`,
		`"admin": {"enabled": true, "addr": "127.0.0.1:9090", "pathPrefix": "/admin"}`,
		`"metrics": {"enabled": true}`,
		`"mq": {"enabled": true`,
		`"driver": "memory"`,
		`"kafka": {"brokers": ["127.0.0.1:9092"]`,
		`"rabbitmq": {"url": "amqp://guest:guest@127.0.0.1:5672/"`,
		`"redisstream": {"redis": {"addr": "127.0.0.1:6379"}`,
		`"ruleFile": "etc/governance.json"`,
		`"watch": true`,
		`"transport": "mq"`,
	} {
		if !strings.Contains(string(configData), want) {
			t.Fatalf("config missing production default %q:\n%s", want, configData)
		}
	}
	var configJSON map[string]any
	if err := json.Unmarshal(configData, &configJSON); err != nil {
		t.Fatalf("generated config should be valid json: %v\n%s", err, configData)
	}
	serviceJSON, ok := configJSON["service"].(map[string]any)
	if !ok {
		t.Fatalf("generated config missing service object: %#v", configJSON["service"])
	}
	if serviceJSON["name"] != "hello" || serviceJSON["startupTimeout"] != float64(5*time.Second) || serviceJSON["shutdownTimeout"] != float64(10*time.Second) {
		t.Fatalf("service config = %#v, want normalized service identity and lifecycle", serviceJSON)
	}
	governanceData, err := os.ReadFile(filepath.Join(dir, "etc", "governance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(governanceData), `"name": "mq-default"`) || !strings.Contains(string(governanceData), `"transport": "mq"`) {
		t.Fatalf("governance.json missing mq rule:\n%s", governanceData)
	}
	adminData, err := os.ReadFile(filepath.Join(dir, "internal", "admin", "admin.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package admin",
		`"github.com/gofly/gofly/core/controlplane"`,
		`"net/http/pprof"`,
		`prefix+"/metrics"`,
		`prefix+"/control-plane"`,
		"func WithControlPlaneSnapshot(snapshot func(context.Context) (controlplane.Snapshot, error)) Option",
		"func (s *Server) serveControlPlane",
		`prefix+"/debug/pprof/goroutine"`,
		`prefix+"/rpc/admin/"`,
		"s.rpcServer.ServeHTTP(w, r)",
	} {
		if !strings.Contains(string(adminData), want) {
			t.Fatalf("admin.go missing diagnostic wiring %q:\n%s", want, adminData)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "admin", "admin_test.go")); err != nil {
		t.Fatalf("expected generated admin diagnostics test: %v", err)
	}
	assertGovernanceRuleFileLoads(t, filepath.Join(dir, "etc", "governance.json"))
	svcData, err := os.ReadFile(filepath.Join(dir, "internal", "svc", "service_context.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(svcData), `"github.com/gofly/gofly/core/mq"`) || !strings.Contains(string(svcData), "MQ     mq.Broker") {
		t.Fatalf("service context missing mq broker wiring:\n%s", svcData)
	}
	mainData, err = os.ReadFile(filepath.Join(dir, "cmd", "hello", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"restConf := serviceConf.RESTConfig(c.Rest)",
		"rest.MustNewServer(\n\t\trestConf,",
		"rpcOptions := append(serviceConf.RPCServerOptions()",
		"rpc.NewServer(rpcOptions...)",
	} {
		if !strings.Contains(string(mainData), want) {
			t.Fatalf("main.go missing ServiceConf runtime wiring %q:\n%s", want, mainData)
		}
	}
	greeterClientTestData, err := os.ReadFile(filepath.Join(dir, "internal", "rpc", "greeter_client_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`http.Get(httpServer.URL + "/rpc/admin/descriptors/greeter")`,
		`http.Post(httpServer.URL+"/rpc/admin/descriptors/greeter/compatibility"`,
		"var descriptor rpc.Descriptor",
		"var report rpc.DescriptorCompatibilityReport",
		"report.IsCompatible()",
	} {
		if !strings.Contains(string(greeterClientTestData), want) {
			t.Fatalf("greeter_client_test.go missing descriptor self-validation %q:\n%s", want, greeterClientTestData)
		}
	}
	configGoData, err := os.ReadFile(filepath.Join(dir, "internal", "config", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Service     app.ServiceConf",
		"Scaffold    ScaffoldConfig",
		"Discovery   DiscoveryConfig",
		"OpenAPI     OpenAPIConfig",
		`"github.com/gofly/gofly/core/controlplane"`,
		`"github.com/gofly/gofly/core/discovery"`,
		"func ConfigPaths(name string) []string",
		"func ResolveConfigPath(name string) string",
		`paths := []string{"config.yaml", "config.yml", "config.toml", "config.json"}`,
		"func (c Config) ServiceConf() app.ServiceConf",
		"func (c Config) ControlPlaneContributor() ControlPlaneContributor",
		"func (c Config) ControlPlaneSnapshot(ctx context.Context) (controlplane.Snapshot, error)",
		"func (c Config) ControlPlaneSnapshotWithDiscovery(ctx context.Context, registry any) (controlplane.Snapshot, error)",
		"func (c ControlPlaneContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error",
		`addGeneratedControlPlaneConfig("discovery", cfg.Discovery.Sanitized())`,
		"func (c DiscoveryConfig) ProviderName() string",
		"func (c DiscoveryConfig) RegistryTTL() time.Duration",
		"func (c DiscoveryConfig) ResolvedEndpoints() []string",
		"func (c DiscoveryConfig) RegisterOptions() []discovery.RegisterOption",
		"func ValidateDiscoveryConfig(c DiscoveryConfig) error",
		`"generated.project.contract"`,
		"func (c Config) OpenAPIEnabled() bool",
		"func (c Config) OpenAPIInfo() rest.OpenAPIInfo",
		"func ValidateOpenAPIConfig(c Config) error",
		"func (c Config) EffectiveScaffoldFeatures() []string",
		"func RegisteredScaffoldFeatures() []string",
		"func ValidateScaffoldFeatures(features []string) error",
		"func NormalizeScaffoldFeatures(features []string) []string",
		"func Validate(c Config) error",
		"app.ValidateProductionConfig",
		"rest.ValidateProductionConfig",
	} {
		if !strings.Contains(string(configGoData), want) {
			t.Fatalf("config.go missing production validator %q:\n%s", want, configGoData)
		}
	}
	discoveryData, err := os.ReadFile(filepath.Join(dir, "internal", "discovery", "registry.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package discovery",
		`"github.com/gofly/gofly/core/discovery/consul"`,
		`"github.com/gofly/gofly/core/discovery/etcdv3"`,
		"func NewRegistry(ctx context.Context, cfg appconfig.DiscoveryConfig) (corediscovery.Registry, closeFunc, error)",
		`case "memory":`,
		`case "consul":`,
		`case "etcdv3":`,
		"Endpoints:   cfg.ResolvedEndpoints()",
	} {
		if !strings.Contains(string(discoveryData), want) {
			t.Fatalf("registry.go missing %q:\n%s", want, discoveryData)
		}
	}
	configTestData, err := os.ReadFile(filepath.Join(dir, "internal", "config", "config_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func TestOpenAPIConfigDefaultsAndOverrides(t *testing.T)",
		"func TestControlPlaneSnapshotExposesGeneratedContract(t *testing.T)",
		"OpenAPI should be enabled by default",
		`"generated.project.contract"`,
		"ValidateOpenAPIConfig(defaultConfig)",
		"OpenAPIConfig{Enabled: boolPtr(false)}",
	} {
		if !strings.Contains(string(configTestData), want) {
			t.Fatalf("config_test.go missing %q:\n%s", want, configTestData)
		}
	}
	discoveryConfigTestData, err := os.ReadFile(filepath.Join(dir, "internal", "config", "discovery_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func TestDiscoveryConfigDefaultsValidationAndSnapshot(t *testing.T)",
		"func TestControlPlaneSnapshotWithDiscoveryIncludesRegistryAndSanitizesDiscovery(t *testing.T)",
		`DiscoveryConfig{Address: " 127.0.0.1:2379, ,127.0.0.2:2379 "}).ResolvedEndpoints()`,
		"discovery.NewMemoryRegistry()",
		"cfg.ControlPlaneSnapshotWithDiscovery(context.Background(), registry)",
		`json.Unmarshal(snapshot.Configs["generated.discovery"], &discoveryConfig)`,
		`discoveryConfig.TokenEnv != "CONSUL_HTTP_TOKEN"`,
	} {
		if !strings.Contains(string(discoveryConfigTestData), want) {
			t.Fatalf("discovery_test.go missing %q:\n%s", want, discoveryConfigTestData)
		}
	}
	brokerData, err := os.ReadFile(filepath.Join(dir, "internal", "mq", "broker.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package mq",
		"func NewBroker(cfg config.MQConfig, manager *governance.Manager) (coremq.Broker, error)",
		`case "", "memory":`,
		`case "kafka":`,
		`case "rabbitmq":`,
		`case "redisstream":`,
		"coremq.NewGovernanceBroker",
	} {
		if !strings.Contains(string(brokerData), want) {
			t.Fatalf("broker.go missing %q:\n%s", want, brokerData)
		}
	}
	assertGeneratedProjectCompiles(t, dir)
}

func TestApplyEnvOverlay(t *testing.T) {
	ApplyEnvOverlay(nil)

	t.Setenv("GOFLY_SERVICE_NAME", "envsvc")
	t.Setenv("GOFLY_MODULE", "example.com/envsvc")
	t.Setenv("GOFLY_STYLE", ServiceStyleProduction)
	t.Setenv("GOFLY_TEMPLATE_DIR", "/tmp/templates")
	t.Setenv("GOFLY_TEMPLATE_REMOTE", "https://example.com/templates.git")
	t.Setenv("GOFLY_TEMPLATE_BRANCH", "main")
	t.Setenv("GOFLY_FEATURES", " http-compat, ,rpc-compat ")
	t.Setenv("GOFLY_RPC_PROFILE", string(ProfileKitexCompatible))
	t.Setenv("GOFLY_DISCOVERY", "etcdv3")
	t.Setenv("GOFLY_DISCOVERY_ENDPOINTS", " 127.0.0.1:2379, ,127.0.0.2:2379 ")
	t.Setenv("GOFLY_DISCOVERY_PREFIX", "/services")
	t.Setenv("GOFLY_DISCOVERY_TTL", "20s")
	t.Setenv("GOFLY_DISCOVERY_DIAL_TIMEOUT", "3s")
	t.Setenv("GOFLY_DISCOVERY_USERNAME_ENV", "ETCD_USERNAME")
	t.Setenv("GOFLY_DISCOVERY_PASSWORD_ENV", "ETCD_PASSWORD")

	cfg := &Config{
		ServiceName:    "filesvc",
		Module:         "example.com/filesvc",
		Style:          ServiceStyleMinimal,
		TemplateDir:    "/file/templates",
		TemplateRemote: "https://example.com/file.git",
		TemplateBranch: "dev",
		Features:       []string{"ecosystem-compat"},
	}
	ApplyEnvOverlay(cfg)

	if cfg.ServiceName != "envsvc" || cfg.Module != "example.com/envsvc" || cfg.Style != ServiceStyleProduction {
		t.Fatalf("identity overlay = %#v", cfg)
	}
	if cfg.TemplateDir != "/tmp/templates" || cfg.TemplateRemote != "https://example.com/templates.git" || cfg.TemplateBranch != "main" {
		t.Fatalf("template overlay = %#v", cfg)
	}
	if strings.Join(cfg.Features, ",") != "http-compat,rpc-compat" {
		t.Fatalf("features overlay = %v, want http-compat,rpc-compat", cfg.Features)
	}
	if cfg.RPC == nil || cfg.RPC.Profile != string(ProfileKitexCompatible) {
		t.Fatalf("rpc profile overlay = %#v, want kitex-compatible", cfg.RPC)
	}
	if cfg.Discovery == nil || cfg.Discovery.Provider != "etcdv3" || strings.Join(cfg.Discovery.Endpoints, ",") != "127.0.0.1:2379,127.0.0.2:2379" || cfg.Discovery.Prefix != "/services" || cfg.Discovery.TTL != "20s" || cfg.Discovery.DialTimeout != "3s" || cfg.Discovery.UsernameEnv != "ETCD_USERNAME" || cfg.Discovery.PasswordEnv != "ETCD_PASSWORD" {
		t.Fatalf("discovery overlay = %#v, want etcdv3 endpoints and secret env names", cfg.Discovery)
	}
}

func TestTemplateAndKubeHelperBoundaries(t *testing.T) {
	tests := []struct {
		name string
		kind string
		want string
	}{
		{name: "blank defaults deployment", kind: " ", want: "orders.yaml"},
		{name: "deployment", kind: "deployment", want: "orders.yaml"},
		{name: "deploy alias", kind: "deploy", want: "orders.yaml"},
		{name: "svc alias", kind: "svc", want: "orders-service.yaml"},
		{name: "ing alias", kind: "ing", want: "orders-ingress.yaml"},
		{name: "configmap alias", kind: "cm", want: "orders-configmap.yaml"},
		{name: "job", kind: "job", want: "orders-job.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kubeOutputName("orders", tt.kind); got != tt.want {
				t.Fatalf("kubeOutputName(%q) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}

	if got, err := resolveNamedTemplates("", "", "", []string{"missing.tpl"}, "fallback"); err != nil || got != "fallback" {
		t.Fatalf("resolveNamedTemplates fallback = %q/%v, want fallback/nil", got, err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "api.tpl"), []byte("api-template"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveNamedTemplates(dir, "", "", []string{" ", "api.tpl"}, "fallback")
	if err != nil || got != "api-template" {
		t.Fatalf("resolveNamedTemplates file = %q/%v, want api-template/nil", got, err)
	}

	badDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(badDir, "bad.tpl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveNamedTemplates(badDir, "", "", []string{"bad.tpl"}, "fallback"); err == nil {
		t.Fatal("resolveNamedTemplates directory candidate succeeded, want read error")
	}

	cleanDir := filepath.Join(t.TempDir(), "templates")
	if err := os.MkdirAll(cleanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cleanDir, "api.tpl"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CleanTemplates(TemplateOptions{Dir: cleanDir}); err != nil {
		t.Fatalf("CleanTemplates: %v", err)
	}
	if _, err := os.Stat(cleanDir); !os.IsNotExist(err) {
		t.Fatalf("cleaned template dir stat err = %v, want not exist", err)
	}

	files := ListTemplates(TemplateOptions{Dir: "/tmp/templates"})
	if len(files) != 9 || files[0].Path == "" || !strings.HasPrefix(files[0].Path, "/tmp/templates") {
		t.Fatalf("ListTemplates = %#v, want fixed template paths", files)
	}
}

func TestTemplateSyncFilesystemBoundaries(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "api.tpl"), []byte("api"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "ignored"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyDir(src, filepath.Join(src, "child")); err == nil {
		t.Fatal("copyDir destination inside source succeeded, want error")
	}
	if err := copyDir(src, src); err != nil {
		t.Fatalf("copyDir same path = %v, want nil", err)
	}

	dst := filepath.Join(root, "dst")
	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "nested", "api.tpl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "api" {
		t.Fatalf("copied file = %q, want api", data)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git", "ignored")); !os.IsNotExist(err) {
		t.Fatalf("hidden dir file stat = %v, want not exist", err)
	}

	link := filepath.Join(src, "nested", "link.tpl")
	if err := os.Symlink(filepath.Join(src, "nested", "api.tpl"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := copyDir(src, filepath.Join(root, "symlink-dst")); err == nil {
		t.Fatal("copyDir with symlink source succeeded, want error")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	copyDst := filepath.Join(root, "copy", "out.tpl")
	if err := copyFile(filepath.Join(src, "nested", "api.tpl"), copyDst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	if got, err := os.ReadFile(copyDst); err != nil || string(got) != "api" {
		t.Fatalf("copyFile output = %q/%v, want api/nil", got, err)
	}
	if err := copyFile(filepath.Join(src, "nested", "api.tpl"), filepath.Join(src, "nested", "api.tpl")); err != nil {
		t.Fatalf("copyFile same path = %v, want nil", err)
	}

	if !samePath(src, src) {
		t.Fatal("samePath self = false, want true")
	}
	if !childPath(src, filepath.Join(src, "nested")) {
		t.Fatal("childPath child = false, want true")
	}
	if childPath(src, src) {
		t.Fatal("childPath self = true, want false")
	}
	if childPath(src, filepath.Join(root, "sibling")) {
		t.Fatal("childPath sibling = true, want false")
	}
	if err := validateTemplateSyncDir(" "); err == nil {
		t.Fatal("validateTemplateSyncDir blank succeeded, want error")
	}
}

func TestGenerateNewServiceVariantsBoundaries(t *testing.T) {
	if err := GenerateAPINew(APINewOptions{Module: "example.com/app"}); err == nil {
		t.Fatal("GenerateAPINew without name succeeded, want error")
	}
	if err := GenerateAPINew(APINewOptions{Name: "api"}); err == nil {
		t.Fatal("GenerateAPINew without module succeeded, want error")
	}
	if err := GenerateRPCNew(RPCNewOptions{Module: "example.com/app"}); err == nil {
		t.Fatal("GenerateRPCNew without name succeeded, want error")
	}
	if err := GenerateRPCNew(RPCNewOptions{Name: "rpc"}); err == nil {
		t.Fatal("GenerateRPCNew without module succeeded, want error")
	}

	apiDir := filepath.Join(t.TempDir(), "api")
	if err := GenerateAPINew(APINewOptions{Name: "orders", Module: "example.com/orders", Dir: apiDir, SkipAPISpec: true}); err != nil {
		t.Fatalf("GenerateAPINew skip api spec: %v", err)
	}
	if _, err := os.Stat(filepath.Join(apiDir, "orders.api")); !os.IsNotExist(err) {
		t.Fatalf("skipped api spec stat = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(apiDir, "go.mod")); err != nil {
		t.Fatalf("GenerateAPINew did not create service go.mod: %v", err)
	}

	rpcDir := filepath.Join(t.TempDir(), "rpc")
	if err := GenerateRPCNew(RPCNewOptions{Name: "Greeter", Module: "example.com/greeter", Dir: rpcDir}); err != nil {
		t.Fatalf("GenerateRPCNew: %v", err)
	}
	protoData, err := os.ReadFile(filepath.Join(rpcDir, "Greeter.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(protoData), "package greeter;") || strings.Contains(string(protoData), "package greeter.v1;") {
		t.Fatalf("generated rpc proto package not normalized:\n%s", protoData)
	}

	kitexDir := filepath.Join(t.TempDir(), "kitex-rpc")
	if err := GenerateRPCNew(RPCNewOptions{Name: "Greeter", Module: "example.com/greeter", Dir: kitexDir, Profile: string(ProfileKitexCompatible)}); err != nil {
		t.Fatalf("GenerateRPCNew kitex-compatible: %v", err)
	}
	if _, err := os.Stat(filepath.Join(kitexDir, "internal", "compat", "kitex", "adapter.go")); err != nil {
		t.Fatalf("GenerateRPCNew kitex-compatible adapter: %v", err)
	}
	kitexProtoData, err := os.ReadFile(filepath.Join(kitexDir, "Greeter.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kitexProtoData), "package greeter;") || strings.Contains(string(kitexProtoData), "package greeter.v1;") {
		t.Fatalf("generated kitex-compatible rpc proto package not normalized:\n%s", kitexProtoData)
	}
	assertGeneratedProjectCompiles(t, kitexDir)
}

func TestGenerateMigrationBoundaries(t *testing.T) {
	if err := GenerateMigration(MigrationOptions{Name: " "}); err == nil {
		t.Fatal("GenerateMigration blank name succeeded, want error")
	}

	dir := t.TempDir()
	when := time.Date(2026, 6, 19, 12, 30, 45, 0, time.UTC)
	if err := GenerateMigration(MigrationOptions{Name: " Create-User Table!! ", Dir: dir, Time: when}); err != nil {
		t.Fatalf("GenerateMigration: %v", err)
	}
	base := filepath.Join(dir, "20260619123045_create_user_table")
	for _, suffix := range []string{".up.sql", ".down.sql"} {
		data, err := os.ReadFile(base + suffix)
		if err != nil {
			t.Fatalf("read migration %s: %v", suffix, err)
		}
		if len(data) == 0 {
			t.Fatalf("migration %s is empty", suffix)
		}
	}
	if got := migrationName(" !!! "); got != "migration" {
		t.Fatalf("migrationName punctuation = %q, want migration", got)
	}
}

func TestGenerateHandlerAndMiddlewareBoundaries(t *testing.T) {
	if err := GenerateHandler(HandlerOptions{Name: "", Module: "example.com/app", Dir: t.TempDir()}); err == nil {
		t.Fatal("GenerateHandler blank name succeeded, want error")
	}
	if err := GenerateHandler(HandlerOptions{Name: "ListUsers", Module: "example.com/app", Dir: t.TempDir(), Path: "../escape"}); err == nil {
		t.Fatal("GenerateHandler path traversal succeeded, want error")
	}
	if err := GenerateHandler(HandlerOptions{Name: "ListUsers", Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "read go.mod") {
		t.Fatalf("GenerateHandler missing module error = %v, want go.mod read error", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/orders\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateHandler(HandlerOptions{Name: "ListUsers", Dir: dir, Path: "v1/admin"}); err != nil {
		t.Fatalf("GenerateHandler inferred module: %v", err)
	}
	handlerPath := filepath.Join(dir, "internal", "api", "v1", "admin", "list_users.go")
	handlerData, err := os.ReadFile(handlerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"package admin", `"example.com/orders/internal/svc"`, "func ListUsersHandler", `"message": "ListUsers"`} {
		if !strings.Contains(string(handlerData), want) {
			t.Fatalf("generated handler missing %q:\n%s", want, handlerData)
		}
	}

	if got := handlerPackageName(""); got != "api" {
		t.Fatalf("handlerPackageName blank = %q, want api", got)
	}
	if got := handlerPackageName(filepath.Join("v1", "admin-api")); got != "adminapi" {
		t.Fatalf("handlerPackageName nested = %q, want adminapi", got)
	}
	if got, err := cleanHandlerSubdir("."); err != nil || got != "" {
		t.Fatalf("cleanHandlerSubdir dot = %q/%v, want empty/nil", got, err)
	}

	if err := GenerateMiddleware(MiddlewareOptions{Names: []string{" ", ""}, Dir: t.TempDir()}); err == nil {
		t.Fatal("GenerateMiddleware blank names succeeded, want error")
	}
	middlewareDir := t.TempDir()
	if err := GenerateMiddleware(MiddlewareOptions{Names: []string{"trace-id", "trace_id", "Audit.Log"}, Dir: middlewareDir}); err != nil {
		t.Fatalf("GenerateMiddleware: %v", err)
	}
	for _, rel := range []string{
		filepath.Join("internal", "middleware", "trace_id.go"),
		filepath.Join("internal", "middleware", "audit_log.go"),
	} {
		if _, err := os.Stat(filepath.Join(middlewareDir, rel)); err != nil {
			t.Fatalf("expected middleware file %s: %v", rel, err)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(middlewareDir, "internal", "middleware")); err != nil || len(entries) != 2 {
		t.Fatalf("middleware entries = %d/%v, want two deduplicated files", len(entries), err)
	}
}

func TestSyncTemplateRemoteLocalBoundaries(t *testing.T) {
	if err := SyncTemplateRemote(TemplateOptions{Dir: t.TempDir(), Remote: " "}); err != nil {
		t.Fatalf("SyncTemplateRemote empty remote = %v, want nil", err)
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote")
	payload := filepath.Join(remote, "templates")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "api.tpl"), []byte("from-remote"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "templates")
	if err := os.WriteFile(dir, []byte("not-a-dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncTemplateRemote(TemplateOptions{Dir: dir, Remote: "file://" + remote}); err != nil {
		t.Fatalf("SyncTemplateRemote local payload: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "api.tpl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "from-remote" {
		t.Fatalf("synced template = %q, want from-remote", data)
	}

	fileRemote := filepath.Join(root, "remote-file")
	if err := os.WriteFile(fileRemote, []byte("not-dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncTemplateRemote(TemplateOptions{Dir: filepath.Join(root, "out"), Remote: "file://" + fileRemote}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("SyncTemplateRemote file remote error = %v, want not a directory", err)
	}

	link := filepath.Join(root, "template-link")
	if err := os.Symlink(filepath.Join(root, "elsewhere"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := SyncTemplateRemote(TemplateOptions{Dir: link, Remote: remote}); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("SyncTemplateRemote symlink dir error = %v, want symlink rejection", err)
	}
}

func TestHandlerCompleterBoundaries(t *testing.T) {
	if _, err := CompleteHandlersFromIDL(HandlerCompleteOptions{IDLFile: ""}); err == nil {
		t.Fatal("CompleteHandlersFromIDL blank idl succeeded, want error")
	}

	dir := t.TempDir()
	apiPath := filepath.Join(dir, "users.api")
	api := `type LoginReq {
  Username string
}
type LoginResp {
  Token string
}
service user-api {
  @handler login
  post /login (LoginReq) returns (LoginResp)
  get /health returns (LoginResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	methods := methodsFromIDLDocument(IDLDocument{Services: []IDLService{{
		Name: "user-api",
		Methods: []IDLMethod{
			{Name: "ListUsers", Handler: "list", HTTPMethod: "get", HTTPPath: "/users"},
			{Name: "", Handler: ""},
		},
	}}})
	if len(methods) != 1 || methods[0].Name != "list" || !strings.Contains(methods[0].Body, "GET /users") {
		t.Fatalf("methodsFromIDLDocument = %#v, want handler-backed method", methods)
	}

	file := filepath.Join(dir, "handler.go")
	completer := NewHandlerCompleter(file, "h", "handler", []string{"context"})
	existing, err := completer.ExistingMethods()
	if err != nil || existing != nil {
		t.Fatalf("ExistingMethods missing file = %#v/%v, want nil/nil", existing, err)
	}
	count, err := completer.Complete([]Method{
		{Name: "login", Comment: "Login handles user login.\nSecond line.", Body: "\t_ = context.Background()\n"},
		{Name: "", Body: "\tpanic(\"skip\")\n"},
	})
	if err != nil || count != 1 {
		t.Fatalf("Complete create = %d/%v, want one method", count, err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"package handler", `"context"`, "// Login handles user login.", "// Second line.", "func (h *H) Login()"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("created handler file missing %q:\n%s", want, data)
		}
	}
	existing, err = completer.ExistingMethods()
	if err != nil || strings.Join(existing, ",") != "Login" {
		t.Fatalf("ExistingMethods = %#v/%v, want Login", existing, err)
	}
	count, err = completer.Complete([]Method{
		{Name: "Login", Body: "\tpanic(\"duplicate\")\n"},
		{Name: "health", Signature: "func (h *H) Health(ctx context.Context) error {\n", Body: "\treturn nil\n"},
	})
	if err != nil || count != 1 {
		t.Fatalf("Complete append = %d/%v, want one new method", count, err)
	}
	data, err = os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "func (h *H) Login()") != 1 || !strings.Contains(string(data), "func (h *H) Health(ctx context.Context) error") {
		t.Fatalf("appended handler methods not deduplicated/appended:\n%s", data)
	}

	completedFile := filepath.Join(dir, "from_idl.go")
	count, err = CompleteHandlersFromIDL(HandlerCompleteOptions{IDLFile: apiPath, File: completedFile, Receiver: "handler", Package: "handler"})
	if err != nil || count != 2 {
		t.Fatalf("CompleteHandlersFromIDL = %d/%v, want two generated methods", count, err)
	}
	completedData, err := os.ReadFile(completedFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(completedData), "func (handler *Handler) Login()") || !strings.Contains(string(completedData), "func (handler *Handler) GetHealth()") {
		t.Fatalf("CompleteHandlersFromIDL output missing generated methods:\n%s", completedData)
	}

	if got := ExtractMethodSignature("func (h *Handler) Ping() error {\n\treturn nil\n}"); got != "func (h *Handler) Ping() error" {
		t.Fatalf("ExtractMethodSignature = %q, want method signature", got)
	}
	if got := ExtractMethodSignature("var notFunc = true"); got != "" {
		t.Fatalf("ExtractMethodSignature invalid = %q, want empty", got)
	}
	block := RenderMethodBlock("ping", "\treturn\n", "Ping handles health.")
	if !strings.Contains(block, "// Ping handles health.") || !strings.Contains(block, "func Ping()") {
		t.Fatalf("RenderMethodBlock = %q, want comment and exported function", block)
	}
}

func TestGenerateServiceMinimalStyle(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateService(ServiceOptions{Name: "hello", Module: "example.com/hello", Dir: dir, Style: ServiceStyleMinimal}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"go.mod",
		filepath.Join("cmd", "hello", "main.go"),
		filepath.Join("etc", "hello.json"),
		filepath.Join("internal", "config", "config.go"),
		filepath.Join("internal", "config", "config_test.go"),
		filepath.Join("internal", "routes", "routes.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected minimal generated file %s: %v", rel, err)
		}
	}
	for _, rel := range []string{
		"Dockerfile",
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join("etc", "governance.json"),
		filepath.Join("internal", "discovery", "registry.go"),
		filepath.Join("internal", "rpc", "greeter.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("unexpected production file in minimal scaffold %s", rel)
		}
	}
	mainData, err := os.ReadFile(filepath.Join(dir, "cmd", "hello", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mainData), "governance.NewManager") || strings.Contains(string(mainData), "rpc.NewServer") {
		t.Fatalf("minimal main contains production wiring:\n%s", mainData)
	}
	for _, want := range []string{
		"config.WithStrictFields()",
		"config.WithEnvExpansion()",
		"config.WithLoadValidator(appconfig.Validate)",
		"serviceConf.BootstrapConfig",
		"serviceConf.RunOptions()",
		"app.Bootstrap",
		"if c.OpenAPIEnabled()",
		"httpServer.AddOpenAPIRoutes(c.OpenAPIInfo())",
	} {
		if !strings.Contains(string(mainData), want) {
			t.Fatalf("minimal main missing bootstrap/config governance %q:\n%s", want, mainData)
		}
	}
	configData, err := os.ReadFile(filepath.Join(dir, "internal", "config", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configData), "Governance") || strings.Contains(string(configData), "RPC") {
		t.Fatalf("minimal config contains production-only fields:\n%s", configData)
	}
	if strings.Contains(string(configData), "app.MetricsConfig") || strings.Contains(string(configData), "Log  app.LogConfig") {
		t.Fatalf("minimal config contains legacy top-level service fields:\n%s", configData)
	}
	if !strings.Contains(string(configData), "func Validate(c Config) error") || !strings.Contains(string(configData), "rest.ValidateProductionConfig") {
		t.Fatalf("minimal config missing production validator:\n%s", configData)
	}
	for _, want := range []string{
		"Scaffold    ScaffoldConfig",
		"OpenAPI     OpenAPIConfig",
		`"github.com/gofly/gofly/core/controlplane"`,
		"func (c Config) ControlPlaneSnapshot(ctx context.Context) (controlplane.Snapshot, error)",
		"func (c ControlPlaneContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error",
		`"generated.project.runtime"] = "service,rest"`,
		"func (c Config) OpenAPIEnabled() bool",
		"func (c Config) OpenAPIInfo() rest.OpenAPIInfo",
		"func ValidateOpenAPIConfig(c Config) error",
		"func (c Config) EffectiveScaffoldFeatures() []string",
		"func RegisteredScaffoldFeatures() []string",
		"func ValidateScaffoldFeatures(features []string) error",
		"func NormalizeScaffoldFeatures(features []string) []string",
	} {
		if !strings.Contains(string(configData), want) {
			t.Fatalf("minimal config missing scaffold feature helper %q:\n%s", want, configData)
		}
	}
	configTestData, err := os.ReadFile(filepath.Join(dir, "internal", "config", "config_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configTestData), "func TestOpenAPIConfigDefaultsAndOverrides(t *testing.T)") ||
		!strings.Contains(string(configTestData), "func TestControlPlaneSnapshotExposesGeneratedContract(t *testing.T)") ||
		!strings.Contains(string(configTestData), "ValidateOpenAPIConfig(defaultConfig)") {
		t.Fatalf("minimal config_test.go missing OpenAPI helper tests:\n%s", configTestData)
	}
	minimalConfigJSON, err := os.ReadFile(filepath.Join(dir, "etc", "hello.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"scaffold": {"features": ["ecosystem-compat"]}`,
		`"openapi": {"enabled": true, "title": "hello API", "version": "1.0.0"`,
		`"trace": {"enabled": true, "sampler": "always_on"}`,
		`"trace": true`,
		`"log": true`,
	} {
		if !strings.Contains(string(minimalConfigJSON), want) {
			t.Fatalf("minimal json config missing %q:\n%s", want, minimalConfigJSON)
		}
	}
}

func TestGenerateServiceBasicStyle(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateService(ServiceOptions{Name: "hello", Module: "example.com/hello", Dir: dir, Style: ServiceStyleBasic}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"go.mod",
		"Dockerfile",
		"Makefile",
		filepath.Join("cmd", "hello", "main.go"),
		filepath.Join("etc", "hello.json"),
		filepath.Join("internal", "routes", "routes.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected basic generated file %s: %v", rel, err)
		}
	}
	for _, rel := range []string{
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join("etc", "governance.json"),
		filepath.Join("internal", "discovery", "registry.go"),
		filepath.Join("internal", "rpc", "greeter.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("unexpected production file in basic scaffold %s", rel)
		}
	}
	assertGeneratedProjectCompiles(t, dir)
}

func assertGeneratedProjectCompiles(t *testing.T, dir string) {
	t.Helper()
	frameworkPath := repositoryRoot(t)
	runGoCommand(t, dir, 30*time.Second, "mod", "edit", "-replace", "github.com/gofly/gofly="+frameworkPath)
	runGoCommand(t, dir, 2*time.Minute, "mod", "tidy")
	runGoCommand(t, dir, 2*time.Minute, "test", "./...")
}

func assertGovernanceRuleFileLoads(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated governance rule file: %v", err)
	}
	var rules []governance.Rule
	if err := json.Unmarshal(data, &rules); err != nil {
		t.Fatalf("decode generated governance rule file: %v\n%s", err, data)
	}
	if err := governance.ValidateRules(rules...); err != nil {
		t.Fatalf("validate generated governance rules: %v\n%s", err, data)
	}
	m, err := governance.NewManager(governance.Config{RuleFile: path})
	if err != nil {
		t.Fatalf("create governance manager for generated rule file: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("load generated governance rule file: %v", err)
	}
	mqRule := firstMQRule(t, rules)
	if decision := m.RuleSet().Match(requestForRule(mqRule)); !decision.Matched {
		t.Fatalf("generated governance rules should include mq rule, decision=%#v", decision)
	}
}

func assertEmbeddedGovernanceConfigLoads(t *testing.T, data []byte) {
	t.Helper()
	var doc struct {
		Governance governance.Config `json:"governance"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode generated config: %v\n%s", err, data)
	}
	if err := governance.ValidateRules(doc.Governance.Rules...); err != nil {
		t.Fatalf("validate generated embedded governance rules: %v\n%s", err, data)
	}
	m, err := governance.NewManager(doc.Governance)
	if err != nil {
		t.Fatalf("create governance manager for embedded config: %v", err)
	}
	mqRule := firstMQRule(t, doc.Governance.Rules)
	decision := m.RuleSet().Match(requestForRule(mqRule))
	if !decision.Matched {
		t.Fatalf("embedded governance rules should include mq rule, decision=%#v", decision)
	}
}

func firstMQRule(t *testing.T, rules []governance.Rule) governance.Rule {
	t.Helper()
	for _, rule := range rules {
		if rule.Transport == governance.TransportMQ {
			return rule
		}
	}
	t.Fatalf("generated governance rules missing mq rule: %#v", rules)
	return governance.Rule{}
}

func requestForRule(rule governance.Rule) governance.Request {
	return governance.Request{
		Transport: rule.Transport,
		Service:   rule.Service,
		Method:    rule.Method,
		Path:      rule.Path,
		Tags:      rule.Tags,
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root: runtime caller unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read framework go.mod: %v", err)
	}
	if !strings.Contains(string(data), "module github.com/gofly/gofly") {
		t.Fatalf("framework root %s has unexpected go.mod:\n%s", root, data)
	}
	return root
}

func runGoCommand(t *testing.T, dir string, timeout time.Duration, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("go %s timed out after %s in %s:\n%s", strings.Join(args, " "), timeout, dir, out)
	}
	if err != nil {
		t.Fatalf("go %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

func TestGenerateAPINewSupportsProductionStyle(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateAPINew(APINewOptions{Name: "hello", Module: "example.com/hello", Dir: dir, Style: ServiceStyleProduction}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"hello.api",
		"Dockerfile",
		filepath.Join("etc", "governance.json"),
		filepath.Join("internal", "rpc", "greeter.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected api production generated file %s: %v", rel, err)
		}
	}
}

func TestGenerateAPINewCanSkipAPISpec(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateAPINew(APINewOptions{Name: "hello", Module: "example.com/hello", Dir: dir, SkipAPISpec: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.api")); err == nil {
		t.Fatal("unexpected api spec when SkipAPISpec is enabled")
	}
}

func TestGenerateDockerfileWithGoctlOptions(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "Dockerfile.api")
	if err := GenerateDockerfile(DockerOptions{
		Name:      "hello",
		Output:    output,
		GoFile:    "./cmd/api",
		Exe:       "api",
		GoVersion: "1.25",
		BaseImage: "alpine:3.20",
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FROM golang:1.25 AS builder",
		"go build -o /out/api ./cmd/api",
		"FROM alpine:3.20",
		`ENTRYPOINT ["/app/api"]`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, data)
		}
	}
}

func TestGenerateKubeWithOutput(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "kube", "deploy.yaml")
	if err := GenerateKube(KubeOptions{Name: "hello", Output: output, Image: "example/hello:v1"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "image: example/hello:v1") {
		t.Fatalf("kube yaml = %s", data)
	}
}

func TestGenerateKubeProductionOptions(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "deploy.yaml")
	if err := GenerateKube(KubeOptions{
		Name:            "hello",
		Output:          output,
		Namespace:       "apps",
		Image:           "example/hello:v2",
		Port:            "9090",
		Replicas:        "3",
		Secret:          "regcred",
		NodePort:        "30090",
		Revisions:       "5",
		MinReplicas:     "2",
		MaxReplicas:     "5",
		RequestCPU:      "100m",
		RequestMem:      "128Mi",
		LimitCPU:        "500m",
		LimitMem:        "512Mi",
		ImagePullPolicy: "IfNotPresent",
		ServiceAccount:  "hello-sa",
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		"revisionHistoryLimit: 5",
		"serviceAccountName: hello-sa",
		"imagePullSecrets:",
		"- name: regcred",
		"imagePullPolicy: IfNotPresent",
		"resources:",
		"requests:",
		"cpu: 100m",
		"memory: 128Mi",
		"limits:",
		"cpu: 500m",
		"memory: 512Mi",
		"type: NodePort",
		"nodePort: 30090",
		"kind: HorizontalPodAutoscaler",
		"minReplicas: 2",
		"maxReplicas: 5",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("kube yaml missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateKubeResourceKinds(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateKube(KubeOptions{Name: "hello", Dir: dir, Kind: "service", Port: "9090"}); err != nil {
		t.Fatal(err)
	}
	serviceData, err := os.ReadFile(filepath.Join(dir, "hello-service.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serviceData), "kind: Service") || !strings.Contains(string(serviceData), "port: 9090") {
		t.Fatalf("service yaml = %s", serviceData)
	}

	if err := GenerateKube(KubeOptions{Name: "hello", Dir: dir, Kind: "ingress", Host: "hello.example.com", Path: "/api"}); err != nil {
		t.Fatal(err)
	}
	ingressData, err := os.ReadFile(filepath.Join(dir, "hello-ingress.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ingressData), "kind: Ingress") || !strings.Contains(string(ingressData), "host: hello.example.com") {
		t.Fatalf("ingress yaml = %s", ingressData)
	}

	if err := GenerateKube(KubeOptions{Name: "hello", Dir: dir, Kind: "configmap", Config: map[string]string{"app.json": `{"debug":true}`}}); err != nil {
		t.Fatal(err)
	}
	configData, err := os.ReadFile(filepath.Join(dir, "hello-configmap.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), "kind: ConfigMap") || !strings.Contains(string(configData), `app.json: "{\"debug\":true}"`) {
		t.Fatalf("configmap yaml = %s", configData)
	}
}

func TestGenerateKubeJobConfigMultilineAndErrors(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateKube(KubeOptions{
		Name:      "worker",
		Dir:       dir,
		Kind:      "job",
		Image:     "example/worker:v1",
		Namespace: "batch",
	}); err != nil {
		t.Fatalf("GenerateKube job: %v", err)
	}
	jobData, err := os.ReadFile(filepath.Join(dir, "worker-job.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"kind: Job",
		"namespace: batch",
		"image: example/worker:v1",
	} {
		if !strings.Contains(string(jobData), want) {
			t.Fatalf("job yaml missing %q:\n%s", want, jobData)
		}
	}
	if err := GenerateKube(KubeOptions{Name: "settings", Dir: dir, Kind: "configmap", Config: map[string]string{"app.yaml": "debug: true\nlevel: info\n"}}); err != nil {
		t.Fatalf("GenerateKube configmap multiline: %v", err)
	}
	configData, err := os.ReadFile(filepath.Join(dir, "settings-configmap.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app.yaml: |", "debug: true", "level: info"} {
		if !strings.Contains(string(configData), want) {
			t.Fatalf("configmap yaml missing %q:\n%s", want, configData)
		}
	}
	if err := GenerateKube(KubeOptions{Name: "bad", Dir: dir, Kind: "cronjob"}); err == nil || !strings.Contains(err.Error(), "unsupported kube resource kind") {
		t.Fatalf("unsupported kube kind error = %v", err)
	}
	if err := GenerateKube(KubeOptions{Dir: dir}); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("missing kube name error = %v", err)
	}
}

func TestGenerateDockerfileDefaultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateDockerfile(DockerOptions{Name: "hello", Dir: dir}); err != nil {
		t.Fatalf("GenerateDockerfile defaults: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FROM golang:1.26 AS builder",
		"go build -o /out/hello ./cmd/hello",
		"FROM gcr.io/distroless/static-debian12",
		`ENTRYPOINT ["/app/hello"]`,
		"ENV TZ=UTC",
		"EXPOSE 8080",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("default Dockerfile missing %q:\n%s", want, data)
		}
	}
	if err := GenerateDockerfile(DockerOptions{Dir: dir}); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("missing docker name error = %v", err)
	}
}

func TestTemplateListAndClean(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := GenerateTemplateInit(TemplateOptions{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	files := ListTemplates(TemplateOptions{Dir: dir})
	wantTemplates := []string{"api.tpl", "rpc.tpl", "model.tpl", "docker.tpl", "kube-deployment.tpl", "kube-service.tpl", "kube-ingress.tpl", "kube-configmap.tpl", "kube-job.tpl"}
	if len(files) != len(wantTemplates) {
		t.Fatalf("template files = %+v, want %d templates", files, len(wantTemplates))
	}
	for i, want := range wantTemplates {
		if files[i].Name != want {
			t.Fatalf("template files = %+v, want template %d to be %s", files, i, want)
		}
	}
	if err := CleanTemplates(TemplateOptions{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("template directory still exists after clean")
	}
}

func TestTemplateRemoteSyncAndIDLTemplateOverride(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(filepath.Join(remote, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "templates", "api.tpl"), []byte("syntax = v1\n\nservice {{.Name}} {\n\t@handler RemotePing\n\tget /remote returns (string)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "templates", "rpc.tpl"), []byte("syntax = \"proto3\";\npackage {{.Name}}.remote;\nservice Remote{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	local := filepath.Join(dir, "local")
	if err := GenerateTemplateInit(TemplateOptions{Dir: local, Remote: remote, StrictRemote: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(local, "api.tpl")); err != nil {
		t.Fatalf("expected synced api template: %v", err)
	}

	apiOut := filepath.Join(dir, "remote.api")
	if err := GenerateAPITemplate(IDLTemplateOptions{Output: apiOut, Name: "hello", TemplateDir: local}); err != nil {
		t.Fatal(err)
	}
	apiData, err := os.ReadFile(apiOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(apiData), "RemotePing") || !strings.Contains(string(apiData), "service hello") {
		t.Fatalf("remote api template not applied:\n%s", apiData)
	}

	rpcOut := filepath.Join(dir, "remote.proto")
	if err := GenerateRPCTemplate(IDLTemplateOptions{Output: rpcOut, Name: "greeter", Remote: remote}); err != nil {
		t.Fatal(err)
	}
	rpcData, err := os.ReadFile(rpcOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rpcData), "package greeter.remote") {
		t.Fatalf("remote rpc template not applied:\n%s", rpcData)
	}
}

func TestResolveTemplateSourceStrictAndFallback(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing-remote")
	if _, err := ResolveTemplateSource(filepath.Join(dir, "fallback"), missing, "main", false); err != nil {
		t.Fatalf("non-strict remote failure should fall back without error: %v", err)
	}
	if _, err := ResolveTemplateSource(filepath.Join(dir, "strict"), missing, "main", true); err == nil {
		t.Fatal("strict remote failure succeeded, want error")
	}
}

func TestTemplatePayloadDirPriority(t *testing.T) {
	dir := t.TempDir()
	rootTemplate := filepath.Join(dir, "api.tpl")
	nestedTemplate := filepath.Join(dir, "templates", "api.tpl")
	if err := os.WriteFile(rootTemplate, []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(nestedTemplate), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nestedTemplate, []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := templatePayloadDir(dir); got != filepath.Join(dir, "templates") {
		t.Fatalf("payload dir = %s, want nested templates directory", got)
	}
}

func TestApplyTemplateExtensionRejectsSymlinkTemplate(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.tpl")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(templates, "api.tpl")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err := ApplyTemplateExtension(templates, map[string]string{"api.tpl": "safe"})
	if err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("ApplyTemplateExtension symlink err = %v, want symlink rejection", err)
	}
}

func TestCopyFileSamePathIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "template.tpl")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(path, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("same-path copy changed file to %q", data)
	}
}

func TestCopyDirSamePathIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "template.tpl")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(dir, dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("same-path copy changed file to %q", data)
	}
}

func TestCopyDirRejectsDestinationInsideSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "template.tpl"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := copyDir(dir, filepath.Join(dir, "nested", "templates"))
	if err == nil || !strings.Contains(err.Error(), "inside source") {
		t.Fatalf("copyDir nested destination error = %v, want inside source error", err)
	}
}

func TestCopyDirRejectsSymlinkSourceEntry(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(src, "secret.tpl")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	err := copyDir(src, dst)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("copyDir symlink error = %v, want symlink rejection", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "secret.tpl")); err == nil {
		t.Fatal("copyDir copied symlink target into destination")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat copied symlink target: %v", err)
	}
}

func TestValidateTemplateSyncDirRejectsDangerousTargets(t *testing.T) {
	if err := validateTemplateSyncDir(string(filepath.Separator)); err == nil || !strings.Contains(err.Error(), "dangerous") {
		t.Fatalf("validateTemplateSyncDir(root) = %v, want dangerous target error", err)
	}
	link := filepath.Join(t.TempDir(), "templates-link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := validateTemplateSyncDir(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("validateTemplateSyncDir(symlink) = %v, want symlink error", err)
	}
}

func TestSyncTemplateRemoteRejectsMissingLocalFileRemote(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "missing")
	err := SyncTemplateRemote(TemplateOptions{Dir: filepath.Join(dir, "templates"), Remote: "file://" + remote})
	if err == nil || !strings.Contains(err.Error(), "stat template remote") {
		t.Fatalf("SyncTemplateRemote missing file remote error = %v, want stat template remote", err)
	}

	fileRemote := filepath.Join(dir, "template.tpl")
	if err := os.WriteFile(fileRemote, []byte("template"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = SyncTemplateRemote(TemplateOptions{Dir: filepath.Join(dir, "templates"), Remote: "file://" + fileRemote})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("SyncTemplateRemote file remote error = %v, want not a directory", err)
	}
}

func TestGenerateServiceScaffoldAppliesRemoteTemplateDirectory(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "go.mod.tpl"), []byte("module {{.Module}}\n\ngo 1.26\n\n// remote template\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "svc")
	if err := GenerateServiceScaffold(ServiceScaffoldOptions{Name: "hello", Module: "example.com/hello", Dir: out, Style: ServiceStyleMinimal, TemplateRemote: remote}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "// remote template") {
		t.Fatalf("remote scaffold template was not applied:\n%s", data)
	}
}

func TestDefaultEcosystemCompatibilityFeatures(t *testing.T) {
	features := ListFeatures()
	for _, want := range []string{"ecosystem-compat", "http-compat", "rpc-compat"} {
		if !containsString(features, want) {
			t.Fatalf("features = %v, want default feature %q", features, want)
		}
	}
	for _, removed := range []string{"go-zero", "gozero", "kitex"} {
		if containsString(features, removed) {
			t.Fatalf("features = %v, should not contain removed compatibility alias %q", features, removed)
		}
	}
}

func TestGenerateServiceScaffoldNeutralCompatibilityFeatureAliases(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateServiceScaffold(ServiceScaffoldOptions{
		Name:     "hello",
		Module:   "example.com/hello",
		Dir:      dir,
		Style:    ServiceStyleMinimal,
		Features: []string{"http-compat", "rpc-compat"},
	}); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		filepath.Join("internal", "compat", "gozero", "adapter.go"),
		filepath.Join("internal", "compat", "kitex", "adapter.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("neutral compatibility feature aliases should generate %s: %v", rel, err)
		}
	}
}

func TestGenerateServiceScaffoldEcosystemCompatibilityFeature(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateServiceScaffold(ServiceScaffoldOptions{
		Name:     "hello",
		Module:   "example.com/hello",
		Dir:      dir,
		Style:    ServiceStyleMinimal,
		Features: []string{"ecosystem-compat"},
	}); err != nil {
		t.Fatal(err)
	}

	gozeroPath := filepath.Join(dir, "internal", "compat", "gozero", "adapter.go")
	gozeroData, err := os.ReadFile(gozeroPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package gozero",
		"type Handler func(http.ResponseWriter, *http.Request)",
		"func FromHandler(handler Handler) rest.HandlerFunc",
		"func FromMiddleware(middleware Middleware) rest.Middleware",
	} {
		if !strings.Contains(string(gozeroData), want) {
			t.Fatalf("go-zero adapter missing %q:\n%s", want, gozeroData)
		}
	}

	kitexPath := filepath.Join(dir, "internal", "compat", "kitex", "adapter.go")
	kitexData, err := os.ReadFile(kitexPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package kitex",
		"type Endpoint func(context.Context, any) (any, error)",
		"func Method(name string, newRequest func() any, endpoint Endpoint, opts ...MethodOption) rpc.MethodDesc",
		"func Service(name string, methods ...rpc.MethodDesc) rpc.ServiceDesc",
	} {
		if !strings.Contains(string(kitexData), want) {
			t.Fatalf("kitex adapter missing %q:\n%s", want, kitexData)
		}
	}
	assertGeneratedProjectCompiles(t, dir)
}

func TestGenerateServiceScaffoldGoZeroCompatibleLayeredOutput(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateServiceScaffold(ServiceScaffoldOptions{
		Name:        "hello",
		Module:      "example.com/hello",
		Dir:         dir,
		Style:       ServiceStyleMinimal,
		Profile:     string(ProfileGoZeroCompatible),
		SkipAPISpec: true,
	}); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		filepath.Join("cmd", "hello", "main.go"),
		filepath.Join("internal", "handler", "routes.go"),
		filepath.Join("internal", "handler", "pinghandler.go"),
		filepath.Join("internal", "logic", "pinglogic.go"),
		filepath.Join("internal", "svc", "servicecontext.go"),
		filepath.Join("internal", "types", "types.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected gozero-compatible generated file %s: %v", rel, err)
		}
	}
	for _, rel := range []string{
		filepath.Join("internal", "routes", "routes.go"),
		filepath.Join("internal", "api", "v1", "ping", "ping.go"),
		filepath.Join("internal", "service", "ping.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("unexpected legacy scaffold file %s", rel)
		}
	}

	handlerData, err := os.ReadFile(filepath.Join(dir, "internal", "handler", "pinghandler.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package handler",
		`"example.com/hello/internal/logic"`,
		`"example.com/hello/internal/types"`,
		"ctx.BindQuery(&req)",
		"logic.NewPingLogic(ctx.Request.Context(), svcCtx).Ping(&req)",
	} {
		if !strings.Contains(string(handlerData), want) {
			t.Fatalf("pinghandler.go missing %q:\n%s", want, handlerData)
		}
	}

	logicData, err := os.ReadFile(filepath.Join(dir, "internal", "logic", "pinglogic.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package logic",
		"type PingLogic struct",
		"func NewPingLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PingLogic",
		`return &types.PingResp{Message: "hello " + name}, nil`,
	} {
		if !strings.Contains(string(logicData), want) {
			t.Fatalf("pinglogic.go missing %q:\n%s", want, logicData)
		}
	}

	typesData, err := os.ReadFile(filepath.Join(dir, "internal", "types", "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package types",
		"type PingReq struct",
		"type PingResp struct",
		`json:"name,optional" form:"name,optional"`,
	} {
		if !strings.Contains(string(typesData), want) {
			t.Fatalf("types.go missing %q:\n%s", want, typesData)
		}
	}

	routesData, err := os.ReadFile(filepath.Join(dir, "internal", "handler", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package handler",
		"func RegisterHandlers(server *rest.Server, svcCtx *svc.ServiceContext)",
		`Path: "/ping"`,
		`rest.WithPrefix("/api/v1")`,
	} {
		if !strings.Contains(string(routesData), want) {
			t.Fatalf("routes.go missing %q:\n%s", want, routesData)
		}
	}

	mainData, err := os.ReadFile(filepath.Join(dir, "cmd", "hello", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"example.com/hello/internal/handler"`,
		"handler.RegisterHandlers(httpServer, svcCtx)",
		"svc.NewServiceContext(c)",
	} {
		if !strings.Contains(string(mainData), want) {
			t.Fatalf("main.go missing %q:\n%s", want, mainData)
		}
	}

	assertGeneratedProjectCompiles(t, dir)
}

func TestGenerateServiceScaffoldKitexCompatibleAdapter(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateServiceScaffold(ServiceScaffoldOptions{
		Name:    "greeter",
		Module:  "example.com/greeter",
		Dir:     dir,
		Style:   ServiceStyleMinimal,
		Profile: string(ProfileKitexCompatible),
		Kind:    "rpc",
	}); err != nil {
		t.Fatal(err)
	}

	adapterPath := filepath.Join(dir, "internal", "compat", "kitex", "adapter.go")
	adapterData, err := os.ReadFile(adapterPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package kitex",
		"type Endpoint func(context.Context, any) (any, error)",
		"func Method(name string, newRequest func() any, endpoint Endpoint, opts ...MethodOption) rpc.MethodDesc",
		"func Service(name string, methods ...rpc.MethodDesc) rpc.ServiceDesc",
	} {
		if !strings.Contains(string(adapterData), want) {
			t.Fatalf("kitex-compatible adapter missing %q:\n%s", want, adapterData)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "greeter.proto")); err != nil {
		t.Fatalf("kitex-compatible rpc scaffold should include proto contract: %v", err)
	}
	assertGeneratedProjectCompiles(t, dir)
}

func TestGenerateServiceScaffoldRejectsUnknownFeature(t *testing.T) {
	dir := t.TempDir()
	err := GenerateServiceScaffold(ServiceScaffoldOptions{
		Name:     "hello",
		Module:   "example.com/hello",
		Dir:      dir,
		Style:    ServiceStyleMinimal,
		Features: []string{"missing-feature"},
	})
	if err == nil || !strings.Contains(err.Error(), `feature "missing-feature" is not registered`) {
		t.Fatalf("GenerateServiceScaffold unknown feature error = %v", err)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		t.Fatalf("unknown feature should fail before writing files, got %d entries", len(entries))
	}
}

func TestGenerateServiceScaffoldRejectsRemovedCompatibilityAliases(t *testing.T) {
	for _, feature := range []string{"go-zero", "gozero", "kitex"} {
		t.Run(feature, func(t *testing.T) {
			dir := t.TempDir()
			err := GenerateServiceScaffold(ServiceScaffoldOptions{
				Name:     "hello",
				Module:   "example.com/hello",
				Dir:      dir,
				Style:    ServiceStyleMinimal,
				Features: []string{feature},
			})
			if err == nil || !strings.Contains(err.Error(), `feature "`+feature+`" is not registered`) {
				t.Fatalf("GenerateServiceScaffold(%q) error = %v, want unregistered feature", feature, err)
			}
			if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
				t.Fatalf("removed compatibility alias %q should fail before writing files, got %d entries", feature, len(entries))
			}
		})
	}
}

func TestApplyFeatureNamesPreservesExplicitOrder(t *testing.T) {
	const (
		firstFeature  = "bits-ut-order-first"
		secondFeature = "bits-ut-order-second"
	)
	if !RegisterFeature(firstFeature, func(scope ExtensionScope) ExtensionPatch {
		return ExtensionPatch{DataMerge: map[string]string{"Order": "first"}}
	}) {
		t.Fatalf("register feature %s", firstFeature)
	}
	if !RegisterFeature(secondFeature, func(scope ExtensionScope) ExtensionPatch {
		return ExtensionPatch{DataMerge: map[string]string{"Order": "second"}}
	}) {
		t.Fatalf("register feature %s", secondFeature)
	}

	_, data, err := ApplyFeatureNames(
		[]string{secondFeature, firstFeature},
		ExtensionScope{},
		map[string]string{},
		map[string]string{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := data["Order"]; got != "first" {
		t.Fatalf("ApplyFeatureNames order data = %q, want first", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestGenerateServiceRejectsUnknownStyle(t *testing.T) {
	err := GenerateService(ServiceOptions{Name: "hello", Module: "example.com/hello", Dir: t.TempDir(), Style: "enterprise"})
	if err == nil || !strings.Contains(err.Error(), "unknown service style") {
		t.Fatalf("error = %v, want unknown style", err)
	}
}

func TestGenerateServiceAllowsExplicitFrameworkPath(t *testing.T) {
	dir := t.TempDir()
	frameworkPath := filepath.Join(t.TempDir(), "gofly")
	if err := GenerateService(ServiceOptions{Name: "hello", Module: "example.com/hello", Dir: dir, FrameworkPath: frameworkPath}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "replace github.com/gofly/gofly => "+frameworkPath) {
		t.Fatalf("go.mod missing explicit local replace: %s", data)
	}
}

func TestGenerateServiceCleansLegacyScaffoldFiles(t *testing.T) {
	dir := t.TempDir()
	legacyFiles := []string{
		filepath.Join("internal", "handler", "routes.go"),
		filepath.Join("internal", "handler", "routes_test.go"),
		filepath.Join("internal", "handler", "ping.go"),
		filepath.Join("internal", "handler", "ping_handler.go"),
		filepath.Join("internal", "logic", "ping_logic.go"),
	}
	for _, rel := range legacyFiles {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package legacy\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := GenerateService(ServiceOptions{Name: "hello", Module: "example.com/hello", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range legacyFiles {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("legacy scaffold file %s still exists", rel)
		}
	}
	for _, rel := range []string{
		filepath.Join("internal", "routes", "routes.go"),
		filepath.Join("internal", "api", "v1", "ping", "ping.go"),
		filepath.Join("internal", "middleware", "trim.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected generated file %s: %v", rel, err)
		}
	}
}

func TestGenerateHandler(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateService(ServiceOptions{Name: "hello", Module: "example.com/hello", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateHandler(HandlerOptions{Name: "CreateUser", Dir: dir, Path: "v1/user"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "internal", "api", "v1", "user", "create_user.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package user",
		"func CreateUserHandler",
		`"example.com/hello/internal/svc"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("generated handler missing %q:\n%s", want, data)
		}
	}
}

func TestGenerateMiddleware(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateMiddleware(MiddlewareOptions{Names: []string{"Auth", "AuditLog"}, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		filepath.Join("internal", "middleware", "auth.go"),
		filepath.Join("internal", "middleware", "audit_log.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected generated middleware %s: %v", rel, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "internal", "middleware", "auth.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "func AuthMiddleware() rest.Middleware") {
		t.Fatalf("generated middleware = %s", data)
	}
}

func TestGenerateHandlerDefaultPath(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateService(ServiceOptions{Name: "hello", Module: "example.com/hello", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateHandler(HandlerOptions{Name: "status", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "internal", "api", "status.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "package api") {
		t.Fatalf("generated default handler = %s", data)
	}
}

func TestGenerateHandlerValidation(t *testing.T) {
	if err := GenerateHandler(HandlerOptions{}); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestGenerateMiddlewareValidation(t *testing.T) {
	if err := GenerateMiddleware(MiddlewareOptions{}); err == nil {
		t.Fatal("expected error for empty names")
	}
	if err := GenerateMiddleware(MiddlewareOptions{Names: []string{""}}); err == nil {
		t.Fatal("expected error for empty name after cleaning")
	}
}
