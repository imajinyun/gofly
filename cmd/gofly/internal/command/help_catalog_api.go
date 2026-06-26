package command

func apiCommandHelp(command string) (commandHelp, bool) {
	switch command {
	case "api go":
		return commandHelp{
			Name:  "api go",
			Short: "Generate REST service code from an .api file.",
			Usage: "gofly api go --api <service.api> --dir <dir> [flags]",
			Flags: []string{
				"-api, --api, --file <file>       API definition file",
				"-dir, --dir <dir>                output directory",
				"--package <pkg>                  generated Go package name",
				"--rpc-package <import>           RPC package import for gateway wiring",
				"--style go_zero                  scaffold style option",
				"--home, --remote, --branch       template source compatibility flags",
				"--json                           emit generation result as JSON",
			},
			Examples: []string{
				"gofly api go -api user.api -dir . -style go_zero",
				"gofly api gen --file user.api --dir internal --rpc-package example.com/user/rpc",
			},
		}, true
	case "gen api", "gen rest":
		return commandHelp{
			Name:  command,
			Short: "Generate REST service code from an .api file.",
			Usage: "gofly " + command + " --api <service.api> --dir <dir> [flags]",
			Flags: []string{
				"-api, --api, --file <file>       API definition file",
				"-dir, --dir <dir>                output directory",
				"--package <pkg>                  generated Go package name",
				"--rpc-package <import>           RPC package import for gateway wiring",
				"--style go_zero                  scaffold style option",
				"--home, --remote, --branch       template source compatibility flags",
			},
			Examples: []string{"gofly " + command + " -api user.api -dir . -style go_zero"},
		}, true
	case "api check":
		return commandHelp{Name: "api check", Short: "Validate an .api file.", Usage: "gofly api check --api <service.api>", Flags: []string{"-api, --api, --file <file>  API definition file"}, Examples: []string{"gofly api check -api user.api"}}, true
	case "api format":
		return commandHelp{Name: "api format", Short: "Format one .api file or all .api files in a directory.", Usage: "gofly api format --api <service.api> [--write] | gofly api format --dir <api-dir> [--iu]", Flags: []string{"-api, --api, --file <file>  API definition file", "--write, --w                  write formatted content back", "--o <file>                    write formatted content to file", "--dir <dir>                   format .api files in directory", "--iu                          preserve import/use layout"}, Examples: []string{"gofly api format -api user.api -w", "gofly api format -dir apis --iu"}}, true
	case "api swagger", "api doc":
		return commandHelp{Name: command, Short: "Generate API documentation from an .api file.", Usage: "gofly " + command + " --api <service.api> --dir <dir> [flags]", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   output directory", "--o, --filename <file>        output filename", "--format markdown|openapi|json|yaml|oas3", "--oas3                        write OpenAPI v3 JSON output", "--json                        write OpenAPI JSON output", "--yaml                        write YAML OpenAPI output"}, Examples: []string{"gofly api swagger -api user.api -dir docs -filename user.yaml -yaml", "gofly api doc -api user.api -dir docs --oas3", "gofly api doc -api user.api -dir docs -format markdown"}}, true
	case "api route":
		return commandHelp{Name: "api route", Short: "Print or export route table from an .api file.", Usage: "gofly api route --api <service.api> [--format text|markdown|json]", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   optional output directory", "--format text|markdown|json   output format"}, Examples: []string{"gofly api route -api user.api -format markdown"}}, true
	case "api import":
		return commandHelp{Name: "api import", Short: "Convert OpenAPI/Swagger document to .api syntax.", Usage: "gofly api import --src <openapi.json|yaml> --dir <dir> [--service <name>]", Flags: []string{"--src <file>       OpenAPI/Swagger source", "--dir <dir>       output directory", "--service <name>  service name override"}, Examples: []string{"gofly api import -src openapi.yaml -dir apis -service user-api"}}, true
	case "api diff":
		return commandHelp{Name: "api diff", Short: "Compare two .api files for route/type changes.", Usage: "gofly api diff --base <old.api> --target <new.api> [--format text|markdown|json]", Flags: []string{"--base <file>                  old API file", "--target <file>                new API file", "--o <file>                     output file", "--format text|markdown|json    output format"}, Examples: []string{"gofly api diff old.api new.api --format markdown"}}, true
	case "api breaking":
		return commandHelp{Name: "api breaking", Short: "Detect breaking changes between two .api files.", Usage: "gofly api breaking --base <old.api> --target <new.api>", Flags: []string{"--base <file>      old API file", "--target <file>    new API file"}, Examples: []string{"gofly api breaking old.api new.api"}}, true
	case "api types":
		return commandHelp{Name: "api types", Short: "Generate Go DTO types from an .api file.", Usage: "gofly api types --api <service.api> --dir <dir> [--package <pkg>]", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   output directory", "--package <pkg>               generated package name"}, Examples: []string{"gofly api types -api user.api -dir internal/types -package types"}}, true
	case "api new", "new api":
		return commandHelp{Name: command, Short: "Create an API service scaffold.", Usage: "gofly " + command + " <name> --module <module> [flags]", Flags: []string{"--name <name>                  API service name", "--module <module>              Go module path", "--dir <dir>                    output directory", "--style minimal|basic|production", "--profile <profile>            generation profile: gofly-ai|gozero-compatible|kitex-compatible", "--home, --remote, --branch     template source options", "--client, --idea, --go_opt     accepted scaffold options", "--json                         emit scaffold result as JSON"}, Examples: []string{"gofly new api hello --module example.com/hello --style go_zero", "gofly api new hello --module example.com/hello --dir hello --profile gozero-compatible --json"}}, true
	case "api client", "api ts", "api js", "api dart", "api java", "api kotlin":
		return commandHelp{Name: command, Short: "Generate typed API client code from an .api file.", Usage: "gofly " + command + " --api <service.api> --dir <dir> [flags]", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   output directory", "--language <name>             client language for api client", "--caller, --unwrap, --legacy  client generation options", "--hostname, --scheme, --pkg   client generation options"}, Examples: []string{"gofly api ts -api user.api -dir web/src/client", "gofly api client -api user.api -dir clients -language java"}}, true
	case "api plugin":
		return commandHelp{Name: "api plugin", Short: "Run an API generation plugin.", Usage: "gofly api plugin --api <service.api> --plugin <plugin> [--dir <dir>]", Flags: []string{"-api, --api, --file <file>  API definition file", "--plugin, -p <plugin>         plugin executable", "--dir <dir>                   working/output directory", "--style <style>               plugin style option"}, Examples: []string{"gofly api plugin -api user.api -plugin ./my-plugin -dir ."}}, true
	case "api middleware":
		return commandHelp{Name: "api middleware", Short: "Generate middleware skeletons by name or from an API file.", Usage: "gofly api middleware <name> --dir <service-dir> | gofly api middleware --api <service.api> --dir <service-dir>", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   service root directory"}, Examples: []string{"gofly api middleware auth --dir .", "gofly api middleware -api user.api -dir ."}}, true
	case "gen middleware":
		return commandHelp{Name: "gen middleware", Short: "Generate middleware skeletons by name or from an API file.", Usage: "gofly gen middleware <name> --dir <service-dir> | gofly gen middleware --api <service.api> --dir <service-dir>", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   service root directory"}, Examples: []string{"gofly gen middleware auth --dir .", "gofly gen middleware -api user.api -dir ."}}, true
	case "api":
		return commandHelp{
			Name:  "api",
			Short: "Generate, validate, format, document, diff and extend API definition files.",
			Usage: "gofly api <command> [arguments]",
			Commands: []helpCommand{
				{Name: "go", Short: "generate REST service code from .api"},
				{Name: "check", Short: "validate an API file"},
				{Name: "format", Short: "format one API file or a directory"},
				{Name: "swagger", Short: "generate OpenAPI/Swagger document"},
				{Name: "types", Short: "generate Go DTO types"},
				{Name: "route", Short: "print or export route table"},
				{Name: "import", Short: "convert OpenAPI/Swagger to .api"},
				{Name: "diff", Short: "compare two API files"},
				{Name: "plugin", Short: "run API generation plugin"},
				{Name: "middleware", Short: "generate middleware skeletons"},
				{Name: "ts/js/dart/java/kt", Short: "generate API clients"},
				{Name: "new", Short: "create an API service scaffold"},
			},
			Flags: []string{"-h, --help  show help for api", "-o <file>   generate a starter .api template"},
			Examples: []string{
				"gofly api go -api user.api -dir . -style go_zero",
				"gofly api swagger -api user.api -dir docs -filename user.yaml -yaml",
				"gofly api -o user.api",
			},
		}, true
	default:
		return commandHelp{}, false
	}
}
