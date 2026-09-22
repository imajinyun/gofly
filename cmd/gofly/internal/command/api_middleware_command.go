package command

import (
	"flag"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiMiddlewareCommand(args []string) error {
	leadingNames, args := splitLeadingNames(args)
	fs := flag.NewFlagSet("api middleware", flag.ContinueOnError)
	name := fs.String("name", "", "middleware name, comma-separated for multiple middlewares")
	api := registerAPIFileFlags(fs, "api file to discover middleware declarations")
	dir := fs.String("dir", ".", "service root directory")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = strings.Join(leadingNames, ",")
	}
	fillNameFromArgs(name, remaining)
	apiFile := api.resolve("", nil)
	names := splitCSV(*name)
	switch {
	case len(leadingNames) > 0:
		names = append(names, remaining...)
	case *name != "" && len(remaining) > 1:
		names = append(names, remaining[1:]...)
	case *name == "":
		names = append(names, remaining...)
	}
	if apiFile != "" {
		apiNames, err := apiMiddlewareNames(apiFile)
		if err != nil {
			return err
		}
		names = append(names, apiNames...)
	}
	return generator.GenerateMiddleware(generator.MiddlewareOptions{Names: names, Dir: *dir})
}

func apiMiddlewareNames(path string) ([]string, error) {
	doc, err := generator.LoadAPI(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, service := range doc.Services {
		names = append(names, service.Server.Middleware...)
	}
	return uniqueStrings(names), nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
