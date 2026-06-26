package generator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func normalizeServiceStyle(style string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "", ServiceStyleProduction:
		return ServiceStyleProduction, nil
	case ServiceStyleBasic:
		return ServiceStyleBasic, nil
	case ServiceStyleMinimal:
		return ServiceStyleMinimal, nil
	default:
		return "", fmt.Errorf("unknown service style %q", style)
	}
}

func serviceFiles(style, name string) map[string]string {
	return serviceFilesForProfile(style, name, ProfileGoflyAI)
}

func serviceFilesForProfile(style, name string, profile GenerationProfile) map[string]string {
	if profile == ProfileGoZeroCompatible {
		return goZeroServiceFiles(style, name)
	}

	files := map[string]string{
		"go.mod":                                                  goModTemplate,
		filepath.Join("cmd", name, "main.go"):                     mainTemplate,
		filepath.Join("etc", name+".json"):                        configTemplate,
		filepath.Join("internal", "config", "config.go"):          configGoTemplate,
		filepath.Join("internal", "config", "config_test.go"):     configTestTemplate,
		filepath.Join("internal", "svc", "service_context.go"):    svcTemplate,
		filepath.Join("internal", "routes", "routes.go"):          routesTemplate,
		filepath.Join("internal", "routes", "routes_test.go"):     routesTestTemplate,
		filepath.Join("internal", "api", "v1", "ping", "ping.go"): pingHandlerTemplate,
		filepath.Join("internal", "middleware", "trim.go"):        trimMiddlewareTemplate,
		filepath.Join("internal", "middleware", "trim_test.go"):   trimMiddlewareTestTemplate,
		filepath.Join("internal", "service", "ping.go"):           pingServiceTemplate,
		filepath.Join("internal", "service", "ping_test.go"):      pingServiceTestTemplate,
	}
	if style == ServiceStyleMinimal || style == ServiceStyleBasic {
		files[filepath.Join("cmd", name, "main.go")] = minimalMainTemplate
		files[filepath.Join("etc", name+".json")] = minimalConfigTemplate
		files[filepath.Join("internal", "config", "config.go")] = minimalConfigGoTemplate
		if style == ServiceStyleBasic {
			files["Dockerfile"] = dockerfileTemplate
			files["Makefile"] = makefileTemplate
		}
		addKitexProfileFiles(files, profile)
		return files
	}
	files[filepath.Join("etc", "governance.json")] = governanceTemplate
	files[filepath.Join("internal", "admin", "admin.go")] = adminServerTemplate
	files[filepath.Join("internal", "admin", "admin_test.go")] = adminServerTestTemplate
	files[filepath.Join("internal", "config", "discovery_test.go")] = configDiscoveryTestTemplate
	files[filepath.Join("internal", "discovery", "registry.go")] = discoveryRegistryTemplate
	files[filepath.Join("internal", "mq", "broker.go")] = mqBrokerTemplate
	files[filepath.Join("internal", "rpc", "greeter.go")] = greeterTemplate
	files[filepath.Join("internal", "rpc", "greeter_client_test.go")] = greeterClientTestTemplate
	files[filepath.Join("internal", "rpc", "greeter_test.go")] = greeterTestTemplate
	files[filepath.Join("internal", "smoke", "service_smoke_test.go")] = smokeTestTemplate
	files["Dockerfile"] = dockerfileTemplate
	files[filepath.Join("deploy", "k8s", name+".yaml")] = kubeTemplate
	files[filepath.Join("deploy", "helm", "Chart.yaml")] = helmChartTemplate
	files[filepath.Join("deploy", "helm", "values.yaml")] = helmValuesTemplate
	files[filepath.Join("deploy", "helm", "templates", "workload.yaml")] = helmWorkloadTemplate
	files[filepath.Join("deploy", "observability", "prometheus.yaml")] = prometheusStackTemplate
	files[filepath.Join("deploy", "observability", "otel-collector.yaml")] = otelCollectorTemplate
	files[filepath.Join("deploy", "observability", "grafana-dashboard.json")] = grafanaDashboardTemplate
	files[filepath.Join("deploy", "observability", "logs-correlation.yaml")] = logsCorrelationTemplate
	files[filepath.Join("bin", "production-check.sh")] = productionCheckScriptTemplate
	files["Makefile"] = makefileTemplate
	files[filepath.Join(".github", "workflows", "ci.yml")] = ciWorkflowTemplate
	addKitexProfileFiles(files, profile)
	return files
}

func addKitexProfileFiles(files map[string]string, profile GenerationProfile) {
	if profile != ProfileKitexCompatible {
		return
	}
	files[filepath.Join("internal", "compat", "kitex", "adapter.go")] = kitexCompatibilityTemplate
}

func goZeroServiceFiles(style, name string) map[string]string {
	files := map[string]string{
		"go.mod":                                                goModTemplate,
		filepath.Join("cmd", name, "main.go"):                   goZeroMainTemplate,
		filepath.Join("etc", name+".json"):                      minimalConfigTemplate,
		filepath.Join("internal", "config", "config.go"):        minimalConfigGoTemplate,
		filepath.Join("internal", "config", "config_test.go"):   configTestTemplate,
		filepath.Join("internal", "svc", "servicecontext.go"):   goZeroSvcTemplate,
		filepath.Join("internal", "types", "types.go"):          goZeroTypesTemplate,
		filepath.Join("internal", "logic", "pinglogic.go"):      goZeroPingLogicTemplate,
		filepath.Join("internal", "handler", "pinghandler.go"):  goZeroPingHandlerTemplate,
		filepath.Join("internal", "handler", "routes.go"):       goZeroRoutesTemplate,
		filepath.Join("internal", "middleware", "trim.go"):      trimMiddlewareTemplate,
		filepath.Join("internal", "middleware", "trim_test.go"): trimMiddlewareTestTemplate,
	}
	if style == ServiceStyleBasic {
		files["Dockerfile"] = dockerfileTemplate
		files["Makefile"] = makefileTemplate
	}
	return files
}

func cleanupLegacyServiceFiles(dir string) error {
	return cleanupLegacyServiceFilesForProfile(dir, ProfileGoflyAI)
}

func cleanupLegacyServiceFilesForProfile(dir string, profile GenerationProfile) error {
	legacyFiles := []string{
		filepath.Join("internal", "handler", "routes_test.go"),
		filepath.Join("internal", "handler", "ping.go"),
		filepath.Join("internal", "handler", "ping_handler.go"),
	}
	if profile == ProfileGoZeroCompatible {
		legacyFiles = append(legacyFiles, filepath.Join("internal", "svc", "service_context.go"))
	} else {
		legacyFiles = append(legacyFiles,
			filepath.Join("internal", "handler", "routes.go"),
			filepath.Join("internal", "handler", "pinghandler.go"),
			filepath.Join("internal", "logic", "pinglogic.go"),
			filepath.Join("internal", "svc", "servicecontext.go"),
			filepath.Join("internal", "types", "types.go"),
		)
	}
	for _, rel := range legacyFiles {
		path := filepath.Join(dir, rel)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove legacy generated file %s: %w", path, err)
		}
	}
	legacyDirs := legacyServiceDirs(profile)
	for _, rel := range legacyDirs {
		path := filepath.Join(dir, rel)
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove legacy generated directory %s: %w", path, err)
		}
	}
	return nil
}

func legacyServiceDirs(profile GenerationProfile) []string {
	switch profile {
	case ProfileGoZeroCompatible:
		return []string{
			filepath.Join("internal", "routes"),
			filepath.Join("internal", "api"),
			filepath.Join("internal", "service"),
		}
	default:
		return []string{
			filepath.Join("internal", "logic"),
			filepath.Join("internal", "handler"),
			filepath.Join("internal", "types"),
		}
	}
}
