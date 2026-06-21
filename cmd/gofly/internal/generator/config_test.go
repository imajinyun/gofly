package generator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopySortedMapReturnsIndependentCopy(t *testing.T) {
	src := map[string]string{"b": "2", "a": "1"}
	got := copySortedMap(src)
	src["a"] = "changed"

	if got["a"] != "1" {
		t.Fatalf("copySortedMap reused source map, got a=%q", got["a"])
	}
	if got["b"] != "2" {
		t.Fatalf("copySortedMap missing b, got %q", got["b"])
	}
	if len(got) != 2 {
		t.Fatalf("copySortedMap len = %d, want 2", len(got))
	}
}

func TestCopySortedMapHandlesNilInput(t *testing.T) {
	got := copySortedMap(nil)
	if got == nil {
		t.Fatal("copySortedMap(nil) returned nil map, want writable empty map")
	}
	got["key"] = "value"
	if got["key"] != "value" {
		t.Fatalf("copySortedMap(nil) returned non-writable map: %#v", got)
	}
}

func TestConfigSaveOverlayAndStableStringBoundaries(t *testing.T) {
	if err := SaveConfig("", DefaultConfig("orders", "example.com/orders")); err == nil || !strings.Contains(err.Error(), "config path is required") {
		t.Fatalf("SaveConfig blank path error = %v, want required path", err)
	}
	if err := SaveConfig(filepath.Join(t.TempDir(), "config.json"), nil); err == nil || !strings.Contains(err.Error(), "config is nil") {
		t.Fatalf("SaveConfig nil config error = %v, want nil config", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, ".gofly", "config.json")
	cfg := DefaultConfig("orders", "example.com/orders")
	cfg.Features = []string{"rpc-compat", "http-compat"}
	cfg.Dependencies = map[string]string{"z": "2", "a": "1"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("saved config should be valid JSON: %v\n%s", err, data)
	}
	if saved.GeneratedBy != "gofly" || cfg.GeneratedBy != "gofly" {
		t.Fatalf("GeneratedBy saved/current = %q/%q, want gofly", saved.GeneratedBy, cfg.GeneratedBy)
	}

	base := &Config{
		ServiceName:    "base",
		Module:         "example.com/base",
		Style:          ServiceStyleBasic,
		TemplateDir:    "base/templates",
		TemplateRemote: "https://example.com/base.git",
		TemplateBranch: "main",
		Features:       []string{"ecosystem-compat", "rpc-compat"},
		RPC:            &RPCConfig{Transport: "grpc", Profile: string(ProfileKitexCompatible), Includes: []string{"z.proto", "a.proto"}, Plugins: []string{"z", "a"}},
		API:            &APIConfig{Plugins: []string{"z", "a"}, Middleware: []string{"trace", "auth"}},
		Model:          &ModelConfig{IgnoreColumns: []string{"updated_at", "created_at"}, TypesMap: map[string]string{"uuid": "string", "jsonb": "[]byte"}},
		LLM:            &LLMConfig{Provider: "noop", Model: "noop"},
		Dependencies:   map[string]string{"z": "2", "a": "1"},
		Extra:          map[string]string{"z": "2", "a": "1"},
	}
	overlay := base.ApplyOverlay("orders", "example.com/orders", ServiceStyleProduction, "override/templates", []string{"rpc-compat", "", "audit"})
	if overlay == base || overlay.ServiceName != "orders" || overlay.Module != "example.com/orders" || overlay.Style != ServiceStyleProduction || overlay.TemplateDir != "override/templates" {
		t.Fatalf("ApplyOverlay identity fields = %#v", overlay)
	}
	if strings.Join(overlay.Features, ",") != "ecosystem-compat,rpc-compat,audit" {
		t.Fatalf("ApplyOverlay features = %v, want merged deduplicated features", overlay.Features)
	}
	if base.RPC == overlay.RPC || base.API == overlay.API || base.LLM == overlay.LLM {
		t.Fatalf("ApplyOverlay reused nested config pointers: base=%#v overlay=%#v", base, overlay)
	}
	overlay.RPC.Transport = "http"
	overlay.LLM.Provider = "custom"
	if base.RPC.Transport != "grpc" || base.LLM.Provider != "noop" {
		t.Fatalf("ApplyOverlay pointer mutation leaked to base: base=%#v overlay=%#v", base, overlay)
	}

	overlay2 := base.ApplyOverlayWithTemplateSource("", "", "", "", "https://example.com/templates.git", "release", []string{"http-compat"})
	if overlay2.TemplateRemote != "https://example.com/templates.git" || overlay2.TemplateBranch != "release" {
		t.Fatalf("ApplyOverlayWithTemplateSource template source = %#v", overlay2)
	}
	if got := (*Config)(nil).String(); got != "{}" {
		t.Fatalf("nil Config.String = %q, want {}", got)
	}

	stable := base.String()
	for _, want := range []string{
		`"features": [`,
		`"ecosystem-compat"`,
		`"rpc-compat"`,
		`"profile": "kitex-compatible"`,
		`"dependencies": {`,
		`"a": "1"`,
		`"z": "2"`,
		`"includes": [`,
		`"a.proto"`,
		`"z.proto"`,
		`"ignoreColumns": [`,
		`"created_at"`,
		`"updated_at"`,
	} {
		if !strings.Contains(stable, want) {
			t.Fatalf("Config.String missing %q:\n%s", want, stable)
		}
	}
	if strings.Index(stable, `"a": "1"`) > strings.Index(stable, `"z": "2"`) {
		t.Fatalf("Config.String map output not stable sorted:\n%s", stable)
	}
}
