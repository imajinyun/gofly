package generator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imajinyun/gofly/gateway"
)

func TestGenerateGatewayWiresGovernanceManager(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateGateway(GatewayOptions{Name: "edge", Module: "example.com/edge", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	mainData, err := os.ReadFile(filepath.Join(dir, "cmd", "edge", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"github.com/imajinyun/gofly/core/governance"`,
		`appmq "example.com/edge/internal/mq"`,
		"configPath := appconfig.ResolveConfigPath(\"edge\")",
		"config.Load(configPath",
		"config.WithEnvExpansion()",
		"config.WithStrictFields()",
		"config.WithLoadValidator(appconfig.Validate)",
		"app.Bootstrap",
		"serviceConf.BootstrapConfig",
		"restConf := serviceConf.RESTConfig(c.Rest)",
		"rest.MustNewServer(\n\t\trestConf,",
		"serviceConf.RunOptions()",
		"governance.NewManager",
		"governance.WithPlugin(serviceConf.ProductionGovernancePlugin())",
		"appmq.NewBroker(c.MQ, governanceManager)",
		"gateway.WithGovernanceManager(governanceManager)",
		"rest.WithGovernanceManager(governanceManager)",
		"svc.NewServiceContext(c, mqBroker)",
	} {
		if !strings.Contains(string(mainData), want) {
			t.Fatalf("main.go missing governance wiring %q:\n%s", want, mainData)
		}
	}
	configData, err := os.ReadFile(filepath.Join(dir, "internal", "config", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), "Governance") || !strings.Contains(string(configData), "governance.Config") || !strings.Contains(string(configData), "app.ServiceConf") || !strings.Contains(string(configData), "MQConfig") {
		t.Fatalf("config.go missing governance config:\n%s", configData)
	}
	for _, want := range []string{"Service      app.ServiceConf", "func ConfigPaths(name string) []string", "func ResolveConfigPath(name string) string", `paths := []string{"config.yaml", "config.yml", "config.toml", "config.json"}`, "func (c Config) ServiceConf() app.ServiceConf", "func Validate(c Config) error", "app.ValidateProductionConfig", "rest.ValidateProductionConfig", "production gateway admin requires"} {
		if !strings.Contains(string(configData), want) {
			t.Fatalf("gateway config.go missing production validator %q:\n%s", want, configData)
		}
	}
	jsonData, err := os.ReadFile(filepath.Join(dir, "etc", "edge.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"environment": "development"`,
		`"service": {"name": "edge"`,
		`"startupTimeout": 5000000000`,
		`"timeoutConfig": {"duration": 3000000000`,
		`"breakerConfig": {"openTimeout": 5000000000`,
		`"metrics": {"enabled": true}`,
		`"mq": {"enabled": true`,
		`"driver": "memory"`,
		`"transport": "mq"`,
		`"gateway": {`,
		`"timeout": 3000000000`,
		`"pathPrefix": "/api"`,
		`"retry": {"attempts": 2, "backoff": 100000000, "statuses": [502, 503, 504], "methods": ["GET", "HEAD"]}`,
		`"rateLimit": {"rate": 100, "burst": 100}`,
		`"concurrency": {"limit": 64}`,
		`"transport": "gateway"`,
	} {
		if !strings.Contains(string(jsonData), want) {
			t.Fatalf("gateway config missing %q:\n%s", want, jsonData)
		}
	}
	assertEmbeddedGovernanceConfigLoads(t, jsonData)
	brokerData, err := os.ReadFile(filepath.Join(dir, "internal", "mq", "broker.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"case \"kafka\":", "case \"rabbitmq\":", "case \"redisstream\":"} {
		if !strings.Contains(string(brokerData), want) {
			t.Fatalf("gateway broker.go missing %q:\n%s", want, brokerData)
		}
	}
	svcData, err := os.ReadFile(filepath.Join(dir, "internal", "svc", "service.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(svcData), `"github.com/imajinyun/gofly/core/mq"`) || !strings.Contains(string(svcData), "MQ     mq.Broker") {
		t.Fatalf("gateway service context missing mq broker wiring:\n%s", svcData)
	}
	assertGeneratedProjectCompiles(t, dir)
}

func TestGenerateGatewayDefaultResilienceProfileReachesRuntime(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateGateway(GatewayOptions{Name: "edge", Module: "example.com/edge", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	configData, err := os.ReadFile(filepath.Join(dir, "etc", "edge.json"))
	if err != nil {
		t.Fatal(err)
	}
	var generated struct {
		Gateway gateway.Config `json:"gateway"`
	}
	if err := json.Unmarshal(configData, &generated); err != nil {
		t.Fatalf("decode generated gateway config: %v\n%s", err, configData)
	}
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "retry", http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	if len(generated.Gateway.Routes) != 1 {
		t.Fatalf("generated gateway routes = %#v, want one default route", generated.Gateway.Routes)
	}
	generated.Gateway.Routes[0].Targets = []string{upstream.URL}
	gw, err := gateway.NewFromConfig(generated.Gateway, nil)
	if err != nil {
		t.Fatalf("build gateway from generated config: %v", err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	before := gw.RuntimeSnapshot()
	if before.DefaultTimeout != 3*time.Second || before.RouteCount != 1 || len(before.Routes) != 1 {
		t.Fatalf("gateway runtime = %+v, want generated default timeout and one route", before)
	}
	route := before.Routes[0]
	if route.Timeout != 5*time.Second || route.EffectiveTimeout != 5*time.Second {
		t.Fatalf("generated route timeout = %+v, want explicit gateway route timeout", route)
	}
	if route.Retry.Attempts != 2 || route.Retry.Backoff != 100*time.Millisecond {
		t.Fatalf("generated route retry = %+v, want shared default retry profile", route.Retry)
	}
	if !route.Breaker.Enabled || route.Breaker.OpenTimeout != 5*time.Second || route.RateLimit.Rate != 100 || route.Concurrency.Limit != 64 {
		t.Fatalf("generated route resilience = breaker %+v rate %+v concurrency %+v", route.Breaker, route.RateLimit, route.Concurrency)
	}

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orders", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" || calls.Load() != 2 {
		t.Fatalf("generated gateway response = %d body = %q calls = %d, want retry success", rec.Code, rec.Body.String(), calls.Load())
	}
	after := gw.RuntimeSnapshot()
	if after.Cache.RateLimiters != 1 || after.Cache.ConcurrencyLimiters != 1 || after.Cache.Breakers != 1 {
		t.Fatalf("generated gateway runtime cache = %+v, want materialized resilience primitives", after.Cache)
	}
}
