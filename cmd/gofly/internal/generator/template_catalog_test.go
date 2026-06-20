package generator

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProjectTemplateCatalogIsStableAndDefensive(t *testing.T) {
	templates := ListProjectTemplates()
	if len(templates) < 8 {
		t.Fatalf("ListProjectTemplates returned %d templates, want at least 8", len(templates))
	}
	for i := 1; i < len(templates); i++ {
		if templates[i-1].ID > templates[i].ID {
			t.Fatalf("templates are not sorted: %q before %q", templates[i-1].ID, templates[i].ID)
		}
	}
	templates[0].Features[0] = "mutated"
	fresh := ListProjectTemplates()
	if fresh[0].Features[0] == "mutated" {
		t.Fatal("ListProjectTemplates returned mutable shared feature slice")
	}
}

func TestGetAndRecommendProjectTemplate(t *testing.T) {
	tmpl, ok := GetProjectTemplate("GO-AI-AGENT")
	if !ok {
		t.Fatal("GetProjectTemplate did not find go-ai-agent case-insensitively")
	}
	if tmpl.Kind != "ai-agent" || tmpl.RiskLevel == "" || tmpl.Command == "" {
		t.Fatalf("go-ai-agent template = %+v, want kind/risk/command", tmpl)
	}

	recommended := RecommendProjectTemplate("需要 RAG 服务，包含 embedding、retriever 和 vector store", "")
	if recommended.ID != "go-rag-service" {
		t.Fatalf("RecommendProjectTemplate rag = %q, want go-rag-service", recommended.ID)
	}

	worker := RecommendProjectTemplate("普通后台消费任务", "worker")
	if worker.ID != "go-worker-mq" {
		t.Fatalf("RecommendProjectTemplate worker kind = %q, want go-worker-mq", worker.ID)
	}
}

func TestProjectFeatureLibraryAppliesTemplateTags(t *testing.T) {
	plugins := ListProjectFeaturePlugins()
	if len(plugins) == 0 {
		t.Fatal("ListProjectFeaturePlugins returned no plugins")
	}
	if plugins[0].Name() != "auth-jwt" {
		t.Fatalf("ListProjectFeaturePlugins first = %q, want stable name order", plugins[0].Name())
	}
	tags := plugins[0].Tags()
	tags[0] = "mutated"
	if plugins[0].Tags()[0] == "mutated" {
		t.Fatal("FeaturePlugin.Tags leaked mutable tag slice")
	}

	tmpl, ok := GetProjectTemplate("go-rest-clean-postgres")
	if !ok {
		t.Fatal("missing go-rest-clean-postgres template")
	}
	dir := t.TempDir()
	results, err := ApplyProjectFeaturePlugins(ProjectFeatureOptions{
		Dir:      dir,
		Name:     "orders",
		Module:   "example.com/orders",
		Features: tmpl.Features,
	})
	if err != nil {
		t.Fatalf("ApplyProjectFeaturePlugins: %v", err)
	}
	pluginNames := make([]string, 0, len(results))
	var postgresResult ProjectFeatureResult
	for _, result := range results {
		pluginNames = append(pluginNames, result.Plugin)
		if result.Plugin == "postgres-repository" {
			postgresResult = result
		}
	}
	if got := strings.Join(pluginNames, ","); got != "ci-docker,observability,openapi,postgres-repository" {
		t.Fatalf("ApplyProjectFeaturePlugins plugins = %q, want stable project feature plugin set", got)
	}
	if got := strings.Join(postgresResult.Dependencies, ","); got != "github.com/jackc/pgx/v5@latest" {
		t.Fatalf("postgres feature dependencies = %q, want pgx declaration", got)
	}
	if len(postgresResult.ConfigHints) != 1 || postgresResult.ConfigHints[0].Key != "DATABASE_URL" {
		t.Fatalf("postgres feature config hints = %+v, want DATABASE_URL", postgresResult.ConfigHints)
	}
	if got := strings.Join(postgresResult.VerifyCommands, ","); got != "go test ./..." {
		t.Fatalf("postgres feature verify commands = %q, want go test", got)
	}
	written := []string{
		filepath.Join("Dockerfile"),
		filepath.Join("docs", "openapi.yaml"),
		filepath.Join("internal", "observability", "observability.go"),
		filepath.Join("internal", "repository", "postgres.go"),
		filepath.Join("migrations", "000001_init.sql"),
	}
	for _, rel := range written {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("ApplyProjectFeaturePlugins did not write %s: %v", rel, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "docs", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "title: orders API") {
		t.Fatalf("openapi feature was not rendered with project name:\n%s", data)
	}
}

func TestProjectFeatureLibraryRejectsUnsafeOutput(t *testing.T) {
	_, err := ApplyProjectFeaturePlugins(ProjectFeatureOptions{Dir: "", Name: "bad", Module: "example.com/bad", Features: []string{"openapi"}})
	if err == nil || !strings.Contains(err.Error(), "output directory is required") {
		t.Fatalf("ApplyProjectFeaturePlugins empty dir error = %v, want output directory error", err)
	}
}

func TestProjectFeatureLibraryContractGovernance(t *testing.T) {
	t.Run("built-ins declare safe contracts", func(t *testing.T) {
		plugins := builtInProjectFeaturePlugins()
		if len(plugins) == 0 {
			t.Fatal("builtInProjectFeaturePlugins returned no plugins")
		}
		for _, plugin := range plugins {
			p, ok := plugin.(projectFeaturePlugin)
			if !ok {
				t.Fatalf("built-in plugin %T is not a projectFeaturePlugin", plugin)
			}
			if err := validateProjectFeaturePluginContract(p); err != nil {
				t.Fatalf("built-in feature plugin %s contract is invalid: %v", p.name, err)
			}
		}
	})

	t.Run("rejects unsafe dependency config verify and path contracts", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*projectFeaturePlugin)
			want   string
		}{
			{name: "absolute path", mutate: func(p *projectFeaturePlugin) {
				p.files = map[string]string{filepath.Join(string(filepath.Separator), "tmp", "owned.go"): "package bad"}
			}, want: "must be relative"},
			{name: "parent traversal path", mutate: func(p *projectFeaturePlugin) {
				p.files = map[string]string{filepath.Join("..", "owned.go"): "package bad"}
			}, want: "escapes output directory"},
			{name: "windows parent traversal path", mutate: func(p *projectFeaturePlugin) {
				p.files = map[string]string{`..\owned.go`: "package bad"}
			}, want: "escapes output directory"},
			{name: "windows drive path", mutate: func(p *projectFeaturePlugin) {
				p.files = map[string]string{`C:\tmp\owned.go`: "package bad"}
			}, want: "must be relative"},
			{name: "dependency without version", mutate: func(p *projectFeaturePlugin) { p.dependencies = []string{"github.com/example/pkg"} }, want: "unsafe dependency"},
			{name: "dependency with shell metacharacter", mutate: func(p *projectFeaturePlugin) { p.dependencies = []string{"github.com/example/pkg@latest;rm"} }, want: "unsafe dependency"},
			{name: "dependency with extra version separator", mutate: func(p *projectFeaturePlugin) { p.dependencies = []string{"github.com/example/pkg@latest@evil"} }, want: "unsafe dependency"},
			{name: "dependency with URL scheme", mutate: func(p *projectFeaturePlugin) { p.dependencies = []string{"https://example.com/pkg@latest"} }, want: "unsafe dependency"},
			{name: "invalid config key", mutate: func(p *projectFeaturePlugin) {
				p.configHints = []ConfigHint{{Key: "jwt-secret", Description: "secret"}}
			}, want: "unsafe config hint"},
			{name: "empty config description", mutate: func(p *projectFeaturePlugin) { p.configHints = []ConfigHint{{Key: "JWT_SECRET"}} }, want: "unsafe config hint"},
			{name: "unsupported verify command", mutate: func(p *projectFeaturePlugin) { p.verifyCommands = []string{"sh -c go test ./..."} }, want: "unsupported verify command"},
			{name: "hardcoded secret template", mutate: func(p *projectFeaturePlugin) {
				p.files = map[string]string{filepath.Join("internal", "bad", "bad.go"): "package bad\nconst password=\"super-secret\""}
			}, want: "forbidden security-sensitive content"},
			{name: "network pipe template", mutate: func(p *projectFeaturePlugin) {
				p.files = map[string]string{"Dockerfile": "FROM scratch\nRUN curl https://example.invalid/install.sh | sh"}
			}, want: "forbidden security-sensitive content"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				plugin := projectFeaturePlugin{
					name:           "contract-test",
					tags:           []string{"contract-test"},
					files:          map[string]string{filepath.Join("internal", "contract", "contract.go"): "package contract"},
					dependencies:   []string{"github.com/example/pkg@latest"},
					configHints:    []ConfigHint{{Key: "CONTRACT_TEST", Description: "contract test value"}},
					verifyCommands: []string{"go test ./..."},
				}
				tt.mutate(&plugin)
				err := validateProjectFeaturePluginContract(plugin)
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("validateProjectFeaturePluginContract error = %v, want containing %q", err, tt.want)
				}
			})
		}
	})
}

func TestProjectFeatureLibraryIsDeterministicAndIdempotent(t *testing.T) {
	features := []string{"redis", "rag", "postgres", "observability"}
	firstDir := filepath.Join(t.TempDir(), "first")
	secondDir := filepath.Join(t.TempDir(), "second")

	first, err := ApplyProjectFeaturePlugins(ProjectFeatureOptions{Dir: firstDir, Name: "kb", Module: "example.com/kb", Features: features})
	if err != nil {
		t.Fatalf("first ApplyProjectFeaturePlugins: %v", err)
	}
	second, err := ApplyProjectFeaturePlugins(ProjectFeatureOptions{Dir: secondDir, Name: "kb", Module: "example.com/kb", Features: features})
	if err != nil {
		t.Fatalf("second ApplyProjectFeaturePlugins: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("feature results are not deterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}

	firstSnapshot := projectFeatureFileSnapshot(t, firstDir, first)
	secondSnapshot := projectFeatureFileSnapshot(t, secondDir, second)
	if !reflect.DeepEqual(firstSnapshot, secondSnapshot) {
		t.Fatalf("feature file snapshots are not deterministic:\nfirst=%+v\nsecond=%+v", firstSnapshot, secondSnapshot)
	}

	reapplied, err := ApplyProjectFeaturePlugins(ProjectFeatureOptions{Dir: firstDir, Name: "kb", Module: "example.com/kb", Features: features})
	if err != nil {
		t.Fatalf("reapply ApplyProjectFeaturePlugins: %v", err)
	}
	if !reflect.DeepEqual(first, reapplied) {
		t.Fatalf("feature results are not idempotent:\nfirst=%+v\nreapplied=%+v", first, reapplied)
	}
	reappliedSnapshot := projectFeatureFileSnapshot(t, firstDir, reapplied)
	if !reflect.DeepEqual(firstSnapshot, reappliedSnapshot) {
		t.Fatalf("feature file snapshot changed after reapply:\nfirst=%+v\nreapplied=%+v", firstSnapshot, reappliedSnapshot)
	}
}

func projectFeatureFileSnapshot(t *testing.T, dir string, results []ProjectFeatureResult) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	for _, result := range results {
		for _, file := range result.Files {
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
			if err != nil {
				t.Fatalf("read generated feature file %s: %v", file, err)
			}
			snapshot[file] = string(data)
		}
	}
	return snapshot
}
