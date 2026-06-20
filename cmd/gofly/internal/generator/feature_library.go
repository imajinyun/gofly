package generator

import (
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
)

// FeaturePlugin is a deterministic built-in project feature generator. Project
// templates use high-level feature tags for recommendation; this interface turns
// those tags into concrete, compilable scaffold files after the base project is
// generated.
type FeaturePlugin interface {
	Name() string
	Tags() []string
	Generate(ProjectFeatureOptions) (ProjectFeatureResult, error)
}

// ProjectFeatureOptions carries the generated project context into feature
// plugins. Features must write only under Dir.
type ProjectFeatureOptions struct {
	Dir      string
	Name     string
	Module   string
	Features []string
}

// ProjectFeatureResult reports the concrete files written for one feature
// plugin. It is JSON-friendly for AI scaffold apply results.
type ProjectFeatureResult struct {
	Plugin         string       `json:"plugin"`
	Tags           []string     `json:"tags"`
	Files          []string     `json:"files"`
	Dependencies   []string     `json:"dependencies,omitempty"`
	ConfigHints    []ConfigHint `json:"configHints,omitempty"`
	VerifyCommands []string     `json:"verifyCommands,omitempty"`
}

// ConfigHint describes one configuration value a generated feature expects or
// commonly needs when it is wired into a real service.
type ConfigHint struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Example     string `json:"example,omitempty"`
}

type projectFeaturePlugin struct {
	name           string
	tags           []string
	files          map[string]string
	dependencies   []string
	configHints    []ConfigHint
	verifyCommands []string
}

func (p projectFeaturePlugin) Name() string { return p.name }

func (p projectFeaturePlugin) Tags() []string { return append([]string(nil), p.tags...) }

func (p projectFeaturePlugin) Generate(opts ProjectFeatureOptions) (ProjectFeatureResult, error) {
	if strings.TrimSpace(opts.Dir) == "" {
		return ProjectFeatureResult{}, fmt.Errorf("feature plugin %s: output directory is required", p.name)
	}
	data := map[string]string{
		"Name":       opts.Name,
		"Module":     opts.Module,
		"ExportName": exportName(opts.Name),
	}
	files := make([]string, 0, len(p.files))
	paths := make([]string, 0, len(p.files))
	for path := range p.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		content := []byte(render(p.files[path], data))
		if filepath.Ext(path) == ".go" {
			formatted, err := format.Source(content)
			if err != nil {
				return ProjectFeatureResult{}, fmt.Errorf("feature plugin %s format %s: %w", p.name, path, err)
			}
			content = formatted
		}
		if err := writeGeneratedFileUnder(opts.Dir, path, content); err != nil {
			return ProjectFeatureResult{}, fmt.Errorf("feature plugin %s write %s: %w", p.name, path, err)
		}
		files = append(files, filepath.ToSlash(path))
	}
	return ProjectFeatureResult{
		Plugin:         p.name,
		Tags:           p.Tags(),
		Files:          files,
		Dependencies:   append([]string(nil), p.dependencies...),
		ConfigHints:    cloneConfigHints(p.configHints),
		VerifyCommands: append([]string(nil), p.verifyCommands...),
	}, nil
}

func cloneConfigHints(hints []ConfigHint) []ConfigHint {
	return append([]ConfigHint(nil), hints...)
}

// ListProjectFeaturePlugins returns the built-in feature library in stable name
// order. The returned plugins are immutable value objects.
func ListProjectFeaturePlugins() []FeaturePlugin {
	plugins := builtInProjectFeaturePlugins()
	sort.SliceStable(plugins, func(i, j int) bool { return plugins[i].Name() < plugins[j].Name() })
	return plugins
}

// ApplyProjectFeaturePlugins applies every built-in feature plugin whose tags
// intersect the requested template features. Plugins run at most once, in stable
// name order, and only write files through writeGeneratedFileUnder.
func ApplyProjectFeaturePlugins(opts ProjectFeatureOptions) ([]ProjectFeatureResult, error) {
	requested := featureTagSet(opts.Features)
	if len(requested) == 0 {
		return nil, nil
	}
	plugins := ListProjectFeaturePlugins()
	results := make([]ProjectFeatureResult, 0, len(plugins))
	for _, plugin := range plugins {
		if !featurePluginMatches(plugin, requested) {
			continue
		}
		result, err := plugin.Generate(opts)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func featurePluginMatches(plugin FeaturePlugin, requested map[string]struct{}) bool {
	for _, tag := range plugin.Tags() {
		if _, ok := requested[strings.ToLower(strings.TrimSpace(tag))]; ok {
			return true
		}
	}
	return false
}

func featureTagSet(features []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, feature := range features {
		feature = strings.ToLower(strings.TrimSpace(feature))
		if feature == "" {
			continue
		}
		set[feature] = struct{}{}
	}
	return set
}

func builtInProjectFeaturePlugins() []FeaturePlugin {
	return []FeaturePlugin{
		projectFeaturePlugin{
			name: "auth-jwt",
			tags: []string{"auth", "jwt"},
			files: map[string]string{
				filepath.Join("internal", "auth", "jwt.go"): authJWTFeatureTemplate,
			},
			configHints: []ConfigHint{
				{Key: "JWT_SECRET", Description: "HMAC signing secret used by internal/auth.SignJWT and VerifyJWT", Example: "change-me-in-production"},
			},
			verifyCommands: []string{"go test ./..."},
		},
		projectFeaturePlugin{
			name: "ci-docker",
			tags: []string{"ci", "docker"},
			files: map[string]string{
				"Dockerfile": dockerFeatureTemplate,
				filepath.Join(".github", "workflows", "ci.yml"): ciFeatureTemplate,
			},
			verifyCommands: []string{"go test ./..."},
		},
		projectFeaturePlugin{
			name: "observability",
			tags: []string{
				"observability", "metrics", "otel", "audit", "redaction", "governance", "health",
				"rate-limit", "breaker", "discovery",
			},
			files: map[string]string{
				filepath.Join("internal", "observability", "observability.go"): observabilityFeatureTemplate,
			},
			configHints: []ConfigHint{
				{Key: "LOG_LEVEL", Description: "structured logging level for generated audit and observability paths", Example: "info"},
				{
					Key:         "OTEL_EXPORTER_OTLP_ENDPOINT",
					Description: "OpenTelemetry collector endpoint when tracing or metrics exporters are enabled later",
					Example:     "http://localhost:4318",
				},
			},
			verifyCommands: []string{"go vet ./..."},
		},
		projectFeaturePlugin{
			name: "openapi",
			tags: []string{"openapi"},
			files: map[string]string{
				filepath.Join("docs", "openapi.yaml"): openAPIFeatureTemplate,
			},
			verifyCommands: []string{"go test ./..."},
		},
		projectFeaturePlugin{
			name: "postgres-repository",
			tags: []string{"postgres", "migration", "repository"},
			files: map[string]string{
				filepath.Join("internal", "repository", "postgres.go"): postgresRepositoryFeatureTemplate,
				filepath.Join("migrations", "000001_init.sql"):         postgresMigrationFeatureTemplate,
			},
			dependencies: []string{"github.com/jackc/pgx/v5@latest"},
			configHints: []ConfigHint{
				{
					Key:         "DATABASE_URL",
					Description: "Postgres DSN used when wiring internal/repository.Repository to a real *sql.DB",
					Example:     "postgres://postgres@localhost:5432/orders?sslmode=disable",
				},
			},
			verifyCommands: []string{"go test ./..."},
		},
		projectFeaturePlugin{
			name: "queue-worker",
			tags: []string{"worker", "mq", "queue", "retry"},
			files: map[string]string{
				filepath.Join("internal", "worker", "worker.go"): queueWorkerFeatureTemplate,
			},
			configHints: []ConfigHint{
				{Key: "WORKER_CONCURRENCY", Description: "number of worker handlers to run when the queue is wired to a real broker", Example: "4"},
				{Key: "QUEUE_NAME", Description: "logical queue or topic consumed by generated worker handlers", Example: "default"},
			},
			verifyCommands: []string{"go test ./..."},
		},
		projectFeaturePlugin{
			name: "rag-agent",
			tags: []string{
				"rag", "embedding", "vector-store", "retriever", "llm", "agent", "tool-call", "stream",
			},
			files: map[string]string{
				filepath.Join("internal", "ai", "rag.go"): ragAgentFeatureTemplate,
			},
			configHints: []ConfigHint{
				{Key: "LLM_PROVIDER", Description: "LLM provider name for the service implementation that wraps internal/ai.Retriever", Example: "noop"},
				{Key: "VECTOR_STORE", Description: "vector store backend for retrieval augmented generation", Example: "memory"},
			},
			verifyCommands: []string{"go test ./..."},
		},
		projectFeaturePlugin{
			name: "redis-cache",
			tags: []string{"redis", "cache", "redisstream"},
			files: map[string]string{
				filepath.Join("internal", "cache", "redis.go"): redisCacheFeatureTemplate,
			},
			dependencies: []string{"github.com/redis/go-redis/v9@latest"},
			configHints: []ConfigHint{
				{Key: "REDIS_ADDR", Description: "Redis server address used by cache or stream integrations", Example: "127.0.0.1:6379"},
				{Key: "REDIS_DB", Description: "Redis logical database index", Example: "0"},
			},
			verifyCommands: []string{"go test ./..."},
		},
	}
}

const authJWTFeatureTemplate = `package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Claims struct {
	Subject   string ` + "`json:\"sub\"`" + `
	ExpiresAt int64  ` + "`json:\"exp\"`" + `
}

func SignJWT(claims Claims, secret []byte) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("jwt secret is required")
	}
	if claims.ExpiresAt == 0 {
		claims.ExpiresAt = time.Now().Add(time.Hour).Unix()
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(` + "`" + `{"alg":"HS256","typ":"JWT"}` + "`" + `))
	payloadData, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadData)
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature, nil
}

func VerifyJWT(token string, secret []byte) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("invalid jwt token")
	}
	if len(secret) == 0 {
		return Claims{}, errors.New("jwt secret is required")
	}
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(got, want) {
		return Claims{}, errors.New("invalid jwt signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, err
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, err
	}
	if claims.ExpiresAt > 0 && time.Now().Unix() > claims.ExpiresAt {
		return Claims{}, errors.New("jwt token expired")
	}
	return claims, nil
}
`

const observabilityFeatureTemplate = `package observability

import (
	"context"
	"log/slog"
	"time"
)

func Audit(ctx context.Context, action string, attrs ...any) {
	values := append([]any{"action", action, "ts", time.Now().UTC().Format(time.RFC3339)}, attrs...)
	slog.InfoContext(ctx, "audit", values...)
}

func RedactSecret(value string) string {
	if value == "" {
		return ""
	}
	return "***"
}
`

const openAPIFeatureTemplate = `openapi: 3.0.3
info:
  title: {{.Name}} API
  version: 0.1.0
paths:
  /ping:
    get:
      summary: Health ping
      responses:
        "200":
          description: OK
`

const postgresRepositoryFeatureTemplate = `package repository

import (
	"context"
	"database/sql"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Ping(ctx context.Context) error {
	if r == nil || r.db == nil {
		return sql.ErrConnDone
	}
	return r.db.PingContext(ctx)
}
`

const postgresMigrationFeatureTemplate = `-- Initial migration for {{.Name}}.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

const queueWorkerFeatureTemplate = `package worker

import "context"

type Job struct {
	Name    string
	Payload []byte
}

type Handler func(context.Context, Job) error

func RunOnce(ctx context.Context, job Job, handler Handler) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return handler(ctx, job)
}
`

const ragAgentFeatureTemplate = `package ai

import "context"

type Document struct {
	ID      string
	Content string
}

type Retriever interface {
	Retrieve(context.Context, string) ([]Document, error)
}

type MemoryRetriever struct {
	documents []Document
}

func NewMemoryRetriever(documents []Document) *MemoryRetriever {
	return &MemoryRetriever{documents: append([]Document(nil), documents...)}
}

func (r *MemoryRetriever) Retrieve(ctx context.Context, query string) ([]Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_ = query
	return append([]Document(nil), r.documents...), nil
}
`

const redisCacheFeatureTemplate = `package cache

import "context"

type RedisConfig struct {
	Addr string
	DB   int
}

type RedisClient interface {
	Ping(context.Context) error
}

func DefaultRedisConfig() RedisConfig {
	return RedisConfig{Addr: "127.0.0.1:6379"}
}
`

const dockerFeatureTemplate = `FROM golang:1.26 AS build
WORKDIR /src
COPY . .
RUN go build -o /out/{{.Name}} ./cmd/{{.Name}}

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/{{.Name}} /{{.Name}}
ENTRYPOINT ["/{{.Name}}"]
`

const ciFeatureTemplate = `name: ci
on:
  pull_request:
  push:
    branches: [main]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      - run: go test ./...
`
