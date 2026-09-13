package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func TestConfigCommandDryRunDoesNotWriteConfig(t *testing.T) {
	dir := t.TempDir()
	var plans []string
	err := Command([]string{"init", "--dir", dir, "--name", "hello", "--module", "example.com/hello", "--dry-run"}, testConfigHooks(&plans))
	if err != nil {
		t.Fatalf("config init --dry-run: %v", err)
	}
	if len(plans) != 1 || plans[0] != "config.init" {
		t.Fatalf("plans = %v, want config.init", plans)
	}
	if _, err := os.Stat(filepath.Join(dir, generator.DefaultConfigFile)); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote config file, stat err=%v", err)
	}
}

func TestConfigCommandUsageError(t *testing.T) {
	hooks := testConfigHooks(nil)
	err := Command(nil, hooks)
	if err == nil || !errors.Is(err, hooks.Usage) {
		t.Fatalf("Command(nil) error = %v, want usage", err)
	}
}

func TestConfigFields(t *testing.T) {
	for _, tc := range []struct{ name, key, value, want, empty string }{
		{"service", " Service-Name ", "demo", "demo", ""},
		{"module", "module", "example.com/demo", "example.com/demo", ""},
		{"style", "style", "production", "production", ""},
		{"templates", "templates", "templates", "templates", ""},
		{"go version", "go-version", "1.26", "1.26", ""},
		{"empty features", "features", " , ", "", ""},
		{"rpc plugins", "rpc-plugins", " one, ,two ", "one,two", ""},
		{"rpc transport", "rpc.transport", "grpc", "grpc", ""},
		{"rpc profile", "profile", "gozero-compatible", "gozero-compatible", ""},
		{"api plugins", "api.plugins", "a,b", "a,b", ""},
		{"api profile", "api-profile", "production", "production", ""},
		{"model style", "model-style", "gozero", "gozero", ""},
		{"model columns", "model-ignore-columns", "id, created_at", "id,created_at", ""},
		{"model types", "model-types-map", " z = int, a=string,empty,=ignored,,z=bool", "a=string,empty=,z=bool", ""},
		{"model cache", "model-cache", " YES ", "true", "false"},
		{"model strict", "model.strict", "on", "true", "false"},
		{"llm provider", "llm-provider", "noop", "noop", ""},
		{"llm model", "llm.model", "small", "small", ""},
		{"input tokens", "llm-max-input-tokens", "12", "12", "0"},
		{"output tokens", "llm.maxoutputtokens", "13", "13", "0"},
		{"total tokens", "llm.max-total-tokens", "14", "14", "0"},
		{"rate", "llm-rate-limit", "15", "15", "0"},
		{"burst", "llm.rateburst", "16", "16", "0"},
		{"timeout", "llm-timeout", "2s", "2s", ""},
		{"extension", "Custom", "value", "value", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &generator.Config{}
			if got := GetField(cfg, tc.key); got != tc.empty {
				t.Fatalf("empty %s=%q want %q", tc.key, got, tc.empty)
			}
			for range 2 {
				if err := SetField(cfg, tc.key, tc.value, errors.New("usage")); err != nil {
					t.Fatal(err)
				}
				if got := GetField(cfg, tc.key); got != tc.want {
					t.Fatalf("%s=%q want %q", tc.key, got, tc.want)
				}
			}
		})
	}
	t.Run("invalid values preserve config", func(t *testing.T) {
		usage := errors.New("usage")
		for _, key := range []string{"llm.maxinputtokens", "llm.maxoutputtokens", "llm.maxtotaltokens", "llm.ratelimit", "llm.rateburst", "llm.timeout"} {
			for _, value := range []string{"invalid", ""} {
				if key == "llm.timeout" && value == "" {
					continue
				}
				cfg := &generator.Config{}
				if err := SetField(cfg, key, value, usage); !errors.Is(err, usage) {
					t.Fatalf("%s=%q err=%v", key, value, err)
				}
				if cfg.LLM != nil {
					t.Fatalf("invalid %s allocated config", key)
				}
			}
		}
		cfg := &generator.Config{}
		if err := SetField(cfg, "features", "not-registered-config-test", usage); err == nil {
			t.Fatal("unknown feature accepted")
		}
		if err := SetField(cfg, "llm.maxinputtokens", "-1", usage); !errors.Is(err, usage) {
			t.Fatalf("negative value: %v", err)
		}
		if err := SetField(cfg, "llm.timeout", "", usage); err != nil || cfg.LLM.Timeout != "" {
			t.Fatalf("clear timeout: %v", err)
		}
	})
	t.Run("helper defaults", func(t *testing.T) {
		cfg := &generator.Config{}
		model := EnsureModelConfig(cfg)
		model.TypesMap["id"] = "string"
		if EnsureModelConfig(cfg) != model || model.TypesMap["id"] != "string" {
			t.Fatal("existing model replaced")
		}
		if encodeStringMap(nil) != "" || parseBoolString("false") || !IsFeaturesKey(" Features ") || IsFeaturesKey("feature") {
			t.Fatal("incorrect empty/boolean/key defaults")
		}
		if GetField(&generator.Config{Extra: map[string]string{"other": "value"}}, "missing") != "" {
			t.Fatal("missing extension returned value")
		}
	})
}

func TestConfigCommandLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, generator.DefaultConfigFile)
	var output bytes.Buffer
	var plans []Plan
	hooks := Hooks{
		PrintTextf:  func(format string, args ...any) { _, _ = fmt.Fprintf(&output, format, args...) },
		PrintTextln: func(args ...any) { _, _ = fmt.Fprintln(&output, args...) },
		PrintPlan:   func(_ string, plan Plan) error { plans = append(plans, plan); return nil },
	}
	run := func(args ...string) {
		t.Helper()
		output.Reset()
		args = append(args[:1], append([]string{"--dir", dir}, args[1:]...)...)
		if err := Command(args, hooks); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	run("init", "--name", "demo", "--module", "example.com/demo", "--style", "production")
	run("get", "service")
	if output.String() != "demo\n" {
		t.Fatalf("get=%q", output.String())
	}
	run("set", "service", "changed")
	run("show")
	if !strings.Contains(output.String(), "changed") {
		t.Fatalf("show=%s", output.String())
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	run("set", "--plan", "--key", "module", "--value", "example.com/preview")
	run("clean", "--dry-run")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || len(plans) != 2 || plans[0].Actions[0].Operation != "update-config" || plans[1].Actions[0].Operation != "remove-config" {
		t.Fatalf("dry run changed file or plans: %+v", plans)
	}
	run("set", "--key", "features", "--value=")
	run("clean")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("clean stat=%v", err)
	}
	run("clean")
}

func TestConfigCommandFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		corrupt bool
	}{
		{"unknown command", []string{"unknown"}, false},
		{"bad flag", []string{"init", "--unknown"}, false},
		{"get key missing", []string{"get"}, false},
		{"set key missing", []string{"set"}, false},
		{"set value missing", []string{"set", "--key", "module"}, false},
		{"empty non feature", []string{"set", "--key", "module", "--value="}, false},
		{"show malformed", []string{"show"}, true},
		{"get malformed", []string{"get", "--key", "module"}, true},
		{"set malformed", []string{"set", "--key", "module", "--value", "x"}, true},
		{"set invalid", []string{"set", "--key", "llm.timeout", "--value", "invalid"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.corrupt {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, generator.DefaultConfigFile)), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, generator.DefaultConfigFile), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{tc.args[0], "--dir", dir}, tc.args[1:]...)
			if err := Command(args, testConfigHooks(nil)); err == nil {
				t.Fatalf("%v accepted", args)
			}
		})
	}
	t.Run("write and remove errors", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, generator.DefaultConfigFile)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{"init", "clean"} {
			if err := Command([]string{command, "--dir", dir}, Hooks{}); err == nil {
				t.Fatalf("%s accepted directory", command)
			}
		}
	})
	t.Run("help short circuits", func(t *testing.T) {
		if err := Command(nil, Hooks{PrintHelp: func(string, []string) bool { return true }}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("empty dir is current directory", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if err := Command([]string{"init", "--dir=", "--plan"}, Hooks{}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(generator.DefaultConfigFile); !os.IsNotExist(err) {
			t.Fatalf("plan wrote config: %v", err)
		}
	})
	t.Run("plan error propagates", func(t *testing.T) {
		want := errors.New("output failure")
		if err := Command([]string{"init", "--dry-run"}, Hooks{PrintPlan: func(string, Plan) error { return want }}); !errors.Is(err, want) {
			t.Fatalf("plan err=%v", err)
		}
	})
}

func TestConfigDefaultHooks(t *testing.T) {
	hooks := normalizeHooks(Hooks{})
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	if _, err := hooks.ParseFlags(fs, []string{"--missing"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
	if err := hooks.PrintPlan("config.init", configPlan("init", "path", true, nil, nil)); err != nil {
		t.Fatal(err)
	}
	hooks.PrintTextf("%s", "")
	hooks.PrintTextln()
	if valueFromBoolFlag(nil) {
		t.Fatal("nil bool was true")
	}
	plan := configPlan("init", "path", true, nil, nil)
	if !reflect.DeepEqual(plan.Inputs, map[string]string{"path": "path"}) {
		t.Fatalf("inputs=%v", plan.Inputs)
	}
}

func testConfigHooks(plans *[]string) Hooks {
	usage := errors.New("invalid usage")
	return Hooks{
		PrintHelp: func(string, []string) bool { return false },
		ParseFlags: func(fs *flag.FlagSet, args []string) ([]string, error) {
			if err := fs.Parse(args); err != nil {
				return nil, err
			}
			return fs.Args(), nil
		},
		RegisterDryRunFlags: func(fs *flag.FlagSet, usage string) func() bool {
			dryRun := fs.Bool("dry-run", false, usage)
			plan := fs.Bool("plan", false, "alias for --dry-run")
			return func() bool { return *dryRun || *plan }
		},
		PrintPlan: func(command string, _ Plan) error {
			if plans != nil {
				*plans = append(*plans, command)
			}
			return nil
		},
		PrintTextf:  func(string, ...any) {},
		PrintTextln: func(...any) {},
		Usage:       usage,
	}
}
