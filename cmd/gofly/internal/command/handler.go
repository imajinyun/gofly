package command

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/gofly/gofly/cmd/gofly/internal/generator"
)

func handlerCommand(args []string) error {
	if printCommandHelp("handler", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly handler gen|complete`", errUsage)
	}
	switch args[0] {
	case "gen":
		return handlerGenCommand(args[1:])
	case "complete":
		return handlerCompleteCommand(args[1:])
	default:
		return fmt.Errorf("%w: expected `gofly handler gen|complete`", errUsage)
	}
}

func handlerGenCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("handler gen", flag.ContinueOnError)
	name := fs.String("name", "", "handler name")
	module := fs.String("module", "", "go module path, inferred from go.mod when empty")
	dir := fs.String("dir", ".", "service root directory")
	path := fs.String("path", "", "handler subdirectory under internal/api")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if err := generator.GenerateHandler(generator.HandlerOptions{
		Name:   *name,
		Module: *module,
		Dir:    *dir,
		Path:   *path,
	}); err != nil {
		return err
	}
	outDir := *dir
	if outDir == "" {
		outDir = "."
	}
	outPath := filepath.Join(outDir, "internal", "api", *path)
	cliOutputf("api handler generated: %s\n", outPath)
	return nil
}
