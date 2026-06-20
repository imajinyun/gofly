package generator

import (
	"os"
	"path/filepath"
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
