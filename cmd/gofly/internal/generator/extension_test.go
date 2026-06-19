package generator

import "testing"

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
