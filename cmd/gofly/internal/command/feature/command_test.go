package feature

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func TestCommand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		command string
		wantErr bool
	}{
		{"list text", []string{"list"}, "", false},
		{"list json", []string{"ls", "--json"}, "feature.list", false},
		{"run text", []string{"run", "http-compat"}, "", false},
		{"run json", []string{"run", "--json", "http-compat"}, "feature.run", false},
		{"run flags", []string{"run", "--json", "--feature", "http-compat", "--features", "rpc-compat"}, "feature.run", false},
		{"missing command", nil, "", true},
		{"unknown command", []string{"unknown"}, "", true},
		{"bad list flag", []string{"list", "--bad"}, "", true},
		{"bad run flag", []string{"run", "--bad"}, "", true},
		{"missing feature", []string{"run", "--json"}, "feature.run", true},
		{"unknown feature", []string{"run", "missing-test-feature", "--json"}, "feature.run", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			var out bytes.Buffer
			var command string
			var value any
			hooks := Hooks{
				PrintTextlnIf:     func(args ...any) { _, _ = fmt.Fprintln(&out, args...) },
				PrintTextfIf:      func(format string, args ...any) { _, _ = fmt.Fprintf(&out, format, args...) },
				PrintJSONEnvelope: func(c string, v any) error { command, value = c, v; return nil },
				PrintJSONError:    func(c string, err error) error { command = c; return err },
			}
			err := Command(tc.args, hooks)
			if (err != nil) != tc.wantErr || command != tc.command {
				t.Fatalf("args=%v err=%v command=%q want=%q", tc.args, err, command, tc.command)
			}
			if !tc.wantErr {
				switch command {
				case "feature.list":
					got, ok := value.(featureListPreview)
					if !ok || !reflect.DeepEqual(got.Features, generator.ListFeatures()) {
						t.Fatalf("list=%#v", value)
					}
				case "feature.run":
					got, ok := value.(featureRunPreview)
					if !ok || len(got.Features) == 0 || len(got.Files) == 0 {
						t.Fatalf("run=%#v", value)
					}
				default:
					if tc.args[0] == "list" && !strings.Contains(out.String(), "http-compat") {
						t.Fatalf("list=%s", out.String())
					}
					if tc.args[0] == "run" && !strings.Contains(out.String(), "# file:") {
						t.Fatalf("run=%s", out.String())
					}
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("preview wrote files: %v %v", entries, err)
			}
		})
	}
	t.Run("help bypasses execution", func(t *testing.T) {
		if err := Command(nil, Hooks{PrintHelp: func(string, []string) bool { return true }}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("output errors", func(t *testing.T) {
		want := errors.New("output failure")
		for _, args := range [][]string{{"list", "--json"}, {"run", "http-compat", "--json"}} {
			if err := Command(args, Hooks{PrintJSONEnvelope: func(string, any) error { return want }}); !errors.Is(err, want) {
				t.Fatalf("%v err=%v", args, err)
			}
		}
	})
	t.Run("default output hooks", func(t *testing.T) {
		for _, args := range [][]string{{"list"}, {"list", "--json"}, {"run", "http-compat"}} {
			if err := Command(args, Hooks{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := Command([]string{"run", "--json"}, Hooks{}); err == nil {
			t.Fatal("missing feature accepted")
		}
	})
}

func TestBuildFeatureRunPreview(t *testing.T) {
	names := []string{"one", "two"}
	got := buildFeatureRunPreview(names, map[string]string{"z.go": "xyz", "a.go": "a"}, map[string]string{"z": "last", "a": "first"})
	names[0] = "changed"
	want := featureRunPreview{Features: []string{"one", "two"}, Files: []featureFilePreview{{Path: "a.go", Bytes: 1}, {Path: "z.go", Bytes: 3}}, Data: []featureDataPreview{{Key: "a", Value: "first"}, {Key: "z", Value: "last"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preview=%+v want=%+v", got, want)
	}
	if empty := buildFeatureRunPreview(nil, nil, nil); len(empty.Files) != 0 || len(empty.Data) != 0 {
		t.Fatalf("empty=%+v", empty)
	}
	if got := splitCSV(joinCSV(" a, ", "b", "")); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("names=%v", got)
	}
}
