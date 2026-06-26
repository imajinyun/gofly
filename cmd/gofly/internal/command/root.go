// Package command implements the gofly CLI: code generation, scaffolding,
// governance, service discovery, deployment and developer tooling.
package command

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

func warnNoopFlag(command, flagName, reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = "accepted for compatibility"
	}
	errorf("[gofly] %s: --%s is currently a compatibility no-op (%s)\n", command, flagName, reason)
}

func flagProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			provided = true
		}
	})
	return provided
}

type cliPlan struct {
	Command           string            `json:"command"`
	DryRun            bool              `json:"dryRun"`
	MutatesFilesystem bool              `json:"mutatesFilesystem"`
	Inputs            map[string]string `json:"inputs,omitempty"`
	Actions           []cliPlanAction   `json:"actions"`
	GeneratedFiles    int               `json:"generatedFiles"`
	PluginEffects     []cliPluginEffect `json:"pluginEffects,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	NextActions       []string          `json:"nextActions,omitempty"`
}

type cliPlanAction struct {
	Operation   string `json:"operation"`
	Target      string `json:"target"`
	Description string `json:"description"`
	RiskLevel   string `json:"riskLevel"`
}

type cliPluginEffect struct {
	Name     string `json:"name"`
	Executed bool   `json:"executed"`
	Files    int    `json:"files"`
	Patches  int    `json:"patches"`
	Note     string `json:"note,omitempty"`
}

func usage() string {
	return colorizeHelpText(`gofly is the flycli-style Go microservice framework toolkit.

Usage:
  version - Print version metadata.
  new - Scaffold new production, API, or RPC services.
    new service <name> --style production --module <module> [--dir <dir>]
  new api <name> --module <module> [--dir <dir>] [--style minimal|basic|production] [--api-spec] [--home <template-dir>]
  new api <name> --module <module> [--dir <dir>] [--config .gofly/config.json] [--template-dir <dir>] [--feature http-compat|rpc-compat|ecosystem-compat] [--rpc-plugin <plugin>]
  new rpc <name> --module <module> [--dir <dir>] [--config .gofly/config.json] [--template-dir <dir>] [--feature <names>] [--rpc-plugin <plugin>]
  rpc - Generate and validate RPC services.
    rpc new <name> --module <module> [--dir <dir>]
    rpc idl --file <service.proto|service.thrift> [--format text|json]
    rpc thrift --file <service.thrift> --out <dir>
    rpc client|server --file <service.proto|service.thrift> --out <dir> [--package <pkg>]
    rpc middleware <name> --dir <service-dir>
    rpc lint|deps --file <service.proto|service.thrift>
  gen - Run unified code generators.
    gen handler --name <name> --dir <service-dir> [--module <module>] [--path <subdir>]
    gen middleware <name> --dir <service-dir>
    gen rpc --file|--src <service.proto> --dir|--out <dir> [--package <pkg>] [--transport grpc|gofly|both] [--with-middleware] [--with-recovery] [--with-validator] [--standard] [--style go_zero] [--home <dir>]
    gen api --file <service.api> --dir <dir> [--package <pkg>] [--rpc-package <import>] [--style go_zero] [--home <dir>]
    gen model --ddl <schema.sql> --dir <dir> [--package <pkg>] [--module <module>] [--table|--tables <tables>]
    gen gateway --name <name> --module <module> --dir <dir>
  api - Generate and manage API definition files.
    api format --file|--api <service.api> [--write] [--o <formatted.api>]
    api format --dir <api-dir> [--iu]
    api doc --file <service.api> --dir <dir> [--format markdown|openapi|yaml]
    api swagger --api <service.api> --dir <dir> [--o <openapi.json>]
    api route --api <service.api> --dir <dir> [--format text|markdown|json]
    api import --src <openapi.json|yaml> --dir <dir> [--service <name>]
    api diff --base <old.api> --target <new.api> --dir <dir> [--format text|markdown|json]
    api client --file <service.api> --dir <dir> [--language typescript|javascript|dart|java|kotlin]
    api types --api <service.api> --dir <dir> [--package <pkg>]
    api plugin --api <service.api> --plugin <plugin> [--dir <dir>]
    api middleware <name> --dir <service-dir>
    api middleware --api <service.api> --dir <service-dir>
  docker - Generate Dockerfile assets.
    docker --name <name> [--dir <dir>]
    docker <name> [--go <main-pkg>] [--exe <binary>] [--base <image>] [--output|--o <file>]
  kube - Generate Kubernetes manifests.
    kube --name <name> [--dir <dir>] [--image <image>] [--namespace <ns>]
    kube deploy <name> [--image <image>] [--o <file>]
    kube service|ingress|configmap|job <name> [--o <file>]
  template - Manage local or remote generation templates.
    template init [--dir <dir>] [--remote <repo|dir>] [--branch <branch>]
    template list|clean|update|revert [--dir <dir>] [--remote <repo|dir>] [--branch <branch>]
  quickstart - Create runnable services quickly.
    quickstart <name> --module <module> [--dir <dir>] [--style minimal|basic|production]
  migrate - Create SQL migration files.
    migrate create <name> [--dir <dir>]
  env - Inspect local toolchain environment.
    env [--json]
    env check [--json]
  bug - Print diagnostic bug reports.
    bug [--json]
  upgrade - Print or run upgrade commands.
    upgrade [--version <version>] [--execute] [--json]
  release - Run release readiness checks.
    release check [--api-base <old.api> --api-target <new.api>] [--rpc-base <old.proto> --rpc-target <new.proto>] [--strict] [--json]
  completion - Emit shell completion scripts.
    completion bash|zsh|fish|powershell|pwsh
  config - Manage .gofly configuration.
    config init|show|get|set --dir <dir> [--name <name>] [--module <module>] [--style basic] [--key k --value v]
  feature - List or preview scaffold features.
    feature list
    feature run <name> --name <service> --module <module> --dir <dir> [--style basic]
  plugin - List, install or run generation plugins.
    plugin run <plugin> --name <service> --module <module> --dir <dir> [--command service]
    plugin run --remote <repo-or-url>@<version> --name <service> --module <module> --dir <dir>
    plugin run --go-plugin <path-or-dir> --name <service> --module <module> --dir <dir>
    plugin install --remote <repo-or-url>@<version>
    plugin uninstall --remote <repo-or-url>@<version>
    plugin list
  handler - Generate or complete API handlers.
    handler complete --file <service.go> --method <name> [--comment "..."]
  complete - Emit legacy completion scripts.
    complete handler bash|zsh|fish|powershell|pwsh
  doctor - Diagnose local environment and toolchain readiness.
    doctor [--json]
  example - List or run built-in examples.
    example list [--json]
    example run <name> [--dir <dir>]
  ai - Emit machine-readable tool metadata for LLM/Agent callers.
    ai manifest [--format json|text] [--json]
    ai complete --prompt <text> [--config .gofly/config.json] [--provider noop] [--model <model>] [--max-input-tokens <n>] [--max-output-tokens <n>] [--max-total-tokens <n>] [--rate-limit <n>] [--timeout <duration>] [--dry-run|--plan] [--json]

Aliases:
  api - API generation and validation shortcuts.
    api new <name> --module <module> [--dir <dir>] [--style minimal|basic|production] [--api-spec]
    api check --file <service.api>
    api validate --file <service.api>
    api gen --file <service.api> --dir <dir> [--package <pkg>] [--rpc-package <import>] [--style go_zero] [--home <dir>] [--remote <repo>] [--branch <branch>]
    api go --file <service.api> --dir <dir> [--package <pkg>] [--rpc-package <import>] [--style go_zero] [--home <dir>] [--remote <repo>] [--branch <branch>]
    api types --api <service.api> --dir <dir> [--package <pkg>]
    api swagger --api <service.api> --dir <dir> [--o <openapi.json>]
    api route --api <service.api> --dir <dir> [--format text|markdown|json]
    api import --src <openapi.json|yaml> --dir <dir> [--service <name>]
    api diff --base <old.api> --target <new.api> --dir <dir> [--format text|markdown|json]
    api diff <old.api> <new.api> [--o <diff.json>] [--format text|markdown|json]
    api plugin --api <service.api> --plugin <plugin> [--dir <dir>]
    api middleware <name> --dir <service-dir>
    api ts --file <service.api> --dir <dir>
    api js --file <service.api> --dir <dir>
    api dart --file <service.api> --dir <dir>
    api java --file <service.api> --dir <dir>
    api kotlin --file <service.api> --dir <dir>
  new - Service scaffolding shortcuts.
    new rpc <name> --module <module> [--dir <dir>]
  handler - Handler generation shortcuts.
    handler gen --name <name> --dir <service-dir> [--module <module>] [--path <subdir>]
  rpc - RPC generation and validation shortcuts.
    rpc idl --file <service.proto|service.thrift> [--format text|json]
    rpc inspect --file <service.proto|service.thrift> [--format text|json]
    rpc thrift --file <service.thrift> --out <dir>
    rpc thrift2proto --file <service.thrift> --out <dir>
    rpc client --file <service.proto|service.thrift> --out <dir> [--package <pkg>]
    rpc server --file <service.proto|service.thrift> --out <dir> [--package <pkg>]
    rpc middleware <name> --dir <service-dir>
    rpc lint --file <service.proto|service.thrift>
    rpc deps --file <service.proto|service.thrift> [--format text|json]
    rpc check --file|--src <service.proto>
    rpc gen --file|--src <service.proto> --dir|--out <dir> [--package <pkg>] [--transport grpc|gofly|both] [--with-middleware] [--with-recovery] [--with-validator] [--standard] [--timeout <duration>] [--style go_zero] [--home <dir>] [--remote <repo>] [--branch <branch>]
    rpc gen <service.proto> --out <dir> [--package <pkg>]
    rpc protoc --file|--src <service.proto> --dir <dir> [--proto_path <paths>]
    rpc protoc <service.proto> [--I <paths>] [--go_out <dir>] [--go-grpc_out <dir>] [--extra <args>]
    rpc template [-o <file>] [--name <name>] [--home <dir>] [--remote <repo|dir>] [--branch <branch>]
    rpc template init|list|clean|update|revert [--dir <dir>] [--remote <repo|dir>] [--branch <branch>]
  model - Model generation shortcuts.
    model gen --ddl <schema.sql> --dir <dir> [--package <pkg>] [--module <module>] [--table|--tables <tables>]
    model mysql ddl --src <schema.sql> --dir <dir> [--package <pkg>] [--module <module>] [--style go_zero] [--home <dir>]
    model pg ddl --src <schema.sql> --dir <dir> [--package <pkg>] [--module <module>] [--style go_zero] [--home <dir>]
    model mysql datasource --url|--dsn <dsn> --table|--tables <tables> --dir <dir> [--database <db>] [--style go_zero] [--home <dir>]
    model pg datasource --url|--dsn <dsn> --table|--tables <tables> --dir <dir> [--database <db>] [--schema <schema>] [--style go_zero] [--home <dir>]
    model mongo --type <name> --dir <dir> [--package <pkg>]
  template - Template management shortcuts.
    template clean --dir .gofly/templates
    template update --dir .gofly/templates [--remote <repo|dir>] [--branch <branch>]
    template revert --dir .gofly/templates
  upgrade - upgrade command aliases.
    upgrade --version latest
    upgrade --execute --version latest
    upgrade --json --version latest`)
}

type helpCommand struct {
	Name  string
	Short string
}

type commandHelp struct {
	Name     string
	Short    string
	Usage    string
	Commands []helpCommand
	Flags    []string
	Examples []string
}

var completionShells = []string{"bash", "zsh", "fish", "powershell", "pwsh"}

const completionShellUsage = "bash|zsh|fish|powershell|pwsh"

func commandUsage(command string) string {
	return renderCommandHelp(commandHelpFor(command))
}

func canonicalHelpTopic(command string) string {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ""
	}
	if alias, ok := topLevelHelpAliases[parts[0]]; ok {
		parts[0] = alias
	}
	if len(parts) >= 2 {
		if aliases := nestedHelpAliases[parts[0]]; aliases != nil {
			if alias, ok := aliases[parts[1]]; ok {
				parts[1] = alias
			}
		}
		switch parts[0] {
		case "complete":
			if len(parts) >= 3 && parts[1] == "handler" && parts[2] == "pwsh" {
				parts[2] = "powershell"
			}
		}
	}
	parts = trimHelpTopicPositionals(parts)
	return strings.Join(parts, " ")
}

func trimHelpTopicPositionals(parts []string) []string {
	if len(parts) < 2 {
		return parts
	}
	switch parts[0] {
	case "api":
		if isAPIHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "rpc":
		if isRPCHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "new":
		if parts[1] == "service" || parts[1] == "api" || parts[1] == "rpc" {
			return parts[:2]
		}
	case "model":
		if len(parts) >= 3 && isModelDriverHelpSubcommand(parts[1], parts[2]) {
			return parts[:3]
		}
		if isModelHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "gen":
		if isGenHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "handler":
		if parts[1] == "gen" || parts[1] == "complete" {
			return parts[:2]
		}
	case "feature":
		if isFeatureHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "plugin":
		if (parts[1] == "install" || parts[1] == "uninstall") && len(parts) >= 3 {
			return parts[:3]
		}
		if isPluginHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "config":
		if isConfigHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "env":
		if isEnvHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "kube":
		if isKubeHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "template":
		if isTemplateHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "migrate", "migration":
		if parts[1] == "create" || parts[1] == "new" {
			return parts[:2]
		}
	case "complete":
		if parts[1] == "handler" {
			if len(parts) >= 3 && isCompleteHandlerShell(parts[2]) {
				return parts[:3]
			}
			return parts[:2]
		}
	case "quickstart", "docker":
		return parts[:1]
	case "version", "upgrade", "bug", "doctor":
		return parts[:1]
	case "ai":
		if len(parts) >= 2 && isAIHelpSubcommand(parts[1]) {
			return parts[:2]
		}
		return parts[:1]
	case "example", "examples":
		if parts[1] == "list" || parts[1] == "run" {
			return parts[:2]
		}
		return parts[:1]
	case "completion":
		if isCompletionHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	}
	return parts
}

func isGenHelpSubcommand(command string) bool {
	switch command {
	case "handler", "rpc", "api", "rest", "middleware", "model", "gateway":
		return true
	default:
		return false
	}
}

func isAPIHelpSubcommand(command string) bool {
	switch command {
	case "go", "check", "format", "swagger", "doc", "route":
		return true
	case "import", "diff", "breaking", "types", "new":
		return true
	case "client", "ts", "js", "dart", "java", "kotlin":
		return true
	case "plugin", "middleware":
		return true
	default:
		return false
	}
}

func isRPCHelpSubcommand(command string) bool {
	switch command {
	case "idl", "thrift", "client", "server", "middleware", "lint", "deps":
		return true
	case "gen", "protoc", "check", "doc", "breaking", "descriptor", "plugin", "template", "new":
		return true
	default:
		return false
	}
}

func isModelDriverHelpSubcommand(driver string, command string) bool {
	switch driver {
	case "mysql", "pg":
		return command == "ddl" || command == "datasource"
	default:
		return false
	}
}

func isModelHelpSubcommand(command string) bool {
	switch command {
	case "gen", "mongo":
		return true
	default:
		return false
	}
}

func isConfigHelpSubcommand(command string) bool {
	switch command {
	case "init", "show", "get", "set", "clean":
		return true
	default:
		return false
	}
}

func isFeatureHelpSubcommand(command string) bool {
	switch command {
	case "list", "run":
		return true
	default:
		return false
	}
}

func isPluginHelpSubcommand(command string) bool {
	switch command {
	case "list", "search", "install", "uninstall", "run":
		return true
	default:
		return false
	}
}

func isKubeHelpSubcommand(command string) bool {
	switch command {
	case "deploy", "service", "ingress", "configmap", "job":
		return true
	default:
		return false
	}
}

func isTemplateHelpSubcommand(command string) bool {
	switch command {
	case "init", "list", "clean", "update", "revert":
		return true
	default:
		return false
	}
}

func isEnvHelpSubcommand(command string) bool {
	return command == "check" || command == "install"
}

func isCompletionHelpSubcommand(command string) bool {
	return isCompletionShell(command)
}

func isAIHelpSubcommand(command string) bool {
	return command == "manifest" || command == "plan" || command == "new" || command == "complete" || command == "stream" || command == "doctor" || command == "control-plane"
}

func isCompleteHandlerShell(command string) bool {
	return isCompletionShell(command)
}

func isCompletionShell(shell string) bool {
	shell = strings.ToLower(strings.TrimSpace(shell))
	for _, supported := range completionShells {
		if shell == supported {
			return true
		}
	}
	return false
}

func commandHelpFor(command string) commandHelp {
	command = canonicalHelpTopic(command)
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
		}
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
		}
	case "api check":
		return commandHelp{Name: "api check", Short: "Validate an .api file.", Usage: "gofly api check --api <service.api>", Flags: []string{"-api, --api, --file <file>  API definition file"}, Examples: []string{"gofly api check -api user.api"}}
	case "api format":
		return commandHelp{Name: "api format", Short: "Format one .api file or all .api files in a directory.", Usage: "gofly api format --api <service.api> [--write] | gofly api format --dir <api-dir> [--iu]", Flags: []string{"-api, --api, --file <file>  API definition file", "--write, --w                  write formatted content back", "--o <file>                    write formatted content to file", "--dir <dir>                   format .api files in directory", "--iu                          preserve import/use layout"}, Examples: []string{"gofly api format -api user.api -w", "gofly api format -dir apis --iu"}}
	case "api swagger", "api doc":
		return commandHelp{Name: command, Short: "Generate API documentation from an .api file.", Usage: "gofly " + command + " --api <service.api> --dir <dir> [flags]", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   output directory", "--o, --filename <file>        output filename", "--format markdown|openapi|json|yaml|oas3", "--oas3                        write OpenAPI v3 JSON output", "--json                        write OpenAPI JSON output", "--yaml                        write YAML OpenAPI output"}, Examples: []string{"gofly api swagger -api user.api -dir docs -filename user.yaml -yaml", "gofly api doc -api user.api -dir docs --oas3", "gofly api doc -api user.api -dir docs -format markdown"}}
	case "api route":
		return commandHelp{Name: "api route", Short: "Print or export route table from an .api file.", Usage: "gofly api route --api <service.api> [--format text|markdown|json]", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   optional output directory", "--format text|markdown|json   output format"}, Examples: []string{"gofly api route -api user.api -format markdown"}}
	case "api import":
		return commandHelp{Name: "api import", Short: "Convert OpenAPI/Swagger document to .api syntax.", Usage: "gofly api import --src <openapi.json|yaml> --dir <dir> [--service <name>]", Flags: []string{"--src <file>       OpenAPI/Swagger source", "--dir <dir>       output directory", "--service <name>  service name override"}, Examples: []string{"gofly api import -src openapi.yaml -dir apis -service user-api"}}
	case "api diff":
		return commandHelp{Name: "api diff", Short: "Compare two .api files for route/type changes.", Usage: "gofly api diff --base <old.api> --target <new.api> [--format text|markdown|json]", Flags: []string{"--base <file>                  old API file", "--target <file>                new API file", "--o <file>                     output file", "--format text|markdown|json    output format"}, Examples: []string{"gofly api diff old.api new.api --format markdown"}}
	case "api breaking":
		return commandHelp{Name: "api breaking", Short: "Detect breaking changes between two .api files.", Usage: "gofly api breaking --base <old.api> --target <new.api>", Flags: []string{"--base <file>      old API file", "--target <file>    new API file"}, Examples: []string{"gofly api breaking old.api new.api"}}
	case "api types":
		return commandHelp{Name: "api types", Short: "Generate Go DTO types from an .api file.", Usage: "gofly api types --api <service.api> --dir <dir> [--package <pkg>]", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   output directory", "--package <pkg>               generated package name"}, Examples: []string{"gofly api types -api user.api -dir internal/types -package types"}}
	case "new service":
		return commandHelp{Name: "new service", Short: "Create the golden-path production service scaffold.", Usage: "gofly new service <name> --module <module> [--dir <dir>] [flags]", Flags: []string{"--name <name>                  service name", "--module <module>              Go module path", "--dir <dir>                    output directory", "--style production             defaults to production", "--discovery memory|consul|etcdv3", "--discovery-address <addr>      discovery address", "--discovery-endpoints <list>    discovery endpoints", "--home, --remote, --branch      template source options", "--feature <names>               feature names to enable", "--plugin <paths>                plugin executables", "--json                         emit scaffold result as JSON"}, Examples: []string{"gofly new service orders --style production --module example.com/orders", "gofly new service orders --module example.com/orders --discovery memory --dir orders --json"}}
	case "api new", "new api":
		return commandHelp{Name: command, Short: "Create an API service scaffold.", Usage: "gofly " + command + " <name> --module <module> [flags]", Flags: []string{"--name <name>                  API service name", "--module <module>              Go module path", "--dir <dir>                    output directory", "--style minimal|basic|production", "--profile <profile>            generation profile: gofly-ai|gozero-compatible|kitex-compatible", "--home, --remote, --branch     template source options", "--client, --idea, --go_opt     accepted scaffold options", "--json                         emit scaffold result as JSON"}, Examples: []string{"gofly new api hello --module example.com/hello --style go_zero", "gofly api new hello --module example.com/hello --dir hello --profile gozero-compatible --json"}}
	case "api client", "api ts", "api js", "api dart", "api java", "api kotlin":
		return commandHelp{Name: command, Short: "Generate typed API client code from an .api file.", Usage: "gofly " + command + " --api <service.api> --dir <dir> [flags]", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   output directory", "--language <name>             client language for api client", "--caller, --unwrap, --legacy  client generation options", "--hostname, --scheme, --pkg   client generation options"}, Examples: []string{"gofly api ts -api user.api -dir web/src/client", "gofly api client -api user.api -dir clients -language java"}}
	case "api plugin":
		return commandHelp{Name: "api plugin", Short: "Run an API generation plugin.", Usage: "gofly api plugin --api <service.api> --plugin <plugin> [--dir <dir>]", Flags: []string{"-api, --api, --file <file>  API definition file", "--plugin, -p <plugin>         plugin executable", "--dir <dir>                   working/output directory", "--style <style>               plugin style option"}, Examples: []string{"gofly api plugin -api user.api -plugin ./my-plugin -dir ."}}
	case "api middleware":
		return commandHelp{Name: "api middleware", Short: "Generate middleware skeletons by name or from an API file.", Usage: "gofly api middleware <name> --dir <service-dir> | gofly api middleware --api <service.api> --dir <service-dir>", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   service root directory"}, Examples: []string{"gofly api middleware auth --dir .", "gofly api middleware -api user.api -dir ."}}
	case "gen middleware":
		return commandHelp{Name: "gen middleware", Short: "Generate middleware skeletons by name or from an API file.", Usage: "gofly gen middleware <name> --dir <service-dir> | gofly gen middleware --api <service.api> --dir <service-dir>", Flags: []string{"-api, --api, --file <file>  API definition file", "--dir <dir>                   service root directory"}, Examples: []string{"gofly gen middleware auth --dir .", "gofly gen middleware -api user.api -dir ."}}
	case "rpc gen":
		return commandHelp{Name: "rpc gen", Short: "Generate gofly/gRPC service code from a protobuf file.", Usage: "gofly rpc gen --src <service.proto> --out <dir> [flags]", Flags: []string{"--src, --file <file>           protobuf source file", "--out, --dir <dir>             output directory", "--package <pkg>                generated package name", "--profile <profile>            generation profile: gofly-ai|gozero-compatible|kitex-compatible", "--transport grpc|gofly|both    transport targets", "--with-middleware              generate middleware/interceptor chain helpers", "--with-recovery                generate recovery option helpers", "--with-validator               generate validator and biz error helpers", "--standard                     also run standard protoc plugins", "--timeout <duration>           maximum protoc execution time with --standard", "--style go_zero                scaffold style option", "--home, --remote, --branch     template source options", "--json                         emit generation result as JSON"}, Examples: []string{"gofly rpc gen -src greeter.proto -out . -style go_zero", "gofly rpc gen greeter.proto --out rpc --transport gofly --profile kitex-compatible --json"}}
	case "rpc idl":
		return commandHelp{Name: "rpc idl", Short: "Inspect proto or thrift IDL metadata.", Usage: "gofly rpc idl --file <service.proto|service.thrift> [--format text|json]", Flags: []string{"--file, --src <file>       proto or thrift IDL file", "--format text|json        output format"}, Examples: []string{"gofly rpc idl greeter.proto --format json", "gofly rpc idl --file greeter.thrift"}}
	case "rpc thrift":
		return commandHelp{Name: "rpc thrift", Short: "Convert a thrift IDL to a proto compatibility skeleton.", Usage: "gofly rpc thrift --file <service.thrift> --out <dir>", Flags: []string{"--file, --src <file>       thrift IDL file", "--out, --dir <dir>         output directory"}, Examples: []string{"gofly rpc thrift greeter.thrift --out proto"}}
	case "rpc client":
		return commandHelp{Name: "rpc client", Short: "Generate gofly RPC client wrapper code from proto or thrift IDL.", Usage: "gofly rpc client --file <service.proto|service.thrift> --out <dir> [--package <pkg>]", Flags: []string{"--file, --src <file>       proto or thrift IDL file", "--out, --dir <dir>         output directory", "--package <pkg>            generated package name"}, Examples: []string{"gofly rpc client greeter.proto --out internal/rpcclient --package rpcclient"}}
	case "rpc server":
		return commandHelp{Name: "rpc server", Short: "Generate gofly RPC server implementation stubs from proto or thrift IDL.", Usage: "gofly rpc server --file <service.proto|service.thrift> --out <dir> [--package <pkg>]", Flags: []string{"--file, --src <file>       proto or thrift IDL file", "--out, --dir <dir>         output directory", "--package <pkg>            generated package name"}, Examples: []string{"gofly rpc server greeter.proto --out internal/rpcimpl --package rpcimpl"}}
	case "rpc middleware":
		return commandHelp{Name: "rpc middleware", Short: "Generate a gRPC unary middleware skeleton.", Usage: "gofly rpc middleware <name> --dir <service-dir>", Flags: []string{"--name <name>       middleware name", "--dir <dir>         service root directory"}, Examples: []string{"gofly rpc middleware auth --dir ."}}
	case "rpc lint":
		return commandHelp{Name: "rpc lint", Short: "Lint proto or thrift IDL for service/method contract completeness.", Usage: "gofly rpc lint --file <service.proto|service.thrift>", Flags: []string{"--file, --src <file>       proto or thrift IDL file"}, Examples: []string{"gofly rpc lint greeter.proto"}}
	case "rpc deps":
		return commandHelp{Name: "rpc deps", Short: "List proto imports or thrift includes.", Usage: "gofly rpc deps --file <service.proto|service.thrift> [--format text|json]", Flags: []string{"--file, --src <file>       proto or thrift IDL file", "--format text|json        output format"}, Examples: []string{"gofly rpc deps greeter.proto", "gofly rpc deps greeter.thrift --format json"}}
	case "gen rpc":
		return commandHelp{Name: "gen rpc", Short: "Generate gofly/gRPC service code from a protobuf file.", Usage: "gofly gen rpc --src <service.proto> --out <dir> [flags]", Flags: []string{"--src, --file <file>           protobuf source file", "--out, --dir <dir>             output directory", "--package <pkg>                generated package name", "--profile <profile>            generation profile: gofly-ai|gozero-compatible|kitex-compatible", "--transport grpc|gofly|both    transport targets", "--with-middleware              generate middleware/interceptor chain helpers", "--with-recovery                generate recovery option helpers", "--with-validator               generate validator and biz error helpers", "--standard                     also run standard protoc plugins", "--timeout <duration>           maximum protoc execution time with --standard", "--style go_zero                scaffold style option", "--home, --remote, --branch     template source options", "--json                         emit generation result as JSON"}, Examples: []string{"gofly gen rpc -src greeter.proto -out . -style go_zero", "gofly gen rpc greeter.proto --out rpc --transport gofly --profile kitex-compatible --json"}}
	case "rpc protoc":
		return commandHelp{Name: "rpc protoc", Short: "Run standard protoc Go plugins.", Usage: "gofly rpc protoc <service.proto> [--I <paths>] [--go_out <dir>] [--go-grpc_out <dir>]", Flags: []string{"--src, --file <file>    protobuf source file", "--dir <dir>             output directory", "--I, --proto_path       include paths", "--go_out <dir>          protoc-gen-go output", "--go-grpc_out <dir>     protoc-gen-go-grpc output", "--zrpc_out <dir>        service output directory", "--extra <args>          extra protoc arguments", "--timeout <duration>    maximum protoc execution time"}, Examples: []string{"gofly rpc protoc greeter.proto -I . --go_out . --go-grpc_out ."}}
	case "rpc check":
		return commandHelp{Name: "rpc check", Short: "Validate protobuf syntax and generator support.", Usage: "gofly rpc check --src <service.proto>", Flags: []string{"--src, --file <file>  protobuf source file"}, Examples: []string{"gofly rpc check -src greeter.proto"}}
	case "rpc breaking":
		return commandHelp{Name: "rpc breaking", Short: "Detect breaking changes between two protobuf files.", Usage: "gofly rpc breaking --base <old.proto> --target <new.proto>", Flags: []string{"--base <file>      old protobuf file", "--target <file>    new protobuf file"}, Examples: []string{"gofly rpc breaking old.proto new.proto"}}
	case "rpc descriptor":
		return commandHelp{Name: "rpc descriptor", Short: "Compare runtime RPC descriptors for compatibility.", Usage: "gofly rpc descriptor --base <old-descriptor.json|url> --target <new-descriptor.json|url> [--format text|json]", Flags: []string{"--base <file|url>       old rpc.Descriptor json, descriptor URL, or admin base URL", "--target <file|url>     new rpc.Descriptor json, descriptor URL, or admin base URL", "--url <admin-url>       alias source for a remote admin descriptor endpoint", "--service <name>        service name when URL points at /admin or /rpc/admin/descriptors", "--token <token>         bearer token for descriptor URL sources", "--format text|json      output format"}, Examples: []string{"gofly rpc descriptor old.json new.json", "gofly rpc descriptor --url http://127.0.0.1:9090/admin --service greeter --target next.json", "gofly rpc descriptor --base http://127.0.0.1:8081/rpc/admin/descriptors/greeter --target next.json --token secret", "gofly rpc descriptor --base old.json --target new.json --format json"}}
	case "rpc plugin":
		return commandHelp{Name: "rpc plugin", Short: "Run a gofly RPC plugin.", Usage: "gofly rpc plugin <plugin> --file <service.proto> [--dir <dir>]", Flags: []string{"--file, --src <file>    protobuf source file", "--plugin <plugin>      plugin executable", "--dir <dir>            working/output directory"}, Examples: []string{"gofly rpc plugin ./my-plugin --file greeter.proto --dir ."}}
	case "rpc template":
		return commandHelp{Name: "rpc template", Short: "Generate starter proto templates or manage local/remote templates.", Usage: "gofly rpc template [-o <file>] [--name <name>] [--home <dir>] [--remote <repo|dir>] [--branch <branch>]", Flags: []string{"-o, --output <file>  write starter .proto template", "--name <name>       service name used by template", "--home <dir>        template directory for starter template", "--remote <repo|dir> remote git repository or local template directory", "--branch <branch>   remote git branch", "init|list|clean|update|revert manage template directory"}, Examples: []string{"gofly rpc template -o greeter.proto --name Greeter", "gofly rpc template -o greeter.proto --remote ./company-templates", "gofly rpc template init --home .gofly/templates"}}
	case "rpc new", "new rpc":
		return commandHelp{Name: command, Short: "Create an RPC service scaffold.", Usage: "gofly " + command + " <name> --module <module> [flags]", Flags: []string{"--name <name>                  RPC service name", "--module <module>              Go module path", "--dir <dir>                    output directory", "--style minimal|basic|production", "--profile <profile>            generation profile: gofly-ai|gozero-compatible|kitex-compatible", "--home, --remote, --branch     template source options", "--client, --go_opt, --go-grpc_opt accepted scaffold options", "--json                         emit scaffold result as JSON"}, Examples: []string{"gofly new rpc greeter --module example.com/greeter --style go_zero", "gofly rpc new greeter --module example.com/greeter --dir greeter --profile kitex-compatible --json"}}
	case "model gen", "model mysql ddl", "model pg ddl":
		return commandHelp{Name: command, Short: "Generate SQL model code from DDL.", Usage: "gofly " + command + " --ddl <schema.sql> [<dir>] [flags]", Flags: []string{"--ddl, --src, -s <file>        SQL DDL file", "--dir, -d <dir>                output directory", "--package <pkg>                generated package name", "--module <module>              module import path", "--table, --tables, -t <tables> table filter", "--style go_zero|gorm           model style", "--cache, -c                    cache option", "--home, --remote, --branch     template source options"}, Examples: []string{"gofly model gen -ddl schema.sql ./internal --style gorm", "gofly model mysql ddl -src schema.sql -dir . -style go_zero"}}
	case "gen model":
		return commandHelp{Name: "gen model", Short: "Generate SQL model code from DDL.", Usage: "gofly gen model --ddl <schema.sql> [<dir>] [flags]", Flags: []string{"--ddl, --src, -s <file>        SQL DDL file", "--dir, -d <dir>                output directory", "--package <pkg>                generated package name", "--module <module>              module import path", "--table, --tables, -t <tables> table filter", "--style go_zero|gorm           model style", "--cache, -c                    cache option", "--home, --remote, --branch     template source options"}, Examples: []string{"gofly gen model -ddl schema.sql ./internal --style gorm"}}
	case "model mysql datasource", "model pg datasource":
		return commandHelp{Name: command, Short: "Generate SQL model code by introspecting a database datasource.", Usage: "gofly " + command + " --url <dsn> --table <tables> --dir <dir> [flags]", Flags: []string{"--url, --dsn, --datasource <dsn> database datasource URL", "--table, --tables, -t <tables>   table filter", "--dir, -d <dir>                   output directory", "--package <pkg>                   generated package name", "--module <module>                 module import path", "--database <db>, --schema <name>  database/schema compatibility flags", "--style go_zero|gorm              model style"}, Examples: []string{"gofly model mysql datasource -datasource 'user:pass@tcp(localhost:3306)/app' -t users -d .", "gofly model pg datasource -url postgres://localhost/app -t accounts -d ."}}
	case "model mongo":
		return commandHelp{Name: "model mongo", Short: "Generate Mongo repository skeleton.", Usage: "gofly model mongo --type <name> --dir <dir> [--package <pkg>]", Flags: []string{"--type, -t <name>     model type name", "--dir, -d <dir>       output directory", "--package <pkg>       generated package name"}, Examples: []string{"gofly model mongo -t UserProfile -d internal/model"}}
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
		}
	case "rpc":
		return commandHelp{
			Name:  "rpc",
			Short: "Generate, check and scaffold RPC services from proto or thrift IDL files.",
			Usage: "gofly rpc <command> [arguments]",
			Commands: []helpCommand{
				{Name: "new", Short: "create an RPC service scaffold"},
				{Name: "idl", Short: "inspect proto/thrift IDL metadata"},
				{Name: "thrift", Short: "convert thrift IDL to proto skeleton"},
				{Name: "client", Short: "generate RPC client wrapper"},
				{Name: "server", Short: "generate RPC server stubs"},
				{Name: "middleware", Short: "generate gRPC middleware skeleton"},
				{Name: "lint", Short: "lint IDL contract completeness"},
				{Name: "deps", Short: "list IDL imports/includes"},
				{Name: "gen", Short: "generate gofly/grpc code from proto"},
				{Name: "protoc", Short: "run standard protoc Go plugins"},
				{Name: "check", Short: "validate proto and generator support"},
				{Name: "breaking", Short: "compare proto/API compatibility"},
				{Name: "descriptor", Short: "compare runtime descriptor compatibility"},
				{Name: "plugin", Short: "run RPC plugin"},
				{Name: "template", Short: "generate proto template or manage templates"},
			},
			Flags: []string{"-h, --help  show help for rpc", "-o <file>   generate a starter .proto template"},
			Examples: []string{
				"gofly rpc gen -src greeter.proto -out . -style go_zero",
				"gofly rpc protoc greeter.proto -zrpc_out . -go_opt paths=source_relative",
				"gofly rpc -o greeter.proto",
			},
		}
	case "model":
		return commandHelp{
			Name:  "model",
			Short: "Generate SQL or Mongo models from DDL or database datasource.",
			Usage: "gofly model <command> [arguments]",
			Commands: []helpCommand{
				{Name: "gen", Short: "generate SQL model from DDL"},
				{Name: "mysql ddl", Short: "generate SQL model from MySQL DDL"},
				{Name: "mysql datasource", Short: "generate model by introspecting MySQL"},
				{Name: "pg ddl", Short: "generate SQL model from PostgreSQL DDL"},
				{Name: "pg datasource", Short: "generate model by introspecting PostgreSQL"},
				{Name: "mongo", Short: "generate Mongo repository skeleton"},
			},
			Flags: []string{"--style go_zero|gorm  choose model style", "-s, --src <ddl>       DDL source file", "-d, --dir <dir>       output directory"},
			Examples: []string{
				"gofly model mysql ddl -src schema.sql -dir . -style gorm",
				"gofly model mysql datasource -datasource 'user:pass@tcp(localhost:3306)/app' -t users -d .",
			},
		}
	case "new":
		return commandHelp{
			Name:     "new",
			Short:    "Create production, API, or RPC service scaffolds.",
			Usage:    "gofly new service|api|rpc <name> --module <module> [flags]",
			Commands: []helpCommand{{Name: "service", Short: "create golden-path production service"}, {Name: "api", Short: "create API service"}, {Name: "rpc", Short: "create RPC service"}},
			Examples: []string{"gofly new service orders --style production --module example.com/orders", "gofly new api hello -module example.com/hello -dir hello", "gofly new rpc greeter -module example.com/greeter -style go_zero"},
		}
	case "gen":
		return commandHelp{
			Name:     "gen",
			Short:    "Unified generator entrypoint.",
			Usage:    "gofly gen handler|rpc|api|rest|middleware|model|gateway [arguments]",
			Commands: []helpCommand{{Name: "handler", Short: "generate REST handler"}, {Name: "rpc", Short: "generate RPC code"}, {Name: "api/rest", Short: "generate REST code"}, {Name: "middleware", Short: "generate middleware skeletons"}, {Name: "model", Short: "generate model code"}, {Name: "gateway", Short: "generate API gateway"}},
		}
	case "gen handler", "handler gen":
		return commandHelp{Name: command, Short: "Generate REST handler skeletons.", Usage: "gofly " + command + " <name> --dir <service-dir> [--module <module>] [--path <subdir>]", Flags: []string{"--name <name>      handler name", "--dir <dir>        service root directory", "--module <module>  Go module path", "--path <subdir>    handler subdirectory under internal/api"}, Examples: []string{"gofly " + command + " CreateOrder --dir . --path v1/order"}}
	case "handler complete":
		return commandHelp{Name: "handler complete", Short: "Append missing methods to an existing handler Go file.", Usage: "gofly handler complete --file <handler.go> --method <name> [flags]", Flags: []string{"--file <file>       handler Go source file", "--method <name>     method or handler name", "--receiver <name>   receiver name override", "--package <pkg>     package name when creating a file", "--body <stmt>       method body Go statements", "--comment <text>    comment attached to the method"}, Examples: []string{"gofly handler complete --file internal/svc/service.go --method HealthCheck"}}
	case "gen gateway":
		return commandHelp{Name: "gen gateway", Short: "Generate an API gateway scaffold.", Usage: "gofly gen gateway <name> --module <module> --dir <dir>", Flags: []string{"--name <name>      gateway service name", "--module <module>  Go module path", "--dir <dir>        output directory"}, Examples: []string{"gofly gen gateway edge --module example.com/edge --dir edge-gateway"}}
	case "version":
		return commandHelp{Name: "version", Short: "Print version and build metadata.", Usage: "gofly version [--json]", Flags: []string{"--json  print structured version metadata as JSON"}, Examples: []string{"gofly version", "gofly version --json"}}
	case "docker":
		return commandHelp{Name: "docker", Short: "Generate a Dockerfile.", Usage: "gofly docker <name> [--go <main>] [--exe <binary>] [--base <image>] [--o <file>]", Flags: []string{"--name <name>        service name, also accepted as positional", "--dir <dir>          output directory", "--output, --o <file> output Dockerfile path", "--go <main>          main package or Go file to build", "--exe <binary>       binary name", "--base <image>       runtime base image"}, Examples: []string{"gofly docker hello --go ./cmd/server --exe server --base alpine:3.20"}}
	case "kube":
		return commandHelp{Name: "kube", Short: "Generate Kubernetes manifests.", Usage: "gofly kube deploy|service|ingress|configmap|job <name> [flags]", Commands: []helpCommand{{Name: "deploy", Short: "generate Deployment"}, {Name: "service", Short: "generate Service"}, {Name: "ingress", Short: "generate Ingress"}, {Name: "configmap", Short: "generate ConfigMap"}, {Name: "job", Short: "generate Job"}}, Examples: []string{"gofly kube deploy hello --image example/hello:v1 --namespace apps --o deploy.yaml"}}
	case "kube deploy", "kube service", "kube ingress", "kube configmap", "kube job":
		return commandHelp{Name: command, Short: "Generate a Kubernetes " + strings.TrimPrefix(command, "kube ") + " manifest.", Usage: "gofly " + command + " <name> [flags]", Flags: []string{"--name <name>          resource/service name", "--dir <dir>            output directory", "--output, --o <file>   output YAML path", "--namespace <ns>       Kubernetes namespace", "--image <image>        container image", "--port <port>          HTTP container port", "--targetPort <port>    target container port", "--rpc-port <port>      RPC container port", "--replicas <n>         Deployment replicas", "--host <host>          ingress host", "--path <path>          ingress path", "--data k=v             ConfigMap data"}, Examples: []string{"gofly " + command + " hello --image example/hello:v1 --namespace apps --o manifest.yaml"}}
	case "template":
		return commandHelp{Name: "template", Short: "Manage local or remote generation templates.", Usage: "gofly template init|list|clean|update|revert [--dir|--home <dir>] [--remote <repo|dir>] [--branch <branch>]", Commands: []helpCommand{{Name: "init", Short: "write default templates or sync remote templates"}, {Name: "list", Short: "list resolved templates"}, {Name: "clean", Short: "remove template directory"}, {Name: "update", Short: "refresh local templates from remote or defaults"}, {Name: "revert", Short: "restore default templates"}}, Examples: []string{"gofly template init --home .gofly/templates", "gofly template update --remote ./company-templates --home .gofly/templates"}}
	case "template init", "template list", "template clean", "template update", "template revert":
		return commandHelp{Name: command, Short: "Manage local or remote generation templates.", Usage: "gofly " + command + " [--dir|--home <template-dir>] [--remote <repo|dir>] [--branch <branch>]", Flags: []string{"--dir <dir>       template directory", "--home <dir>      template directory", "--remote <repo>   remote git repository or local template directory", "--branch <branch> remote git branch", "--category, -c    template category filter", "--name, -n        template name filter"}, Examples: []string{"gofly " + command + " --home .gofly/templates", "gofly template update --remote ./company-templates --home .gofly/templates"}}
	case "quickstart":
		return commandHelp{Name: "quickstart", Short: "Create a runnable API service quickly.", Usage: "gofly quickstart <name> --module <module> [--dir <dir>] [--style minimal|basic|production]", Flags: []string{"--name <name>          service name, also accepted as positional", "--module <module>      Go module path", "--dir <dir>            output directory", "--style <style>        scaffold style", "--api-spec             generate an .api file", "--service-type, -t     quickstart service type: mono or micro"}, Examples: []string{"gofly quickstart checkout --module example.com/checkout --t micro"}}
	case "migrate", "migration", "migrate create", "migrate new", "migration create", "migration new":
		return commandHelp{Name: command, Short: "Create SQL migration files.", Usage: "gofly migrate create <name> [--dir <dir>]", Flags: []string{"--name <name>  migration name, also accepted as positional", "--dir <dir>    migration output directory"}, Examples: []string{"gofly migrate create add-users --dir migrations"}}
	case "env":
		return commandHelp{Name: "env", Short: "Print and check local toolchain environment.", Usage: "gofly env [--json] | gofly env check [--json]", Flags: []string{"--json          print environment as JSON", "--write, -w     write environment key=value", "--verbose, -v   print verbose output"}, Examples: []string{"gofly env check --json"}}
	case "env check", "env install":
		return commandHelp{Name: command, Short: "Check local toolchain dependencies.", Usage: "gofly env check [--json] [--install]", Flags: []string{"--json          print check result as JSON", "--install, -i   request installation guidance"}, Examples: []string{"gofly env check --json", "gofly env install"}}
	case "config":
		return commandHelp{Name: "config", Short: "Manage .gofly/config.json.", Usage: "gofly config init|show|get|set|clean [flags]", Examples: []string{"gofly config init --dir . --name hello --module example.com/hello"}}
	case "config init":
		return commandHelp{Name: "config init", Short: "Create a .gofly/config.json for a service.", Usage: "gofly config init --dir <service-dir> --name <service> --module <module> [--style <style>]", Flags: []string{"--dir <dir>       service root directory", "--name <name>     service name", "--module <path>   Go module path", "--style <style>   scaffold style"}, Examples: []string{"gofly config init --dir . --name hello --module example.com/hello --style basic"}}
	case "config show":
		return commandHelp{Name: "config show", Short: "Print the resolved .gofly/config.json.", Usage: "gofly config show --dir <service-dir>", Flags: []string{"--dir <dir>  service root directory"}, Examples: []string{"gofly config show --dir ."}}
	case "config get":
		return commandHelp{Name: "config get", Short: "Read one value from .gofly/config.json.", Usage: "gofly config get <key> --dir <service-dir>", Flags: []string{"--key <key>  config key, also accepted as positional", "--dir <dir>  service root directory"}, Examples: []string{"gofly config get module --dir .", "gofly config get --key rpc.transport --dir ."}}
	case "config set":
		return commandHelp{Name: "config set", Short: "Update one value in .gofly/config.json.", Usage: "gofly config set <key> <value> --dir <service-dir>", Flags: []string{"--key <key>      config key, also accepted as positional", "--value <value>  config value, also accepted as positional", "--dir <dir>      service root directory"}, Examples: []string{"gofly config set style production --dir .", "gofly config set --key module --value example.com/hello --dir ."}}
	case "config clean":
		return commandHelp{Name: "config clean", Short: "Remove .gofly/config.json from a service directory.", Usage: "gofly config clean --dir <service-dir>", Flags: []string{"--dir <dir>  service root directory"}, Examples: []string{"gofly config clean --dir ."}}
	case "feature":
		return commandHelp{Name: "feature", Short: "List and preview built-in scaffold features.", Usage: "gofly feature list | gofly feature run <feature> [features...] [flags]", Commands: []helpCommand{{Name: "list", Short: "list registered features"}, {Name: "run", Short: "preview files generated by one or more features"}}, Examples: []string{"gofly feature list", "gofly feature list --json", "gofly feature run ecosystem-compat --name hello --module example.com/hello --dir .", "gofly feature run --features http-compat,rpc-compat --format json --name hello --module example.com/hello"}}
	case "feature list":
		return commandHelp{Name: "feature list", Short: "List registered scaffold features.", Usage: "gofly feature list [--format text|json] [--json]", Flags: []string{"--format <format>  output format: text|json", "--json             output JSON"}, Examples: []string{"gofly feature list", "gofly feature list --json"}}
	case "feature run":
		return commandHelp{Name: "feature run", Short: "Preview generated files for one or more scaffold features.", Usage: "gofly feature run <feature-name> [features...] --name <service> --module <module> --dir <dir> [--style <style>] [--format text|json]", Flags: []string{"--name <name>       service name", "--module <path>     Go module path", "--dir <dir>         service directory", "--style <style>     scaffold style", "--feature <names>   feature names, comma-separated", "--features <names>  alias for --feature", "--format <format>   output format: text|json", "--json              output JSON"}, Examples: []string{"gofly feature run ecosystem-compat --name hello --module example.com/hello --dir .", "gofly feature run http-compat rpc-compat --name hello --module example.com/hello", "gofly feature run --features http-compat,rpc-compat --format json --name hello --module example.com/hello"}}
	case "plugin":
		return commandHelp{Name: "plugin", Short: "List, search, install and run gofly generation plugins.", Usage: "gofly plugin list | gofly plugin search --registry <url-or-path> [query] | gofly plugin install --remote <repo>@<version> | gofly plugin run <plugin> [flags]", Commands: []helpCommand{{Name: "list", Short: "list built-in and cached plugins"}, {Name: "search", Short: "search a plugin registry index"}, {Name: "install", Short: "download a version-pinned remote plugin"}, {Name: "uninstall", Short: "remove a version-pinned cached plugin"}, {Name: "run", Short: "run a built-in, cached or external plugin"}}, Examples: []string{"gofly plugin list --json", "gofly plugin search --registry ./plugins.json redis --json", "gofly plugin install --remote https://example.com/gofly-plugin@v1.2.3 --json", "gofly plugin run --remote https://example.com/gofly-plugin@v1.2.3 --name hello --module example.com/hello --dir . --json", "gofly plugin run --go-plugin ./plugins --name hello --module example.com/hello --dir ."}}
	case "plugin list":
		return commandHelp{Name: "plugin list", Short: "List built-in generation plugins and cached generation plugins.", Usage: "gofly plugin list [--format text|json] [--json]", Flags: []string{"--format <format>  output format: text|json", "--json             output JSON"}, Examples: []string{"gofly plugin list", "gofly plugin list --json"}}
	case "plugin search":
		return commandHelp{Name: "plugin search", Short: "Search a plugin registry index without installing plugins.", Usage: "gofly plugin search --registry <url-or-path> [query] [--format text|json] [--json]", Flags: []string{"--registry <url-or-path>  registry JSON URL or path", "--query <query>           search query, also accepted as positional", "--format <format>         output format: text|json", "--json                    output JSON"}, Examples: []string{"gofly plugin search --registry ./plugins.json", "gofly plugin search --registry https://example.com/gofly-plugins.json redis --json"}}
	case "plugin install":
		return commandHelp{Name: "plugin install", Short: "Install a version-pinned remote generation plugin into ~/.cache/gofly/plugins/<hash>.", Usage: "gofly plugin install --remote <repo-or-url>@<version> [--json]", Flags: []string{"--remote <repo>@<version>  version-pinned plugin URL, file:// path, file path or executable directory", "--json                    output JSON with binary digest metadata"}, Examples: []string{"gofly plugin install --remote https://example.com/gofly-plugin@v1.2.3", "gofly plugin install --remote ./plugins/my-plugin@dev --json"}}
	case "plugin uninstall":
		return commandHelp{Name: "plugin uninstall", Short: "Remove a version-pinned remote generation plugin from ~/.cache/gofly/plugins/<hash>.", Usage: "gofly plugin uninstall --remote <repo-or-url>@<version> [--json]", Flags: []string{"--remote <repo>@<version>  version-pinned plugin identity", "--json                    output JSON"}, Examples: []string{"gofly plugin uninstall --remote https://example.com/gofly-plugin@v1.2.3 --json"}}
	case "plugin run":
		return commandHelp{Name: "plugin run", Short: "Run a built-in or external generation plugin, including cached plugins.", Usage: "gofly plugin run <plugin-name-or-path> --name <service> --module <module> --dir <dir> [--command <kind>] [--json]", Flags: []string{"--name <name>                  service name", "--module <path>                Go module path", "--dir <dir>                    service directory", "--command <kind>               plugin command: service|handler|model", "--remote <repo>@<version>      auto-install and run cached remote plugin", "--go-plugin <path-or-dir>      run one executable plugin or traverse a directory of executable plugins", "--json                         output JSON run summary"}, Examples: []string{"gofly plugin run ./my-plugin --name hello --module example.com/hello --dir . --json", "gofly plugin run --remote https://example.com/gofly-plugin@v1.2.3 --name hello --module example.com/hello --dir .", "gofly plugin run --go-plugin ./plugins --command service --dir ."}}
	case "complete", "complete handler":
		return commandHelp{Name: command, Short: "Emit shell completion scripts.", Usage: "gofly complete handler " + completionShellUsage, Examples: []string{"gofly complete handler bash > gofly.bash", "gofly complete handler zsh > _gofly"}}
	case "completion":
		return commandHelp{Name: "completion", Short: "Emit shell completion scripts.", Usage: "gofly completion " + completionShellUsage, Examples: []string{"gofly completion bash > gofly.bash"}}
	case "completion bash", "completion zsh", "completion fish", "completion powershell":
		shell := strings.TrimPrefix(command, "completion ")
		return commandHelp{Name: command, Short: "Emit " + shell + " completion script.", Usage: "gofly completion " + shell, Examples: []string{"gofly completion " + shell + " > gofly." + shell}}
	case "release":
		return commandHelp{Name: "release", Short: "Run release readiness checks before publishing artifacts.", Usage: "gofly release check [flags]", Commands: []helpCommand{{Name: "check", Short: "run API/RPC/API-compat/changelog/tidy release gates"}}, Examples: []string{"gofly release check", "gofly release check --strict --json"}}
	case "release check":
		return commandHelp{Name: "release check", Short: "Aggregate API/RPC breaking checks, Go public API compatibility, changelog version, and module tidiness.", Usage: "gofly release check [--api-base <old.api> --api-target <new.api>] [--rpc-base <old.proto> --rpc-target <new.proto>] [--changelog CHANGELOG.md] [--strict] [--json]", Flags: []string{"--api-base <file>     base .api file for breaking detection", "--api-target <file>   target .api file for breaking detection", "--rpc-base <file>     base .proto file for breaking detection", "--rpc-target <file>   target .proto file for breaking detection", "--changelog <file>    changelog file to inspect (default CHANGELOG.md)", "--strict              treat warnings as blockers", "--json                output JSON report"}, Examples: []string{"gofly release check", "gofly release check --api-base old.api --api-target new.api --rpc-base old.proto --rpc-target new.proto --strict", "gofly release check --json"}}
	case "complete handler bash", "complete handler zsh", "complete handler fish", "complete handler powershell":
		shell := strings.TrimPrefix(command, "complete handler ")
		return commandHelp{Name: command, Short: "Emit " + shell + " completion script.", Usage: "gofly complete handler " + shell, Examples: []string{"gofly complete handler " + shell + " > gofly." + shell}}
	case "doctor":
		return commandHelp{Name: "doctor", Short: "Diagnose local environment and toolchain readiness.", Usage: "gofly doctor [--json]", Flags: []string{"--json  print report as JSON"}, Examples: []string{"gofly doctor", "gofly doctor --json"}}
	case "ai":
		return commandHelp{Name: "ai", Short: "Emit machine-readable metadata and governed LLM calls for AI agents.", Usage: "gofly ai manifest [--format json|text] [--schema jsonschema] [--json] | gofly ai control-plane [--format json|text] [--schema jsonschema] [--source <url>] [--admin-token <token>] [--from-checksum <sha256>|--from-snapshot <file>] [--watch --max-events <n>] [--json] | gofly ai plan --prompt <text> [flags] | gofly ai new --prompt <text> [--apply] [flags] | gofly ai complete --prompt <text> [flags] | gofly ai stream --prompt <text> [flags]", Commands: []helpCommand{{Name: "manifest", Short: "print command schemas, side effects and output contract"}, {Name: "control-plane", Short: "print or watch the deterministic AI control-plane snapshot"}, {Name: "plan", Short: "plan an AI-first project scaffold without writing files"}, {Name: "new", Short: "plan or apply an AI-first project scaffold"}, {Name: "complete", Short: "run a governed no-op completion for integration testing"}, {Name: "stream", Short: "emit governed streaming completion events"}, {Name: "doctor", Short: "run AI subsystem self-diagnostics"}}, Examples: []string{"gofly ai manifest --format json", "gofly ai manifest --schema jsonschema", "gofly ai control-plane --schema jsonschema", "gofly ai control-plane --json", "gofly ai control-plane --source http://127.0.0.1:8080/admin/control-plane --json", "gofly ai control-plane --from-checksum <sha256> --json", "gofly ai control-plane --from-snapshot previous-control-plane.json --json", "gofly ai control-plane --watch --max-events 1 --json", "gofly --output json ai manifest", "gofly ai plan 'create a rag service' --json", "gofly ai new 'create a rest api' --name hello --module example.com/hello --dir hello --apply", "gofly ai complete --prompt 'summarize this' --max-total-tokens 128 --json", "gofly ai stream --prompt 'summarize this' --json", "gofly ai doctor --json"}}
	case "ai manifest":
		return commandHelp{Name: "ai manifest", Short: "Print the gofly AI tool manifest.", Usage: "gofly ai manifest [--format json|text] [--schema jsonschema] [--json]", Flags: []string{"--format <format>  output format: json|text (default json)", "--schema <schema>  output manifest JSON Schema: jsonschema", "--json             output JSON envelope"}, Examples: []string{"gofly ai manifest --format json", "gofly ai manifest --schema jsonschema", "gofly tools manifest --json"}}
	case "ai plan":
		return commandHelp{Name: "ai plan", Short: "Plan an AI-first project scaffold without writing files.", Usage: "gofly ai plan --prompt <requirement> [--kind service|rpc|worker|cli|ai-agent|rag|gateway] [--name <name>] [--module <module>] [--dir <dir>] [--format text|json] [--json]", Flags: []string{"--prompt <text>       natural language requirement, also accepted as positional text", "--kind <kind>         optional project kind hint", "--name <name>         project or service name", "--module <module>     Go module path", "--dir <dir>           output directory", "--format <format>     output format: text|json", "--json                output JSON envelope"}, Examples: []string{"gofly ai plan 'create a rag service with redis vector store' --json", "gofly ai plan --prompt 'create a gateway' --kind gateway --name edge --module example.com/edge --dir edge"}}
	case "ai new":
		return commandHelp{Name: "ai new", Short: "Plan or apply an AI-first project scaffold.", Usage: "gofly ai new --prompt <requirement> [--template <id>] [--kind <kind>] --name <name> --module <module> --dir <dir> [--dry-run|--plan|--apply] [--verify] [--format text|json] [--json]", Flags: []string{"--prompt <text>       natural language requirement, also accepted as positional text", "--template <id>       explicit template id from `gofly template list`", "--kind <kind>         optional project kind hint", "--name <name>         project or service name", "--module <module>     Go module path", "--dir <dir>           output directory", "--dry-run, --plan     print plan without writing files (default)", "--apply               write scaffold files using the selected built-in generator", "--verify              run supported post-generation checks after --apply", "--verify-timeout <d>  timeout per verification command (default 2m)", "--format <format>     output format: text|json", "--json                output JSON envelope"}, Examples: []string{"gofly ai new 'create a rest api' --name hello --module example.com/hello --dir hello --dry-run --json", "gofly ai new --template go-rpc-grpc --name greeter --module example.com/greeter --dir greeter --apply --verify"}}
	case "ai complete":
		return commandHelp{Name: "ai complete", Short: "Execute a governed no-op LLM completion for deterministic AI tool integration tests.", Usage: "gofly ai complete --prompt <text> [--stream] [--config .gofly/config.json] [--provider noop] [--model <model>] [--max-input-tokens <n>] [--max-output-tokens <n>] [--max-total-tokens <n>] [--rate-limit <n>] [--timeout <duration>] [--allow-failover|--failover] [--dry-run|--plan] [--format text|json] [--json]", Flags: []string{"--prompt <text>          prompt text, also accepted as positional text", "--stream                 emit governed stream events", "--config <file>          gofly config file with llm defaults", "--dir <dir>              service root for .gofly/config.json", "--provider <provider>    provider mode; only noop is built in", "--model <model>          model label for audit and output", "--max-input-tokens <n>   input token budget", "--max-output-tokens <n>  output token budget", "--max-total-tokens <n>   total token budget", "--rate-limit <n>         calls per second; zero disables rate limiting", "--rate-burst <n>         rate limit burst; zero uses rate-limit", "--timeout <duration>     provider call timeout", "--allow-failover         manually retry retryable failures against configured fallback providers", "--failover               alias for --allow-failover", "--dry-run, --plan        print governance plan without invoking provider", "--format <format>        output format: text|json", "--json                   output JSON envelope"}, Examples: []string{"gofly ai complete --prompt 'hello' --config .gofly/config.json --json", "GOFLY_LLM_MODEL=local gofly ai complete 'email user@example.com' --dry-run --format json"}}
	case "ai stream":
		return commandHelp{Name: "ai stream", Short: "Execute a governed streaming completion and emit text deltas or JSON event envelopes.", Usage: "gofly ai stream --prompt <text> [--config .gofly/config.json] [--provider noop|openai-compatible] [--model <model>] [--max-input-tokens <n>] [--max-output-tokens <n>] [--max-total-tokens <n>] [--rate-limit <n>] [--timeout <duration>] [--allow-failover|--failover] [--dry-run|--plan] [--format text|json] [--json]", Flags: []string{"--prompt <text>          prompt text, also accepted as positional text", "--config <file>          gofly config file with llm defaults", "--dir <dir>              service root for .gofly/config.json", "--provider <provider>    provider mode: noop|openai-compatible", "--model <model>          model label for audit and output", "--max-input-tokens <n>   input token budget", "--max-output-tokens <n>  output token budget", "--max-total-tokens <n>   total token budget", "--rate-limit <n>         calls per second; zero disables rate limiting", "--rate-burst <n>         rate limit burst; zero uses rate-limit", "--timeout <duration>     provider call timeout", "--allow-failover         manually retry retryable start failures before emitting any event", "--failover               alias for --allow-failover", "--dry-run, --plan        print governance plan without invoking provider", "--format <format>        output format: text|json", "--json                   output newline-delimited JSON envelopes"}, Examples: []string{"gofly ai stream --prompt 'hello' --provider noop --json", "gofly ai stream 'email user@example.com' --dry-run --format json"}}
	case "ai doctor":
		return commandHelp{Name: "ai doctor", Short: "Run AI subsystem self-diagnostics.", Usage: "gofly ai doctor [--json]", Flags: []string{"--json  print diagnostic report as JSON"}, Examples: []string{"gofly ai doctor", "gofly ai doctor --json"}}
	case "ai control-plane":
		return commandHelp{Name: "ai control-plane", Short: "Print or watch the deterministic AI control-plane snapshot.", Usage: "gofly ai control-plane [--format text|json] [--json] [--schema jsonschema] [--source <url>] [--admin-token <token>] [--from-checksum <sha256>|--from-snapshot <file>] [--watch --max-events <n> --timeout <duration>]", Flags: []string{"--format <format>       output format: text|json", "--json                  output JSON envelope", "--schema <schema>       output control-plane JSON Schema: jsonschema", "--source <url>          runtime REST admin /control-plane URL", "--admin-token <token>   bearer token for --source; defaults to GOFLY_CONTROL_PLANE_TOKEN", "--from-checksum <sha>   compare current snapshot checksum with a previous checksum", "--from-snapshot <file>  compare current snapshot with a previous control-plane snapshot JSON file", "--watch                 emit bounded snapshot watch events", "--max-events <n>        maximum watch events to emit (default 1)", "--timeout <duration>    watch timeout boundary (default 2s)"}, Examples: []string{"gofly ai control-plane", "gofly ai control-plane --json", "gofly ai control-plane --schema jsonschema", "gofly ai control-plane --source http://127.0.0.1:8080/admin/control-plane --json", "gofly ai control-plane --from-checksum <sha256> --json", "gofly ai control-plane --from-snapshot previous-control-plane.json --json", "gofly ai control-plane --watch --max-events 1 --json"}}
	case "example", "examples":
		return commandHelp{Name: "example", Short: "List or run built-in examples.", Usage: "gofly example list | gofly example run <name> [--dir <dir>]", Commands: []helpCommand{{Name: "list", Short: "list built-in examples"}, {Name: "run", Short: "copy and run a built-in example"}}, Flags: []string{"--dir <dir>  output directory for example run"}, Examples: []string{"gofly example list", "gofly example run observability", "gofly example run restserver --dir ./demo"}}
	case "example list", "examples list":
		return commandHelp{Name: "example list", Short: "List built-in examples.", Usage: "gofly example list [--json]", Flags: []string{"--json  output JSON"}, Examples: []string{"gofly example list", "gofly example list --json"}}
	case "example run", "examples run":
		return commandHelp{Name: "example run", Short: "Copy a built-in example to a local directory.", Usage: "gofly example run <name> [--dir <dir>]", Flags: []string{"--dir <dir>  output directory (default: example name)"}, Examples: []string{"gofly example run observability", "gofly example run restserver --dir ./demo"}}
	default:
		return commandHelp{
			Name:  command,
			Short: "gofly command help.",
			Usage: "gofly " + command + " [arguments]",
			Examples: []string{
				"gofly help",
				"gofly help api",
			},
		}
	}
}

func renderCommandHelp(topic commandHelp) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", topic.Short)
	fmt.Fprintf(&b, "%s\n  %s\n", helpBlue("Usage:"), helpColoredCommandLine(topic.Usage))
	if len(topic.Commands) > 0 {
		fmt.Fprintf(&b, "\n%s\n", helpBlue("Available Commands:"))
		for _, cmd := range topic.Commands {
			fmt.Fprintf(&b, "  %s %s\n", helpCommandName(cmd.Name, 20), cmd.Short)
		}
	}
	if len(topic.Flags) > 0 {
		fmt.Fprintf(&b, "\n%s\n", helpBlue("Flags:"))
		for _, flag := range topic.Flags {
			fmt.Fprintf(&b, "  %s\n", helpGreen(flag))
		}
	}
	if len(topic.Examples) > 0 {
		fmt.Fprintf(&b, "\n%s\n", helpBlue("Examples:"))
		for _, example := range topic.Examples {
			fmt.Fprintf(&b, "  %s\n", helpColoredCommandLine(example))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func colorizeHelpText(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case "Usage:", "Compatibility aliases:":
			lines[i] = helpBlue(line)
		default:
			if indent := leadingSpaces(line); indent >= 2 {
				commandLine := strings.TrimPrefix(strings.TrimSpace(line), "gofly ")
				lines[i] = strings.Repeat(" ", indent) + helpColoredCommandLine(commandLine)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func helpBlue(text string) string {
	return ansiColor("94", text)
}

func helpGreen(text string) string {
	return ansiColor("92", text)
}

func helpColoredCommandLine(line string) string {
	line = strings.TrimPrefix(strings.TrimSpace(line), "gofly ")
	for _, separator := range []string{" | ", " && ", " ; "} {
		line = strings.ReplaceAll(line, separator+"gofly ", separator)
	}
	return ansiColor(helpCommandColor(line), line)
}

func helpCommandName(name string, padding int) string {
	return ansiColor(helpCommandColor(name), rightPad(name, padding))
}

func helpCommandColor(text string) string {
	command := strings.Fields(strings.TrimSpace(text))
	if len(command) == 0 {
		return "97"
	}
	switch command[0] {
	case "api":
		return "92"
	case "rpc":
		return "95"
	case "model", "template", "upgrade":
		return "93"
	case "new", "kube", "quickstart", "feature":
		return "96"
	case "gen", "handler", "bug":
		return "91"
	case "docker", "config", "completion", "complete":
		return "94"
	case "env":
		return "92"
	case "migrate", "migration", "plugin":
		return "95"
	default:
		return "97"
	}
}

func ansiColor(code string, text string) string {
	if text == "" || os.Getenv("NO_COLOR") != "" || os.Getenv("GOFLY_NO_COLOR") != "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func rightPad(text string, padding int) string {
	if len(text) >= padding {
		return text
	}
	return text + strings.Repeat(" ", padding-len(text))
}

func encodeStringMap(in map[string]string) string {
	if len(in) == 0 {
		return ""
	}
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+in[key])
	}
	return strings.Join(parts, ",")
}
