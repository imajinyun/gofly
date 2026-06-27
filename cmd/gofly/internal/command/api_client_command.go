package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiClientCommand(command string, args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api client", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output file")
	language := fs.String("language", "typescript", "client language: typescript, javascript, dart, java, or kotlin")
	baseURL := fs.String("base-url", "", "default API base URL")
	caller := fs.String("caller", "", "client caller name")
	unwrap := fs.Bool("unwrap", false, "unwrap response envelopes")
	legacy := fs.Bool("legacy", false, "use legacy client output layout")
	hostname := fs.String("hostname", "", "api hostname")
	scheme := fs.String("scheme", "", "api scheme")
	pkg := fs.String("pkg", "", "generated package name")
	if command == "ts" || command == "typescript" {
		*language = "typescript"
	}
	if command == "js" || command == "javascript" {
		*language = "javascript"
	}
	if command == "dart" {
		*language = "dart"
	}
	if command == "java" {
		*language = "java"
	}
	if command == "kotlin" || command == "kt" {
		*language = "kotlin"
	}
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *caller != "" {
		warnNoopFlag("api client", "caller", "client naming is derived from the service name")
	}
	if *unwrap {
		warnNoopFlag("api client", "unwrap", "generated clients currently preserve response shapes from the API spec")
	}
	if *legacy {
		warnNoopFlag("api client", "legacy", "gofly emits the current client layout")
	}
	if *pkg != "" {
		warnNoopFlag("api client", "pkg", "non-Go clients do not use package names; Go DTOs use api types")
	}
	if *file == "" {
		*file = *api
	}
	if *file == "" {
		*file = leadingFile
	}
	if *baseURL == "" && *hostname != "" {
		if *scheme == "" {
			*scheme = "http"
		}
		*baseURL = *scheme + "://" + *hostname
	}
	fillNameFromArgs(file, remaining)
	return generator.GenerateAPIClient(generator.APIClientOptions{
		APIFile:  *file,
		Dir:      *dir,
		Output:   output.resolve(),
		Language: *language,
		BaseURL:  *baseURL,
	})
}
