package command

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// configCommand 处理 `gofly config init|show|get|set|clean`。
func configCommand(args []string) error {
	if printCommandHelp("config", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly config init|show|get|set|clean`", errUsage)
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
	preview := registerDryRunPlanFlags(fs, "print the planned filesystem changes without writing files")
	remaining, err := parseInterspersedFlags(fs, rest)
	if err != nil {
		return err
	}
	previewOnly := preview.enabled()
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
			return printCLIPlan("config.init", configPlan("config init", path, true, map[string]string{"dir": base, "name": *name, "module": *module, "style": cfg.Style}, []cliPlanAction{{Operation: "write-config", Target: path, Description: "create or overwrite gofly config", RiskLevel: "low"}}))
		}
		if err := generator.SaveConfig(path, cfg); err != nil {
			return err
		}
		cliOutputf("wrote gofly config: %s\n", path)
		return nil
	case "show":
		cfg, err := generator.LoadConfig(path)
		if err != nil {
			return err
		}
		cliOutputln(cfg.String())
		return nil
	case "get":
		if *key == "" {
			return fmt.Errorf("%w: --key is required for `gofly config get`", errUsage)
		}
		cfg, err := generator.LoadConfig(path)
		if err != nil {
			return err
		}
		cliOutputln(getConfigField(cfg, *key))
		return nil
	case "set":
		if *key == "" {
			return fmt.Errorf("%w: --key is required for `gofly config set`", errUsage)
		}
		if *value == "" && (!valueExplicit || !isConfigFeaturesKey(*key)) {
			return fmt.Errorf("%w: --key and --value are required for `gofly config set`", errUsage)
		}
		cfg, err := generator.LoadConfig(path)
		if err != nil {
			return err
		}
		if err := setConfigField(cfg, *key, *value); err != nil {
			return err
		}
		if previewOnly {
			return printCLIPlan("config.set", configPlan("config set", path, true, map[string]string{"dir": base, "key": *key, "value": *value}, []cliPlanAction{{Operation: "update-config", Target: path, Description: "update one gofly config value", RiskLevel: "low"}}))
		}
		if err := generator.SaveConfig(path, cfg); err != nil {
			return err
		}
		cliOutputf("updated gofly config: %s\n", path)
		return nil
	case "clean":
		if previewOnly {
			return printCLIPlan("config.clean", configPlan("config clean", path, true, map[string]string{"dir": base}, []cliPlanAction{{Operation: "remove-config", Target: path, Description: "remove gofly config if it exists", RiskLevel: "medium"}}))
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clean gofly config: %w", err)
		}
		cliOutputf("removed gofly config: %s\n", path)
		return nil
	default:
		return fmt.Errorf("%w: expected `gofly config init|show|get|set|clean`", errUsage)
	}
}

func configPlan(command, path string, dryRun bool, inputs map[string]string, actions []cliPlanAction) cliPlan {
	if inputs == nil {
		inputs = map[string]string{}
	}
	inputs["path"] = path
	return cliPlan{
		Command:           command,
		DryRun:            dryRun,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions:           actions,
		NextActions:       []string{"rerun without --dry-run/--plan to apply these actions"},
	}
}
