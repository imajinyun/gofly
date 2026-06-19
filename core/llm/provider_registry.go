package llm

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

var (
	// ErrProviderNotFound reports that no provider factory is registered for the requested name.
	ErrProviderNotFound = errors.New("llm provider not found")
	// ErrProviderAlreadyRegistered reports that a registry already contains a provider name.
	ErrProviderAlreadyRegistered = errors.New("llm provider already registered")
	// ErrSecretNotFound reports that a provider-required secret is not available from the configured resolver.
	ErrSecretNotFound = errors.New("llm provider secret not found")
)

// ProviderPluginManifestSchemaVersion is the public schema identifier for LLM
// provider plugin manifests. Plugins should expose this safe-to-publish shape
// without embedding credential values or endpoint secrets.
const ProviderPluginManifestSchemaVersion = "gofly.llm.provider-plugin.v1"

// SecretResolver resolves provider secrets by name. Implementations must never
// expose values through errors, manifests or audit records.
type SecretResolver interface {
	LookupSecret(name string) (string, bool)
}

// EnvSecretResolver resolves secrets from environment variables.
type EnvSecretResolver struct{}

// LookupSecret returns a non-empty environment variable value.
func (EnvSecretResolver) LookupSecret(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

// ProviderConfig is passed to provider factories. Secrets are resolved lazily
// through SecretResolver so config files and manifests never store secret values.
type ProviderConfig struct {
	Provider string
	Model    string
	Secrets  SecretResolver
	Metadata map[string]string
}

// ProviderFactory constructs one provider instance for a resolved provider config.
type ProviderFactory func(ProviderConfig) (Provider, error)

// ProviderModelSpec describes safe-to-publish model-level capabilities for one
// provider model. It lets callers negotiate features such as streaming,
// tool-calls, JSON mode or embedding dimensions before execution.
type ProviderModelSpec struct {
	Name                string   `json:"name"`
	DisplayName         string   `json:"displayName,omitempty"`
	Default             bool     `json:"default,omitempty"`
	Capabilities        []string `json:"capabilities,omitempty"`
	ContextWindow       int      `json:"contextWindow,omitempty"`
	MaxOutputTokens     int      `json:"maxOutputTokens,omitempty"`
	EmbeddingDimensions int      `json:"embeddingDimensions,omitempty"`
}

// ProviderPluginManifest is the minimum public contract provider plugins should
// return before registration. The manifest is intentionally limited to safe
// metadata: provider identity, environment variable names, provider-level
// capabilities and model-level capability declarations.
type ProviderPluginManifest struct {
	SchemaVersion string              `json:"schemaVersion"`
	Provider      ProviderSpec        `json:"provider"`
	Models        []ProviderModelSpec `json:"models,omitempty"`
}

// ProviderSpec describes a provider's safe-to-publish capabilities and secret requirements.
type ProviderSpec struct {
	Name            string              `json:"name"`
	DisplayName     string              `json:"displayName,omitempty"`
	DefaultModel    string              `json:"defaultModel,omitempty"`
	BuiltIn         bool                `json:"builtIn"`
	NetworkAccess   bool                `json:"networkAccess"`
	RequiresSecrets bool                `json:"requiresSecrets"`
	SecretEnvVars   []string            `json:"secretEnvVars,omitempty"`
	ConfigEnvVars   []string            `json:"configEnvVars,omitempty"`
	Capabilities    []string            `json:"capabilities,omitempty"`
	Models          []ProviderModelSpec `json:"models,omitempty"`
}

type providerRegistration struct {
	spec    ProviderSpec
	factory ProviderFactory
}

// ProviderRegistry maps provider names to factories and safe capability metadata.
type ProviderRegistry struct {
	providers map[string]providerRegistration
}

// NewProviderRegistry creates an empty provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: map[string]providerRegistration{}}
}

// NewDefaultProviderRegistry returns the built-in provider registry. It is a new
// registry per call so tests and applications can extend it without mutating global state.
func NewDefaultProviderRegistry() *ProviderRegistry {
	registry := NewProviderRegistry()
	_ = registry.Register(NoOpProviderSpec(), func(ProviderConfig) (Provider, error) {
		return NoOpProvider{}, nil
	})
	_ = registry.Register(OpenAICompatibleProviderSpec(), NewOpenAICompatibleProviderFromConfig)
	return registry
}

// Register adds a provider factory and safe capability metadata.
func (r *ProviderRegistry) Register(spec ProviderSpec, factory ProviderFactory) error {
	if r == nil {
		return errors.New("llm provider registry is nil")
	}
	if r.providers == nil {
		r.providers = map[string]providerRegistration{}
	}
	name := normalizeProviderName(spec.Name)
	if name == "" {
		return errors.New("llm provider name is required")
	}
	if factory == nil {
		return errors.New("llm provider factory is required")
	}
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("%w: %s", ErrProviderAlreadyRegistered, name)
	}
	spec.Name = name
	spec.SecretEnvVars = cleanStringList(spec.SecretEnvVars)
	spec.ConfigEnvVars = cleanStringList(spec.ConfigEnvVars)
	spec.Capabilities = cleanStringList(spec.Capabilities)
	spec.Models = cleanProviderModelSpecs(spec.Models)
	r.providers[name] = providerRegistration{spec: spec.clone(), factory: factory}
	return nil
}

// RegisterManifest validates a provider plugin manifest and registers its
// provider. It is the preferred entry point for plugin-provided providers
// because it keeps the public contract explicit and safe to publish.
func (r *ProviderRegistry) RegisterManifest(manifest ProviderPluginManifest, factory ProviderFactory) error {
	schema := strings.TrimSpace(manifest.SchemaVersion)
	if schema != "" && schema != ProviderPluginManifestSchemaVersion {
		return fmt.Errorf("llm provider plugin manifest schema %q is not supported", schema)
	}
	spec := manifest.Provider
	if len(spec.Models) == 0 {
		spec.Models = manifest.Models
	}
	return r.Register(spec, factory)
}

// Spec returns safe-to-publish provider metadata.
func (r *ProviderRegistry) Spec(name string) (ProviderSpec, bool) {
	if r == nil {
		return ProviderSpec{}, false
	}
	registration, ok := r.providers[normalizeProviderName(name)]
	if !ok {
		return ProviderSpec{}, false
	}
	return registration.spec.clone(), true
}

// Specs returns all registered provider specs in deterministic name order.
func (r *ProviderRegistry) Specs() []ProviderSpec {
	if r == nil || len(r.providers) == 0 {
		return nil
	}
	names := r.ProviderNames()
	specs := make([]ProviderSpec, 0, len(names))
	for _, name := range names {
		spec, _ := r.Spec(name)
		specs = append(specs, spec)
	}
	return specs
}

// ProviderNames returns registered provider names in deterministic order.
func (r *ProviderRegistry) ProviderNames() []string {
	if r == nil || len(r.providers) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SpecsWithCapability returns registered provider specs that advertise the
// requested capability. Results are sorted by provider name and contain cloned
// safe-to-publish specs so callers cannot mutate registry state.
func (r *ProviderRegistry) SpecsWithCapability(capability string) []ProviderSpec {
	capability = strings.TrimSpace(capability)
	if r == nil || capability == "" {
		return nil
	}
	specs := make([]ProviderSpec, 0)
	for _, name := range r.ProviderNames() {
		spec, ok := r.Spec(name)
		if !ok {
			continue
		}
		if providerSpecHasCapability(spec, capability) {
			specs = append(specs, spec)
		}
	}
	return specs
}

// ProviderSupportsCapability reports whether a registered provider advertises a
// capability. It is intended for CLI/runtime policy negotiation before a caller
// attempts to execute an operation against plugin-provided providers.
func (r *ProviderRegistry) ProviderSupportsCapability(name, capability string) bool {
	spec, ok := r.Spec(name)
	if !ok {
		return false
	}
	return providerSpecHasCapability(spec, strings.TrimSpace(capability))
}

// ProviderModelSupportsCapability reports whether a provider advertises a
// specific model-level capability. An empty model name falls back to the
// provider default model.
func (r *ProviderRegistry) ProviderModelSupportsCapability(provider, model, capability string) bool {
	spec, ok := r.Spec(provider)
	if !ok {
		return false
	}
	return providerModelSpecHasCapability(spec, model, strings.TrimSpace(capability))
}

// SpecsWithModelCapability returns providers that advertise at least one model
// with the requested capability.
func (r *ProviderRegistry) SpecsWithModelCapability(capability string) []ProviderSpec {
	capability = strings.TrimSpace(capability)
	if r == nil || capability == "" {
		return nil
	}
	specs := make([]ProviderSpec, 0)
	for _, name := range r.ProviderNames() {
		spec, ok := r.Spec(name)
		if !ok {
			continue
		}
		if providerModelSpecHasCapability(spec, "", capability) || providerHasAnyModelCapability(spec, capability) {
			specs = append(specs, spec)
		}
	}
	return specs
}

// Build constructs a provider after validating secret availability.
func (r *ProviderRegistry) Build(name string, config ProviderConfig) (Provider, ProviderSpec, error) {
	if r == nil {
		return nil, ProviderSpec{}, errors.New("llm provider registry is nil")
	}
	name = normalizeProviderName(name)
	registration, ok := r.providers[name]
	if !ok {
		return nil, ProviderSpec{}, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
	}
	spec := registration.spec.clone()
	if spec.RequiresSecrets {
		resolver := config.Secrets
		if resolver == nil {
			resolver = EnvSecretResolver{}
		}
		missing := missingSecrets(resolver, spec.SecretEnvVars)
		if len(missing) > 0 {
			return nil, spec, fmt.Errorf("%w: %s requires %s", ErrSecretNotFound, spec.Name, strings.Join(missing, ","))
		}
	}
	config.Provider = spec.Name
	if strings.TrimSpace(config.Model) == "" {
		config.Model = spec.DefaultModel
	}
	if config.Secrets == nil {
		config.Secrets = EnvSecretResolver{}
	}
	provider, err := registration.factory(config)
	if err != nil {
		return nil, spec, fmt.Errorf("build llm provider %s: %w", spec.Name, err)
	}
	if provider == nil {
		return nil, spec, fmt.Errorf("build llm provider %s: provider is nil", spec.Name)
	}
	return provider, spec, nil
}

// NoOpProviderSpec returns the built-in deterministic provider manifest entry.
func NoOpProviderSpec() ProviderSpec {
	return ProviderSpec{
		Name:            "noop",
		DisplayName:     "Deterministic no-op provider",
		DefaultModel:    "noop",
		BuiltIn:         true,
		NetworkAccess:   false,
		RequiresSecrets: false,
		Capabilities:    []string{"complete", "stream", "embed", "deterministic", "offline"},
		Models: []ProviderModelSpec{
			{Name: "noop", Default: true, Capabilities: []string{"complete", "stream", "embed", "json-mode", "deterministic", "offline"}},
		},
	}
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func missingSecrets(resolver SecretResolver, names []string) []string {
	missing := make([]string, 0)
	for _, name := range names {
		if _, ok := resolver.LookupSecret(name); !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func cleanStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cleanProviderModelSpecs(models []ProviderModelSpec) []ProviderModelSpec {
	if len(models) == 0 {
		return nil
	}
	out := make([]ProviderModelSpec, 0, len(models))
	seen := map[string]struct{}{}
	for _, model := range models {
		model.Name = strings.TrimSpace(model.Name)
		if model.Name == "" {
			continue
		}
		if _, ok := seen[model.Name]; ok {
			continue
		}
		seen[model.Name] = struct{}{}
		model.DisplayName = strings.TrimSpace(model.DisplayName)
		model.Capabilities = cleanStringList(model.Capabilities)
		out = append(out, model)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func providerSpecHasCapability(spec ProviderSpec, capability string) bool {
	if capability == "" {
		return false
	}
	for _, got := range spec.Capabilities {
		if got == capability {
			return true
		}
	}
	return false
}

func providerHasAnyModelCapability(spec ProviderSpec, capability string) bool {
	for _, model := range spec.Models {
		if modelSpecHasCapability(model, capability) {
			return true
		}
	}
	return false
}

func providerModelSpecHasCapability(spec ProviderSpec, modelName, capability string) bool {
	if capability == "" {
		return false
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = strings.TrimSpace(spec.DefaultModel)
	}
	for _, model := range spec.Models {
		if modelName != "" && model.Name != modelName {
			continue
		}
		return modelSpecHasCapability(model, capability)
	}
	return false
}

func modelSpecHasCapability(model ProviderModelSpec, capability string) bool {
	for _, got := range model.Capabilities {
		if got == capability {
			return true
		}
	}
	return false
}

func (s ProviderSpec) clone() ProviderSpec {
	s.SecretEnvVars = append([]string(nil), s.SecretEnvVars...)
	s.ConfigEnvVars = append([]string(nil), s.ConfigEnvVars...)
	s.Capabilities = append([]string(nil), s.Capabilities...)
	s.Models = append([]ProviderModelSpec(nil), s.Models...)
	for i := range s.Models {
		s.Models[i].Capabilities = append([]string(nil), s.Models[i].Capabilities...)
	}
	return s
}
