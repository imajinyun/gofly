package main

import "testing"

func TestMigrationProofReport_BitsUT(t *testing.T) {
	report := buildReport()
	if report.Schema != schema {
		t.Fatalf("schema = %q, want %q", report.Schema, schema)
	}
	if len(report.Cases) != 4 || len(report.Rollbacks) != 4 {
		t.Fatalf("cases=%d rollbacks=%d, want 4 each", len(report.Cases), len(report.Rollbacks))
	}
	wantSources := map[string]string{
		"gin":     "examples/restserver",
		"go-zero": "examples/production-orders",
		"kratos":  "examples/microshop",
		"kitex":   "examples/rpc-idl-matrix",
	}
	for _, item := range report.Cases {
		if wantSources[item.Source] != item.Example {
			t.Fatalf("case %q example = %q, want %q", item.Source, item.Example, wantSources[item.Source])
		}
		if item.Rollback == "" || len(item.Validation) == 0 || len(item.Compatibility) == 0 {
			t.Fatalf("case %q is missing rollback, validation, or compatibility: %+v", item.Source, item)
		}
		if len(item.GateCommands) == 0 || len(item.CompatibilityCaveats) == 0 {
			t.Fatalf("case %q is missing gate commands or compatibility caveats: %+v", item.Source, item)
		}
		if item.DecisionTable.ChooseWhen == "" ||
			item.DecisionTable.KeepSourceWhen == "" ||
			item.DecisionTable.AdopterAction == "" ||
			item.DecisionTable.RollbackTrigger == "" {
			t.Fatalf("case %q decision table is incomplete: %+v", item.Source, item.DecisionTable)
		}
		delete(wantSources, item.Source)
	}
	if len(wantSources) != 0 {
		t.Fatalf("missing migration sources: %v", wantSources)
	}
}
