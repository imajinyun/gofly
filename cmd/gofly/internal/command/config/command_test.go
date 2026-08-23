package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
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
