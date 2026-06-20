package generator

import "testing"

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
