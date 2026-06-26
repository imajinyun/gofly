package command

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	dryRun := fs.Bool("dry-run", false, "print the planned filesystem changes without writing files")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	remaining, err := parseInterspersedFlags(fs, rest)
	if err != nil {
		return err
	}
	previewOnly := *dryRun || *plan
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

// getConfigField 返回 Config 的简单字段（用于 `gofly config get <key>`）。
func getConfigField(cfg *generator.Config, key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "servicename", "service-name", "service":
		return cfg.ServiceName
	case "module":
		return cfg.Module
	case "style":
		return cfg.Style
	case "templatedir", "template-dir", "templates":
		return cfg.TemplateDir
	case "goversion", "go-version":
		return cfg.GoVersion
	case "features":
		return strings.Join(cfg.Features, ",")
	case "rpc.plugins", "rpc-plugins":
		if cfg.RPC != nil {
			return strings.Join(cfg.RPC.Plugins, ",")
		}
		return ""
	case "rpc.transport", "rpc-transport":
		if cfg.RPC != nil {
			return cfg.RPC.Transport
		}
		return ""
	case "rpc.profile", "rpc-profile", "profile":
		if cfg.RPC != nil {
			return cfg.RPC.Profile
		}
		return ""
	case "api.plugins", "api-plugins":
		if cfg.API != nil {
			return strings.Join(cfg.API.Plugins, ",")
		}
		return ""
	case "api.profile", "api-profile":
		if cfg.API != nil {
			return cfg.API.Profile
		}
		return ""
	case "model.style", "model-style":
		if cfg.Model != nil {
			return cfg.Model.Style
		}
		return ""
	case "model.ignorecolumns", "model.ignore-columns", "model-ignore-columns":
		if cfg.Model != nil {
			return strings.Join(cfg.Model.IgnoreColumns, ",")
		}
		return ""
	case "model.typesmap", "model.types-map", "model-types-map":
		if cfg.Model != nil {
			return encodeStringMap(cfg.Model.TypesMap)
		}
		return ""
	case "model.cache", "model-cache":
		if cfg.Model != nil && cfg.Model.Cache {
			return "true"
		}
		return "false"
	case "model.strict", "model-strict":
		if cfg.Model != nil && cfg.Model.Strict {
			return "true"
		}
		return "false"
	case "llm.provider", "llm-provider":
		if cfg.LLM != nil {
			return cfg.LLM.Provider
		}
		return ""
	case "llm.model", "llm-model":
		if cfg.LLM != nil {
			return cfg.LLM.Model
		}
		return ""
	case "llm.maxinputtokens", "llm.max-input-tokens", "llm-max-input-tokens":
		if cfg.LLM != nil {
			return fmt.Sprint(cfg.LLM.MaxInputTokens)
		}
		return "0"
	case "llm.maxoutputtokens", "llm.max-output-tokens", "llm-max-output-tokens":
		if cfg.LLM != nil {
			return fmt.Sprint(cfg.LLM.MaxOutputTokens)
		}
		return "0"
	case "llm.maxtotaltokens", "llm.max-total-tokens", "llm-max-total-tokens":
		if cfg.LLM != nil {
			return fmt.Sprint(cfg.LLM.MaxTotalTokens)
		}
		return "0"
	case "llm.ratelimit", "llm.rate-limit", "llm-rate-limit":
		if cfg.LLM != nil {
			return fmt.Sprint(cfg.LLM.RateLimitPerSecond)
		}
		return "0"
	case "llm.rateburst", "llm.rate-burst", "llm-rate-burst":
		if cfg.LLM != nil {
			return fmt.Sprint(cfg.LLM.RateLimitBurst)
		}
		return "0"
	case "llm.timeout", "llm-timeout":
		if cfg.LLM != nil {
			return cfg.LLM.Timeout
		}
		return ""
	default:
		if cfg.Extra != nil {
			if v, ok := cfg.Extra[key]; ok {
				return v
			}
		}
		return ""
	}
}

func isConfigFeaturesKey(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), "features")
}

// setConfigField 写入 Config 的简单字段（用于 `gofly config set <key> <value>`）。
func setConfigField(cfg *generator.Config, key, value string) error {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "servicename", "service-name", "service":
		cfg.ServiceName = value
	case "module":
		cfg.Module = value
	case "style":
		cfg.Style = value
	case "templatedir", "template-dir", "templates":
		cfg.TemplateDir = value
	case "goversion", "go-version":
		cfg.GoVersion = value
	case "features":
		features := splitCSV(value)
		if err := generator.ValidateFeatureNames(features); err != nil {
			return err
		}
		cfg.Features = features
	case "rpc.plugins", "rpc-plugins":
		if cfg.RPC == nil {
			cfg.RPC = &generator.RPCConfig{}
		}
		cfg.RPC.Plugins = splitCSV(value)
	case "rpc.transport", "rpc-transport":
		if cfg.RPC == nil {
			cfg.RPC = &generator.RPCConfig{}
		}
		cfg.RPC.Transport = value
	case "rpc.profile", "rpc-profile", "profile":
		if cfg.RPC == nil {
			cfg.RPC = &generator.RPCConfig{}
		}
		cfg.RPC.Profile = value
	case "api.plugins", "api-plugins":
		if cfg.API == nil {
			cfg.API = &generator.APIConfig{}
		}
		cfg.API.Plugins = splitCSV(value)
	case "api.profile", "api-profile":
		if cfg.API == nil {
			cfg.API = &generator.APIConfig{}
		}
		cfg.API.Profile = value
	case "model.style", "model-style":
		ensureModelConfig(cfg).Style = value
	case "model.ignorecolumns", "model.ignore-columns", "model-ignore-columns":
		ensureModelConfig(cfg).IgnoreColumns = splitCSV(value)
	case "model.typesmap", "model.types-map", "model-types-map":
		ensureModelConfig(cfg).TypesMap = parseKeyValueCSV(value)
	case "model.cache", "model-cache":
		ensureModelConfig(cfg).Cache = parseBoolString(value)
	case "model.strict", "model-strict":
		ensureModelConfig(cfg).Strict = parseBoolString(value)
	case "llm.provider", "llm-provider":
		ensureLLMConfig(cfg).Provider = value
	case "llm.model", "llm-model":
		ensureLLMConfig(cfg).Model = value
	case "llm.maxinputtokens", "llm.max-input-tokens", "llm-max-input-tokens":
		v, err := parseNonNegativeIntConfigValue("llm.maxInputTokens", value)
		if err != nil {
			return err
		}
		ensureLLMConfig(cfg).MaxInputTokens = v
	case "llm.maxoutputtokens", "llm.max-output-tokens", "llm-max-output-tokens":
		v, err := parseNonNegativeIntConfigValue("llm.maxOutputTokens", value)
		if err != nil {
			return err
		}
		ensureLLMConfig(cfg).MaxOutputTokens = v
	case "llm.maxtotaltokens", "llm.max-total-tokens", "llm-max-total-tokens":
		v, err := parseNonNegativeIntConfigValue("llm.maxTotalTokens", value)
		if err != nil {
			return err
		}
		ensureLLMConfig(cfg).MaxTotalTokens = v
	case "llm.ratelimit", "llm.rate-limit", "llm-rate-limit":
		v, err := parseNonNegativeIntConfigValue("llm.rateLimitPerSecond", value)
		if err != nil {
			return err
		}
		ensureLLMConfig(cfg).RateLimitPerSecond = v
	case "llm.rateburst", "llm.rate-burst", "llm-rate-burst":
		v, err := parseNonNegativeIntConfigValue("llm.rateLimitBurst", value)
		if err != nil {
			return err
		}
		ensureLLMConfig(cfg).RateLimitBurst = v
	case "llm.timeout", "llm-timeout":
		if value != "" {
			if _, err := time.ParseDuration(value); err != nil {
				return fmt.Errorf("%w: invalid llm.timeout %q: %v", errUsage, value, err)
			}
		}
		ensureLLMConfig(cfg).Timeout = value
	default:
		if cfg.Extra == nil {
			cfg.Extra = map[string]string{}
		}
		cfg.Extra[key] = value
	}
	return nil
}

func ensureModelConfig(cfg *generator.Config) *generator.ModelConfig {
	if cfg.Model == nil {
		cfg.Model = &generator.ModelConfig{}
	}
	if cfg.Model.TypesMap == nil {
		cfg.Model.TypesMap = map[string]string{}
	}
	return cfg.Model
}

func ensureLLMConfig(cfg *generator.Config) *generator.LLMConfig {
	if cfg.LLM == nil {
		cfg.LLM = &generator.LLMConfig{Provider: "noop", Model: "noop"}
	}
	return cfg.LLM
}

func parseNonNegativeIntConfigValue(name, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", errUsage, name)
	}
	return parsed, nil
}

func parseBoolString(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}
