package command

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// serviceNewCommand implements the golden-path production service scaffold.
// It intentionally routes through the same generator as `new api`/`new rpc`,
// but defaults to the full production template with REST, RPC, OpenAPI,
// governance, admin control-plane, discovery, config tests and smoke tests.
func serviceNewCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("new service", flag.ContinueOnError)
	name := fs.String("name", "", "service name")
	module := fs.String("module", "", "go module path")
	dir := fs.String("dir", "", "output directory")
	style := fs.String("style", generator.ServiceStyleProduction, "service scaffold style: minimal, basic, or production")
	configPath := fs.String("config", "", "gofly config file path (defaults to <dir>/.gofly/config.json)")
	templateDir := fs.String("template-dir", "", "override templates from this directory")
	home := fs.String("home", "", "template home directory")
	remote := fs.String("remote", "", "remote template repository")
	branch := fs.String("branch", "", "remote template branch")
	discovery := fs.String("discovery", "", "service discovery provider: memory, consul, or etcdv3")
	discoveryAddress := fs.String("discovery-address", "", "service discovery address, or comma-separated endpoints for etcdv3")
	discoveryEndpoints := fs.String("discovery-endpoints", "", "service discovery endpoints, comma-separated")
	discoveryPrefix := fs.String("discovery-prefix", "", "service discovery key prefix for etcdv3")
	discoveryTTL := fs.String("discovery-ttl", "", "service discovery registration TTL, e.g. 15s")
	discoveryDialTimeout := fs.String("discovery-dial-timeout", "", "service discovery dial timeout, e.g. 5s")
	discoveryTokenEnv := fs.String("discovery-token-env", "", "environment variable containing the Consul ACL token")
	discoveryUsernameEnv := fs.String("discovery-username-env", "", "environment variable containing the etcd username")
	discoveryPasswordEnv := fs.String("discovery-password-env", "", "environment variable containing the etcd password")
	features := fs.String("feature", "", "feature names to enable, comma-separated")
	featuresAlias := fs.String("features", "", "alias for --feature")
	pluginArg := fs.String("plugin", "", "plugin executable (comma-separated for multiple)")
	apiFile := fs.String("api", "", "API-first .api contract used to generate REST handlers")
	openAPIFile := fs.String("openapi", "", "OpenAPI/Swagger contract used to generate a REST project")
	protoFile := fs.String("proto", "", "RPC-first protobuf contract used to generate RPC code")
	thriftFile := fs.String("thrift", "", "RPC-first thrift contract converted to proto and RPC code")
	saveConfig := fs.Bool("save-config", true, "save resolved config back to --config path")
	dryRun := fs.Bool("dry-run", false, "print the planned filesystem changes without writing files")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	jsonOut := fs.Bool("json", false, "emit scaffold result as JSON")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if *templateDir == "" {
		*templateDir = *home
	}
	if *dir == "" && *name != "" {
		*dir = *name
	}
	verboseOutputf("new service: configuring service %q in %s\n", *name, *dir)
	cfg, resolved, err := loadAndOverlay(*configPath, *dir, *name, *module, *style, *templateDir, *remote, *branch, joinCSV(*features, *featuresAlias), *pluginArg, "service")
	if err != nil {
		return err
	}
	applyDiscoveryCLIOverlay(cfg, discoveryCLIOverlay{
		Provider:    *discovery,
		Address:     *discoveryAddress,
		Endpoints:   *discoveryEndpoints,
		Prefix:      *discoveryPrefix,
		TTL:         *discoveryTTL,
		DialTimeout: *discoveryDialTimeout,
		TokenEnv:    *discoveryTokenEnv,
		UsernameEnv: *discoveryUsernameEnv,
		PasswordEnv: *discoveryPasswordEnv,
	})
	if cfg.Style == "" || isGoctlTemplateStyle(cfg.Style) {
		cfg.Style = generator.ServiceStyleProduction
	}
	if *style == "" {
		cfg.Style = generator.ServiceStyleProduction
	}
	if *dir == "" && cfg.ServiceName != "" {
		*dir = cfg.ServiceName
	}
	plugins := pluginListFromConfig(cfg, "service")
	contractInputs := newServiceContractInputs{
		APIFile:     *apiFile,
		OpenAPIFile: *openAPIFile,
		ProtoFile:   *protoFile,
		ThriftFile:  *thriftFile,
	}
	if *dryRun || *plan {
		if err := validateNewServicePlanInputs(cfg); err != nil {
			return err
		}
		if err := validateNewServiceContractInputs(contractInputs); err != nil {
			return err
		}
		return printCLIPlan("new.service", buildNewServicePlan("new service", *dir, resolved, cfg, plugins, contractInputs, *saveConfig, true), *jsonOut)
	}
	if err := generator.GenerateServiceScaffold(generator.ServiceScaffoldOptions{
		Name:           cfg.ServiceName,
		Module:         cfg.Module,
		Dir:            *dir,
		Style:          cfg.Style,
		TemplateDir:    cfg.TemplateDir,
		TemplateRemote: cfg.TemplateRemote,
		TemplateBranch: cfg.TemplateBranch,
		Features:       cfg.Features,
		Plugins:        plugins,
		Kind:           "service",
	}); err != nil {
		return err
	}
	if err := applyNewServiceContractInputs(contractInputs, cfg.ServiceName, *dir); err != nil {
		return err
	}
	if *saveConfig {
		if err := generator.SaveConfig(resolved, cfg); err != nil {
			return err
		}
	}
	if *jsonOut || outputMode() == outputJSON {
		return printJSONEnvelope("new.service", buildNewServicePlan("new service", *dir, resolved, cfg, plugins, contractInputs, *saveConfig, false))
	}
	return nil
}

type newServiceContractInputs struct {
	APIFile     string
	OpenAPIFile string
	ProtoFile   string
	ThriftFile  string
}

func applyNewServiceContractInputs(inputs newServiceContractInputs, serviceName, dir string) error {
	if err := validateNewServiceContractInputs(inputs); err != nil {
		return err
	}

	apiContract, err := materializeNewServiceAPIContract(inputs, serviceName, dir)
	if err != nil {
		return err
	}
	if apiContract != "" {
		if err := generator.GenerateRESTFromAPI(generator.APIOptions{APIFile: apiContract, Dir: dir, Package: "api", Test: true, TypeGroup: true}); err != nil {
			return fmt.Errorf("generate REST from API contract: %w", err)
		}
	}

	protoContract, err := materializeNewServiceRPCContract(inputs, serviceName, dir)
	if err != nil {
		return err
	}
	if protoContract != "" {
		if err := generator.GenerateRPCFromProto(generator.RPCOptions{ProtoFile: protoContract, Dir: filepath.Join(dir, "internal", "rpc"), Package: "rpc", WithMiddleware: true, WithRecovery: true, WithValidator: true}); err != nil {
			return fmt.Errorf("generate RPC from proto contract: %w", err)
		}
	}
	return nil
}

func validateNewServiceContractInputs(inputs newServiceContractInputs) error {
	if strings.TrimSpace(inputs.APIFile) != "" && strings.TrimSpace(inputs.OpenAPIFile) != "" {
		return fmt.Errorf("%w: --api and --openapi are mutually exclusive", errUsage)
	}
	if strings.TrimSpace(inputs.ProtoFile) != "" && strings.TrimSpace(inputs.ThriftFile) != "" {
		return fmt.Errorf("%w: --proto and --thrift are mutually exclusive", errUsage)
	}
	return nil
}

func materializeNewServiceAPIContract(inputs newServiceContractInputs, serviceName, dir string) (string, error) {
	apiFile := strings.TrimSpace(inputs.APIFile)
	openAPIFile := strings.TrimSpace(inputs.OpenAPIFile)
	if apiFile == "" && openAPIFile == "" {
		return "", nil
	}
	apiOut, err := newServiceContractOutputPath(dir, serviceName, ".api")
	if err != nil {
		return "", err
	}
	if openAPIFile != "" {
		if err := generator.GenerateAPIFromOpenAPI(generator.APIImportOptions{Source: openAPIFile, Output: apiOut, Service: serviceName}); err != nil {
			return "", fmt.Errorf("import OpenAPI contract: %w", err)
		}
		return apiOut, nil
	}
	if err := copyNewServiceContractFile(apiFile, apiOut, dir); err != nil {
		return "", fmt.Errorf("copy API contract: %w", err)
	}
	return apiOut, nil
}

func materializeNewServiceRPCContract(inputs newServiceContractInputs, serviceName, dir string) (string, error) {
	protoFile := strings.TrimSpace(inputs.ProtoFile)
	thriftFile := strings.TrimSpace(inputs.ThriftFile)
	if protoFile == "" && thriftFile == "" {
		return "", nil
	}
	protoOut, err := newServiceContractOutputPath(dir, serviceName, ".proto")
	if err != nil {
		return "", err
	}
	if thriftFile != "" {
		if err := generator.GenerateProtoFromThrift(generator.RPCScaffoldOptions{IDLFile: thriftFile, Dir: dir}); err != nil {
			return "", fmt.Errorf("convert thrift contract: %w", err)
		}
		generatedProto := filepath.Join(dir, strings.TrimSuffix(filepath.Base(thriftFile), filepath.Ext(thriftFile))+".proto")
		if generatedProto != protoOut {
			if err := copyNewServiceContractFile(generatedProto, protoOut, dir); err != nil {
				return "", fmt.Errorf("copy thrift-derived proto contract: %w", err)
			}
		}
		return protoOut, nil
	}
	if err := copyNewServiceContractFile(protoFile, protoOut, dir); err != nil {
		return "", fmt.Errorf("copy proto contract: %w", err)
	}
	return protoOut, nil
}

func copyNewServiceContractFile(src, dst, root string) error {
	return generator.CopyFileToRoot(src, root, dst, 0o600, 0o750, "contract target")
}

func newServiceContractOutputPath(dir, serviceName, ext string) (string, error) {
	name := strings.TrimSpace(serviceName)
	if name == "" {
		return "", fmt.Errorf("%w: name is required", errUsage)
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%w: service name %q cannot be used as a contract filename", errUsage, serviceName)
	}
	return filepath.Join(dir, name+ext), nil
}

func sameFilePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return absA == absB
}
