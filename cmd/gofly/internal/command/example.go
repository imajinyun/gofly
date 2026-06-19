package command

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// exampleInfo describes one built-in example.
type exampleInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

var builtInExamples = []exampleInfo{
	{Name: "gateway-discovery-rpc", Path: "examples/gateway-discovery-rpc", Description: "API gateway with service discovery and RPC backend"},
	{Name: "k8s", Path: "examples/k8s", Description: "Kubernetes deployment and service manifests"},
	{Name: "model-gorm", Path: "examples/model-gorm", Description: "GORM-style SQL model generation"},
	{Name: "model-mongo", Path: "examples/model-mongo", Description: "MongoDB repository skeleton"},
	{Name: "observability", Path: "examples/observability", Description: "Trace, metrics and structured logging demo"},
	{Name: "outbox-mq", Path: "examples/outbox-mq", Description: "Transactional outbox with message queue"},
	{Name: "resilience", Path: "examples/resilience", Description: "Circuit breaker, rate limiter and timeout patterns"},
	{Name: "restserver", Path: "examples/restserver", Description: "REST server with routing and middleware"},
	{Name: "rpcserver", Path: "examples/rpcserver", Description: "gRPC server with gofly RPC runtime"},
	{Name: "saga", Path: "examples/saga", Description: "Saga orchestration and compensation demo"},
}

func exampleCommand(args []string) error {
	if printCommandHelp("example", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly example list|run <name>`", errUsage)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list", "ls":
		return exampleListCommand(rest)
	case "run":
		return exampleRunCommand(rest)
	default:
		return fmt.Errorf("%w: expected `gofly example list|run <name>`", errUsage)
	}
}

func exampleListCommand(args []string) error {
	fs := flag.NewFlagSet("example list", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "output JSON")
	if _, err := parseInterspersedFlags(fs, args); err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(builtInExamples)
	}
	cliOutputln("Built-in examples:")
	for _, ex := range builtInExamples {
		cliOutputf("  %-22s %s\n", ex.Name, ex.Description)
	}
	cliOutputln("\nRun an example:")
	cliOutputln("  gofly example run <name> [--dir <dir>]")
	return nil
}

func exampleRunCommand(args []string) error {
	fs := flag.NewFlagSet("example run", flag.ContinueOnError)
	dir := fs.String("dir", "", "output directory for the example")
	jsonOutput := fs.Bool("json", false, "output JSON result")
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		args = args[1:]
	}
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if name == "" && len(remaining) > 0 {
		name = remaining[0]
	}
	if name == "" {
		return fmt.Errorf("%w: example name is required; run `gofly example list` to see available examples", errUsage)
	}

	var ex *exampleInfo
	for i := range builtInExamples {
		if builtInExamples[i].Name == name {
			ex = &builtInExamples[i]
			break
		}
	}
	if ex == nil {
		return fmt.Errorf("%w: unknown example %q; run `gofly example list` to see available examples", errUsage, name)
	}

	srcDir, err := resolveExampleSourceDir(ex.Path)
	if err != nil {
		return fmt.Errorf("resolve example source: %w", err)
	}

	outDir := *dir
	if outDir == "" {
		outDir = ex.Name
	}
	outDir, err = filepath.Abs(outDir)
	if err != nil {
		return fmt.Errorf("resolve output dir: %w", err)
	}

	if err := copyExampleDir(srcDir, outDir); err != nil {
		return fmt.Errorf("copy example: %w", err)
	}

	result := map[string]string{
		"example": ex.Name,
		"source":  srcDir,
		"output":  outDir,
	}
	if *jsonOutput {
		return printJSON(result)
	}
	cliOutputf("Copied example %q to %s\n", ex.Name, outDir)
	cliOutputln("To run:")
	cliOutputf("  cd %s && go run .\n", outDir)
	return nil
}

// resolveExampleSourceDir finds the examples directory relative to the gofly
// module root. It prefers the module source when running from a gofly checkout.
func resolveExampleSourceDir(examplePath string) (string, error) {
	// When running from source, derive from this file's location.
	_, file, _, ok := runtime.Caller(0)
	if ok {
		// file is in cmd/gofly/internal/command/
		cmdDir := filepath.Dir(file)
		modRoot := filepath.Join(cmdDir, "..", "..", "..", "..")
		candidate := filepath.Join(modRoot, examplePath)
		// #nosec G304 -- candidate is derived from the current module source and built-in example metadata.
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate, nil
		}
	}

	// Fallback: look under GOPATH/pkg/mod or local module cache.
	goModCache := os.Getenv("GOMODCACHE")
	if goModCache == "" {
		goModCache = filepath.Join(os.Getenv("GOPATH"), "pkg", "mod")
	}
	candidate := filepath.Join(goModCache, "github.com", "gofly", "gofly@"+Version, examplePath)
	// #nosec G304 G703 -- candidate is scoped to GOMODCACHE plus built-in module/example metadata.
	if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
		return candidate, nil
	}

	return "", fmt.Errorf("cannot find example source for %s", examplePath)
}

func copyExampleDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyExampleDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		// #nosec G304 -- srcPath is built from a trusted example source root and directory entry name.
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		// #nosec G703 -- dstPath is built from the requested output root and directory entry name.
		if err := os.WriteFile(dstPath, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}
