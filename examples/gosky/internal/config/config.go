package config

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/security"
	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
)

type Config struct {
	Environment   string              `json:"environment"`
	Service       app.ServiceConf     `json:"service"`
	Scaffold      ScaffoldConfig      `json:"scaffold,omitempty"`
	Discovery     DiscoveryConfig     `json:"discovery,omitempty"`
	OpenAPI       OpenAPIConfig       `json:"openapi,omitempty"`
	Authorization AuthorizationConfig `json:"authorization,omitempty"`
	ProjectStore  ProjectStoreConfig  `json:"projectStore,omitempty"`
	Rest          rest.Config         `json:"rest"`
	Admin         AdminConfig         `json:"admin"`
	RPC           RPCConfig           `json:"rpc"`
	MQ            MQConfig            `json:"mq"`
	Governance    governance.Config   `json:"governance"`
}

type ScaffoldConfig struct {
	Features []string `json:"features,omitempty"`
}

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

type OpenAPIConfig struct {
	Enabled     *bool  `json:"enabled,omitempty"`
	Title       string `json:"title,omitempty"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

// AuthorizationConfig only identifies the environment variable containing the
// JWT signing key. The key itself is never stored in a config file.
type AuthorizationConfig struct {
	Enabled      bool   `json:"enabled"`
	JWTSecretEnv string `json:"jwtSecretEnv,omitempty"`
}

// ProjectStoreConfig configures gosky's MySQL-backed Project authorization
// data. The DSN is read only from an environment variable and is never placed
// into a config file or control-plane snapshot.
type ProjectStoreConfig struct {
	Enabled       bool               `json:"enabled"`
	Driver        string             `json:"driver,omitempty"`
	DSNEnv        string             `json:"dsnEnv,omitempty"`
	Pool          storage.PoolConfig `json:"pool,omitempty"`
	Ping          time.Duration      `json:"ping,omitempty"`
	QueryTimeout  time.Duration      `json:"queryTimeout,omitempty"`
	SlowThreshold time.Duration      `json:"slowThreshold,omitempty"`
}

func ValidateProjectStoreConfig(c ProjectStoreConfig) error {
	if !c.Enabled {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(c.Driver), "mysql") {
		return errors.New("projectStore driver must be mysql when enabled")
	}
	if strings.TrimSpace(c.DSNEnv) == "" {
		return errors.New("projectStore dsnEnv is required when enabled")
	}
	if strings.TrimSpace(os.Getenv(c.DSNEnv)) == "" {
		return fmt.Errorf("projectStore DSN environment variable %q is required when enabled", c.DSNEnv)
	}
	return nil
}

// StorageConfig resolves the Project store's environment-owned DSN after
// validating the approved MySQL-only configuration.
func (c ProjectStoreConfig) StorageConfig() (storage.Config, error) {
	if !c.Enabled {
		return storage.Config{}, nil
	}
	if err := ValidateProjectStoreConfig(c); err != nil {
		return storage.Config{}, err
	}
	return storage.Config{
		Driver:        "mysql",
		DSN:           strings.TrimSpace(os.Getenv(c.DSNEnv)),
		Pool:          c.Pool,
		Ping:          c.Ping,
		QueryTimeout:  c.QueryTimeout,
		SlowThreshold: c.SlowThreshold,
	}, nil
}

func (c AuthorizationConfig) SecretEnv() string {
	if name := strings.TrimSpace(c.JWTSecretEnv); name != "" {
		return name
	}
	return "GOSKY_JWT_SECRET"
}

func ValidateAuthorizationConfig(c AuthorizationConfig) error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.SecretEnv()) == "" {
		return errors.New("authorization jwtSecretEnv is required")
	}
	return nil
}

type RPCConfig struct {
	Addr      string       `json:"addr"`
	Advertise string       `json:"advertise"`
	Mux       RPCMuxConfig `json:"mux,omitempty"`
}

type RPCMuxConfig struct {
	Enabled                 bool                  `json:"enabled"`
	Probe                   bool                  `json:"probe"`
	Addr                    string                `json:"addr"`
	Endpoints               []string              `json:"endpoints,omitempty"`
	IdleTimeout             time.Duration         `json:"idleTimeout"`
	MaxOpenRetries          int                   `json:"maxOpenRetries,omitempty"`
	OpenRetryReasons        []string              `json:"openRetryReasons,omitempty"`
	HealthBackoffMultiplier int                   `json:"healthBackoffMultiplier,omitempty"`
	HealthMaxCooldown       time.Duration         `json:"healthMaxCooldown,omitempty"`
	Trace                   RPCMuxTraceConfig     `json:"trace,omitempty"`
	Log                     RPCMuxLogConfig       `json:"log,omitempty"`
	TLS                     RPCMuxTLSConfig       `json:"tls,omitempty"`
	MutualTLS               RPCMuxMutualTLSConfig `json:"mtls,omitempty"`
	ALPN                    RPCMuxALPNConfig      `json:"alpn,omitempty"`
	Candidate               RPCMuxCandidateConfig `json:"candidate,omitempty"`
}

type RPCMuxTraceConfig struct {
	Enabled         bool `json:"enabled"`
	AnnotateStreams bool `json:"annotateStreams"`
}

type RPCMuxLogConfig struct {
	Enabled        bool                          `json:"enabled"`
	Diagnosis      bool                          `json:"diagnosis"`
	ExportEvents   bool                          `json:"exportEvents"`
	EventFamily    string                        `json:"eventFamily,omitempty"`
	Event          string                        `json:"event,omitempty"`
	Endpoint       string                        `json:"endpoint,omitempty"`
	ConnectionID   string                        `json:"connectionId,omitempty"`
	PoolSlot       int                           `json:"poolSlot,omitempty"`
	OTelCompatible RPCMuxOTelCompatibleLogConfig `json:"otelCompatible,omitempty"`
}

func (c RPCMuxLogConfig) EventExportEnabled() bool {
	return c.Enabled && c.ExportEvents
}

func (c RPCMuxLogConfig) Filter() rpc.RPCMuxDiagnosisFilter {
	return rpc.RPCMuxDiagnosisFilter{
		Endpoint:     c.Endpoint,
		ConnectionID: c.ConnectionID,
		PoolSlot:     c.PoolSlot,
		EventFamily:  c.EventFamily,
		Event:        c.Event,
	}
}

type RPCMuxOTelCompatibleLogConfig struct {
	Enabled          bool                         `json:"enabled"`
	Version          string                       `json:"version,omitempty"`
	SchemaVersion    string                       `json:"schemaVersion,omitempty"`
	Sink             string                       `json:"sink,omitempty"`
	Profile          string                       `json:"profile,omitempty"`
	ProfileRef       string                       `json:"profileRef,omitempty"`
	FileSecretRoot   string                       `json:"fileSecretRoot,omitempty"`
	OperatorHistory  RPCMuxOperatorHistoryConfig  `json:"operatorHistory,omitempty"`
	ProfileSchema    string                       `json:"profileSchema,omitempty"`
	ProfileMigration string                       `json:"profileMigration,omitempty"`
	Delivery         RPCMuxExporterDeliveryConfig `json:"delivery,omitempty"`
	Sinks            []RPCMuxOTelSinkConfig       `json:"sinks,omitempty"`
}

type RPCMuxOperatorHistoryConfig struct {
	Enabled               bool          `json:"enabled,omitempty"`
	Store                 string        `json:"store,omitempty"`
	MaxActions            int           `json:"maxActions,omitempty"`
	MaxSizeBytes          int64         `json:"maxSizeBytes,omitempty"`
	MaxLineBytes          int64         `json:"maxLineBytes,omitempty"`
	MaxBackups            int           `json:"maxBackups,omitempty"`
	DebugReplayCooldown   time.Duration `json:"debugReplayCooldown,omitempty"`
	AuditValidateCooldown time.Duration `json:"auditValidateCooldown,omitempty"`
}

const RPCMuxConfigWarningSchema = "gofly.rpc_mux_config_warning.v1"

const RPCMuxConfigWarningJSONSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"required":["schema","field","message","current","recommended"],"properties":{"schema":{"type":"string","const":"gofly.rpc_mux_config_warning.v1"},"field":{"type":"string","enum":["maxActions","maxBackups","maxSizeBytes","maxLineBytes"]},"message":{"type":"string"},"current":{"type":"number"},"recommended":{"type":"number"}}}`

const AIManifestSchemaID = "https://gofly.dev/schemas/ai-tool-manifest.schema.json"

type RPCMuxOTelSinkConfig struct {
	Name             string                       `json:"name"`
	Profile          string                       `json:"profile,omitempty"`
	ProfileRef       string                       `json:"profileRef,omitempty"`
	ProfileSchema    string                       `json:"profileSchema,omitempty"`
	ProfileMigration string                       `json:"profileMigration,omitempty"`
	Priority         int                          `json:"priority,omitempty"`
	Delivery         RPCMuxExporterDeliveryConfig `json:"delivery,omitempty"`
}

type RPCMuxExporterDeliveryConfig struct {
	QueueSize               int                             `json:"queueSize,omitempty"`
	Timeout                 time.Duration                   `json:"timeout,omitempty"`
	MaxHungCalls            int                             `json:"maxHungCalls,omitempty"`
	BreakerFailureThreshold int                             `json:"breakerFailureThreshold,omitempty"`
	BreakerCooldown         time.Duration                   `json:"breakerCooldown,omitempty"`
	ErrorBudget             RPCMuxExporterErrorBudgetConfig `json:"errorBudget,omitempty"`
	Isolation               RPCMuxSinkIsolationConfig       `json:"isolation,omitempty"`
}

type RPCMuxExporterErrorBudgetConfig struct {
	Enabled                   bool          `json:"enabled,omitempty"`
	MinSamples                int64         `json:"minSamples,omitempty"`
	BurnRateThreshold         float64       `json:"burnRateThreshold,omitempty"`
	RecoveryBurnRateThreshold float64       `json:"recoveryBurnRateThreshold,omitempty"`
	PauseDuration             time.Duration `json:"pauseDuration,omitempty"`
}

type RPCMuxSinkIsolationConfig struct {
	Mode            string            `json:"mode,omitempty"`
	ShutdownTimeout time.Duration     `json:"shutdownTimeout,omitempty"`
	MaxMemoryBytes  int64             `json:"maxMemoryBytes,omitempty"`
	MaxCPUPercent   int               `json:"maxCpuPercent,omitempty"`
	AuditFields     map[string]string `json:"auditFields,omitempty"`
}

func (c RPCMuxExporterDeliveryConfig) RuntimeConfig() rpc.RPCMuxDiagnosisExporterDeliveryConfig {
	return rpc.RPCMuxDiagnosisExporterDeliveryConfig{
		QueueSize:               c.QueueSize,
		Timeout:                 c.Timeout,
		MaxHungCalls:            c.MaxHungCalls,
		BreakerFailureThreshold: c.BreakerFailureThreshold,
		BreakerCooldown:         c.BreakerCooldown,
		ErrorBudget: rpc.RPCMuxDiagnosisExporterErrorBudgetConfig{
			Enabled:                   c.ErrorBudget.Enabled,
			MinSamples:                c.ErrorBudget.MinSamples,
			BurnRateThreshold:         c.ErrorBudget.BurnRateThreshold,
			RecoveryBurnRateThreshold: c.ErrorBudget.RecoveryBurnRateThreshold,
			PauseDuration:             c.ErrorBudget.PauseDuration,
		},
		Isolation: rpc.RPCMuxDiagnosisSinkIsolationConfig{
			Mode:            c.Isolation.Mode,
			ShutdownTimeout: c.Isolation.ShutdownTimeout,
			MaxMemoryBytes:  c.Isolation.MaxMemoryBytes,
			MaxCPUPercent:   c.Isolation.MaxCPUPercent,
			AuditFields:     c.Isolation.AuditFields,
		},
	}
}

func (c RPCMuxOTelCompatibleLogConfig) SinkSetConfig() rpc.RPCMuxDiagnosisSinkSetConfig {
	version := strings.TrimSpace(c.Version)
	if version == "" {
		version = "legacy"
	}
	secretResolver := c.SecretResolver()
	if !c.Enabled {
		return rpc.RPCMuxDiagnosisSinkSetConfig{Version: version, SchemaVersion: strings.TrimSpace(c.SchemaVersion), Secrets: secretResolver}
	}
	sinks := make([]rpc.RPCMuxDiagnosisSinkConfig, 0, len(c.Sinks)+1)
	if len(c.Sinks) == 0 {
		name := strings.TrimSpace(c.Sink)
		if name == "" {
			name = "slog"
		}
		sinks = append(sinks, rpc.RPCMuxDiagnosisSinkConfig{
			Name:             name,
			Profile:          c.Profile,
			ProfileRef:       c.ProfileRef,
			ProfileSchema:    c.ProfileSchema,
			ProfileMigration: c.ProfileMigration,
			Delivery:         c.Delivery.RuntimeConfig(),
		})
	} else {
		for _, sink := range c.Sinks {
			sinks = append(sinks, rpc.RPCMuxDiagnosisSinkConfig{
				Name:             sink.Name,
				Profile:          sink.Profile,
				ProfileRef:       sink.ProfileRef,
				ProfileSchema:    sink.ProfileSchema,
				ProfileMigration: sink.ProfileMigration,
				Priority:         sink.Priority,
				Delivery:         sink.Delivery.RuntimeConfig(),
			})
		}
	}
	return rpc.RPCMuxDiagnosisSinkSetConfig{Version: version, SchemaVersion: strings.TrimSpace(c.SchemaVersion), Sinks: sinks, Secrets: secretResolver}
}

func (c RPCMuxOTelCompatibleLogConfig) SecretResolver() rpc.RPCMuxDiagnosisSecretResolver {
	resolvers := []rpc.RPCMuxDiagnosisSecretResolver{rpc.NewRPCMuxDiagnosisEnvSecretResolver()}
	if strings.TrimSpace(c.FileSecretRoot) != "" {
		resolvers = append(resolvers, rpc.NewRPCMuxDiagnosisFileSecretResolver(c.FileSecretRoot, 0))
	}
	return rpc.NewRPCMuxDiagnosisLayeredSecretResolver(resolvers...)
}

func (c RPCMuxOTelCompatibleLogConfig) NewSinkSet() (*rpc.RPCMuxDiagnosisSinkSet, error) {
	sinkSet, err := rpc.NewRPCMuxDiagnosisSinkSet(c.SinkSetConfig())
	if err != nil {
		return nil, err
	}
	store, err := c.OperatorHistoryStore()
	if err != nil {
		if closeErr := sinkSet.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close rpc mux diagnosis sink set: %w", closeErr))
		}
		return nil, err
	}
	if store != nil {
		sinkSet.WithOperatorHistoryStore(store)
	}
	return sinkSet, nil
}

func (c RPCMuxOTelCompatibleLogConfig) ReloadSinkSet(ctx context.Context, sinkSet *rpc.RPCMuxDiagnosisSinkSet) error {
	if sinkSet == nil {
		_, err := c.NewSinkSet()
		return err
	}
	store, err := c.OperatorHistoryStore()
	if err != nil {
		return err
	}
	if err := sinkSet.Reload(ctx, c.SinkSetConfig()); err != nil {
		return err
	}
	sinkSet.WithOperatorHistoryStore(store)
	return nil
}

func (c RPCMuxOTelCompatibleLogConfig) DiffSinkSet(ctx context.Context, sinkSet *rpc.RPCMuxDiagnosisSinkSet) (rpc.RPCMuxDiagnosisSinkSetDiffPlan, error) {
	if sinkSet == nil {
		empty, err := rpc.NewRPCMuxDiagnosisSinkSet(rpc.RPCMuxDiagnosisSinkSetConfig{Version: "legacy", SchemaVersion: strings.TrimSpace(c.SchemaVersion)})
		if err != nil {
			return rpc.RPCMuxDiagnosisSinkSetDiffPlan{}, err
		}
		defer func() { _ = empty.Close() }()
		return empty.DiffRPCMuxDiagnosisSinkSetConfig(ctx, c.SinkSetConfig())
	}
	return sinkSet.DiffRPCMuxDiagnosisSinkSetConfig(ctx, c.SinkSetConfig())
}

func (c RPCMuxOTelCompatibleLogConfig) OperatorHistoryStore() (rpc.RPCMuxDiagnosisOperatorHistoryStore, error) {
	if !c.OperatorHistory.Enabled {
		return nil, nil
	}
	store := strings.TrimSpace(c.OperatorHistory.Store)
	if store == "" {
		return nil, fmt.Errorf("rpc mux operator history store is required when enabled")
	}
	const fileScheme = "file://"
	if !strings.HasPrefix(store, fileScheme) {
		return nil, fmt.Errorf("rpc mux operator history store must use file://")
	}
	relPath := strings.TrimSpace(strings.TrimPrefix(store, fileScheme))
	if relPath == "" || !filepath.IsLocal(relPath) {
		return nil, fmt.Errorf("rpc mux operator history file path must stay under fileSecretRoot")
	}
	root := strings.TrimSpace(c.FileSecretRoot)
	if root == "" || !filepath.IsLocal(root) {
		return nil, fmt.Errorf("rpc mux operator history fileSecretRoot must be local")
	}
	return rpc.NewRPCMuxDiagnosisOperatorHistoryFileStoreWithConfig(
		filepath.Join(root, relPath),
		rpc.RPCMuxDiagnosisOperatorHistoryFileStoreConfig{
			MaxActions:   c.OperatorHistory.MaxActions,
			MaxSizeBytes: c.OperatorHistory.MaxSizeBytes,
			MaxLineBytes: c.OperatorHistory.MaxLineBytes,
			MaxBackups:   c.OperatorHistory.MaxBackups,
		},
	)
}

type RPCMuxTLSConfig struct {
	Enabled            bool   `json:"enabled,omitempty"`
	CertFile           string `json:"certFile,omitempty"`
	KeyFile            string `json:"keyFile,omitempty"`
	CAFile             string `json:"caFile,omitempty"`
	ServerName         string `json:"serverName,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
	MinVersion         uint16 `json:"minVersion,omitempty"`
}

type RPCMuxMutualTLSConfig struct {
	Enabled        bool   `json:"enabled,omitempty"`
	ClientCAFile   string `json:"clientCAFile,omitempty"`
	ClientCertFile string `json:"clientCertFile,omitempty"`
	ClientKeyFile  string `json:"clientKeyFile,omitempty"`
}

type RPCMuxALPNConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

type RPCMuxCandidateConfig struct {
	Enabled                              bool               `json:"enabled"`
	Protocol                             string             `json:"protocol,omitempty"`
	TLS                                  security.TLSConfig `json:"tls,omitempty"`
	DialTimeout                          time.Duration      `json:"dialTimeout,omitempty"`
	KeepAlive                            time.Duration      `json:"keepAlive,omitempty"`
	HandshakeTimeout                     time.Duration      `json:"handshakeTimeout,omitempty"`
	KeepaliveInterval                    time.Duration      `json:"keepaliveInterval,omitempty"`
	KeepaliveIdle                        time.Duration      `json:"keepaliveIdle,omitempty"`
	WriteTimeout                         time.Duration      `json:"writeTimeout,omitempty"`
	CreditWaitTimeout                    time.Duration      `json:"creditWaitTimeout,omitempty"`
	MaxFrameBytes                        int64              `json:"maxFrameBytes,omitempty"`
	MaxMessageBytes                      int64              `json:"maxMessageBytes,omitempty"`
	MaxConcurrentStreams                 int                `json:"maxConcurrentStreams,omitempty"`
	ReceiveQueueSize                     int                `json:"receiveQueueSize,omitempty"`
	ConnectionWindow                     int                `json:"connectionWindow,omitempty"`
	FragmentStreamWindowUpdatePolicy     string             `json:"fragmentStreamWindowUpdatePolicy,omitempty"`
	FragmentConnectionWindowUpdatePolicy string             `json:"fragmentConnectionWindowUpdatePolicy,omitempty"`
	FragmentStreamWindowRefillRatio      float64            `json:"fragmentStreamWindowRefillRatio,omitempty"`
	FragmentConnectionWindowRefillRatio  float64            `json:"fragmentConnectionWindowRefillRatio,omitempty"`
	FragmentMaxDeferredFragments         int                `json:"fragmentMaxDeferredFragments,omitempty"`
	FragmentWindowPolicyRiskMode         string             `json:"fragmentWindowPolicyRiskMode,omitempty"`
	PayloadCodec                         string             `json:"payloadCodec,omitempty"`
	FrameCodec                           string             `json:"frameCodec,omitempty"`
	DrainGrace                           time.Duration      `json:"drainGrace,omitempty"`
	AllowLegacyDowngrade                 bool               `json:"allowLegacyDowngrade,omitempty"`
}

type ResilienceProfile struct {
	Timeout        bool `json:"timeout"`
	RateLimit      bool `json:"rateLimit"`
	Concurrency    bool `json:"concurrency"`
	Breaker        bool `json:"breaker"`
	Retry          bool `json:"retry"`
	AdaptiveLimit  bool `json:"adaptiveLimit"`
	RESTEnabled    bool `json:"restEnabled"`
	RPCEnabled     bool `json:"rpcEnabled"`
	GatewayEnabled bool `json:"gatewayEnabled"`
}

type AdminConfig struct {
	Enabled    bool   `json:"enabled"`
	Addr       string `json:"addr"`
	PathPrefix string `json:"pathPrefix"`
}

type MQConfig struct {
	Enabled     bool                `json:"enabled"`
	Driver      string              `json:"driver"`
	Service     string              `json:"service"`
	Trace       bool                `json:"trace"`
	Log         bool                `json:"log"`
	Timeout     time.Duration       `json:"timeout"`
	Tags        map[string]string   `json:"tags"`
	Kafka       MQKafkaConfig       `json:"kafka"`
	RabbitMQ    MQRabbitMQConfig    `json:"rabbitmq"`
	RedisStream MQRedisStreamConfig `json:"redisstream"`
}

type MQKafkaConfig struct {
	Brokers      []string      `json:"brokers"`
	WriteTimeout time.Duration `json:"writeTimeout"`
	ReadTimeout  time.Duration `json:"readTimeout"`
	MinBytes     int           `json:"minBytes"`
	MaxBytes     int           `json:"maxBytes"`
}

type MQRabbitMQConfig struct {
	URL            string `json:"url"`
	ExchangePrefix string `json:"exchangePrefix"`
	Prefetch       int    `json:"prefetch"`
}

type MQRedisStreamConfig struct {
	Redis         RedisConfig   `json:"redis"`
	MaxLen        int64         `json:"maxLen"`
	Consumer      string        `json:"consumer"`
	BlockInterval time.Duration `json:"blockInterval"`
	ReadCount     int           `json:"readCount"`
}

func ConfigPaths(name string) []string {
	name = strings.TrimSpace(name)
	paths := []string{"config.yaml", "config.yml", "config.toml", "config.json"}
	if name != "" {
		paths = append(paths,
			filepath.Join("etc", name+".yaml"),
			filepath.Join("etc", name+".yml"),
			filepath.Join("etc", name+".toml"),
			filepath.Join("etc", name+".json"),
		)
	}
	return paths
}

func ResolveConfigPath(name string) string {
	for _, path := range ConfigPaths(name) {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if strings.TrimSpace(name) == "" {
		return "config.json"
	}
	return filepath.Join("etc", strings.TrimSpace(name)+".json")
}

type RedisConfig struct {
	Addr                    string             `json:"addr"`
	Addrs                   []string           `json:"addrs,omitempty"`
	Cluster                 bool               `json:"cluster,omitempty"`
	MasterName              string             `json:"masterName,omitempty"`
	SentinelUsername        string             `json:"sentinelUsername,omitempty"`
	SentinelPassword        string             `json:"sentinelPassword,omitempty"`
	ReadOnly                bool               `json:"readOnly,omitempty"`
	RouteByLatency          bool               `json:"routeByLatency,omitempty"`
	RouteRandomly           bool               `json:"routeRandomly,omitempty"`
	Username                string             `json:"username,omitempty"`
	Password                string             `json:"password"`
	TLS                     security.TLSConfig `json:"tls,omitempty"`
	Protocol                int                `json:"protocol,omitempty"`
	EnableIdentity          bool               `json:"enableIdentity,omitempty"`
	MaintNotifications      string             `json:"maintNotifications,omitempty"`
	EagerConnect            bool               `json:"eagerConnect,omitempty"`
	PingTimeout             time.Duration      `json:"pingTimeout,omitempty"`
	DB                      int                `json:"db"`
	DialTimeout             time.Duration      `json:"dialTimeout"`
	Timeout                 time.Duration      `json:"timeout"`
	MaxConns                int                `json:"maxConns"`
	MaxIdleConns            int                `json:"maxIdleConns"`
	ConnMaxIdleTime         time.Duration      `json:"connMaxIdleTime"`
	ConnMaxLifetime         time.Duration      `json:"connMaxLifetime"`
	MinIdleConns            int                `json:"minIdleConns,omitempty"`
	PoolTimeout             time.Duration      `json:"poolTimeout,omitempty"`
	MaxRetries              int                `json:"maxRetries,omitempty"`
	SlowThreshold           time.Duration      `json:"slowThreshold,omitempty"`
	DisableBreaker          bool               `json:"disableBreaker,omitempty"`
	BreakerAdaptive         bool               `json:"breakerAdaptive,omitempty"`
	BreakerFailureThreshold int                `json:"breakerFailureThreshold,omitempty"`
	BreakerOpenTimeout      time.Duration      `json:"breakerOpenTimeout,omitempty"`
}

func (c Config) ServiceConf() app.ServiceConf {
	service := c.Service
	if service.Name == "" {
		service.Name = c.Rest.Name
	}
	if service.Environment == "" {
		service.Environment = c.Environment
	}
	return service.WithDefaults(c.Rest.Name)
}

func (c RPCMuxConfig) CandidateConfig() rpc.ExperimentalMuxCandidateConfig {
	return c.CandidateClientConfig()
}

func (c RPCMuxConfig) CandidateServerConfig() rpc.ExperimentalMuxCandidateConfig {
	return c.candidateConfigWithTLS(c.serverTLSConfig())
}

func (c RPCMuxConfig) CandidateClientConfig() rpc.ExperimentalMuxCandidateConfig {
	return c.candidateConfigWithTLS(c.clientTLSConfig())
}

func (c RPCMuxConfig) candidateConfigWithTLS(tlsConfig security.TLSConfig) rpc.ExperimentalMuxCandidateConfig {
	candidate := c.Candidate
	protocol := candidate.Protocol
	if c.ALPN.Enabled && strings.TrimSpace(c.ALPN.Protocol) != "" {
		protocol = strings.TrimSpace(c.ALPN.Protocol)
	}
	return rpc.ExperimentalMuxCandidateConfig{
		Protocol:                             protocol,
		TLS:                                  tlsConfig,
		DialTimeout:                          candidate.DialTimeout,
		KeepAlive:                            candidate.KeepAlive,
		HandshakeTimeout:                     candidate.HandshakeTimeout,
		KeepaliveInterval:                    candidate.KeepaliveInterval,
		KeepaliveIdle:                        candidate.KeepaliveIdle,
		WriteTimeout:                         candidate.WriteTimeout,
		CreditWaitTimeout:                    candidate.CreditWaitTimeout,
		MaxFrameBytes:                        candidate.MaxFrameBytes,
		MaxMessageBytes:                      candidate.MaxMessageBytes,
		MaxConcurrentStreams:                 candidate.MaxConcurrentStreams,
		ReceiveQueueSize:                     candidate.ReceiveQueueSize,
		ConnectionWindow:                     candidate.ConnectionWindow,
		FragmentStreamWindowUpdatePolicy:     candidate.FragmentStreamWindowUpdatePolicy,
		FragmentConnectionWindowUpdatePolicy: candidate.FragmentConnectionWindowUpdatePolicy,
		FragmentStreamWindowRefillRatio:      candidate.FragmentStreamWindowRefillRatio,
		FragmentConnectionWindowRefillRatio:  candidate.FragmentConnectionWindowRefillRatio,
		FragmentMaxDeferredFragments:         candidate.FragmentMaxDeferredFragments,
		FragmentWindowPolicyRiskMode:         candidate.FragmentWindowPolicyRiskMode,
		PayloadCodec:                         candidate.PayloadCodec,
		FrameCodec:                           candidate.FrameCodec,
		DrainGrace:                           candidate.DrainGrace,
		AllowLegacyDowngrade:                 candidate.AllowLegacyDowngrade,
	}
}

func ValidateRPCMuxConfig(c RPCMuxConfig) error {
	warnings, err := ValidateRPCMuxConfigWithWarnings(c)
	logRPCMuxConfigWarnings(warnings)
	return err
}

func ValidateRPCMuxConfigWithWarnings(c RPCMuxConfig) ([]string, error) {
	limits := rpc.RPCMuxDiagnosisOperatorHistoryLimits()
	if cooldown := c.Log.OTelCompatible.OperatorHistory.DebugReplayCooldown; cooldown > 0 && (cooldown < limits.MinDebugReplayCooldown || cooldown > limits.MaxDebugReplayCooldown) {
		return nil, fmt.Errorf("rpc mux operator history debugReplayCooldown must be between %s and %s", limits.MinDebugReplayCooldown, limits.MaxDebugReplayCooldown)
	}
	if cooldown := c.Log.OTelCompatible.OperatorHistory.AuditValidateCooldown; cooldown > 0 && (cooldown < limits.MinAuditValidateCooldown || cooldown > limits.MaxAuditValidateCooldown) {
		return nil, fmt.Errorf("rpc mux operator history auditValidateCooldown must be between %s and %s", limits.MinAuditValidateCooldown, limits.MaxAuditValidateCooldown)
	}
	if c.Log.OTelCompatible.Enabled {
		if err := rpc.ValidateRPCMuxDiagnosisSinkSetConfig(c.Log.OTelCompatible.SinkSetConfig()); err != nil {
			return nil, fmt.Errorf("rpc mux otelCompatible sinks: %w", err)
		}
	}
	warnings := []string(nil)
	if c.Log.OTelCompatible.OperatorHistory.Enabled {
		if _, err := c.Log.OTelCompatible.OperatorHistoryStore(); err != nil {
			return nil, fmt.Errorf("rpc mux operator history: %w", err)
		}
		warnings = rpcMuxOperatorHistoryRecommendedLimitWarnings(c.Log.OTelCompatible.OperatorHistory)
	}
	if !c.Candidate.Enabled {
		return warnings, nil
	}
	return warnings, errors.Join(
		c.CandidateServerConfig().Validate(),
		c.CandidateClientConfig().Validate(),
	)
}

// rpcMuxOperatorHistoryRecommendedLimitWarnings returns non-fatal warnings when the
// operator history configuration exceeds the recommended production bounds. The
// hard limits are still enforced by the store construction; this only surfaces
// values that are valid but larger than recommended so operators can review
// them before rollout.
func rpcMuxOperatorHistoryRecommendedLimitWarnings(history RPCMuxOperatorHistoryConfig) []string {
	recommended := rpc.RPCMuxDiagnosisOperatorHistoryRecommendedLimits()
	warnings := []string(nil)
	if history.MaxActions > recommended.MaxActions {
		warnings = append(warnings, rpcMuxOperatorHistoryRecommendedLimitWarning("maxActions", history.MaxActions, recommended.MaxActions))
	}
	if history.MaxBackups > recommended.MaxBackups {
		warnings = append(warnings, rpcMuxOperatorHistoryRecommendedLimitWarning("maxBackups", history.MaxBackups, recommended.MaxBackups))
	}
	if history.MaxSizeBytes > recommended.MaxSizeBytes {
		warnings = append(warnings, rpcMuxOperatorHistoryRecommendedLimitWarning("maxSizeBytes", history.MaxSizeBytes, recommended.MaxSizeBytes))
	}
	if history.MaxLineBytes > recommended.MaxLineBytes {
		warnings = append(warnings, rpcMuxOperatorHistoryRecommendedLimitWarning("maxLineBytes", history.MaxLineBytes, recommended.MaxLineBytes))
	}
	return warnings
}

func rpcMuxOperatorHistoryRecommendedLimitWarning(field string, current any, recommended any) string {
	data, err := json.Marshal(map[string]any{
		"schema":      RPCMuxConfigWarningSchema,
		"field":       field,
		"message":     fmt.Sprintf("rpc mux operator history %s exceeds recommended", field),
		"current":     current,
		"recommended": recommended,
	})
	if err != nil {
		return fmt.Sprintf("rpc mux operator history %s exceeds recommended: current=%v recommended=%v", field, current, recommended)
	}
	return string(data)
}

func logRPCMuxConfigWarnings(warnings []string) {
	for _, warning := range warnings {
		slog.Warn(warning)
	}
}

func generatedControlPlaneSchemaChecksums() map[string]string {
	return map[string]string{
		"generated.rpcMuxConfigWarningSchema":  checksumGeneratedControlPlaneSchema(RPCMuxConfigWarningJSONSchema),
		"generated.rpcMuxOperatorAuditSchemas": checksumGeneratedControlPlaneSchema(rpc.RPCMuxDiagnosisOperatorAuditSchemas()),
		"aiManifestSchema":                     checksumGeneratedControlPlaneSchema(AIManifestSchemaID),
	}
}

func checksumGeneratedControlPlaneSchema(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func (c RPCMuxConfig) CandidateTLSConfig() security.TLSConfig {
	return c.clientTLSConfig()
}

func (c RPCMuxConfig) serverTLSConfig() security.TLSConfig {
	tlsConfig := c.Candidate.TLS
	if c.TLS.Enabled {
		if tlsConfig.CertFile == "" {
			tlsConfig.CertFile = c.TLS.CertFile
		}
		if tlsConfig.KeyFile == "" {
			tlsConfig.KeyFile = c.TLS.KeyFile
		}
		if tlsConfig.CAFile == "" {
			tlsConfig.CAFile = c.TLS.CAFile
		}
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = c.TLS.ServerName
		}
		if !tlsConfig.InsecureSkipVerify {
			tlsConfig.InsecureSkipVerify = c.TLS.InsecureSkipVerify
		}
		if tlsConfig.MinVersion == 0 {
			tlsConfig.MinVersion = c.TLS.MinVersion
		}
	}
	if c.MutualTLS.Enabled {
		if tlsConfig.ClientCAFile == "" {
			tlsConfig.ClientCAFile = c.MutualTLS.ClientCAFile
		}
	}
	return tlsConfig
}

func (c RPCMuxConfig) clientTLSConfig() security.TLSConfig {
	tlsConfig := c.Candidate.TLS
	if c.TLS.Enabled {
		if tlsConfig.CAFile == "" {
			tlsConfig.CAFile = c.TLS.CAFile
		}
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = c.TLS.ServerName
		}
		if !tlsConfig.InsecureSkipVerify {
			tlsConfig.InsecureSkipVerify = c.TLS.InsecureSkipVerify
		}
		if tlsConfig.MinVersion == 0 {
			tlsConfig.MinVersion = c.TLS.MinVersion
		}
	}
	if c.MutualTLS.Enabled {
		if tlsConfig.CertFile == "" {
			tlsConfig.CertFile = c.MutualTLS.ClientCertFile
		}
		if tlsConfig.KeyFile == "" {
			tlsConfig.KeyFile = c.MutualTLS.ClientKeyFile
		}
	}
	return tlsConfig
}

func (c RPCMuxConfig) ClientOptions() []rpc.ClientOption {
	return c.ClientOptionsWithSinkSet(nil)
}

func (c RPCMuxConfig) ClientOptionsWithSinkSet(sinkSet *rpc.RPCMuxDiagnosisSinkSet) []rpc.ClientOption {
	options := make([]rpc.ClientOption, 0, 2)
	if c.Trace.Enabled && c.Trace.AnnotateStreams {
		options = append(options, rpc.WithMuxTraceAnnotation())
	}
	if c.Log.Enabled && c.Log.Diagnosis {
		options = append(options, rpc.WithMuxDiagnosisLogging(nil))
	}
	if c.Log.EventExportEnabled() {
		filter := c.Log.Filter()
		if c.Log.OTelCompatible.Enabled {
			if sinkSet == nil {
				var err error
				sinkSet, err = c.Log.OTelCompatible.NewSinkSet()
				if err != nil {
					return options
				}
			}
			options = append(options, rpc.WithMuxDiagnosisEventExporter(sinkSet, filter))
		} else {
			options = append(options, rpc.WithMuxDiagnosisEventLogging(nil, filter))
		}
	}
	return options
}

func (c RPCMuxConfig) ServerOptions() []rpc.ServerOption {
	return c.ServerOptionsWithSinkSet(nil)
}

func (c RPCMuxConfig) ServerOptionsWithSinkSet(sinkSet *rpc.RPCMuxDiagnosisSinkSet) []rpc.ServerOption {
	if !c.Log.EventExportEnabled() {
		return nil
	}
	filter := c.Log.Filter()
	if c.Log.OTelCompatible.Enabled {
		if sinkSet == nil {
			var err error
			sinkSet, err = c.Log.OTelCompatible.NewSinkSet()
			if err != nil {
				return nil
			}
		}
		options := []rpc.ServerOption{rpc.WithServerMuxDiagnosisEventExporter(sinkSet, filter)}
		if c.Log.OTelCompatible.OperatorHistory.DebugReplayCooldown > 0 {
			options = append(options, rpc.WithServerMuxDiagnosisDebugReplayCooldown(c.Log.OTelCompatible.OperatorHistory.DebugReplayCooldown))
		}
		if c.Log.OTelCompatible.OperatorHistory.AuditValidateCooldown > 0 {
			options = append(options, rpc.WithServerMuxDiagnosisAuditValidateCooldown(c.Log.OTelCompatible.OperatorHistory.AuditValidateCooldown))
		}
		return options
	}
	return []rpc.ServerOption{rpc.WithServerMuxDiagnosisEventLogging(nil, filter)}
}

func (c Config) ResilienceProfile() ResilienceProfile {
	service := c.ServiceConf()
	serviceGovernance := service.Governance
	profile := ResilienceProfile{
		Timeout:       serviceGovernance.Timeout > 0,
		RateLimit:     serviceGovernance.RateLimit.Rate > 0 && serviceGovernance.RateLimit.Burst > 0,
		Concurrency:   serviceGovernance.MaxConcurrency > 0,
		Breaker:       serviceGovernance.Breaker,
		Retry:         serviceGovernance.Retry.Attempts > 0,
		AdaptiveLimit: serviceGovernance.AdaptiveLimit,
		RESTEnabled:   c.Rest.Middlewares.Timeout && c.Rest.Middlewares.RateLimit && c.Rest.Middlewares.MaxConcurrency && c.Rest.Middlewares.Breaker,
		RPCEnabled:    len(service.RPCServerOptions()) > 0 && len(service.RPCClientOptions()) > 0,
	}
	for _, rule := range c.Governance.Rules {
		if rule.Transport == governance.TransportGateway && rule.Policy.Retry.Attempts > 0 && rule.Policy.RateLimit.Rate > 0 && rule.Policy.Concurrency.Limit > 0 && rule.Policy.Breaker.Enabled {
			profile.GatewayEnabled = true
			break
		}
	}
	return profile
}

type ControlPlaneContributor struct {
	Config Config
}

func (c Config) ControlPlaneContributor() ControlPlaneContributor {
	return ControlPlaneContributor{Config: c}
}

func (c Config) ControlPlaneSnapshot(ctx context.Context) (controlplane.Snapshot, error) {
	return c.ControlPlaneSnapshotWithDiscovery(ctx, nil)
}

func (c Config) ControlPlaneSnapshotWithDiscovery(ctx context.Context, registry any) (controlplane.Snapshot, error) {
	contributors := []controlplane.SnapshotContributor{c.ControlPlaneContributor()}
	if source, ok := registry.(controlplane.DiscoverySnapshotSource); ok {
		contributors = append(contributors, controlplane.DiscoveryContributor{Registry: source})
	}
	provider := controlplane.CompositeProvider{
		Name:         "generated-project",
		Contributors: contributors,
	}
	return provider.Load(ctx)
}

func (c ControlPlaneContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	cfg := c.Config
	service := cfg.ServiceConf()
	addGeneratedControlPlaneConfig := func(name string, value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("marshal generated control-plane config %s: %w", name, err)
		}
		if snapshot.Configs == nil {
			snapshot.Configs = make(map[string]json.RawMessage)
		}
		snapshot.Configs["generated."+name] = data
		return nil
	}
	if err := addGeneratedControlPlaneConfig("service", service); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("resilience", cfg.ResilienceProfile()); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("scaffold", cfg.Scaffold); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("discovery", cfg.Discovery.Sanitized()); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("rpcMuxOTelSinks", rpc.RPCMuxOTelLogSinkRegistry()); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("rpcMuxOperatorAuditSchemas", rpc.RPCMuxDiagnosisOperatorAuditSchemas()); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("openapi", cfg.OpenAPIInfo()); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("projectStore", struct {
		Enabled bool   `json:"enabled"`
		Driver  string `json:"driver,omitempty"`
		DSNEnv  string `json:"dsnEnv,omitempty"`
	}{Enabled: cfg.ProjectStore.Enabled, Driver: cfg.ProjectStore.Driver, DSNEnv: cfg.ProjectStore.DSNEnv}); err != nil {
		return err
	}
	restConfig := cfg.Rest
	restConfig.Admin.Token = ""
	if err := addGeneratedControlPlaneConfig("rest", restConfig); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("rpc", cfg.RPC); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("rpcMuxConfigWarningSchema", json.RawMessage(RPCMuxConfigWarningJSONSchema)); err != nil {
		return err
	}
	if err := addGeneratedControlPlaneConfig("controlPlaneSchemaChecksums", generatedControlPlaneSchemaChecksums()); err != nil {
		return err
	}
	rpcMuxConfigWarnings, err := ValidateRPCMuxConfigWithWarnings(cfg.RPC.Mux)
	if err != nil {
		return fmt.Errorf("validate generated rpc mux config warnings: %w", err)
	}
	if len(rpcMuxConfigWarnings) > 0 {
		if err := addGeneratedControlPlaneConfig("rpcMuxConfigWarnings", rpcMuxConfigWarnings); err != nil {
			return err
		}
	}
	if err := addGeneratedControlPlaneConfig("admin", struct {
		Enabled    bool   `json:"enabled"`
		Addr       string `json:"addr"`
		PathPrefix string `json:"pathPrefix"`
	}{Enabled: cfg.Admin.Enabled, Addr: cfg.Admin.Addr, PathPrefix: cfg.Admin.PathPrefix}); err != nil {
		return err
	}
	snapshot.Policies = append(snapshot.Policies, cfg.Governance.Rules...)
	serviceSnapshot := controlplane.ServiceSnapshot{Name: service.Name, Metadata: map[string]string{"source": "generated-project"}}
	if cfg.Rest.Port > 0 {
		host := strings.TrimSpace(cfg.Rest.Host)
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		serviceSnapshot.Endpoints = append(serviceSnapshot.Endpoints, controlplane.EndpointSnapshot{Address: fmt.Sprintf("http://%s:%d", host, cfg.Rest.Port), Metadata: map[string]string{"transport": "rest"}})
	}
	if strings.TrimSpace(cfg.RPC.Advertise) != "" {
		snapshot.Services = append(snapshot.Services, controlplane.ServiceSnapshot{Name: "greeter", Endpoints: []controlplane.EndpointSnapshot{{Address: strings.TrimSpace(cfg.RPC.Advertise), Metadata: map[string]string{"transport": "rpc"}}}, Metadata: map[string]string{"source": "generated-project"}})
	}
	if len(serviceSnapshot.Endpoints) > 0 {
		snapshot.Services = append(snapshot.Services, serviceSnapshot)
	}
	if snapshot.Metadata == nil {
		snapshot.Metadata = make(map[string]string)
	}
	snapshot.Metadata["generated.project"] = "available"
	snapshot.Metadata["generated.project.service"] = service.Name
	snapshot.Metadata["generated.project.features"] = strings.Join(cfg.EffectiveScaffoldFeatures(), ",")
	snapshot.Metadata["generated.project.runtime"] = "service,rest,rpc,governance,discovery"
	snapshot.Metadata["generated.project.contract"] = "scaffold,runtime-policy,ai-manifest"
	snapshot.Metadata["generated.project.resilience"] = "timeout,rate,concurrency,breaker,retry"
	return nil
}

func (c DiscoveryConfig) ProviderName() string {
	provider := strings.ToLower(strings.TrimSpace(c.Provider))
	if provider == "" {
		return "memory"
	}
	return provider
}

func (c DiscoveryConfig) RegistryTTL() time.Duration {
	return parseDiscoveryDuration(c.TTL, 15*time.Second)
}

func (c DiscoveryConfig) DialTimeoutDuration() time.Duration {
	return parseDiscoveryDuration(c.DialTimeout, 5*time.Second)
}

func (c DiscoveryConfig) ResolvedEndpoints() []string {
	if len(c.Endpoints) > 0 {
		return compactDiscoveryEndpoints(c.Endpoints)
	}
	return compactDiscoveryEndpoints(strings.Split(c.Address, ","))
}

func (c DiscoveryConfig) RegisterOptions() []discovery.RegisterOption {
	ttl := c.RegistryTTL()
	if ttl <= 0 {
		return nil
	}
	return []discovery.RegisterOption{discovery.WithTTL(ttl)}
}

func (c DiscoveryConfig) Sanitized() DiscoveryConfig {
	c.TokenEnv = strings.TrimSpace(c.TokenEnv)
	c.UsernameEnv = strings.TrimSpace(c.UsernameEnv)
	c.PasswordEnv = strings.TrimSpace(c.PasswordEnv)
	return c
}

func ValidateDiscoveryConfig(c DiscoveryConfig) error {
	switch c.ProviderName() {
	case "memory", "consul", "etcdv3":
	default:
		return fmt.Errorf("unsupported discovery provider %q", c.Provider)
	}
	if c.TTL != "" {
		if _, err := time.ParseDuration(c.TTL); err != nil {
			return fmt.Errorf("discovery ttl: %w", err)
		}
	}
	if c.DialTimeout != "" {
		if _, err := time.ParseDuration(c.DialTimeout); err != nil {
			return fmt.Errorf("discovery dial timeout: %w", err)
		}
	}
	if c.ProviderName() == "etcdv3" && len(c.ResolvedEndpoints()) == 0 {
		return errors.New("discovery endpoints are required for etcdv3")
	}
	return nil
}

func compactDiscoveryEndpoints(endpoints []string) []string {
	out := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint != "" {
			out = append(out, endpoint)
		}
	}
	return out
}

func parseDiscoveryDuration(value string, fallback time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func (c Config) OpenAPIEnabled() bool {
	return c.OpenAPI.Enabled == nil || *c.OpenAPI.Enabled
}

func (c Config) OpenAPIInfo() rest.OpenAPIInfo {
	info := rest.OpenAPIInfo{
		Title:       strings.TrimSpace(c.OpenAPI.Title),
		Version:     strings.TrimSpace(c.OpenAPI.Version),
		Description: strings.TrimSpace(c.OpenAPI.Description),
	}
	if info.Title == "" {
		info.Title = c.ServiceConf().Name + " API"
	}
	if info.Version == "" {
		info.Version = "1.0.0"
	}
	return info
}

func ValidateOpenAPIConfig(c Config) error {
	if !c.OpenAPIEnabled() {
		return nil
	}
	info := c.OpenAPIInfo()
	if strings.TrimSpace(info.Title) == "" {
		return errors.New("openapi title is required")
	}
	if strings.TrimSpace(info.Version) == "" {
		return errors.New("openapi version is required")
	}
	return nil
}

func (c Config) EffectiveScaffoldFeatures() []string {
	if c.Scaffold.Features == nil {
		return []string{"ecosystem-compat"}
	}
	return NormalizeScaffoldFeatures(c.Scaffold.Features)
}

func RegisteredScaffoldFeatures() []string {
	features := make([]string, 0, len(registeredScaffoldFeatures))
	for name := range registeredScaffoldFeatures {
		features = append(features, name)
	}
	sort.Strings(features)
	return features
}

func ValidateScaffoldFeatures(features []string) error {
	for _, feature := range NormalizeScaffoldFeatures(features) {
		if !registeredScaffoldFeatures[feature] {
			return fmt.Errorf("feature %q is not registered", feature)
		}
	}
	return nil
}

func NormalizeScaffoldFeatures(features []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(features))
	for _, feature := range features {
		feature = strings.TrimSpace(feature)
		if feature == "" {
			continue
		}
		if _, ok := seen[feature]; ok {
			continue
		}
		seen[feature] = struct{}{}
		normalized = append(normalized, feature)
	}
	return normalized
}

var registeredScaffoldFeatures = map[string]bool{
	"ecosystem-compat": true,
	"http-compat":      true,
	"rpc-compat":       true,
}

func Validate(c Config) error {
	if err := errors.Join(
		ValidateScaffoldFeatures(c.Scaffold.Features),
		ValidateDiscoveryConfig(c.Discovery),
		ValidateOpenAPIConfig(c),
		ValidateAuthorizationConfig(c.Authorization),
		ValidateProjectStoreConfig(c.ProjectStore),
		ValidateRPCMuxConfig(c.RPC.Mux),
	); err != nil {
		return err
	}
	service := c.ServiceConf()
	if !isProduction(service.Environment) {
		return nil
	}
	return errors.Join(
		app.ValidateProductionConfig(service.BootstrapConfig(c.Rest.Name)),
		rest.ValidateProductionConfig(c.Rest),
	)
}

func isProduction(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}
