package command

import (
	"fmt"
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

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
