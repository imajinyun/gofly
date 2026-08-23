package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// Command 处理 `gofly config init|show|get|set|clean`。
func Command(args []string, hooks Hooks) error {
	hooks = normalizeHooks(hooks)
	if hooks.PrintHelp("config", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly config init|show|get|set|clean`", hooks.Usage)
	}
	sub := args[0]
	rest := args[1:]
	fs := flag.NewFlagSet("config "+sub, flag.ContinueOnError)
	dir := fs.String("dir", ".", "service root directory")
	name := fs.String("name", "", "service name override")
	module := fs.String("module", "", "module override")
	style := fs.String("style", "", "style override: minimal|basic|production")
	key := fs.String("key", "", "config key (for get/set)")
	value := fs.String("value", "", "config value (for set)")
	previewEnabled := hooks.RegisterDryRunFlags(fs, "print the planned filesystem changes without writing files")
	remaining, err := hooks.ParseFlags(fs, rest)
	if err != nil {
		return err
	}
	previewOnly := previewEnabled()
	if *key == "" && len(remaining) > 0 {
		*key = remaining[0]
	}
	positionalValueExplicit := false
	if *value == "" && len(remaining) > 1 {
		*value = remaining[1]
		positionalValueExplicit = true
	}
	valueExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "value" {
			valueExplicit = true
		}
	})
	valueExplicit = valueExplicit || positionalValueExplicit
	base := *dir
	if base == "" {
		base = "."
	}
	path := filepath.Join(base, generator.DefaultConfigFile)

	switch sub {
	case "init":
		cfg := generator.DefaultConfig(*name, *module)
		if *style != "" {
			cfg.Style = *style
		}
		if previewOnly {
			return hooks.PrintPlan("config.init", configPlan("config init", path, true, map[string]string{"dir": base, "name": *name, "module": *module, "style": cfg.Style}, []PlanAction{{Operation: "write-config", Target: path, Description: "create or overwrite gofly config", RiskLevel: "low"}}))
		}
		if err := generator.SaveConfig(path, cfg); err != nil {
			return err
		}
		hooks.PrintTextf("wrote gofly config: %s\n", path)
		return nil
	case "show":
		cfg, err := generator.LoadConfig(path)
		if err != nil {
			return err
		}
		hooks.PrintTextln(cfg.String())
		return nil
	case "get":
		if *key == "" {
			return fmt.Errorf("%w: --key is required for `gofly config get`", hooks.Usage)
		}
		cfg, err := generator.LoadConfig(path)
		if err != nil {
			return err
		}
		hooks.PrintTextln(GetField(cfg, *key))
		return nil
	case "set":
		if *key == "" {
			return fmt.Errorf("%w: --key is required for `gofly config set`", hooks.Usage)
		}
		if *value == "" && (!valueExplicit || !IsFeaturesKey(*key)) {
			return fmt.Errorf("%w: --key and --value are required for `gofly config set`", hooks.Usage)
		}
		cfg, err := generator.LoadConfig(path)
		if err != nil {
			return err
		}
		if err := SetField(cfg, *key, *value, hooks.Usage); err != nil {
			return err
		}
		if previewOnly {
			return hooks.PrintPlan("config.set", configPlan("config set", path, true, map[string]string{"dir": base, "key": *key, "value": *value}, []PlanAction{{Operation: "update-config", Target: path, Description: "update one gofly config value", RiskLevel: "low"}}))
		}
		if err := generator.SaveConfig(path, cfg); err != nil {
			return err
		}
		hooks.PrintTextf("updated gofly config: %s\n", path)
		return nil
	case "clean":
		if previewOnly {
			return hooks.PrintPlan("config.clean", configPlan("config clean", path, true, map[string]string{"dir": base}, []PlanAction{{Operation: "remove-config", Target: path, Description: "remove gofly config if it exists", RiskLevel: "medium"}}))
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clean gofly config: %w", err)
		}
		hooks.PrintTextf("removed gofly config: %s\n", path)
		return nil
	default:
		return fmt.Errorf("%w: expected `gofly config init|show|get|set|clean`", hooks.Usage)
	}
}

func configPlan(command, path string, dryRun bool, inputs map[string]string, actions []PlanAction) Plan {
	if inputs == nil {
		inputs = map[string]string{}
	}
	inputs["path"] = path
	return Plan{
		Command:           command,
		DryRun:            dryRun,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions:           actions,
		NextActions:       []string{"rerun without --dry-run/--plan to apply these actions"},
	}
}
