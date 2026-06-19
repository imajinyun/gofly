package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtensionFeatureBoundaries(t *testing.T) {
	if RegisterFeature("", func(ExtensionScope) ExtensionPatch { return ExtensionPatch{} }) {
		t.Fatal("RegisterFeature blank name = true, want false")
	}
	if RegisterFeature("bits-ut-nil", nil) {
		t.Fatal("RegisterFeature nil function = true, want false")
	}

	firstName := "bits-ut-feature-a"
	secondName := "bits-ut-feature-b"
	if !HasFeature(firstName) {
		if ok := RegisterFeature(firstName, func(scope ExtensionScope) ExtensionPatch {
			return ExtensionPatch{
				ExtraFiles: map[string]string{filepath.Join("internal", "feature", "a.go"): scope.Name},
				DataMerge:  map[string]string{"order": "a", "service": scope.Name},
			}
		}); !ok {
			t.Fatalf("RegisterFeature(%q) = false, want true", firstName)
		}
	}
	if !HasFeature(secondName) {
		if ok := RegisterFeature(secondName, func(scope ExtensionScope) ExtensionPatch {
			return ExtensionPatch{
				OverrideFiles: map[string]string{"main.go": "from-b"},
				DataMerge:     map[string]string{"order": "b"},
			}
		}); !ok {
			t.Fatalf("RegisterFeature(%q) = false, want true", secondName)
		}
	}
	if RegisterFeature(firstName, func(ExtensionScope) ExtensionPatch { return ExtensionPatch{} }) {
		t.Fatal("RegisterFeature duplicate = true, want false")
	}
	features := ListFeatures()
	if !containsFeatureName(features, firstName) || !containsFeatureName(features, secondName) || !sortStringsAreSorted(features) {
		t.Fatalf("ListFeatures = %v, want registered test features sorted", features)
	}

	files, data := ApplyFeatures(
		ExtensionScope{Name: "orders", Features: map[string]bool{secondName: true, "missing-feature": true, firstName: true, "disabled": false}},
		map[string]string{"main.go": "base"},
		map[string]string{"base": "1"},
	)
	if files["main.go"] != "from-b" || files[filepath.Join("internal", "feature", "a.go")] != "orders" {
		t.Fatalf("ApplyFeatures files = %#v, want override and extra file", files)
	}
	if data["base"] != "1" || data["service"] != "orders" || data["order"] != "b" {
		t.Fatalf("ApplyFeatures data = %#v, want sorted patch merge", data)
	}

	normalized := normalizeFeatureNames([]string{" ", firstName, firstName, secondName})
	if strings.Join(normalized, ",") != firstName+","+secondName {
		t.Fatalf("normalizeFeatureNames = %v, want trimmed deduplicated names", normalized)
	}
	if err := ValidateFeatureNames([]string{firstName, "missing-feature"}); err == nil || !strings.Contains(err.Error(), "missing-feature") {
		t.Fatalf("ValidateFeatureNames missing error = %v, want missing feature", err)
	}
	if _, _, err := applyFeatureNames([]string{firstName, "missing-feature"}, ExtensionScope{Name: "orders"}, nil, nil); err == nil || !strings.Contains(err.Error(), "missing-feature") {
		t.Fatalf("applyFeatureNames missing error = %v, want missing feature", err)
	}
	files, data, err := ApplyFeatureNames([]string{secondName, firstName}, ExtensionScope{Name: "orders"}, map[string]string{"main.go": "base"}, map[string]string{})
	if err != nil {
		t.Fatalf("ApplyFeatureNames: %v", err)
	}
	if files["main.go"] != "from-b" || data["service"] != "orders" {
		t.Fatalf("ApplyFeatureNames = files %#v data %#v, want applied patches", files, data)
	}
}

func TestLoadTemplateExtensionBoundaries(t *testing.T) {
	ext, err := LoadTemplateExtension("")
	if err != nil || len(ext.FeatureNames) != 0 || len(ext.Dependencies) != 0 {
		t.Fatalf("LoadTemplateExtension blank = %#v/%v, want empty extension", ext, err)
	}

	dir := t.TempDir()
	ext, err = LoadTemplateExtension(dir)
	if err != nil || len(ext.FeatureNames) != 0 || len(ext.Dependencies) != 0 {
		t.Fatalf("LoadTemplateExtension missing file = %#v/%v, want empty extension", ext, err)
	}

	manifest := `features: [http-compat, rpc-compat, ""]
dependencies: {github.com/example/foo: v1.2.3}
`
	if err := os.WriteFile(filepath.Join(dir, ExtensionFileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	ext, err = LoadTemplateExtension(dir)
	if err != nil {
		t.Fatalf("LoadTemplateExtension manifest: %v", err)
	}
	if strings.Join(ext.FeatureNames, ",") != "http-compat,rpc-compat" {
		t.Fatalf("FeatureNames = %v, want http-compat,rpc-compat", ext.FeatureNames)
	}
	if ext.Dependencies == nil || len(ext.Dependencies) != 0 {
		t.Fatalf("Dependencies = %#v, want zero-dependency lightweight parser map", ext.Dependencies)
	}
}

func containsFeatureName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortStringsAreSorted(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] > values[i] {
			return false
		}
	}
	return true
}

func TestParseLightweightYAML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{name: "empty", in: "", want: map[string]string{}},
		{name: "comments and flat map", in: "# ignored\nname: demo\nversion: v1\n", want: map[string]string{"name": "demo", "version": "v1"}},
		{name: "top level list key only", in: "features:\n  - audit\n  - metrics\n", want: map[string]string{"features": ""}},
		{name: "bad line skipped", in: "name demo\nkind: api\n", want: map[string]string{"kind": "api"}},
		{name: "indented lines skipped", in: "name: demo\n  child: ignored\n", want: map[string]string{"name": "demo"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLightweightYAML([]byte(tt.in))
			if err != nil {
				t.Fatalf("parseLightweightYAML() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseLightweightYAML() = %#v, want %#v", got, tt.want)
			}
			for k, want := range tt.want {
				if got[k] != want {
					t.Fatalf("parseLightweightYAML()[%q] = %q, want %q", k, got[k], want)
				}
			}
		})
	}
}

func TestSplitYAMLList(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "empty brackets", in: "[]", want: nil},
		{name: "inline list", in: `["audit", metrics, ""]`, want: []string{"audit", "metrics"}},
		{name: "unsupported block list", in: "- audit\n- metrics", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitYAMLList(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("splitYAMLList(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Fatalf("splitYAMLList(%q)[%d] = %q, want %q", tt.in, i, got[i], want)
				}
			}
		})
	}
}

func TestParseYAMLMapReturnsEmptyMap(t *testing.T) {
	got := parseYAMLMap("name: demo")
	if got == nil {
		t.Fatal("parseYAMLMap returned nil, want empty map")
	}
	if len(got) != 0 {
		t.Fatalf("parseYAMLMap() = %#v, want empty map", got)
	}
}
