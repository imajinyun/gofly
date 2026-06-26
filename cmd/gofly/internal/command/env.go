package command

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type toolCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
}

func envCommand(args []string) error {
	if printCommandHelp("env", args) {
		return nil
	}
	if len(args) > 0 && args[0] == "install" {
		return envCheckCommand([]string{"--install"})
	}
	if len(args) > 0 && args[0] == "check" {
		return envCheckCommand(args[1:])
	}
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print environment as JSON")
	write := fs.String("write", "", "write environment key=value")
	w := fs.String("w", "", "write environment key=value")
	force := fs.Bool("force", false, "overwrite existing environment value")
	f := fs.Bool("f", false, "overwrite existing environment value")
	verbose := fs.Bool("verbose", false, "print verbose output")
	v := fs.Bool("v", false, "print verbose output")
	if _, err := parseInterspersedFlags(fs, args); err != nil {
		return err
	}
	if *write == "" {
		*write = *w
	}
	if *write != "" {
		key, value, ok := strings.Cut(*write, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("%w: --write expects key=value", errUsage)
		}
		if old, exists := os.LookupEnv(key); exists && old != "" && !*force && !*f {
			return fmt.Errorf("%w: environment %s already exists; pass --force to overwrite", errUsage, key)
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set env %s: %w", key, err)
		}
		if *verbose || *v {
			cliOutputf("%s=%s\n", key, value)
		}
	}
	info := envInfo()
	if *jsonOutput {
		return printJSON(info)
	}
	for _, key := range []string{"GOOS", "GOARCH", "GOVERSION", "GOFLY_VERSION"} {
		cliOutputf("%s=%s\n", key, info[key])
	}
	return nil
}

func envCheckCommand(args []string) error {
	fs := flag.NewFlagSet("env check", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print check result as JSON")
	install := fs.Bool("install", false, "request installation guidance")
	i := fs.Bool("i", false, "request installation guidance")
	if _, err := parseInterspersedFlags(fs, args); err != nil {
		return err
	}
	checks := []toolCheck{
		envToolCheck("go"),
		envToolCheck("protoc"),
		envToolCheck("git"),
	}
	if *jsonOutput {
		return printJSON(checks)
	}
	for _, check := range checks {
		cliOutputf("%s\t%s\t%s\n", check.Name, check.Status, check.Path)
	}
	if *install || *i {
		cliOutputln("install guidance:")
		cliOutputln("  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest")
		cliOutputln("  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest")
		cliOutputln("  install protoc from https://grpc.io/docs/protoc-installation/ when protoc is missing")
	}
	return nil
}

func envInfo() map[string]string {
	return map[string]string{
		"GOOS":          runtime.GOOS,
		"GOARCH":        runtime.GOARCH,
		"GOVERSION":     runtime.Version(),
		"GOFLY_VERSION": Version,
	}
}

func envToolCheck(name string) toolCheck {
	path, err := exec.LookPath(name)
	status := "ok"
	if err != nil {
		status = "missing"
	}
	return toolCheck{Name: name, Status: status, Path: path}
}
