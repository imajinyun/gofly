// Package generator implements gofly's code generation engine: service
// scaffolding, API/RPC/model descriptors, proto codegen and template rendering.
package generator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
)

// DefaultConfigFile 默认配置文件路径（相对于项目根）。
const DefaultConfigFile = ".gofly/config.json"

var defaultConfigFeatures = []string{"ecosystem-compat"}

// Config 描述 gofly 代码生成的可复用配置。
// 配置文件允许用户持久化默认选项，以避免在每次调用时重复传递参数。
type Config struct {
	ServiceName    string            `json:"serviceName,omitempty"`
	Module         string            `json:"module,omitempty"`
	Style          string            `json:"style,omitempty"`
	TemplateDir    string            `json:"templateDir,omitempty"`
	TemplateRemote string            `json:"templateRemote,omitempty"`
	TemplateBranch string            `json:"templateBranch,omitempty"`
	Features       []string          `json:"features"`
	Dependencies   map[string]string `json:"dependencies,omitempty"`
	RPC            *RPCConfig        `json:"rpc,omitempty"`
	API            *APIConfig        `json:"api,omitempty"`
	Model          *ModelConfig      `json:"model,omitempty"`
	Discovery      *DiscoveryConfig  `json:"discovery,omitempty"`
	LLM            *LLMConfig        `json:"llm,omitempty"`
	Extra          map[string]string `json:"extra,omitempty"`
	GoVersion      string            `json:"goVersion,omitempty"`
	GeneratedBy    string            `json:"generatedBy,omitempty"`
	GeneratedAt    string            `json:"generatedAt,omitempty"`
}

// RPCConfig 为 RPC 生成保存默认参数。
type RPCConfig struct {
	Transport string   `json:"transport,omitempty"`
	Profile   string   `json:"profile,omitempty"`
	Includes  []string `json:"includes,omitempty"`
	Plugins   []string `json:"plugins,omitempty"`
	Standard  bool     `json:"standard,omitempty"`
}

// APIConfig 为 API 生成保存默认参数。
type APIConfig struct {
	Plugins    []string `json:"plugins,omitempty"`
	Middleware []string `json:"middleware,omitempty"`
	Profile    string   `json:"profile,omitempty"`
}

// ModelConfig 为 model 生成保存默认参数，包含 goctl config 的类型映射能力。
type ModelConfig struct {
	Style         string            `json:"style,omitempty"`
	Cache         bool              `json:"cache,omitempty"`
	Strict        bool              `json:"strict,omitempty"`
	IgnoreColumns []string          `json:"ignoreColumns,omitempty"`
	TypesMap      map[string]string `json:"typesMap,omitempty"`
}

// DiscoveryConfig stores service discovery defaults for generated services.
// Secret material is referenced by environment variable names instead of being
// persisted in the gofly config file.
type DiscoveryConfig struct {
	Provider    string   `json:"provider,omitempty"`
	Address     string   `json:"address,omitempty"`
	Endpoints   []string `json:"endpoints,omitempty"`
	Prefix      string   `json:"prefix,omitempty"`
	TTL         string   `json:"ttl,omitempty"`
	DialTimeout string   `json:"dialTimeout,omitempty"`
	TokenEnv    string   `json:"tokenEnv,omitempty"`
	UsernameEnv string   `json:"usernameEnv,omitempty"`
	PasswordEnv string   `json:"passwordEnv,omitempty"`
}

// LLMConfig stores defaults for governed LLM CLI calls. It intentionally does
// not store secrets; provider credentials should be supplied by environment or
// an external secret manager when real providers are added.
type LLMConfig struct {
	Provider           string `json:"provider,omitempty"`
	Model              string `json:"model,omitempty"`
	MaxInputTokens     int    `json:"maxInputTokens,omitempty"`
	MaxOutputTokens    int    `json:"maxOutputTokens,omitempty"`
	MaxTotalTokens     int    `json:"maxTotalTokens,omitempty"`
	RateLimitPerSecond int    `json:"rateLimitPerSecond,omitempty"`
	RateLimitBurst     int    `json:"rateLimitBurst,omitempty"`
	Timeout            string `json:"timeout,omitempty"`
}

// DefaultConfig 返回一个带有默认值的 Config。
func DefaultConfig(service, module string) *Config {
	return &Config{
		ServiceName:  service,
		Module:       module,
		Style:        ServiceStyleBasic,
		Features:     DefaultConfigFeatures(),
		Dependencies: map[string]string{},
		Extra:        map[string]string{},
		RPC:          &RPCConfig{Transport: "grpc", Profile: string(ProfileGoflyAI)},
		API:          &APIConfig{},
		Model:        &ModelConfig{TypesMap: map[string]string{}},
		LLM:          &LLMConfig{Provider: "noop", Model: "noop"},
		GoVersion:    strings.TrimPrefix(runtime.Version(), "go"),
	}
}

// DefaultConfigFeatures returns the built-in scaffold features enabled for new
// projects unless users explicitly override them in a config file.
func DefaultConfigFeatures() []string {
	return append([]string(nil), defaultConfigFeatures...)
}

// ApplyEnvOverlay applies GOFLY_* environment variables on top of a loaded
// Config. This implements the env layer in the configuration chain:
// flag > env > file > default. Fields set via env vars replace existing values.
func ApplyEnvOverlay(cfg *Config) {
	if cfg == nil {
		return
	}
	if v := os.Getenv("GOFLY_STYLE"); v != "" {
		cfg.Style = v
	}
	if v := os.Getenv("GOFLY_MODULE"); v != "" {
		cfg.Module = v
	}
	if v := os.Getenv("GOFLY_TEMPLATE_DIR"); v != "" {
		cfg.TemplateDir = v
	}
	if v := os.Getenv("GOFLY_TEMPLATE_REMOTE"); v != "" {
		cfg.TemplateRemote = v
	}
	if v := os.Getenv("GOFLY_TEMPLATE_BRANCH"); v != "" {
		cfg.TemplateBranch = v
	}
	if v := os.Getenv("GOFLY_FEATURES"); v != "" {
		cfg.Features = splitEnvList(v)
	}
	// GOFLY_SERVICE_NAME overrides the service name.
	if v := os.Getenv("GOFLY_SERVICE_NAME"); v != "" {
		cfg.ServiceName = v
	}
	if v := os.Getenv("GOFLY_RPC_PROFILE"); v != "" {
		if cfg.RPC == nil {
			cfg.RPC = &RPCConfig{}
		}
		cfg.RPC.Profile = v
	}
	if v := os.Getenv("GOFLY_API_PROFILE"); v != "" {
		if cfg.API == nil {
			cfg.API = &APIConfig{}
		}
		cfg.API.Profile = v
	}
	applyDiscoveryEnvOverlay(cfg)
}

func applyDiscoveryEnvOverlay(cfg *Config) {
	if cfg == nil {
		return
	}
	ensure := func() *DiscoveryConfig {
		if cfg.Discovery == nil {
			cfg.Discovery = &DiscoveryConfig{}
		}
		return cfg.Discovery
	}
	if v := os.Getenv("GOFLY_DISCOVERY"); v != "" {
		ensure().Provider = v
	}
	if v := os.Getenv("GOFLY_DISCOVERY_ADDRESS"); v != "" {
		ensure().Address = v
	}
	if v := os.Getenv("GOFLY_DISCOVERY_ENDPOINTS"); v != "" {
		ensure().Endpoints = splitEnvList(v)
	}
	if v := os.Getenv("GOFLY_DISCOVERY_PREFIX"); v != "" {
		ensure().Prefix = v
	}
	if v := os.Getenv("GOFLY_DISCOVERY_TTL"); v != "" {
		ensure().TTL = v
	}
	if v := os.Getenv("GOFLY_DISCOVERY_DIAL_TIMEOUT"); v != "" {
		ensure().DialTimeout = v
	}
	if v := os.Getenv("GOFLY_DISCOVERY_TOKEN_ENV"); v != "" {
		ensure().TokenEnv = v
	}
	if v := os.Getenv("GOFLY_DISCOVERY_USERNAME_ENV"); v != "" {
		ensure().UsernameEnv = v
	}
	if v := os.Getenv("GOFLY_DISCOVERY_PASSWORD_ENV"); v != "" {
		ensure().PasswordEnv = v
	}
}

func splitEnvList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// LoadConfig 从路径加载配置；若文件不存在返回默认值和 os.ErrNotExist 以外的 nil。
func LoadConfig(path string) (*Config, error) {
	if path == "" {
		return DefaultConfig("", ""), nil
	}
	// #nosec G304 -- gofly config paths are explicit CLI/caller inputs, not request-derived file names.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultConfig("", ""), nil
		}
		return nil, fmt.Errorf("read gofly config %s: %w", path, err)
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse gofly config %s: %w", path, err)
	}
	if cfg.Features == nil {
		cfg.Features = DefaultConfigFeatures()
	}
	if cfg.Dependencies == nil {
		cfg.Dependencies = map[string]string{}
	}
	if cfg.Extra == nil {
		cfg.Extra = map[string]string{}
	}
	if cfg.RPC == nil {
		cfg.RPC = &RPCConfig{Transport: "grpc", Profile: string(ProfileGoflyAI)}
	}
	if cfg.RPC.Profile == "" {
		cfg.RPC.Profile = string(ProfileGoflyAI)
	}
	if cfg.API == nil {
		cfg.API = &APIConfig{}
	}
	if cfg.Model == nil {
		cfg.Model = &ModelConfig{TypesMap: map[string]string{}}
	}
	if cfg.Model.TypesMap == nil {
		cfg.Model.TypesMap = map[string]string{}
	}
	if cfg.LLM == nil {
		cfg.LLM = &LLMConfig{Provider: "noop", Model: "noop"}
	}
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = "noop"
	}
	if cfg.LLM.Model == "" {
		cfg.LLM.Model = "noop"
	}
	return cfg, nil
}

// SaveConfig 将配置写入路径。目录会被自动创建。
func SaveConfig(path string, cfg *Config) error {
	if path == "" {
		return errors.New("config path is required")
	}
	if cfg == nil {
		return errors.New("config is nil")
	}
	if err := ensureGeneratedFileDir(path); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	// 保持输出字段顺序稳定、易读。
	cfg.GeneratedBy = "gofly"
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal gofly config: %w", err)
	}
	// #nosec G306 -- gofly project config is intentionally readable within the generated project.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write gofly config: %w", err)
	}
	return nil
}

// ApplyOverlay 把从命令行参数传递的值叠加到配置上，返回一个新的拷贝。
func (c *Config) ApplyOverlay(name, module, style, templateDir string, features []string) *Config {
	return c.ApplyOverlayWithTemplateSource(name, module, style, templateDir, "", "", features)
}

func (c *Config) ApplyOverlayWithTemplateSource(name, module, style, templateDir, templateRemote, templateBranch string, features []string) *Config {
	out := *c
	if c.RPC != nil {
		rpc := *c.RPC
		out.RPC = &rpc
	}
	if c.API != nil {
		api := *c.API
		out.API = &api
	}
	if c.LLM != nil {
		llm := *c.LLM
		out.LLM = &llm
	}
	if c.Discovery != nil {
		discovery := *c.Discovery
		if len(discovery.Endpoints) > 0 {
			discovery.Endpoints = append([]string(nil), discovery.Endpoints...)
		}
		out.Discovery = &discovery
	}
	if name != "" {
		out.ServiceName = name
	}
	if module != "" {
		out.Module = module
	}
	if style != "" {
		out.Style = style
	}
	if templateDir != "" {
		out.TemplateDir = templateDir
	}
	if templateRemote != "" {
		out.TemplateRemote = templateRemote
	}
	if templateBranch != "" {
		out.TemplateBranch = templateBranch
	}
	if len(features) > 0 {
		seen := map[string]struct{}{}
		merged := make([]string, 0, len(out.Features)+len(features))
		for _, f := range out.Features {
			if _, ok := seen[f]; ok {
				continue
			}
			seen[f] = struct{}{}
			merged = append(merged, f)
		}
		for _, f := range features {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if _, ok := seen[f]; ok {
				continue
			}
			seen[f] = struct{}{}
			merged = append(merged, f)
		}
		out.Features = merged
	}
	return &out
}

// ResolveServiceOptions 把配置转为 GenerateService 的参数集合。
func (c *Config) ResolveServiceOptions(name, module, dir, style string) ServiceOptions {
	if name == "" {
		name = c.ServiceName
	}
	if module == "" {
		module = c.Module
	}
	if style == "" {
		style = c.Style
	}
	return ServiceOptions{Name: name, Module: module, Dir: dir, Style: style}
}

// String 返回稳定的 JSON 字符串表示，方便测试与调试。
func (c *Config) String() string {
	if c == nil {
		return "{}"
	}
	sorted := &Config{
		ServiceName:    c.ServiceName,
		Module:         c.Module,
		Style:          c.Style,
		TemplateDir:    c.TemplateDir,
		TemplateRemote: c.TemplateRemote,
		TemplateBranch: c.TemplateBranch,
		GoVersion:      c.GoVersion,
	}
	if len(c.Features) > 0 {
		f := append([]string(nil), c.Features...)
		sort.Strings(f)
		sorted.Features = f
	}
	if len(c.Dependencies) > 0 {
		sorted.Dependencies = copySortedMap(c.Dependencies)
	}
	if len(c.Extra) > 0 {
		sorted.Extra = copySortedMap(c.Extra)
	}
	if c.RPC != nil {
		rpc := *c.RPC
		if len(rpc.Includes) > 0 {
			inc := append([]string(nil), rpc.Includes...)
			sort.Strings(inc)
			rpc.Includes = inc
		}
		if len(rpc.Plugins) > 0 {
			p := append([]string(nil), rpc.Plugins...)
			sort.Strings(p)
			rpc.Plugins = p
		}
		sorted.RPC = &rpc
	}
	if c.API != nil {
		api := *c.API
		if len(api.Plugins) > 0 {
			p := append([]string(nil), api.Plugins...)
			sort.Strings(p)
			api.Plugins = p
		}
		if len(api.Middleware) > 0 {
			mw := append([]string(nil), api.Middleware...)
			sort.Strings(mw)
			api.Middleware = mw
		}
		sorted.API = &api
	}
	if c.Model != nil {
		model := *c.Model
		if len(model.IgnoreColumns) > 0 {
			cols := append([]string(nil), model.IgnoreColumns...)
			sort.Strings(cols)
			model.IgnoreColumns = cols
		}
		if len(model.TypesMap) > 0 {
			model.TypesMap = copySortedMap(model.TypesMap)
		}
		sorted.Model = &model
	}
	if c.Discovery != nil {
		discovery := *c.Discovery
		if len(discovery.Endpoints) > 0 {
			endpoints := append([]string(nil), discovery.Endpoints...)
			sort.Strings(endpoints)
			discovery.Endpoints = endpoints
		}
		sorted.Discovery = &discovery
	}
	if c.LLM != nil {
		llm := *c.LLM
		sorted.LLM = &llm
	}
	data, err := json.MarshalIndent(sorted, "", "  ")
	if err != nil {
		return fmt.Sprintf("%#v", sorted)
	}
	return string(data)
}

func copySortedMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
