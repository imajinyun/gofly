package command

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// loadAndOverlay 负责「配置文件 + CLI 覆盖」的合并逻辑。
// 当 configPath 为空时，会按 dir/.gofly/config.json 作为默认路径。
func loadAndOverlay(configPath, dir, name, module, style, templateDir, templateRemote, templateBranch, features, plugins, kind string) (*generator.Config, string, error) {
	resolved := configPath
	if resolved == "" {
		root := dir
		if root == "" {
			root = "."
		}
		resolved = filepath.Join(root, generator.DefaultConfigFile)
	}
	cfg, err := generator.LoadConfig(resolved)
	if err != nil {
		return nil, "", err
	}
	// env layer: GOFLY_* overrides file defaults but is overridden by CLI flags.
	generator.ApplyEnvOverlay(cfg)
	featureList := splitCSV(features)
	cfg = cfg.ApplyOverlayWithTemplateSource(name, module, style, templateDir, templateRemote, templateBranch, featureList)
	switch strings.ToLower(kind) {
	case "rpc":
		if cfg.RPC == nil {
			cfg.RPC = &generator.RPCConfig{}
		}
		if pl := splitCSV(plugins); len(pl) > 0 {
			cfg.RPC.Plugins = mergeLists(cfg.RPC.Plugins, pl)
		}
	case "api":
		if cfg.API == nil {
			cfg.API = &generator.APIConfig{}
		}
		if pl := splitCSV(plugins); len(pl) > 0 {
			cfg.API.Plugins = mergeLists(cfg.API.Plugins, pl)
		}
	}
	return cfg, resolved, nil
}

// pluginListFromConfig 按 kind 从配置提取插件列表。
func pluginListFromConfig(cfg *generator.Config, kind string) []string {
	switch strings.ToLower(kind) {
	case "rpc":
		if cfg != nil && cfg.RPC != nil {
			return cfg.RPC.Plugins
		}
	case "api":
		if cfg != nil && cfg.API != nil {
			return cfg.API.Plugins
		}
	}
	return nil
}

type discoveryCLIOverlay struct {
	Provider    string
	Address     string
	Endpoints   string
	Prefix      string
	TTL         string
	DialTimeout string
	TokenEnv    string
	UsernameEnv string
	PasswordEnv string
}

func applyDiscoveryCLIOverlay(cfg *generator.Config, overlay discoveryCLIOverlay) {
	if cfg == nil || !overlay.hasValue() {
		return
	}
	if cfg.Discovery == nil {
		cfg.Discovery = &generator.DiscoveryConfig{}
	}
	if overlay.Provider != "" {
		cfg.Discovery.Provider = overlay.Provider
	}
	if overlay.Address != "" {
		cfg.Discovery.Address = overlay.Address
	}
	if overlay.Endpoints != "" {
		cfg.Discovery.Endpoints = splitCSV(overlay.Endpoints)
	}
	if overlay.Prefix != "" {
		cfg.Discovery.Prefix = overlay.Prefix
	}
	if overlay.TTL != "" {
		cfg.Discovery.TTL = overlay.TTL
	}
	if overlay.DialTimeout != "" {
		cfg.Discovery.DialTimeout = overlay.DialTimeout
	}
	if overlay.TokenEnv != "" {
		cfg.Discovery.TokenEnv = overlay.TokenEnv
	}
	if overlay.UsernameEnv != "" {
		cfg.Discovery.UsernameEnv = overlay.UsernameEnv
	}
	if overlay.PasswordEnv != "" {
		cfg.Discovery.PasswordEnv = overlay.PasswordEnv
	}
}

func (o discoveryCLIOverlay) hasValue() bool {
	return o.Provider != "" || o.Address != "" || o.Endpoints != "" || o.Prefix != "" || o.TTL != "" || o.DialTimeout != "" || o.TokenEnv != "" || o.UsernameEnv != "" || o.PasswordEnv != ""
}

func isGoctlTemplateStyle(style string) bool {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "go_zero", "gozero", "go-zero", "http-compat", "rpc-compat":
		return true
	default:
		return false
	}
}

func validateNewServicePlanInputs(cfg *generator.Config) error {
	if cfg == nil {
		return fmt.Errorf("%w: service config is required", errUsage)
	}
	if strings.TrimSpace(cfg.ServiceName) == "" {
		return fmt.Errorf("%w: name is required", errUsage)
	}
	if strings.TrimSpace(cfg.Module) == "" {
		return fmt.Errorf("%w: module is required", errUsage)
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Style)) {
	case "", generator.ServiceStyleMinimal, generator.ServiceStyleBasic, generator.ServiceStyleProduction:
		return nil
	default:
		return fmt.Errorf("%w: unknown service style %q", errUsage, cfg.Style)
	}
}

func buildNewServicePlan(command, dir, configPath string, cfg *generator.Config, plugins []string, contracts newServiceContractInputs, saveConfig bool, dryRun bool) cliPlan {
	inputs := map[string]string{
		"dir": dir,
	}
	if cfg != nil {
		inputs["name"] = cfg.ServiceName
		inputs["module"] = cfg.Module
		inputs["style"] = cfg.Style
		if len(cfg.Features) > 0 {
			inputs["features"] = strings.Join(cfg.Features, ",")
		}
		if cfg.TemplateDir != "" {
			inputs["templateDir"] = cfg.TemplateDir
		}
		if cfg.TemplateRemote != "" {
			inputs["templateRemote"] = cfg.TemplateRemote
		}
		if cfg.RPC != nil && cfg.RPC.Profile != "" {
			inputs["profile"] = cfg.RPC.Profile
		}
		if cfg.API != nil && cfg.API.Profile != "" {
			inputs["profile"] = cfg.API.Profile
		}
		if cfg.Discovery != nil && cfg.Discovery.Provider != "" {
			inputs["discovery"] = cfg.Discovery.Provider
		}
	}
	if len(plugins) > 0 {
		inputs["plugins"] = strings.Join(plugins, ",")
	}
	if apiFile := strings.TrimSpace(contracts.APIFile); apiFile != "" {
		inputs["api"] = apiFile
	}
	if openAPIFile := strings.TrimSpace(contracts.OpenAPIFile); openAPIFile != "" {
		inputs["openapi"] = openAPIFile
	}
	if protoFile := strings.TrimSpace(contracts.ProtoFile); protoFile != "" {
		inputs["proto"] = protoFile
	}
	if thriftFile := strings.TrimSpace(contracts.ThriftFile); thriftFile != "" {
		inputs["thrift"] = thriftFile
	}

	actions := []cliPlanAction{
		{Operation: "create-directory", Target: dir, Description: "ensure the service output directory exists", RiskLevel: "low"},
		{Operation: "write-files", Target: dir, Description: "render scaffold files under the service output directory", RiskLevel: "medium"},
	}
	if target := firstNonBlank(contracts.APIFile, contracts.OpenAPIFile); target != "" {
		actions = append(actions,
			cliPlanAction{Operation: "materialize-api-contract", Target: target, Description: "copy or import the REST contract into the generated service", RiskLevel: "medium"},
			cliPlanAction{Operation: "generate-rest-from-contract", Target: dir, Description: "generate REST handlers and tests from the API contract", RiskLevel: "medium"},
		)
	}
	if target := firstNonBlank(contracts.ProtoFile, contracts.ThriftFile); target != "" {
		actions = append(actions,
			cliPlanAction{Operation: "materialize-rpc-contract", Target: target, Description: "copy or convert the RPC contract into the generated service", RiskLevel: "medium"},
			cliPlanAction{Operation: "generate-rpc-from-contract", Target: filepath.Join(dir, "internal", "rpc"), Description: "generate RPC descriptors and middleware adapters from the proto contract", RiskLevel: "medium"},
		)
	}
	if saveConfig {
		actions = append(actions, cliPlanAction{Operation: "write-config", Target: configPath, Description: "save the resolved gofly config", RiskLevel: "low"})
	}
	if len(plugins) > 0 {
		actions = append(actions, cliPlanAction{Operation: "run-plugins", Target: strings.Join(plugins, ","), Description: "execute configured scaffold plugins and apply returned files or patches", RiskLevel: "high"})
	}
	pluginEffects := planPluginEffects(plugins, !dryRun)

	warnings := []string(nil)
	if cfg != nil && cfg.TemplateRemote != "" {
		warnings = append(warnings, "dry-run does not download or validate remote templates")
	}
	if len(plugins) > 0 {
		warnings = append(warnings, "dry-run does not execute plugins; plugin-produced files and patches are not enumerated")
	}

	nextActions := []string{"cd " + dir, "go mod tidy", "go test ./..."}
	if dryRun {
		nextActions = []string{"rerun without --dry-run/--plan to apply these actions"}
	}

	return cliPlan{
		Command:           command,
		DryRun:            dryRun,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions:           actions,
		GeneratedFiles:    countGeneratedGoProjectFiles(dir),
		PluginEffects:     pluginEffects,
		Warnings:          warnings,
		NextActions:       nextActions,
	}
}

func planPluginEffects(plugins []string, executed bool) []cliPluginEffect {
	effects := make([]cliPluginEffect, 0, len(plugins))
	for _, plugin := range plugins {
		plugin = strings.TrimSpace(plugin)
		if plugin == "" {
			continue
		}
		effect := cliPluginEffect{Name: plugin, Executed: executed}
		if executed {
			effect.Note = "plugin output is applied by the generator; exact file and patch counts are plugin-reported when available"
		} else {
			effect.Note = "dry-run does not execute plugins"
		}
		effects = append(effects, effect)
	}
	return effects
}

func countGeneratedGoProjectFiles(dir string) int {
	if strings.TrimSpace(dir) == "" {
		return 0
	}
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".gofly":
				return filepath.SkipDir
			}
			return nil
		}
		count++
		return nil
	})
	return count
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
