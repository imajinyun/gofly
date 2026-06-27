package command

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

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

type newScaffoldPlanOutput struct {
	Command     string
	DisplayName string
	Dir         string
	ConfigPath  string
	Config      *generator.Config
	Plugins     []string
	Contracts   newServiceContractInputs
	SaveConfig  bool
}

func newScaffoldPlanOutputFor(command, displayName, dir, configPath string, cfg *generator.Config, plugins []string, contracts newServiceContractInputs, saveConfig bool) newScaffoldPlanOutput {
	return newScaffoldPlanOutput{
		Command:     command,
		DisplayName: displayName,
		Dir:         dir,
		ConfigPath:  configPath,
		Config:      cfg,
		Plugins:     plugins,
		Contracts:   contracts,
		SaveConfig:  saveConfig,
	}
}

func (o newScaffoldPlanOutput) printPlan(forceJSON bool) error {
	plan := buildNewServicePlan(o.DisplayName, o.Dir, o.ConfigPath, o.Config, o.Plugins, o.Contracts, o.SaveConfig, true)
	return printCLIPlan(o.Command, plan, forceJSON)
}

func (o newScaffoldPlanOutput) printDryRunPlan(forceJSON bool, validateContracts bool) error {
	if err := validateNewServicePlanInputs(o.Config); err != nil {
		return err
	}
	if validateContracts {
		if err := validateNewServiceContractInputs(o.Contracts); err != nil {
			return err
		}
	}
	return o.printPlan(forceJSON)
}

func (o newScaffoldPlanOutput) printResult() error {
	plan := buildNewServicePlan(o.DisplayName, o.Dir, o.ConfigPath, o.Config, o.Plugins, o.Contracts, o.SaveConfig, false)
	return printJSONEnvelope(o.Command, plan)
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
