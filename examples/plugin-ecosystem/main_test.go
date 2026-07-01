package main

import (
	"context"
	"testing"
)

func TestPluginEcosystemReport(t *testing.T) {
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
	for _, note := range []string{"protocol compatibility", "digest provenance", "signature provenance", "permission rationale", "template contract", "rollback and failure isolation behavior"} {
		if !contains(report.Publishing.ReleaseNotes, note) {
			t.Fatalf("publishing release notes = %#v, missing %s", report.Publishing.ReleaseNotes, note)
		}
	}
	if !contains(report.Publishing.TrustSources, "github-actions-oidc") {
		t.Fatalf("publishing trust sources = %#v, missing github-actions-oidc", report.Publishing.TrustSources)
	}
	if !contains(report.Publishing.SourceAllowlist, "github.com") {
		t.Fatalf("publishing source allowlist = %#v, missing github.com", report.Publishing.SourceAllowlist)
	}
	if report.P13Publishing.Schema != "gofly.plugin_publish_hardening_p13.v1" ||
		report.P13Publishing.AiflowTask != "GOFLY-P13-10-PLUGIN-TEMPLATE-PUBLISH-HARDENING" ||
		report.P13Publishing.Status != "blocking" {
		t.Fatalf("P13 publishing contract = %#v, want blocking P13 schema", report.P13Publishing)
	}
	for _, field := range []string{"checksum", "source", "sourcePolicy", "signature", "manifest"} {
		if !contains(report.P13Publishing.RequiredRegistry, field) {
			t.Fatalf("P13 registry fields = %#v, missing %s", report.P13Publishing.RequiredRegistry, field)
		}
	}
	for _, field := range []string{"compatibleVersions", "capabilities", "permissions", "requiresDryRun"} {
		if !contains(report.P13Publishing.RequiredManifest, field) {
			t.Fatalf("P13 manifest fields = %#v, missing %s", report.P13Publishing.RequiredManifest, field)
		}
	}
	for _, field := range []string{"schema", "contractVersion", "entrypoints", "permissions", "checksum", "source"} {
		if !contains(report.P13Publishing.RequiredTemplate, field) {
			t.Fatalf("P13 template fields = %#v, missing %s", report.P13Publishing.RequiredTemplate, field)
		}
	}
	for _, failure := range []string{"digest-mismatch", "malicious-path", "permission-escape", "no-partial-writes"} {
		if !contains(report.P13Publishing.FailureCases, failure) {
			t.Fatalf("P13 failure cases = %#v, missing %s", report.P13Publishing.FailureCases, failure)
		}
	}
	if !contains(report.P13Publishing.SourceAllowlist, "github.com") ||
		!contains(report.P13Publishing.SignatureTrust, "github-actions-oidc") ||
		!containsText(report.P13Publishing.NoPartialWritePolicy, "partial writes") {
		t.Fatalf("P13 publishing trust policy incomplete: %#v", report.P13Publishing)
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
