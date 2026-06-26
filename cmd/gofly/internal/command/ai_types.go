package command

import (
	"net/http"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/llm"
)

const aiControlPlaneSchemaID = "https://gofly.dev/schemas/ai-control-plane.schema.json"

type aiControlPlaneManifest struct {
	Package          string                                `json:"package"`
	Purpose          string                                `json:"purpose"`
	SnapshotVersion  string                                `json:"snapshotVersion"`
	SnapshotChecksum string                                `json:"snapshotChecksum"`
	SchemaID         string                                `json:"schemaId"`
	SchemaCommand    string                                `json:"schemaCommand"`
	SchemaChecksum   string                                `json:"schemaChecksum"`
	ProviderContract []string                              `json:"providerContract"`
	SnapshotFields   []string                              `json:"snapshotFields"`
	EventFields      []string                              `json:"eventFields"`
	Capabilities     []string                              `json:"capabilities"`
	ConsumerActions  []controlplane.SnapshotConsumerAction `json:"consumerActions"`
	Determinism      string                                `json:"determinism"`
	SecretBoundary   string                                `json:"secretBoundary"`
	AgentGuidance    []string                              `json:"agentGuidance"`
	DefaultMetadata  map[string]string                     `json:"defaultMetadata,omitempty"`
}

type aiLLMGovernance struct {
	Package                string                   `json:"package"`
	Capabilities           []string                 `json:"capabilities"`
	Resilience             []string                 `json:"resilience"`
	ProviderPluginContract aiProviderPluginContract `json:"providerPluginContract"`
	TokenBudgetPolicy      aiTokenBudgetPolicy      `json:"tokenBudgetPolicy"`
	RateLimitPolicy        aiRateLimitPolicy        `json:"rateLimitPolicy"`
	OutputContractPolicy   aiOutputContractPolicy   `json:"outputContractPolicy"`
	ErrorContractPolicy    aiErrorContractPolicy    `json:"errorContractPolicy"`
	DataSafetyPolicy       aiDataSafetyPolicy       `json:"dataSafetyPolicy"`
	ToolCallPolicy         aiToolCallPolicy         `json:"toolCallPolicy"`
	FailoverPolicy         aiFailoverPolicy         `json:"failoverPolicy"`
	ResponseCachePolicy    aiResponseCachePolicy    `json:"responseCachePolicy"`
	ObservabilityPolicy    aiObservabilityPolicy    `json:"observabilityPolicy"`
	CostPolicy             aiCostPolicy             `json:"costPolicy"`
	GovernancePipeline     []aiPipelineStage        `json:"governancePipeline"`
	AuditFields            []string                 `json:"auditFields"`
	TelemetryFields        []string                 `json:"telemetryFields"`
	DefaultMode            string                   `json:"defaultMode"`
	Providers              []llm.ProviderSpec       `json:"providers"`
}

type aiFeatureLibraryManifest struct {
	Mode                 string                                   `json:"mode"`
	Deterministic        bool                                     `json:"deterministic"`
	AppliesUnderDirOnly  bool                                     `json:"appliesUnderDirOnly"`
	DependencyPolicy     string                                   `json:"dependencyPolicy"`
	Features             []string                                 `json:"features"`
	Templates            []string                                 `json:"templates"`
	VerifyAllowlist      []string                                 `json:"verifyAllowlist"`
	TemplateVerification aiTemplateVerificationContract           `json:"templateVerification"`
	ResultFields         []string                                 `json:"resultFields"`
	Plugins              []generator.ProjectFeaturePluginContract `json:"plugins"`
}

type aiTemplateVerificationContract struct {
	CatalogField       string   `json:"catalogField"`
	MatrixTarget       string   `json:"matrixTarget"`
	GovernanceRound    string   `json:"governanceRound"`
	CIRequired         bool     `json:"ciRequired"`
	ZeroSkipRequired   bool     `json:"zeroSkipRequired"`
	ValidatedTemplates []string `json:"validatedTemplates"`
}

type aiTokenBudgetPolicy struct {
	DefaultMaxInputTokens  int      `json:"defaultMaxInputTokens"`
	DefaultMaxOutputTokens int      `json:"defaultMaxOutputTokens"`
	DefaultMaxTotalTokens  int      `json:"defaultMaxTotalTokens"`
	Configurable           bool     `json:"configurable"`
	CLIFlags               []string `json:"cliFlags"`
	EnvVars                []string `json:"envVars"`
	Enforcement            string   `json:"enforcement"`
	DeductionPoint         string   `json:"deductionPoint"`
	FailoverBudgetSharing  string   `json:"failoverBudgetSharing"`
	StreamAccounting       string   `json:"streamAccounting"`
	RejectionCode          string   `json:"rejectionCode"`
}

type aiRateLimitPolicy struct {
	DefaultRate  int    `json:"defaultRate"`
	DefaultBurst int    `json:"defaultBurst"`
	EnvVarRate   string `json:"envVarRate"`
	EnvVarBurst  string `json:"envVarBurst"`
	Strategy     string `json:"strategy"`
	Consequence  string `json:"consequence"`
	Configurable bool   `json:"configurable"`
	Scope        string `json:"scope"`
}

type aiProviderPluginContract struct {
	SchemaVersion  string   `json:"schemaVersion"`
	RequiredFields []string `json:"requiredFields"`
	SafeFields     []string `json:"safeFields"`
	SecretBoundary string   `json:"secretBoundary"`
}

type aiOutputContractPolicy struct {
	EnvelopeFields          []string `json:"envelopeFields"`
	ErrorFields             []string `json:"errorFields"`
	NextActions             bool     `json:"nextActions"`
	JSONMode                string   `json:"jsonMode"`
	SchemaValidation        string   `json:"schemaValidation"`
	RetryableErrorSemantics string   `json:"retryableErrorSemantics"`
	StreamSemantics         string   `json:"streamSemantics"`
	PartialFailureSemantics string   `json:"partialFailureSemantics"`
}

type aiErrorContractPolicy struct {
	CodeFormat              string   `json:"codeFormat"`
	StableCodes             []string `json:"stableCodes"`
	RetryableCodes          []string `json:"retryableCodes"`
	NonRetryableCodes       []string `json:"nonRetryableCodes"`
	ProviderStatusClasses   []string `json:"providerStatusClasses"`
	NextActionTypes         []string `json:"nextActionTypes"`
	EnvelopePlacement       string   `json:"envelopePlacement"`
	DetailsPolicy           string   `json:"detailsPolicy"`
	RetryableSemantics      string   `json:"retryableSemantics"`
	ProviderFailureGuidance string   `json:"providerFailureGuidance"`
}

type aiDataSafetyPolicy struct {
	SecretResolution    string   `json:"secretResolution"`
	Redaction           string   `json:"redaction"`
	PromptLogging       string   `json:"promptLogging"`
	ResponseLogging     string   `json:"responseLogging"`
	MetadataLogging     string   `json:"metadataLogging"`
	SecretValueLogging  string   `json:"secretValueLogging"`
	SensitiveEnvVarMode string   `json:"sensitiveEnvVarMode"`
	AuditBoundary       string   `json:"auditBoundary"`
	SafeToExpose        []string `json:"safeToExpose"`
}

type aiToolCallPolicy struct {
	DefaultMode                     string   `json:"defaultMode"`
	RequiresModelCapability         string   `json:"requiresModelCapability"`
	AllowedByDefault                []string `json:"allowedByDefault"`
	SideEffectToolsRequireApproval  bool     `json:"sideEffectToolsRequireApproval"`
	ArgumentSchemaValidation        bool     `json:"argumentSchemaValidation"`
	DryRunRequiredForMutation       bool     `json:"dryRunRequiredForMutation"`
	AuditToolArguments              string   `json:"auditToolArguments"`
	RejectedToolCallCode            string   `json:"rejectedToolCallCode"`
	UnsupportedCapabilityResolution string   `json:"unsupportedCapabilityResolution"`
}

type aiFailoverPolicy struct {
	EnvVar                string             `json:"envVar"`
	Mode                  string             `json:"mode"`
	AutomaticSwitching    bool               `json:"automaticSwitching"`
	ManualOptInFlags      []string           `json:"manualOptInFlags"`
	ExecutionGuardrails   []string           `json:"executionGuardrails"`
	ConfiguredProviders   []string           `json:"configuredProviders,omitempty"`
	InvalidProviders      []string           `json:"invalidProviders,omitempty"`
	ConfiguredSpecs       []llm.ProviderSpec `json:"configuredSpecs,omitempty"`
	EligibleCompleteSpecs []llm.ProviderSpec `json:"eligibleCompleteSpecs,omitempty"`
	EligibleStreamSpecs   []llm.ProviderSpec `json:"eligibleStreamSpecs,omitempty"`
	EligibleJSONModeSpecs []llm.ProviderSpec `json:"eligibleJSONModeSpecs,omitempty"`
	EligibleToolCallSpecs []llm.ProviderSpec `json:"eligibleToolCallSpecs,omitempty"`
}

// aiResponseCachePolicy documents the in-memory response caching behavior
// provided by CachingProvider. Only Complete responses are cached; Stream
// and Embed calls pass through without caching.
type aiResponseCachePolicy struct {
	DefaultTTL         string   `json:"defaultTTL"`
	DefaultMaxSize     int      `json:"defaultMaxSize"`
	CacheKeyComponents []string `json:"cacheKeyComponents"`
	Hash               string   `json:"hash"`
	Coalescing         string   `json:"coalescing"`
	Observable         bool     `json:"observable"`
	CacheScope         string   `json:"cacheScope"`
	CacheUnsupported   []string `json:"cacheUnsupported"`
}

type aiObservabilityPolicy struct {
	Signals                []string `json:"signals"`
	LowCardinalityFields   []string `json:"lowCardinalityFields"`
	ForbiddenFields        []string `json:"forbiddenFields"`
	CorrelationFields      []string `json:"correlationFields"`
	MetricFieldGuidance    string   `json:"metricFieldGuidance"`
	TraceFieldGuidance     string   `json:"traceFieldGuidance"`
	AuditCorrelation       string   `json:"auditCorrelation"`
	RedactionBoundary      string   `json:"redactionBoundary"`
	CardinalityGuardrails  string   `json:"cardinalityGuardrails"`
	ProviderStatusGuidance string   `json:"providerStatusGuidance"`
}

type aiCostPolicy struct {
	AccountingFields       []string `json:"accountingFields"`
	BudgetFields           []string `json:"budgetFields"`
	CurrencyMode           string   `json:"currencyMode"`
	PricingSource          string   `json:"pricingSource"`
	CostDisclosure         string   `json:"costDisclosure"`
	FailoverDisclosure     string   `json:"failoverDisclosure"`
	CacheAccounting        string   `json:"cacheAccounting"`
	AgentGuidance          []string `json:"agentGuidance"`
	UnpricedProviderPolicy string   `json:"unpricedProviderPolicy"`
}

// aiPipelineStage documents each stage in the governed LLM call pipeline.
// Stages execute in order; optional stages may be elided at runtime.
type aiPipelineStage struct {
	Stage       string `json:"stage"`
	Description string `json:"description"`
	Optional    bool   `json:"optional"`
}

type aiToolCommand struct {
	Name              string            `json:"name"`
	Aliases           []string          `json:"aliases,omitempty"`
	Description       string            `json:"description"`
	Usage             string            `json:"usage"`
	InputSchema       aiInputSchema     `json:"inputSchema"`
	OutputContract    *aiOutputContract `json:"outputContract,omitempty"`
	OutputFormats     []string          `json:"outputFormats"`
	SideEffects       []string          `json:"sideEffects"`
	RiskLevel         string            `json:"riskLevel"`
	SupportsDryRun    bool              `json:"supportsDryRun"`
	MutatesFilesystem bool              `json:"mutatesFilesystem"`
	Examples          []string          `json:"examples,omitempty"`
}

type aiOutputContract struct {
	Mode        string            `json:"mode"`
	Envelope    []string          `json:"envelope"`
	EventFields []string          `json:"eventFields,omitempty"`
	Semantics   map[string]string `json:"semantics,omitempty"`
}

type aiInputSchema struct {
	Type                 string                     `json:"type"`
	Properties           map[string]aiInputProperty `json:"properties,omitempty"`
	Required             []string                   `json:"required,omitempty"`
	AdditionalProperties bool                       `json:"additionalProperties"`
}

type aiInputProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

type aiCompleteResult struct {
	Provider   string               `json:"provider"`
	Model      string               `json:"model,omitempty"`
	Text       string               `json:"text,omitempty"`
	Usage      llm.Usage            `json:"usage"`
	Budget     llm.BudgetSnapshot   `json:"budget"`
	Governance aiCompleteGovernance `json:"governance"`
	Warnings   []string             `json:"warnings,omitempty"`
	Metadata   map[string]string    `json:"metadata,omitempty"`
}

type aiStreamEventResult struct {
	Provider   string               `json:"provider"`
	Model      string               `json:"model,omitempty"`
	Index      int                  `json:"index"`
	Delta      string               `json:"delta,omitempty"`
	Done       bool                 `json:"done,omitempty"`
	Usage      llm.Usage            `json:"usage,omitempty"`
	Budget     llm.BudgetSnapshot   `json:"budget,omitempty"`
	Governance aiCompleteGovernance `json:"governance"`
}

type aiCompleteGovernance struct {
	ProviderMode         string   `json:"providerMode"`
	ProviderCapabilities []string `json:"providerCapabilities,omitempty"`
	TelemetryFields      []string `json:"telemetryFields,omitempty"`
	FailoverProviders    []string `json:"failoverProviders,omitempty"`
	FailoverMode         string   `json:"failoverMode,omitempty"`
	FailoverAllowed      bool     `json:"failoverAllowed,omitempty"`
	FailoverUsed         bool     `json:"failoverUsed,omitempty"`
	FailoverFrom         string   `json:"failoverFrom,omitempty"`
	IdempotencyKeySet    bool     `json:"idempotencyKeySet,omitempty"`
	NetworkAccess        bool     `json:"networkAccess"`
	RequiresSecrets      bool     `json:"requiresSecrets"`
	SecretSource         string   `json:"secretSource"`
	Redacted             bool     `json:"redacted"`
	BudgetEnforced       bool     `json:"budgetEnforced"`
	RateLimited          bool     `json:"rateLimited"`
	AuditLogged          bool     `json:"auditLogged"`
}

type aiCompleteConfig struct {
	Provider           string
	Model              string
	FailoverProviders  []string
	AllowFailover      bool
	MaxInputTokens     int
	MaxOutputTokens    int
	MaxTotalTokens     int
	RateLimitPerSecond int
	RateLimitBurst     int
	Timeout            time.Duration
	ConfigPath         string
}

type aiControlPlaneSnapshotResult struct {
	Source         string                              `json:"source"`
	Snapshot       controlplane.Snapshot               `json:"snapshot"`
	Diff           controlplane.SnapshotDiff           `json:"diff,omitempty"`
	ConsumerAction controlplane.SnapshotConsumerAction `json:"consumerAction"`
	AgentGuidance  []string                            `json:"agentGuidance"`
	SecretBoundary string                              `json:"secretBoundary"`
}

type aiControlPlaneWatchEventResult struct {
	Index          int                                 `json:"index"`
	Source         string                              `json:"source,omitempty"`
	Snapshot       controlplane.Snapshot               `json:"snapshot,omitempty"`
	Diff           controlplane.SnapshotDiff           `json:"diff,omitempty"`
	ConsumerAction controlplane.SnapshotConsumerAction `json:"consumerAction"`
	Error          string                              `json:"error,omitempty"`
	SecretBoundary string                              `json:"secretBoundary,omitempty"`
}

type aiControlPlaneBaseline struct {
	Checksum    string
	Snapshot    controlplane.Snapshot
	HasSnapshot bool
}

type httpControlPlaneProvider struct {
	URL           string
	Token         string
	Client        *http.Client
	WatchInterval time.Duration
}
