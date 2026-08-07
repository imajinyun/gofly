package main

import "testing"

func TestGoZeroBasicMigrationReport(t *testing.T) {
	report := buildReport()
	if report.Schema != schema || report.SourceFramework != "go-zero" || report.TargetProfile != "gozero-compatible" {
		t.Fatalf("identity = %+v", report)
	}
	for _, input := range []string{"service.api", "service.proto", "schema.sql", "etc/service-api.yaml"} {
		if !contains(report.ExampleInputs, input) {
			t.Fatalf("example inputs = %v, missing %s", report.ExampleInputs, input)
		}
	}
	for source, target := range map[string]string{
		".api":               "gofly api gen --profile gozero-compatible",
		"svc.ServiceContext": "internal/svc/servicecontext.go",
		"model/cache":        "gofly model gen --style go_zero",
		"zrpc proto":         "gofly rpc gen or rpc protoc with zrpc compatibility review",
	} {
		if report.LayoutMapping[source] != target {
			t.Fatalf("layout mapping[%s] = %q, want %q", source, report.LayoutMapping[source], target)
		}
	}
	for _, evidence := range []string{
		"docs/reference/from-go-zero-migration.md",
		"docs/reference/goctl-real-project-replay.json",
		"docs/reference/zrpc-proto-compatibility.json",
		"docs/reference/api-client-generation.md",
	} {
		if !contains(report.Evidence, evidence) {
			t.Fatalf("evidence = %v, missing %s", report.Evidence, evidence)
		}
	}
	for _, boundary := range []string{
		"external proto imports are recorded but not recursively parsed into generated DTOs",
		"google well-known protobuf types are currently a degraded zRPC compatibility row",
		"standard REST profile does not silently enable rate limiters, breakers, adaptive limiters, or max-concurrency guards",
	} {
		if !contains(report.Boundaries, boundary) {
			t.Fatalf("boundaries = %v, missing %s", report.Boundaries, boundary)
		}
	}
	if report.Rollback == "" || len(report.AdopterChecklist) < 5 || len(report.Commands) < 5 {
		t.Fatalf("rollback/checklist/commands are incomplete: %+v", report)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
