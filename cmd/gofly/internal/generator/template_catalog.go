package generator

import (
	"sort"
	"strings"
)

// TemplateInput describes one user-provided value required or accepted by a
// project template. It is intentionally JSON-friendly so AI callers can inspect
// it without understanding Go structs.
type TemplateInput struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required,omitempty"`
	Default     string   `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Description string   `json:"description,omitempty"`
}

// ProjectTemplate is the declarative catalog entry used by AI-first scaffold
// planning. Built-in entries map to existing gofly generators for now; future
// external catalogs can use the same shape.
type ProjectTemplate struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	Language     string          `json:"language"`
	Architecture string          `json:"architecture"`
	Description  string          `json:"description"`
	Features     []string        `json:"features"`
	Inputs       []TemplateInput `json:"inputs"`
	Files        []string        `json:"files"`
	Verify       []string        `json:"verify"`
	// VerifyE2EValidated records that the template participates in the
	// generated-project verification matrix and every declared verify command is
	// expected to run and pass without skips.
	VerifyE2EValidated bool   `json:"verifyE2EValidated"`
	RiskLevel          string `json:"riskLevel"`
	Command            string `json:"command"`
}

// ListProjectTemplates returns the built-in project scaffold catalog in stable
// ID order. Callers receive defensive copies so tests and plugins cannot mutate
// the global catalog.
func ListProjectTemplates() []ProjectTemplate {
	templates := builtInProjectTemplates()
	sort.SliceStable(templates, func(i, j int) bool { return templates[i].ID < templates[j].ID })
	for i := range templates {
		templates[i] = cloneProjectTemplate(templates[i])
	}
	return templates
}

// GetProjectTemplate finds a template by ID, case-insensitively.
func GetProjectTemplate(id string) (ProjectTemplate, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return ProjectTemplate{}, false
	}
	for _, tmpl := range ListProjectTemplates() {
		if strings.ToLower(tmpl.ID) == id {
			return tmpl, true
		}
	}
	return ProjectTemplate{}, false
}

// RecommendProjectTemplate chooses the best built-in template from a natural
// language prompt and optional kind hint. The heuristic is deterministic by
// design so AI dry-runs are reproducible and easy to test.
func RecommendProjectTemplate(prompt string, kind string) ProjectTemplate {
	prompt = strings.ToLower(prompt)
	kind = strings.ToLower(strings.TrimSpace(kind))
	templates := ListProjectTemplates()
	best := templates[0]
	bestScore := -1
	for _, tmpl := range templates {
		score := templateMatchScore(tmpl, prompt, kind)
		if score > bestScore || (score == bestScore && tmpl.ID < best.ID) {
			best = tmpl
			bestScore = score
		}
	}
	return best
}

func templateMatchScore(tmpl ProjectTemplate, prompt string, kind string) int {
	score := 0
	if kind != "" && strings.EqualFold(tmpl.Kind, kind) {
		score += 8
	}
	fields := []string{tmpl.ID, tmpl.Name, tmpl.Kind, tmpl.Architecture, tmpl.Description}
	fields = append(fields, tmpl.Features...)
	for _, field := range fields {
		for _, token := range splitTemplateTokens(field) {
			if token != "" && strings.Contains(prompt, token) {
				score++
			}
		}
	}
	return score
}

func splitTemplateTokens(value string) []string {
	value = strings.ToLower(value)
	value = strings.NewReplacer("-", " ", "_", " ", "/", " ", ",", " ", ".", " ").Replace(value)
	return strings.Fields(value)
}

func cloneProjectTemplate(t ProjectTemplate) ProjectTemplate {
	t.Features = append([]string(nil), t.Features...)
	t.Files = append([]string(nil), t.Files...)
	t.Verify = append([]string(nil), t.Verify...)
	t.Inputs = append([]TemplateInput(nil), t.Inputs...)
	for i := range t.Inputs {
		t.Inputs[i].Enum = append([]string(nil), t.Inputs[i].Enum...)
	}
	return t
}

func commonTemplateInputs() []TemplateInput {
	return []TemplateInput{
		{Name: "name", Type: "string", Required: true, Description: "service or application name"},
		{Name: "module", Type: "string", Required: true, Description: "Go module path"},
		{Name: "dir", Type: "string", Default: "<name>", Description: "output directory"},
	}
}

func builtInProjectTemplates() []ProjectTemplate {
	return []ProjectTemplate{
		{
			ID:                 "go-rest-minimal",
			Name:               "Go REST Minimal Service",
			Kind:               "service",
			Language:           "go",
			Architecture:       "flat",
			Description:        "Small REST service with config, health, metrics and one ping route.",
			Features:           []string{"rest", "config", "health", "metrics", "openapi"},
			Inputs:             commonTemplateInputs(),
			Files:              []string{".gofly/config.json", "go.mod", "cmd/<name>/main.go", "etc/<name>.json", "docs/openapi.yaml", "internal/config/config.go", "internal/observability/observability.go", "internal/routes/routes.go", "internal/service/ping.go"},
			Verify:             []string{"gofmt", "go mod tidy", "go test ./..."},
			VerifyE2EValidated: true,
			RiskLevel:          "medium",
			Command:            "gofly new api <name> --module <module> --style minimal --dir <dir>",
		},
		{
			ID:                 "go-rest-clean-postgres",
			Name:               "Go REST Clean Architecture + Postgres",
			Kind:               "service",
			Language:           "go",
			Architecture:       "clean",
			Description:        "Production REST service baseline intended for database-backed business APIs.",
			Features:           []string{"rest", "openapi", "postgres", "migration", "repository", "docker", "ci", "observability"},
			Inputs:             append(commonTemplateInputs(), TemplateInput{Name: "database", Type: "enum", Default: "postgres", Enum: []string{"postgres"}, Description: "database backend"}),
			Files:              []string{".gofly/config.json", "go.mod", "cmd/<name>/main.go", "Dockerfile", ".github/workflows/ci.yml", "docs/openapi.yaml", "internal/config/config.go", "internal/observability/observability.go", "internal/repository/postgres.go", "internal/routes/routes.go", "migrations/000001_init.sql"},
			Verify:             []string{"gofmt", "go mod tidy", "go test ./..."},
			VerifyE2EValidated: true,
			RiskLevel:          "medium",
			Command:            "gofly new service <name> --module <module> --style production --feature ecosystem-compat --dir <dir>",
		},
		{
			ID:                 "go-rpc-grpc",
			Name:               "Go RPC/gRPC Service",
			Kind:               "rpc",
			Language:           "go",
			Architecture:       "service",
			Description:        "RPC service scaffold with proto contract, descriptor diagnostics and client compatibility tests.",
			Features:           []string{"rpc", "grpc", "proto", "governance", "observability", "docker", "ci"},
			Inputs:             commonTemplateInputs(),
			Files:              []string{".gofly/config.json", "go.mod", "cmd/<name>/main.go", "<name>.proto", "Dockerfile", ".github/workflows/ci.yml", "internal/admin/admin.go", "internal/observability/observability.go", "internal/rpc/greeter.go"},
			Verify:             []string{"gofmt", "go mod tidy", "go test ./..."},
			VerifyE2EValidated: true,
			RiskLevel:          "medium",
			Command:            "gofly new rpc <name> --module <module> --style production --dir <dir>",
		},
		{
			ID:                 "go-worker-mq",
			Name:               "Go Worker + MQ",
			Kind:               "worker",
			Language:           "go",
			Architecture:       "service",
			Description:        "Background worker baseline for queue consumers, retry and governance policies.",
			Features:           []string{"worker", "mq", "retry", "breaker", "observability", "config"},
			Inputs:             append(commonTemplateInputs(), TemplateInput{Name: "broker", Type: "enum", Default: "memory", Enum: []string{"memory", "kafka", "rabbitmq", "redisstream"}, Description: "message broker driver"}),
			Files:              []string{".gofly/config.json", "go.mod", "cmd/<name>/main.go", "etc/<name>.json", "etc/governance.json", "internal/mq/broker.go", "internal/observability/observability.go", "internal/worker/worker.go"},
			Verify:             []string{"gofmt", "go mod tidy", "go test ./..."},
			VerifyE2EValidated: true,
			RiskLevel:          "medium",
			Command:            "gofly new service <name> --module <module> --style production --dir <dir>",
		},
		{
			ID:                 "go-cli-cobra",
			Name:               "Go CLI Tool",
			Kind:               "cli",
			Language:           "go",
			Architecture:       "flat",
			Description:        "CLI-oriented project skeleton for command parsing, version output and testable stdout/stderr behavior.",
			Features:           []string{"cli", "flags", "config", "version", "tests"},
			Inputs:             commonTemplateInputs(),
			Files:              []string{".gofly/config.json", "go.mod", "cmd/<name>/main.go", "etc/<name>.json", "internal/config/config.go", "internal/routes/routes.go", "internal/service/ping.go"},
			Verify:             []string{"gofmt", "go mod tidy", "go test ./..."},
			VerifyE2EValidated: true,
			RiskLevel:          "medium",
			Command:            "gofly new api <name> --module <module> --style minimal --dir <dir>",
		},
		{
			ID:                 "go-gateway",
			Name:               "Go API Gateway",
			Kind:               "gateway",
			Language:           "go",
			Architecture:       "gateway",
			Description:        "Gateway project baseline for REST ingress, service discovery and downstream RPC routing.",
			Features:           []string{"gateway", "rest", "rpc", "discovery", "rate-limit", "observability"},
			Inputs:             commonTemplateInputs(),
			Files:              []string{"go.mod", "cmd/<name>/main.go", "etc/<name>.json", "internal/config/config.go", "internal/mq/broker.go", "internal/observability/observability.go", "internal/routes/routes.go", "internal/svc/service.go"},
			Verify:             []string{"gofmt", "go mod tidy", "go test ./..."},
			VerifyE2EValidated: true,
			RiskLevel:          "medium",
			Command:            "gofly gen gateway <name> --module <module> --dir <dir>",
		},
		{
			ID:                 "go-ai-agent",
			Name:               "Go AI Agent Service",
			Kind:               "ai-agent",
			Language:           "go",
			Architecture:       "agent",
			Description:        "AI agent service baseline with LLM governance, tool manifest awareness and safe default noop provider.",
			Features:           []string{"llm", "agent", "tool-call", "governance", "redaction", "audit", "stream"},
			Inputs:             commonTemplateInputs(),
			Files:              []string{".gofly/config.json", "go.mod", "cmd/<name>/main.go", "Dockerfile", ".github/workflows/ci.yml", "internal/ai/rag.go", "internal/config/config.go", "internal/observability/observability.go"},
			Verify:             []string{"gofmt", "go mod tidy", "go test ./...", "gofly ai doctor --json"},
			VerifyE2EValidated: true,
			RiskLevel:          "medium",
			Command:            "gofly new service <name> --module <module> --style production --dir <dir>",
		},
		{
			ID:                 "go-rag-service",
			Name:               "Go RAG Service",
			Kind:               "rag",
			Language:           "go",
			Architecture:       "service",
			Description:        "Retrieval augmented generation service baseline for document ingestion, embeddings and governed LLM calls.",
			Features:           []string{"rag", "embedding", "vector-store", "retriever", "llm", "stream", "observability"},
			Inputs:             append(commonTemplateInputs(), TemplateInput{Name: "vectorStore", Type: "enum", Default: "memory", Enum: []string{"memory", "redis"}, Description: "vector store backend"}),
			Files:              []string{".gofly/config.json", "go.mod", "cmd/<name>/main.go", "Dockerfile", ".github/workflows/ci.yml", "internal/ai/rag.go", "internal/observability/observability.go", "internal/routes/routes.go", "internal/service/ping.go"},
			Verify:             []string{"gofmt", "go mod tidy", "go test ./..."},
			VerifyE2EValidated: true,
			RiskLevel:          "medium",
			Command:            "gofly new service <name> --module <module> --style production --dir <dir>",
		},
	}
}
