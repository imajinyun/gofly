package main

import "testing"

func TestMigrationProofReport(t *testing.T) {
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
	wantKinds := map[string]string{
		"gin":     "gin-rest-migration",
		"go-zero": "gozero-api-migration",
		"kratos":  "kratos-app-migration",
		"kitex":   "kitex-coexistence",
	}
	for _, item := range report.Cases {
		if wantSources[item.Source] != item.Example {
			t.Fatalf("case %q example = %q, want %q", item.Source, item.Example, wantSources[item.Source])
		}
		if wantKinds[item.Source] != item.MigrationKind {
			t.Fatalf("case %q migrationKind = %q, want %q", item.Source, item.MigrationKind, wantKinds[item.Source])
		}
		if item.Rollback == "" || len(item.Validation) == 0 || len(item.Compatibility) == 0 {
			t.Fatalf("case %q is missing rollback, validation, or compatibility: %+v", item.Source, item)
		}
		if len(item.GateCommands) == 0 || len(item.CompatibilityCaveats) == 0 {
			t.Fatalf("case %q is missing gate commands or compatibility caveats: %+v", item.Source, item)
		}
		if item.PerformanceBoundary == "" || item.GovernanceBoundary == "" {
			t.Fatalf("case %q is missing performance or governance boundary: %+v", item.Source, item)
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
	p13WantSources := map[string]string{
		"gin":     "examples/restserver",
		"go-zero": "examples/production-orders",
		"kratos":  "examples/microshop",
		"kitex":   "examples/rpc-idl-matrix",
	}
	if report.P13MigrationUpgrade.Schema != "gofly.migration_case_upgrade_p13.v1" ||
		report.P13MigrationUpgrade.AiflowTask != "GOFLY-P13-09-MIGRATION-CASE-UPGRADE" ||
		report.P13MigrationUpgrade.Status != "blocking" {
		t.Fatalf("P13 migration upgrade contract = %+v, want blocking P13 schema", report.P13MigrationUpgrade)
	}
	if len(report.P13MigrationUpgrade.AcceptanceGates) != 2 || len(report.P13MigrationUpgrade.Cases) != 4 {
		t.Fatalf("P13 migration upgrade gates=%v cases=%d, want two gates and four cases", report.P13MigrationUpgrade.AcceptanceGates, len(report.P13MigrationUpgrade.Cases))
	}
	p13Sources := map[string]bool{"gin": false, "go-zero": false, "kratos": false, "kitex": false}
	for _, item := range report.P13MigrationUpgrade.Cases {
		if _, ok := p13Sources[item.Source]; !ok {
			t.Fatalf("unexpected P13 migration source %q", item.Source)
		}
		p13Sources[item.Source] = true
		if item.MigrationKind != wantKinds[item.Source] || item.RunnableExample != p13WantSources[item.Source] {
			t.Fatalf("P13 migration case %q = %+v, want kind/example parity", item.Source, item)
		}
		if item.PrimaryGate == "" || len(item.GateCommands) == 0 || item.RollbackNote == "" || item.CompatibilityCaveat == "" {
			t.Fatalf("P13 migration case %q is missing gate, rollback, or caveat: %+v", item.Source, item)
		}
		if item.FailureReport == "" || item.SupportBundle == "" || item.PerformanceBoundary == "" || item.GovernanceBoundary == "" {
			t.Fatalf("P13 migration case %q is missing failure, support, or boundary evidence: %+v", item.Source, item)
		}
	}
	for source, found := range p13Sources {
		if !found {
			t.Fatalf("P13 migration upgrade missing source %q", source)
		}
	}
	if report.P13MigrationUpgrade.PublishPolicy == "" || report.P13MigrationUpgrade.RollbackPolicy == "" {
		t.Fatalf("P13 migration upgrade policies are required: %+v", report.P13MigrationUpgrade)
	}
}
