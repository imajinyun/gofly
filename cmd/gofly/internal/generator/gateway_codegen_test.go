package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		`"github.com/gofly/gofly/core/governance"`,
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
	for _, want := range []string{`"environment": "development"`, `"service": {"name": "edge"`, `"startupTimeout": 5000000000`, `"timeoutConfig": {"duration": 3000000000`, `"breakerConfig": {"openTimeout": 5000000000`, `"metrics": {"enabled": true}`, `"mq": {"enabled": true`, `"driver": "memory"`, `"transport": "mq"`} {
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
	if !strings.Contains(string(svcData), `"github.com/gofly/gofly/core/mq"`) || !strings.Contains(string(svcData), "MQ     mq.Broker") {
		t.Fatalf("gateway service context missing mq broker wiring:\n%s", svcData)
	}
	assertGeneratedProjectCompiles(t, dir)
}
