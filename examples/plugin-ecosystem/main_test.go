package main

import (
	"context"
	"testing"
)

func TestPluginEcosystemReport_BitsUT(t *testing.T) {
	report, err := buildReport(context.Background())
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if report.Schema != "gofly.plugin_ecosystem.v1" {
		t.Fatalf("schema = %q, want gofly.plugin_ecosystem.v1", report.Schema)
	}
	if report.Protocol != "1" {
		t.Fatalf("protocol = %q, want 1", report.Protocol)
	}
	for _, field := range []string{"name", "version", "protocol", "compatibleVersions", "capabilities", "permissions", "checksum", "source"} {
		if !contains(report.Registry.Fields, field) {
			t.Fatalf("registry fields = %#v, missing %s", report.Registry.Fields, field)
		}
	}
	for _, field := range []string{"name", "version", "compatibleVersions", "capabilities", "permissions", "requiresDryRun"} {
		if !contains(report.Publishing.ManifestFields, field) {
			t.Fatalf("publishing manifest fields = %#v, missing %s", report.Publishing.ManifestFields, field)
		}
	}
	for _, gate := range []string{"make plugin-conformance-check", "go test -C examples/plugin-ecosystem ./...", "go run -C examples/plugin-ecosystem ."} {
		if !contains(report.Publishing.RequiredGates, gate) {
			t.Fatalf("publishing gates = %#v, missing %s", report.Publishing.RequiredGates, gate)
		}
	}
	for _, note := range []string{"protocol compatibility", "digest provenance", "permission rationale", "template contract", "rollback and failure isolation behavior"} {
		if !contains(report.Publishing.ReleaseNotes, note) {
			t.Fatalf("publishing release notes = %#v, missing %s", report.Publishing.ReleaseNotes, note)
		}
	}
	for _, name := range []string{"audit-trail-generator", "company-template-pack"} {
		if !contains(report.Registry.Names, name) {
			t.Fatalf("registry names = %#v, missing %s", report.Registry.Names, name)
		}
	}
	wantCompatibility := map[string]bool{
		"old-protocol":        false,
		"current-protocol":    true,
		"future-plus-current": true,
		"future-only":         false,
	}
	for _, item := range report.Compatibility {
		want, ok := wantCompatibility[item.Name]
		if !ok {
			t.Fatalf("unexpected compatibility case: %#v", item)
		}
		if item.Accepted != want {
			t.Fatalf("compatibility %s accepted = %v, want %v", item.Name, item.Accepted, want)
		}
	}
	for _, example := range report.Examples {
		switch example.Name {
		case "example-file-generator":
			if !contains(example.Capabilities, "generate:file") || !contains(example.Files, "internal/audit/audit.go") {
				t.Fatalf("file example = %#v, want generated audit file", example)
			}
		case "example-patch-generator":
			if !contains(example.Capabilities, "generate:patch") || !contains(example.Patches, "cmd/orders/main.go") {
				t.Fatalf("patch example = %#v, want startup patch", example)
			}
		case "third-party-template-directory":
			if example.Contract != "templates/service/gofly.template.json" {
				t.Fatalf("template example = %#v, want template contract", example)
			}
		}
	}
	for _, boundary := range []string{"version pinned", "sha256 checksums", "relative", "source and protocol"} {
		found := false
		for _, item := range report.Security {
			if containsText(item, boundary) {
				found = true
			}
		}
		if !found {
			t.Fatalf("security boundaries = %#v, missing %s", report.Security, boundary)
		}
	}
}

func containsText(value, want string) bool {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
