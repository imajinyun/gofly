package command

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiMiddlewareCommand(args []string) error {
	leadingNames, args := splitLeadingNames(args)
	fs := flag.NewFlagSet("api middleware", flag.ContinueOnError)
	name := fs.String("name", "", "middleware name, comma-separated for multiple middlewares")
	api := fs.String("api", "", "api file to discover middleware declarations")
	file := fs.String("file", "", "api file, alias for --api")
	dir := fs.String("dir", ".", "service root directory")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = strings.Join(leadingNames, ",")
	}
	fillNameFromArgs(name, remaining)
	if *api == "" {
		*api = *file
	}
	names := splitCSV(*name)
	switch {
	case len(leadingNames) > 0:
		names = append(names, remaining...)
	case *name != "" && len(remaining) > 1:
		names = append(names, remaining[1:]...)
	case *name == "":
		names = append(names, remaining...)
	}
	if *api != "" {
		apiNames, err := apiMiddlewareNames(*api)
		if err != nil {
			return err
		}
		names = append(names, apiNames...)
	}
	return generator.GenerateMiddleware(generator.MiddlewareOptions{Names: names, Dir: *dir})
}

func apiMiddlewareNames(path string) ([]string, error) {
	// #nosec G304 -- middleware discovery reads an explicit API file path supplied to the CLI.
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read api file: %w", err)
	}
	if _, err := generator.ParseAPI(string(content)); err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		names = append(names, middlewareNamesFromLine(line)...)
	}
	return names, nil
}

func middlewareNamesFromLine(line string) []string {
	lower := strings.ToLower(line)
	for _, marker := range []string{"middleware:", "middlewares:"} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		value := line[idx+len(marker):]
		value = strings.Trim(value, " `\"[]{}()")
		return splitCSV(value)
	}
	return nil
}
