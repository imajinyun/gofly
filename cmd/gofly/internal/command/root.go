// Package command implements the gofly CLI: code generation, scaffolding,
// governance, service discovery, deployment and developer tooling.
package command

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/cmd/gofly/internal/spinner"
	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/llm"
)

var runUpgradeInstall = func(target string) ([]byte, error) {
	// #nosec G204 -- upgrade execute intentionally runs `go install` with a single module@version argv value, never through a shell.
	return exec.Command("go", "install", target).CombinedOutput()
}

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

type toolCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
}

type bugReport struct {
	Tool          string            `json:"tool"`
	Version       string            `json:"version"`
	Environment   map[string]string `json:"environment"`
	Checks        []toolCheck       `json:"checks"`
	SupportBundle supportBundleInfo `json:"supportBundle"`
	NextActions   []string          `json:"nextActions"`
}

type supportBundleInfo struct {
	Schema      string   `json:"schema"`
	Redaction   []string `json:"redaction"`
	Commands    []string `json:"commands"`
	Description string   `json:"description"`
}

type upgradePlan struct {
	Command          []string `json:"command"`
	Target           string   `json:"target"`
	Module           string   `json:"module"`
	Version          string   `json:"version"`
	Execute          bool     `json:"execute"`
	ProjectDir       string   `json:"projectDir,omitempty"`
	GeneratedProject bool     `json:"generatedProject,omitempty"`
	DiffCommand      []string `json:"diffCommand,omitempty"`
	VerifyCommand    []string `json:"verifyCommand,omitempty"`
	Output           string   `json:"output,omitempty"`
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

func genCommand(args []string) error {
	if printCommandHelp("gen", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly gen handler|rpc|api|middleware|model|gateway`", errUsage)
	}
	switch args[0] {
	case "handler":
		return handlerCommand(append([]string{"gen"}, args[1:]...))
	case "rpc":
		return rpcGenCommand(args[1:])
	case "api", "rest":
		return apiGenCommand(args[1:])
	case "middleware":
		return apiMiddlewareCommand(args[1:])
	case "model":
		return modelGenCommand(args[1:])
	case "gateway":
		return gatewayGenCommand(args[1:])
	default:
		return fmt.Errorf("%w: expected `gofly gen handler|rpc|api|middleware|model|gateway`", errUsage)
	}
}

func dockerCommand(args []string) error {
	if printCommandHelp("docker", args) {
		return nil
	}
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("docker", flag.ContinueOnError)
	name := fs.String("name", "", "service name")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output Dockerfile path")
	o := fs.String("o", "", "output Dockerfile path")
	goFile := fs.String("go", "", "main package or Go file to build")
	exe := fs.String("exe", "", "binary name")
	goVersion := fs.String("go-version", "1.26", "golang builder image version")
	version := fs.String("version", "", "golang builder image version")
	baseImage := fs.String("base", "gcr.io/distroless/static-debian12", "runtime base image")
	port := fs.String("port", "", "HTTP port metadata")
	tz := fs.String("tz", "", "container timezone metadata")
	home := fs.String("home", "", "template home directory")
	remote := fs.String("remote", "", "remote template repository")
	branch := fs.String("branch", "", "remote template branch")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	if *output == "" {
		*output = *o
	}
	if *version != "" {
		*goVersion = *version
	}
	fillNameFromArgs(name, remaining)
	return generator.GenerateDockerfile(generator.DockerOptions{
		Name:        *name,
		Dir:         *dir,
		Output:      *output,
		GoFile:      *goFile,
		Exe:         *exe,
		GoVersion:   *goVersion,
		BaseImage:   *baseImage,
		Port:        *port,
		Timezone:    *tz,
		TemplateDir: *home,
		Remote:      *remote,
		Branch:      *branch,
	})
}

func kubeCommand(args []string) error {
	if printCommandHelp("kube", args) {
		return nil
	}
	kind := "deploy"
	if len(args) > 0 && isKubeKind(args[0]) {
		kind = args[0]
		args = args[1:]
	}
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("kube", flag.ContinueOnError)
	name := fs.String("name", "", "service name")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output yaml path")
	o := fs.String("o", "", "output yaml path")
	namespace := fs.String("namespace", "default", "kubernetes namespace")
	image := fs.String("image", "", "container image")
	secret := fs.String("secret", "", "image pull secret name")
	port := fs.String("port", "8080", "http container port")
	targetPort := fs.String("targetPort", "", "target container port")
	nodePort := fs.String("nodePort", "", "Kubernetes node port")
	rpcPort := fs.String("rpc-port", "8081", "rpc container port")
	replicas := fs.String("replicas", "2", "deployment replicas")
	revisions := fs.String("revisions", "", "revision history limit")
	minReplicas := fs.String("minReplicas", "", "minimum autoscale replicas")
	maxReplicas := fs.String("maxReplicas", "", "maximum autoscale replicas")
	requestCPU := fs.String("requestCpu", "", "requested CPU resource")
	requestMem := fs.String("requestMem", "", "requested memory resource")
	limitCPU := fs.String("limitCpu", "", "CPU resource limit")
	limitMem := fs.String("limitMem", "", "memory resource limit")
	imagePullPolicy := fs.String("imagePullPolicy", "", "image pull policy")
	serviceAccount := fs.String("serviceAccount", "", "Kubernetes service account")
	home := fs.String("home", "", "template home directory")
	remote := fs.String("remote", "", "remote template repository")
	branch := fs.String("branch", "", "remote template branch")
	host := fs.String("host", "", "ingress host")
	path := fs.String("path", "/", "ingress path")
	data := fs.String("data", "", "configmap data as comma-separated key=value pairs")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *output == "" {
		*output = *o
	}
	if *name == "" {
		*name = leadingName
	}
	if *targetPort != "" {
		*port = *targetPort
	}
	fillNameFromArgs(name, remaining)
	return generator.GenerateKube(generator.KubeOptions{
		Name:            *name,
		Dir:             *dir,
		Output:          *output,
		Kind:            kind,
		Namespace:       *namespace,
		Image:           *image,
		Port:            *port,
		RPCPort:         *rpcPort,
		Replicas:        *replicas,
		Host:            *host,
		Path:            *path,
		Config:          parseKeyValueCSV(*data),
		Secret:          *secret,
		NodePort:        *nodePort,
		Revisions:       *revisions,
		MinReplicas:     *minReplicas,
		MaxReplicas:     *maxReplicas,
		RequestCPU:      *requestCPU,
		RequestMem:      *requestMem,
		LimitCPU:        *limitCPU,
		LimitMem:        *limitMem,
		ImagePullPolicy: *imagePullPolicy,
		ServiceAccount:  *serviceAccount,
		TemplateDir:     *home,
		Remote:          *remote,
		Branch:          *branch,
	})
}

func isKubeKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "deploy", "deployment", "service", "svc", "ingress", "ing", "configmap", "cm", "job":
		return true
	default:
		return false
	}
}

func parseKeyValueCSV(value string) map[string]string {
	parts := strings.Split(value, ",")
	out := map[string]string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, ok := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !ok {
			out[key] = ""
			continue
		}
		out[key] = strings.TrimSpace(val)
	}
	return out
}

func templateCommand(args []string) error {
	if printCommandHelp("template", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly template init|list|inspect|clean|update|revert`", errUsage)
	}
	subcommand := args[0]
	fs := flag.NewFlagSet("template "+subcommand, flag.ContinueOnError)
	dir := fs.String("dir", "", "template output directory")
	home := fs.String("home", "", "template output directory")
	remote := fs.String("remote", "", "remote template repository or local directory")
	branch := fs.String("branch", "", "remote template branch")
	category := fs.String("category", "", "template category filter")
	c := fs.String("c", "", "template category filter")
	name := fs.String("name", "", "template name filter")
	n := fs.String("n", "", "template name filter")
	formatName := fs.String("format", "text", "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON")
	remaining, err := parseInterspersedFlags(fs, args[1:])
	if err != nil {
		return err
	}
	if *dir == "" {
		*dir = *home
	}
	if *category == "" {
		*category = *c
	}
	if *name == "" {
		*name = *n
	}
	if *name == "" && len(remaining) > 0 {
		*name = remaining[0]
	}
	useJSON := *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), outputJSON)
	opts := generator.TemplateOptions{Dir: *dir, Remote: *remote, Branch: *branch, StrictRemote: true}
	switch subcommand {
	case "init", "update":
		if *category != "" || *name != "" {
			warnNoopFlag("template "+subcommand, "category/name", "template init/update currently syncs the full template set")
		}
		return generator.GenerateTemplateInit(opts)
	case "revert":
		if *category != "" || *name != "" {
			warnNoopFlag("template revert", "category/name", "template revert currently restores the full default template set")
		}
		return generator.GenerateTemplateInit(generator.TemplateOptions{Dir: *dir})
	case "list", "ls":
		catalog := filterProjectTemplates(generator.ListProjectTemplates(), *category, *name)
		if useJSON {
			return printJSONEnvelope("template.list", catalog)
		}
		for _, tmpl := range catalog {
			cliOutputf("%s\t%s\t%s\t%s\n", tmpl.ID, tmpl.Kind, tmpl.Architecture, tmpl.Description)
		}
		for _, file := range generator.ListTemplates(opts) {
			if !templateFilterMatch(file.Name, *category, *name) {
				continue
			}
			cliOutputf("%s\t%s\n", file.Name, file.Path)
		}
		return nil
	case "inspect", "show", "describe":
		if *name == "" {
			return fmt.Errorf("%w: template id is required for `gofly template inspect`", errUsage)
		}
		tmpl, ok := generator.GetProjectTemplate(*name)
		if !ok {
			return fmt.Errorf("%w: unknown project template %q", errUsage, *name)
		}
		if useJSON {
			return printJSONEnvelope("template.inspect", tmpl)
		}
		cliOutputf("id: %s\nname: %s\nkind: %s\narchitecture: %s\nrisk: %s\ncommand: %s\n", tmpl.ID, tmpl.Name, tmpl.Kind, tmpl.Architecture, tmpl.RiskLevel, tmpl.Command)
		cliOutputf("features: %s\n", strings.Join(tmpl.Features, ","))
		return nil
	case "clean":
		if *category != "" || *name != "" {
			for _, file := range generator.ListTemplates(opts) {
				if !templateFilterMatch(file.Name, *category, *name) {
					continue
				}
				if err := os.Remove(file.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("clean template %s: %w", file.Path, err)
				}
			}
			return nil
		}
		return generator.CleanTemplates(opts)
	default:
		return fmt.Errorf("%w: expected `gofly template init|list|inspect|clean|update|revert`", errUsage)
	}
}

func filterProjectTemplates(templates []generator.ProjectTemplate, category, name string) []generator.ProjectTemplate {
	out := make([]generator.ProjectTemplate, 0, len(templates))
	for _, tmpl := range templates {
		if templateCatalogFilterMatch(tmpl, category, name) {
			out = append(out, tmpl)
		}
	}
	return out
}

func templateCatalogFilterMatch(tmpl generator.ProjectTemplate, category, name string) bool {
	category = strings.ToLower(strings.TrimSpace(category))
	name = strings.ToLower(strings.TrimSpace(name))
	if name != "" && strings.ToLower(tmpl.ID) != name && !strings.Contains(strings.ToLower(tmpl.Name), name) {
		return false
	}
	if category == "" {
		return true
	}
	if strings.EqualFold(tmpl.Kind, category) || strings.EqualFold(tmpl.Language, category) || strings.EqualFold(tmpl.Architecture, category) {
		return true
	}
	for _, feature := range tmpl.Features {
		if strings.EqualFold(feature, category) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(tmpl.ID), category)
}

func templateFilterMatch(templateName, category, name string) bool {
	templateName = strings.ToLower(strings.TrimSpace(templateName))
	category = strings.ToLower(strings.TrimSpace(category))
	name = strings.ToLower(strings.TrimSpace(name))
	if name != "" && templateName != name && strings.TrimSuffix(templateName, filepath.Ext(templateName)) != name {
		return false
	}
	if category == "" {
		return true
	}
	switch category {
	case "api", "rpc", "model", "docker":
		return strings.HasPrefix(templateName, category)
	case "kube", "kubernetes":
		return strings.HasPrefix(templateName, "kube")
	default:
		return strings.Contains(templateName, category)
	}
}

func quickstartCommand(args []string) error {
	if printCommandHelp("quickstart", args) {
		return nil
	}
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("quickstart", flag.ContinueOnError)
	name := fs.String("name", "", "api service name")
	module := fs.String("module", "", "go module path")
	dir := fs.String("dir", "", "output directory")
	style := fs.String("style", generator.ServiceStyleBasic, "api scaffold style: minimal, basic, or production")
	apiSpec := fs.Bool("api-spec", true, "generate an .api file")
	serviceType := fs.String("service-type", "", "quickstart service type: mono or micro")
	t := fs.String("t", "", "quickstart service type")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *serviceType == "" {
		*serviceType = *t
	}
	if *serviceType == "micro" && *style == generator.ServiceStyleBasic {
		*style = generator.ServiceStyleProduction
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if *dir == "" && *name != "" {
		*dir = *name
	}
	if err := generator.GenerateAPINew(generator.APINewOptions{
		Name:        *name,
		Module:      *module,
		Dir:         *dir,
		Style:       *style,
		SkipAPISpec: !*apiSpec,
	}); err != nil {
		return err
	}
	if !*apiSpec {
		return nil
	}
	apiFile := generator.APIOptions{
		APIFile: filepath.Join(*dir, *name+".api"),
		Dir:     *dir,
		Package: "api",
	}
	return generator.GenerateRESTFromAPI(apiFile)
}

func migrateCommand(args []string) error {
	if printCommandHelp("migrate", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly migrate create <name>`", errUsage)
	}
	subcommand := args[0]
	if subcommand != "create" && subcommand != "new" {
		return fmt.Errorf("%w: expected `gofly migrate create <name>`", errUsage)
	}
	leadingName, rest := splitLeadingName(args[1:])
	fs := flag.NewFlagSet("migrate create", flag.ContinueOnError)
	name := fs.String("name", "", "migration name")
	dir := fs.String("dir", filepath.Join(".", "migrations"), "migration output directory")
	remaining, err := parseInterspersedFlags(fs, rest)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	return generator.GenerateMigration(generator.MigrationOptions{Name: *name, Dir: *dir})
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

func bugCommand(args []string) error {
	if printCommandHelp("bug", args) {
		return nil
	}
	fs := flag.NewFlagSet("bug", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print bug report as JSON")
	if _, err := parseInterspersedFlags(fs, args); err != nil {
		return err
	}
	report := bugReport{
		Tool:        "gofly",
		Version:     Version,
		Environment: envInfo(),
		Checks: []toolCheck{
			envToolCheck("go"),
			envToolCheck("protoc"),
			envToolCheck("git"),
		},
		SupportBundle: supportBundleInfo{
			Schema:      "gofly.support_bundle.v1",
			Redaction:   []string{"Authorization", "Cookie", "Set-Cookie", "GOFLY_LLM_*", "*TOKEN*", "*SECRET*", "*PASSWORD*"},
			Commands:    []string{"gofly doctor --json", "gofly env check --json", "gofly release check --json --strict", "gofly bug --json"},
			Description: "Attach this JSON with command output and generated-project failure logs after removing secrets.",
		},
		NextActions: []string{
			"attach this support bundle when opening an issue or asking for help",
			"run `gofly doctor --json` and fix failed checks before rerunning generators",
			"run `gofly release check --json --strict` before publishing release artifacts",
		},
	}
	if *jsonOutput {
		return printJSON(report)
	}
	cliOutputln("gofly bug report")
	cliOutputf("version: %s\n", Version)
	for _, key := range []string{"GOOS", "GOARCH", "GOVERSION", "GOFLY_VERSION"} {
		cliOutputf("%s=%s\n", key, report.Environment[key])
	}
	cliOutputln("tools:")
	for _, check := range report.Checks {
		cliOutputf("  %s\t%s\t%s\n", check.Name, check.Status, check.Path)
	}
	cliOutputln("Please include this report when filing an issue.")
	return nil
}

func upgradeCommand(args []string) error {
	if printCommandHelp("upgrade", args) {
		return nil
	}
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	version := fs.String("version", "latest", "version to install")
	module := fs.String("module", "github.com/imajinyun/gofly/cmd/gofly", "module path to install")
	projectDir := fs.String("project-dir", "", "generated project directory to include upgrade/diff verification commands")
	dir := fs.String("dir", "", "alias for --project-dir")
	execute := fs.Bool("execute", false, "execute go install instead of printing the upgrade command")
	jsonOutput := fs.Bool("json", false, "print upgrade plan/result as JSON")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("%w: upgrade does not accept positional arguments; use --version %s", errUsage, remaining[0])
	}
	*module = strings.TrimSpace(*module)
	*version = strings.TrimSpace(*version)
	if *module == "" {
		return fmt.Errorf("%w: upgrade module is required", errUsage)
	}
	if *version == "" {
		return fmt.Errorf("%w: upgrade version is required", errUsage)
	}
	if *projectDir == "" {
		*projectDir = strings.TrimSpace(*dir)
	}
	target := *module + "@" + *version
	plan := upgradePlan{
		Command:    []string{"go", "install", target},
		Target:     target,
		Module:     *module,
		Version:    *version,
		Execute:    *execute,
		ProjectDir: strings.TrimSpace(*projectDir),
	}
	if plan.ProjectDir != "" {
		plan.GeneratedProject = true
		plan.DiffCommand = []string{"gofly", "api", "diff", "--base", filepath.Join(plan.ProjectDir, "api.previous.api"), "--target", filepath.Join(plan.ProjectDir, "api.current.api"), "--format", "json"}
		plan.VerifyCommand = []string{"go", "test", "./..."}
	}
	if !*execute {
		if *jsonOutput {
			return printJSON(plan)
		}
		cliOutputf("go install %s\n", target)
		if plan.GeneratedProject {
			cliOutputf("# generated project diff: %s\n", strings.Join(plan.DiffCommand, " "))
			cliOutputf("# generated project verify: cd %s && %s\n", plan.ProjectDir, strings.Join(plan.VerifyCommand, " "))
		}
		return nil
	}
	if check := envToolCheck("go"); check.Status != "ok" {
		return fmt.Errorf("upgrade gofly: go tool is missing")
	}
	out, err := runUpgradeInstall(target)
	plan.Output = string(out)
	if len(out) > 0 && !*jsonOutput {
		cliOutput(string(out))
	}
	if err != nil {
		return fmt.Errorf("upgrade gofly: %w", err)
	}
	if *jsonOutput {
		return printJSON(plan)
	}
	return nil
}

const (
	aiToolManifestSchemaVersion = "gofly.ai.tool-manifest.v1"
	aiControlPlaneSchemaID      = "https://gofly.dev/schemas/ai-control-plane.schema.json"
)

type aiToolManifest struct {
	SchemaVersion  string                   `json:"schemaVersion"`
	Tool           string                   `json:"tool"`
	Version        string                   `json:"version"`
	Description    string                   `json:"description"`
	Invocation     string                   `json:"invocation"`
	Docs           []aiManifestLink         `json:"docs"`
	Examples       []aiManifestLink         `json:"examples"`
	VerifyCommands []string                 `json:"verifyCommands"`
	Output         aiOutputSchema           `json:"output"`
	ControlPlane   aiControlPlaneManifest   `json:"controlPlane"`
	LLMGovernance  aiLLMGovernance          `json:"llmGovernance"`
	FeatureLibrary aiFeatureLibraryManifest `json:"featureLibrary"`
	Commands       []aiToolCommand          `json:"commands"`
}

type aiManifestLink struct {
	Title string `json:"title"`
	Path  string `json:"path"`
}

type aiOutputSchema struct {
	Mode        string   `json:"mode"`
	Envelope    []string `json:"envelope"`
	ErrorFields []string `json:"errorFields"`
}

type aiControlPlaneManifest struct {
	Package          string                                `json:"package"`
	Purpose          string                                `json:"purpose"`
	SnapshotVersion  string                                `json:"snapshotVersion"`
	SnapshotChecksum string                                `json:"snapshotChecksum"`
	SchemaID         string                                `json:"schemaId"`
	SchemaCommand    string                                `json:"schemaCommand"`
	SchemaChecksum   string                                `json:"schemaChecksum"`
	ProviderContract []string                              `json:"providerContract"`
	SnapshotFields   []string                              `json:"snapshotFields"`
	EventFields      []string                              `json:"eventFields"`
	Capabilities     []string                              `json:"capabilities"`
	ConsumerActions  []controlplane.SnapshotConsumerAction `json:"consumerActions"`
	Determinism      string                                `json:"determinism"`
	SecretBoundary   string                                `json:"secretBoundary"`
	AgentGuidance    []string                              `json:"agentGuidance"`
	DefaultMetadata  map[string]string                     `json:"defaultMetadata,omitempty"`
}

type aiLLMGovernance struct {
	Package                string                   `json:"package"`
	Capabilities           []string                 `json:"capabilities"`
	Resilience             []string                 `json:"resilience"`
	ProviderPluginContract aiProviderPluginContract `json:"providerPluginContract"`
	TokenBudgetPolicy      aiTokenBudgetPolicy      `json:"tokenBudgetPolicy"`
	RateLimitPolicy        aiRateLimitPolicy        `json:"rateLimitPolicy"`
	OutputContractPolicy   aiOutputContractPolicy   `json:"outputContractPolicy"`
	ErrorContractPolicy    aiErrorContractPolicy    `json:"errorContractPolicy"`
	DataSafetyPolicy       aiDataSafetyPolicy       `json:"dataSafetyPolicy"`
	ToolCallPolicy         aiToolCallPolicy         `json:"toolCallPolicy"`
	FailoverPolicy         aiFailoverPolicy         `json:"failoverPolicy"`
	ResponseCachePolicy    aiResponseCachePolicy    `json:"responseCachePolicy"`
	ObservabilityPolicy    aiObservabilityPolicy    `json:"observabilityPolicy"`
	CostPolicy             aiCostPolicy             `json:"costPolicy"`
	GovernancePipeline     []aiPipelineStage        `json:"governancePipeline"`
	AuditFields            []string                 `json:"auditFields"`
	TelemetryFields        []string                 `json:"telemetryFields"`
	DefaultMode            string                   `json:"defaultMode"`
	Providers              []llm.ProviderSpec       `json:"providers"`
}

type aiFeatureLibraryManifest struct {
	Mode                 string                                   `json:"mode"`
	Deterministic        bool                                     `json:"deterministic"`
	AppliesUnderDirOnly  bool                                     `json:"appliesUnderDirOnly"`
	DependencyPolicy     string                                   `json:"dependencyPolicy"`
	Features             []string                                 `json:"features"`
	Templates            []string                                 `json:"templates"`
	VerifyAllowlist      []string                                 `json:"verifyAllowlist"`
	TemplateVerification aiTemplateVerificationContract           `json:"templateVerification"`
	ResultFields         []string                                 `json:"resultFields"`
	Plugins              []generator.ProjectFeaturePluginContract `json:"plugins"`
}

type aiTemplateVerificationContract struct {
	CatalogField       string   `json:"catalogField"`
	MatrixTarget       string   `json:"matrixTarget"`
	GovernanceRound    string   `json:"governanceRound"`
	CIRequired         bool     `json:"ciRequired"`
	ZeroSkipRequired   bool     `json:"zeroSkipRequired"`
	ValidatedTemplates []string `json:"validatedTemplates"`
}

type aiTokenBudgetPolicy struct {
	DefaultMaxInputTokens  int      `json:"defaultMaxInputTokens"`
	DefaultMaxOutputTokens int      `json:"defaultMaxOutputTokens"`
	DefaultMaxTotalTokens  int      `json:"defaultMaxTotalTokens"`
	Configurable           bool     `json:"configurable"`
	CLIFlags               []string `json:"cliFlags"`
	EnvVars                []string `json:"envVars"`
	Enforcement            string   `json:"enforcement"`
	DeductionPoint         string   `json:"deductionPoint"`
	FailoverBudgetSharing  string   `json:"failoverBudgetSharing"`
	StreamAccounting       string   `json:"streamAccounting"`
	RejectionCode          string   `json:"rejectionCode"`
}

type aiRateLimitPolicy struct {
	DefaultRate  int    `json:"defaultRate"`
	DefaultBurst int    `json:"defaultBurst"`
	EnvVarRate   string `json:"envVarRate"`
	EnvVarBurst  string `json:"envVarBurst"`
	Strategy     string `json:"strategy"`
	Consequence  string `json:"consequence"`
	Configurable bool   `json:"configurable"`
	Scope        string `json:"scope"`
}

type aiProviderPluginContract struct {
	SchemaVersion  string   `json:"schemaVersion"`
	RequiredFields []string `json:"requiredFields"`
	SafeFields     []string `json:"safeFields"`
	SecretBoundary string   `json:"secretBoundary"`
}

type aiOutputContractPolicy struct {
	EnvelopeFields          []string `json:"envelopeFields"`
	ErrorFields             []string `json:"errorFields"`
	NextActions             bool     `json:"nextActions"`
	JSONMode                string   `json:"jsonMode"`
	SchemaValidation        string   `json:"schemaValidation"`
	RetryableErrorSemantics string   `json:"retryableErrorSemantics"`
	StreamSemantics         string   `json:"streamSemantics"`
	PartialFailureSemantics string   `json:"partialFailureSemantics"`
}

type aiErrorContractPolicy struct {
	CodeFormat              string   `json:"codeFormat"`
	StableCodes             []string `json:"stableCodes"`
	RetryableCodes          []string `json:"retryableCodes"`
	NonRetryableCodes       []string `json:"nonRetryableCodes"`
	ProviderStatusClasses   []string `json:"providerStatusClasses"`
	NextActionTypes         []string `json:"nextActionTypes"`
	EnvelopePlacement       string   `json:"envelopePlacement"`
	DetailsPolicy           string   `json:"detailsPolicy"`
	RetryableSemantics      string   `json:"retryableSemantics"`
	ProviderFailureGuidance string   `json:"providerFailureGuidance"`
}

type aiDataSafetyPolicy struct {
	SecretResolution    string   `json:"secretResolution"`
	Redaction           string   `json:"redaction"`
	PromptLogging       string   `json:"promptLogging"`
	ResponseLogging     string   `json:"responseLogging"`
	MetadataLogging     string   `json:"metadataLogging"`
	SecretValueLogging  string   `json:"secretValueLogging"`
	SensitiveEnvVarMode string   `json:"sensitiveEnvVarMode"`
	AuditBoundary       string   `json:"auditBoundary"`
	SafeToExpose        []string `json:"safeToExpose"`
}

type aiToolCallPolicy struct {
	DefaultMode                     string   `json:"defaultMode"`
	RequiresModelCapability         string   `json:"requiresModelCapability"`
	AllowedByDefault                []string `json:"allowedByDefault"`
	SideEffectToolsRequireApproval  bool     `json:"sideEffectToolsRequireApproval"`
	ArgumentSchemaValidation        bool     `json:"argumentSchemaValidation"`
	DryRunRequiredForMutation       bool     `json:"dryRunRequiredForMutation"`
	AuditToolArguments              string   `json:"auditToolArguments"`
	RejectedToolCallCode            string   `json:"rejectedToolCallCode"`
	UnsupportedCapabilityResolution string   `json:"unsupportedCapabilityResolution"`
}

type aiFailoverPolicy struct {
	EnvVar                string             `json:"envVar"`
	Mode                  string             `json:"mode"`
	AutomaticSwitching    bool               `json:"automaticSwitching"`
	ManualOptInFlags      []string           `json:"manualOptInFlags"`
	ExecutionGuardrails   []string           `json:"executionGuardrails"`
	ConfiguredProviders   []string           `json:"configuredProviders,omitempty"`
	InvalidProviders      []string           `json:"invalidProviders,omitempty"`
	ConfiguredSpecs       []llm.ProviderSpec `json:"configuredSpecs,omitempty"`
	EligibleCompleteSpecs []llm.ProviderSpec `json:"eligibleCompleteSpecs,omitempty"`
	EligibleStreamSpecs   []llm.ProviderSpec `json:"eligibleStreamSpecs,omitempty"`
	EligibleJSONModeSpecs []llm.ProviderSpec `json:"eligibleJSONModeSpecs,omitempty"`
	EligibleToolCallSpecs []llm.ProviderSpec `json:"eligibleToolCallSpecs,omitempty"`
}

// aiResponseCachePolicy documents the in-memory response caching behavior
// provided by CachingProvider. Only Complete responses are cached; Stream
// and Embed calls pass through without caching.
type aiResponseCachePolicy struct {
	DefaultTTL         string   `json:"defaultTTL"`
	DefaultMaxSize     int      `json:"defaultMaxSize"`
	CacheKeyComponents []string `json:"cacheKeyComponents"`
	Hash               string   `json:"hash"`
	Coalescing         string   `json:"coalescing"`
	Observable         bool     `json:"observable"`
	CacheScope         string   `json:"cacheScope"`
	CacheUnsupported   []string `json:"cacheUnsupported"`
}

type aiObservabilityPolicy struct {
	Signals                []string `json:"signals"`
	LowCardinalityFields   []string `json:"lowCardinalityFields"`
	ForbiddenFields        []string `json:"forbiddenFields"`
	CorrelationFields      []string `json:"correlationFields"`
	MetricFieldGuidance    string   `json:"metricFieldGuidance"`
	TraceFieldGuidance     string   `json:"traceFieldGuidance"`
	AuditCorrelation       string   `json:"auditCorrelation"`
	RedactionBoundary      string   `json:"redactionBoundary"`
	CardinalityGuardrails  string   `json:"cardinalityGuardrails"`
	ProviderStatusGuidance string   `json:"providerStatusGuidance"`
}

type aiCostPolicy struct {
	AccountingFields       []string `json:"accountingFields"`
	BudgetFields           []string `json:"budgetFields"`
	CurrencyMode           string   `json:"currencyMode"`
	PricingSource          string   `json:"pricingSource"`
	CostDisclosure         string   `json:"costDisclosure"`
	FailoverDisclosure     string   `json:"failoverDisclosure"`
	CacheAccounting        string   `json:"cacheAccounting"`
	AgentGuidance          []string `json:"agentGuidance"`
	UnpricedProviderPolicy string   `json:"unpricedProviderPolicy"`
}

// aiPipelineStage documents each stage in the governed LLM call pipeline.
// Stages execute in order; optional stages may be elided at runtime.
type aiPipelineStage struct {
	Stage       string `json:"stage"`
	Description string `json:"description"`
	Optional    bool   `json:"optional"`
}

type aiToolCommand struct {
	Name              string            `json:"name"`
	Aliases           []string          `json:"aliases,omitempty"`
	Description       string            `json:"description"`
	Usage             string            `json:"usage"`
	InputSchema       aiInputSchema     `json:"inputSchema"`
	OutputContract    *aiOutputContract `json:"outputContract,omitempty"`
	OutputFormats     []string          `json:"outputFormats"`
	SideEffects       []string          `json:"sideEffects"`
	RiskLevel         string            `json:"riskLevel"`
	SupportsDryRun    bool              `json:"supportsDryRun"`
	MutatesFilesystem bool              `json:"mutatesFilesystem"`
	Examples          []string          `json:"examples,omitempty"`
}

type aiOutputContract struct {
	Mode        string            `json:"mode"`
	Envelope    []string          `json:"envelope"`
	EventFields []string          `json:"eventFields,omitempty"`
	Semantics   map[string]string `json:"semantics,omitempty"`
}

type aiInputSchema struct {
	Type                 string                     `json:"type"`
	Properties           map[string]aiInputProperty `json:"properties,omitempty"`
	Required             []string                   `json:"required,omitempty"`
	AdditionalProperties bool                       `json:"additionalProperties"`
}

type aiInputProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

type aiCompleteResult struct {
	Provider   string               `json:"provider"`
	Model      string               `json:"model,omitempty"`
	Text       string               `json:"text,omitempty"`
	Usage      llm.Usage            `json:"usage"`
	Budget     llm.BudgetSnapshot   `json:"budget"`
	Governance aiCompleteGovernance `json:"governance"`
	Warnings   []string             `json:"warnings,omitempty"`
	Metadata   map[string]string    `json:"metadata,omitempty"`
}

type aiStreamEventResult struct {
	Provider   string               `json:"provider"`
	Model      string               `json:"model,omitempty"`
	Index      int                  `json:"index"`
	Delta      string               `json:"delta,omitempty"`
	Done       bool                 `json:"done,omitempty"`
	Usage      llm.Usage            `json:"usage,omitempty"`
	Budget     llm.BudgetSnapshot   `json:"budget,omitempty"`
	Governance aiCompleteGovernance `json:"governance"`
}

type aiCompleteGovernance struct {
	ProviderMode         string   `json:"providerMode"`
	ProviderCapabilities []string `json:"providerCapabilities,omitempty"`
	TelemetryFields      []string `json:"telemetryFields,omitempty"`
	FailoverProviders    []string `json:"failoverProviders,omitempty"`
	FailoverMode         string   `json:"failoverMode,omitempty"`
	FailoverAllowed      bool     `json:"failoverAllowed,omitempty"`
	FailoverUsed         bool     `json:"failoverUsed,omitempty"`
	FailoverFrom         string   `json:"failoverFrom,omitempty"`
	IdempotencyKeySet    bool     `json:"idempotencyKeySet,omitempty"`
	NetworkAccess        bool     `json:"networkAccess"`
	RequiresSecrets      bool     `json:"requiresSecrets"`
	SecretSource         string   `json:"secretSource"`
	Redacted             bool     `json:"redacted"`
	BudgetEnforced       bool     `json:"budgetEnforced"`
	RateLimited          bool     `json:"rateLimited"`
	AuditLogged          bool     `json:"auditLogged"`
}

type aiCompleteConfig struct {
	Provider           string
	Model              string
	FailoverProviders  []string
	AllowFailover      bool
	MaxInputTokens     int
	MaxOutputTokens    int
	MaxTotalTokens     int
	RateLimitPerSecond int
	RateLimitBurst     int
	Timeout            time.Duration
	ConfigPath         string
}

type aiControlPlaneSnapshotResult struct {
	Source         string                              `json:"source"`
	Snapshot       controlplane.Snapshot               `json:"snapshot"`
	Diff           controlplane.SnapshotDiff           `json:"diff,omitempty"`
	ConsumerAction controlplane.SnapshotConsumerAction `json:"consumerAction"`
	AgentGuidance  []string                            `json:"agentGuidance"`
	SecretBoundary string                              `json:"secretBoundary"`
}

type aiControlPlaneWatchEventResult struct {
	Index          int                                 `json:"index"`
	Source         string                              `json:"source,omitempty"`
	Snapshot       controlplane.Snapshot               `json:"snapshot,omitempty"`
	Diff           controlplane.SnapshotDiff           `json:"diff,omitempty"`
	ConsumerAction controlplane.SnapshotConsumerAction `json:"consumerAction"`
	Error          string                              `json:"error,omitempty"`
	SecretBoundary string                              `json:"secretBoundary,omitempty"`
}

type aiControlPlaneBaseline struct {
	Checksum    string
	Snapshot    controlplane.Snapshot
	HasSnapshot bool
}

type httpControlPlaneProvider struct {
	URL           string
	Token         string
	Client        *http.Client
	WatchInterval time.Duration
}

func aiCommand(args []string) error {
	if printCommandHelp("ai", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly ai manifest`", errUsage)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "manifest":
		return aiManifestCommand(rest)
	case "plan":
		return aiPlanCommand(rest)
	case "new":
		return aiNewCommand(rest)
	case "complete":
		return aiCompleteCommand(rest)
	case "stream":
		return aiStreamCommand(rest)
	case "doctor":
		return aiDoctorCommand(rest)
	case "control-plane":
		return aiControlPlaneCommand(rest)
	default:
		return fmt.Errorf("%w: expected `gofly ai manifest|control-plane|plan|new|complete|stream|doctor`", errUsage)
	}
}

type aiProjectPlan struct {
	Prompt            string                    `json:"prompt"`
	ProjectType       string                    `json:"projectType"`
	Template          generator.ProjectTemplate `json:"template"`
	Features          []string                  `json:"features"`
	Command           string                    `json:"command"`
	RiskLevel         string                    `json:"riskLevel"`
	MutatesFilesystem bool                      `json:"mutatesFilesystem"`
	DryRun            bool                      `json:"dryRun"`
	Verify            []string                  `json:"verify"`
	Warnings          []string                  `json:"warnings,omitempty"`
	NextActions       []string                  `json:"nextActions"`
}

type aiProjectApplyResult struct {
	Plan              aiProjectPlan                    `json:"plan"`
	Applied           bool                             `json:"applied"`
	OutputDir         string                           `json:"outputDir"`
	ExecutedCommand   string                           `json:"executedCommand"`
	GeneratedFeatures []generator.ProjectFeatureResult `json:"generatedFeatures,omitempty"`
	Dependencies      []string                         `json:"dependencies,omitempty"`
	ConfigHints       []generator.ConfigHint           `json:"configHints,omitempty"`
	FeatureVerify     []string                         `json:"featureVerify,omitempty"`
	Verify            []string                         `json:"verify"`
	VerifyRan         bool                             `json:"verifyRan"`
	VerifyPassed      bool                             `json:"verifyPassed"`
	Verification      []aiProjectVerificationResult    `json:"verification,omitempty"`
	Warnings          []string                         `json:"warnings,omitempty"`
	NextActions       []string                         `json:"nextActions"`
	MutatesFilesystem bool                             `json:"mutatesFilesystem"`
}

type aiProjectVerificationResult struct {
	Command string `json:"command"`
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

type aiProjectApplyOptions struct {
	Verify        bool
	VerifyTimeout time.Duration
}

func aiPlanCommand(args []string) error {
	fs := flag.NewFlagSet("ai plan", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "natural language project requirement")
	kind := fs.String("kind", "", "optional project kind hint, such as service, rpc, worker, cli, ai-agent, rag or gateway")
	name := fs.String("name", "", "project or service name used in the generated command")
	module := fs.String("module", "", "Go module path used in the generated command")
	dir := fs.String("dir", "", "output directory used in the generated command")
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	dryRun := fs.Bool("dry-run", true, "plan only without writing files")
	plan := fs.Bool("plan", true, "alias for --dry-run")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *prompt == "" && len(remaining) > 0 {
		*prompt = strings.Join(remaining, " ")
	} else if len(remaining) > 0 {
		return fmt.Errorf("%w: ai plan accepts either --prompt or positional prompt text, not both", errUsage)
	}
	if strings.TrimSpace(*prompt) == "" {
		return fmt.Errorf("%w: --prompt or positional prompt text is required for `gofly ai plan`", errUsage)
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputText
	}
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	projectPlan := buildAIProjectPlan(*prompt, *kind, *name, *module, *dir, *dryRun || *plan)
	if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
		return printJSONEnvelope("ai.plan", projectPlan)
	}
	cliOutputfIf("template=%s kind=%s risk=%s\n", projectPlan.Template.ID, projectPlan.ProjectType, projectPlan.RiskLevel)
	cliOutputfIf("features=%s\n", strings.Join(projectPlan.Features, ","))
	cliOutputfIf("command=%s\n", projectPlan.Command)
	for _, warning := range projectPlan.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
	return nil
}

func aiNewCommand(args []string) error {
	fs := flag.NewFlagSet("ai new", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "natural language project requirement")
	kind := fs.String("kind", "", "optional project kind hint, such as service, rpc, worker, cli, ai-agent, rag or gateway")
	templateID := fs.String("template", "", "explicit project template id; run `gofly template list --json` to inspect choices")
	name := fs.String("name", "", "project or service name")
	module := fs.String("module", "", "Go module path")
	dir := fs.String("dir", "", "output directory")
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	dryRun := fs.Bool("dry-run", true, "print the scaffold plan without writing files")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	apply := fs.Bool("apply", false, "apply the planned scaffold and write files")
	verify := fs.Bool("verify", false, "run supported post-generation verification commands after --apply")
	verifyTimeoutText := fs.String("verify-timeout", "2m", "timeout for each verification command")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *prompt == "" && len(remaining) > 0 {
		*prompt = strings.Join(remaining, " ")
	} else if len(remaining) > 0 {
		return fmt.Errorf("%w: ai new accepts either --prompt or positional prompt text, not both", errUsage)
	}
	if strings.TrimSpace(*prompt) == "" && strings.TrimSpace(*templateID) == "" {
		return fmt.Errorf("%w: --prompt, positional prompt text, or --template is required for `gofly ai new`", errUsage)
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputText
	}
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	if *apply && (*dryRun || *plan) && !flagWasProvided(fs, "dry-run") && !flagWasProvided(fs, "plan") {
		*dryRun = false
	}
	if *apply && (*dryRun || *plan) {
		return fmt.Errorf("%w: --apply cannot be combined with --dry-run or --plan", errUsage)
	}
	verifyTimeout, err := time.ParseDuration(strings.TrimSpace(*verifyTimeoutText))
	if err != nil || verifyTimeout <= 0 {
		return fmt.Errorf("%w: --verify-timeout must be a positive duration", errUsage)
	}
	projectPlan, err := buildAIProjectNewPlan(*prompt, *kind, *templateID, *name, *module, *dir, !*apply || *dryRun || *plan)
	if err != nil {
		return err
	}
	if !*apply {
		if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
			return printJSONEnvelope("ai.new", projectPlan)
		}
		printAIProjectPlanText(projectPlan)
		return nil
	}
	result, err := applyAIProjectPlan(projectPlan, aiProjectApplyOptions{Verify: *verify, VerifyTimeout: verifyTimeout})
	if err != nil {
		return err
	}
	if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
		return printJSONEnvelope("ai.new", result)
	}
	cliOutputfIf("applied template=%s kind=%s output=%s\n", result.Plan.Template.ID, result.Plan.ProjectType, result.OutputDir)
	cliOutputfIf("command=%s\n", result.ExecutedCommand)
	if len(result.GeneratedFeatures) > 0 {
		for _, feature := range result.GeneratedFeatures {
			cliOutputfIf("feature=%s files=%s\n", feature.Plugin, strings.Join(feature.Files, ","))
			if len(feature.Dependencies) > 0 {
				cliOutputfIf("  dependencies=%s\n", strings.Join(feature.Dependencies, ","))
			}
			for _, hint := range feature.ConfigHints {
				cliOutputfIf("  configHint=%s description=%q example=%q\n", hint.Key, hint.Description, hint.Example)
			}
			if len(feature.VerifyCommands) > 0 {
				cliOutputfIf("  verify=%s\n", strings.Join(feature.VerifyCommands, ","))
			}
		}
	}
	if result.VerifyRan {
		cliOutputfIf("verify=%t\n", result.VerifyPassed)
		for _, check := range result.Verification {
			cliOutputfIf("  - %s: %s\n", check.Command, check.Status)
		}
	}
	for _, warning := range result.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
	for _, next := range result.NextActions {
		cliOutputfIf("next: %s\n", next)
	}
	return nil
}

func buildAIProjectPlan(prompt, kind, name, module, dir string, dryRun bool) aiProjectPlan {
	tmpl := generator.RecommendProjectTemplate(prompt, kind)
	return buildAIProjectPlanFromTemplate(prompt, tmpl, name, module, dir, dryRun)
}

func buildAIProjectNewPlan(prompt, kind, templateID, name, module, dir string, dryRun bool) (aiProjectPlan, error) {
	var tmpl generator.ProjectTemplate
	if strings.TrimSpace(templateID) != "" {
		var ok bool
		tmpl, ok = generator.GetProjectTemplate(templateID)
		if !ok {
			return aiProjectPlan{}, fmt.Errorf("%w: unknown project template %q", errUsage, templateID)
		}
	} else {
		tmpl = generator.RecommendProjectTemplate(prompt, kind)
	}
	if err := validateAIProjectTemplateCommand(tmpl); err != nil {
		return aiProjectPlan{}, err
	}
	projectPlan := buildAIProjectPlanFromTemplate(prompt, tmpl, name, module, dir, dryRun)
	if err := validateAIProjectApplyInputs(projectPlan); err != nil {
		return aiProjectPlan{}, err
	}
	return projectPlan, nil
}

func buildAIProjectPlanFromTemplate(prompt string, tmpl generator.ProjectTemplate, name, module, dir string, dryRun bool) aiProjectPlan {
	command := materializeTemplateCommand(tmpl.Command, name, module, dir)
	warnings := []string{
		"ai plan uses deterministic local template matching and does not call an external LLM provider",
		"rerun the proposed command with --dry-run first before applying filesystem mutations",
	}
	return aiProjectPlan{
		Prompt:            strings.TrimSpace(prompt),
		ProjectType:       tmpl.Kind,
		Template:          tmpl,
		Features:          append([]string(nil), tmpl.Features...),
		Command:           command,
		RiskLevel:         tmpl.RiskLevel,
		MutatesFilesystem: !dryRun,
		DryRun:            dryRun,
		Verify:            append([]string(nil), tmpl.Verify...),
		Warnings:          warnings,
		NextActions:       []string{"inspect the selected template with `gofly template inspect " + tmpl.ID + " --json`", "run the proposed scaffold command with --dry-run", "run generated project verification commands after applying the scaffold"},
	}
}

func validateAIProjectTemplateCommand(tmpl generator.ProjectTemplate) error {
	fields := strings.Fields(tmpl.Command)
	if len(fields) < 3 || fields[0] != "gofly" {
		return fmt.Errorf("%w: template %q has unsupported command %q", errUsage, tmpl.ID, tmpl.Command)
	}
	for _, field := range fields {
		if containsShellMetachar(field) {
			return fmt.Errorf("%w: template %q command %q contains unsupported shell metacharacter", errUsage, tmpl.ID, tmpl.Command)
		}
	}
	switch strings.Join(fields[:3], " ") {
	case "gofly new service", "gofly new api", "gofly new rpc", "gofly gen gateway":
		return nil
	default:
		return fmt.Errorf("%w: template %q command %q is not supported by `gofly ai new`", errUsage, tmpl.ID, tmpl.Command)
	}
}

func containsShellMetachar(value string) bool {
	return strings.ContainsAny(value, ";&|$`")
}

func validateAIProjectApplyInputs(plan aiProjectPlan) error {
	if strings.TrimSpace(plan.Template.ID) == "" {
		return fmt.Errorf("%w: project template is required", errUsage)
	}
	name, module, dir := aiProjectPlanValues(plan)
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", errUsage)
	}
	if strings.TrimSpace(module) == "" {
		return fmt.Errorf("%w: module is required", errUsage)
	}
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("%w: dir is required", errUsage)
	}
	if containsParentTraversalPath(dir) {
		return fmt.Errorf("%w: project directory must not contain parent traversal", errUsage)
	}
	return nil
}

func containsParentTraversalPath(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func applyAIProjectPlan(plan aiProjectPlan, opts aiProjectApplyOptions) (aiProjectApplyResult, error) {
	if err := validateAIProjectApplyInputs(plan); err != nil {
		return aiProjectApplyResult{}, err
	}
	name, module, dir := aiProjectPlanValues(plan)
	commandArgs, err := aiProjectApplyArgs(plan)
	if err != nil {
		return aiProjectApplyResult{}, err
	}
	if len(commandArgs) == 0 {
		return aiProjectApplyResult{}, fmt.Errorf("%w: no scaffold command generated", errUsage)
	}
	if err := withCommandIO(IOStreams{In: nil, Out: io.Discard, Err: currentErr()}, outputText, verbosityQuiet, func() error {
		return runAIProjectApplyCommand(commandArgs)
	}); err != nil {
		return aiProjectApplyResult{}, err
	}
	generatedFeatures, err := generator.ApplyProjectFeaturePlugins(generator.ProjectFeatureOptions{
		Dir:      dir,
		Name:     name,
		Module:   module,
		Features: plan.Features,
	})
	if err != nil {
		return aiProjectApplyResult{}, err
	}
	featureDependencies, featureConfigHints, featureVerify := aggregateProjectFeatureContract(generatedFeatures)
	verifyCommands := appendUniqueStrings(append([]string(nil), plan.Verify...), featureVerify...)
	warnings := append([]string(nil), plan.Warnings...)
	warnings = append(warnings, "ai new --apply writes scaffold files using built-in local generators only")
	verification := []aiProjectVerificationResult(nil)
	verifyPassed := false
	if opts.Verify {
		var err error
		verification, verifyPassed, err = runAIProjectVerification(dir, verifyCommands, opts.VerifyTimeout)
		if err != nil {
			return aiProjectApplyResult{}, err
		}
		controlPlaneResult := runAIProjectControlPlaneSnapshotAssertion(dir, opts.VerifyTimeout)
		if controlPlaneResult.Status != "skipped" {
			if controlPlaneResult.Status == "failed" {
				verifyPassed = false
			}
			verification = append(verification, controlPlaneResult)
		}
	} else {
		warnings = append(warnings, "generated verification commands are reported but not executed; pass --verify to run supported checks")
	}
	return aiProjectApplyResult{
		Plan:              plan,
		Applied:           true,
		OutputDir:         dir,
		ExecutedCommand:   "gofly " + strings.Join(commandArgs, " "),
		GeneratedFeatures: generatedFeatures,
		Dependencies:      featureDependencies,
		ConfigHints:       featureConfigHints,
		FeatureVerify:     featureVerify,
		Verify:            verifyCommands,
		VerifyRan:         opts.Verify,
		VerifyPassed:      verifyPassed,
		Verification:      verification,
		Warnings:          warnings,
		NextActions: aiProjectApplyNextActions(
			dir,
			verifyCommands,
			featureDependencies,
			featureConfigHints,
			opts.Verify,
			verifyPassed,
		),
		MutatesFilesystem: true,
	}, nil
}

func aggregateProjectFeatureContract(features []generator.ProjectFeatureResult) ([]string, []generator.ConfigHint, []string) {
	dependencies := []string{}
	configHints := []generator.ConfigHint{}
	verifyCommands := []string{}
	seenConfigHints := map[string]struct{}{}
	for _, feature := range features {
		dependencies = appendUniqueStrings(dependencies, feature.Dependencies...)
		verifyCommands = appendUniqueStrings(verifyCommands, feature.VerifyCommands...)
		for _, hint := range feature.ConfigHints {
			key := strings.ToLower(strings.TrimSpace(hint.Key))
			if key == "" {
				continue
			}
			if _, ok := seenConfigHints[key]; ok {
				continue
			}
			seenConfigHints[key] = struct{}{}
			configHints = append(configHints, hint)
		}
	}
	return dependencies, configHints, verifyCommands
}

func appendUniqueStrings(values []string, more ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(more))
	unique := make([]string, 0, len(values)+len(more))
	for _, value := range append(values, more...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func aiProjectApplyNextActions(
	dir string,
	verify []string,
	dependencies []string,
	configHints []generator.ConfigHint,
	verifyRan bool,
	verifyPassed bool,
) []string {
	next := []string{"cd " + dir}
	if len(dependencies) > 0 {
		next = append(next, "review feature dependencies: go get "+strings.Join(dependencies, " "))
	}
	for _, hint := range configHints {
		action := "configure " + hint.Key + ": " + hint.Description
		if hint.Example != "" {
			action += " (example: " + hint.Example + ")"
		}
		next = append(next, action)
	}
	if len(verify) == 0 {
		return next
	}
	if !verifyRan {
		return append(next, "run: "+strings.Join(verify, " && "))
	}
	if verifyPassed {
		return append(next, "review generated files and commit when ready")
	}
	return append(next, "fix failed verification output, then rerun: "+strings.Join(verify, " && "))
}

func runAIProjectVerification(dir string, verify []string, timeout time.Duration) ([]aiProjectVerificationResult, bool, error) {
	if timeout <= 0 {
		return nil, false, fmt.Errorf("%w: verification timeout must be positive", errUsage)
	}
	results := make([]aiProjectVerificationResult, 0, len(verify))
	passed := true
	for _, command := range verify {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		result := runAIProjectVerificationCommand(dir, command, timeout)
		if result.Status == "failed" {
			passed = false
		}
		results = append(results, result)
	}
	return results, passed, nil
}

func runAIProjectControlPlaneSnapshotAssertion(dir string, timeout time.Duration) aiProjectVerificationResult {
	const command = "control-plane snapshot"
	if timeout <= 0 {
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: "verification timeout must be positive"}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: err.Error()}
	}
	defer func() { _ = root.Close() }()
	testFile, err := root.Open(filepath.Join("internal", "config", "config_test.go"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return aiProjectVerificationResult{Command: command, Status: "skipped", Error: "generated project does not expose a control-plane snapshot contract test"}
		}
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: err.Error()}
	}
	data, err := io.ReadAll(testFile)
	_ = testFile.Close()
	if err != nil {
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: err.Error()}
	}
	if !strings.Contains(string(data), "TestControlPlaneSnapshotExposesGeneratedContract") {
		return aiProjectVerificationResult{Command: command, Status: "skipped", Error: "generated project does not expose a control-plane snapshot contract test"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./internal/config", "-run", "TestControlPlaneSnapshotExposesGeneratedContract", "-count=1")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	result := aiProjectVerificationResult{Command: command, Status: "passed", Output: truncateVerificationOutput(string(out))}
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "failed"
		result.Error = "control-plane snapshot assertion timed out"
		return result
	}
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	return result
}

func runAIProjectVerificationCommand(dir, command string, timeout time.Duration) aiProjectVerificationResult {
	name, args, ok := aiProjectVerificationCommandArgs(command)
	if !ok {
		return aiProjectVerificationResult{Command: command, Status: "skipped", Error: "unsupported verification command"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// #nosec G204 -- verification commands are selected from aiProjectVerificationCommandArgs allow-list and never executed through a shell.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if command == "gofly ai doctor --json" {
		if frameworkPath := strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH")); frameworkPath != "" {
			cmd.Dir = frameworkPath
		}
	}
	out, err := cmd.CombinedOutput()
	result := aiProjectVerificationResult{Command: command, Status: "passed", Output: truncateVerificationOutput(string(out))}
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "failed"
		result.Error = "verification command timed out"
		return result
	}
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	return result
}

func aiProjectVerificationCommandArgs(command string) (string, []string, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", nil, false
	}
	switch strings.Join(fields, " ") {
	case "gofmt":
		return "go", []string{"fmt", "./..."}, true
	case "go test ./...":
		return "go", []string{"test", "./..."}, true
	case "go mod tidy":
		return "go", []string{"mod", "tidy"}, true
	case "go vet ./...":
		return "go", []string{"vet", "./..."}, true
	case "gofly ai doctor --json":
		if frameworkPath := strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH")); frameworkPath != "" {
			return "go", []string{"run", "./cmd/gofly", "ai", "doctor", "--json"}, true
		}
		return "gofly", []string{"ai", "doctor", "--json"}, true
	default:
		return "", nil, false
	}
}

func truncateVerificationOutput(output string) string {
	const maxVerificationOutputBytes = 4096
	output = strings.TrimSpace(output)
	if len(output) <= maxVerificationOutputBytes {
		return output
	}
	return output[:maxVerificationOutputBytes] + "\n... truncated ..."
}

func runAIProjectApplyCommand(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: scaffold command is incomplete", errUsage)
	}
	switch {
	case args[0] == "new" && args[1] == "service":
		return serviceNewCommand(args[2:])
	case args[0] == "new" && args[1] == "api":
		return apiNewCommand(args[2:])
	case args[0] == "new" && args[1] == "rpc":
		return rpcNewCommand(args[2:])
	case args[0] == "gen" && args[1] == "gateway":
		return gatewayGenCommand(args[2:])
	default:
		return fmt.Errorf("%w: unsupported scaffold command `gofly %s`", errUsage, strings.Join(args, " "))
	}
}

func aiProjectPlanValues(plan aiProjectPlan) (name, module, dir string) {
	fields := strings.Fields(plan.Command)
	if len(fields) > 3 && fields[0] == "gofly" && !strings.HasPrefix(fields[3], "-") {
		name = fields[3]
	}
	if v := templateInputValue(plan.Command, "--name"); v != "" {
		name = v
	}
	return name, templateInputValue(plan.Command, "--module"), templateInputValue(plan.Command, "--dir")
}

func aiProjectApplyArgs(plan aiProjectPlan) ([]string, error) {
	name, module, dir := aiProjectPlanValues(plan)
	fields := strings.Fields(plan.Template.Command)
	if len(fields) < 3 || fields[0] != "gofly" {
		return nil, fmt.Errorf("%w: unsupported scaffold command %q", errUsage, plan.Template.Command)
	}
	args := make([]string, 0, len(fields)-1)
	for _, field := range fields[1:] {
		switch field {
		case "<name>":
			args = append(args, name)
		case "<module>":
			args = append(args, module)
		case "<dir>":
			args = append(args, dir)
		default:
			args = append(args, field)
		}
	}
	args = stripCommandFlags(args, "--dry-run", "--plan", "--json")
	return args, nil
}

func stripCommandFlags(args []string, names ...string) []string {
	remove := make(map[string]struct{}, len(names))
	for _, name := range names {
		remove[name] = struct{}{}
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, hasInlineValue := splitFlagName(arg)
		if _, ok := remove[name]; ok {
			if !hasInlineValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

func splitFlagName(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") {
		return arg, false
	}
	name, _, ok := strings.Cut(arg, "=")
	if ok {
		return name, true
	}
	return arg, false
}

func templateInputValue(command, flagName string) string {
	fields := strings.Fields(command)
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == flagName && i+1 < len(fields) {
			return fields[i+1]
		}
		prefix := flagName + "="
		if strings.HasPrefix(field, prefix) {
			return strings.TrimPrefix(field, prefix)
		}
	}
	return ""
}

func flagWasProvided(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func printAIProjectPlanText(projectPlan aiProjectPlan) {
	cliOutputfIf("template=%s kind=%s risk=%s\n", projectPlan.Template.ID, projectPlan.ProjectType, projectPlan.RiskLevel)
	cliOutputfIf("features=%s\n", strings.Join(projectPlan.Features, ","))
	cliOutputfIf("command=%s\n", projectPlan.Command)
	for _, warning := range projectPlan.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
}

func materializeTemplateCommand(command, name, module, dir string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "demo"
	}
	module = strings.TrimSpace(module)
	if module == "" {
		module = "example.com/" + name
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = name
	}
	replacer := strings.NewReplacer("<name>", name, "<module>", module, "<dir>", dir)
	return replacer.Replace(command)
}

func aiCompleteCommand(args []string) error {
	fs := flag.NewFlagSet("ai complete", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "prompt text")
	provider := fs.String("provider", "", "provider mode; use ai manifest to inspect available providers")
	model := fs.String("model", "", "model label")
	maxInputTokens := fs.Int("max-input-tokens", 0, "maximum cumulative input tokens")
	maxOutputTokens := fs.Int("max-output-tokens", 0, "maximum cumulative output tokens")
	maxTotalTokens := fs.Int("max-total-tokens", 0, "maximum cumulative total tokens")
	rateLimitPerSecond := fs.Int("rate-limit", 0, "maximum LLM calls per second; zero disables rate limiting")
	rateLimitBurst := fs.Int("rate-burst", 0, "LLM rate limit burst; zero uses rate-limit")
	timeoutText := fs.String("timeout", "", "provider call timeout, for example 2s or 500ms")
	configPath := fs.String("config", "", "gofly config file path")
	dir := fs.String("dir", ".", "service root used to resolve .gofly/config.json when --config is omitted")
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	dryRun := fs.Bool("dry-run", false, "print the governance plan without invoking the provider")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	stream := fs.Bool("stream", false, "stream completion events; compatible alias for `gofly ai stream`")
	allowFailover := fs.Bool("allow-failover", false, "manually retry retryable provider failures against GOFLY_LLM_FAILOVER_PROVIDERS")
	failover := fs.Bool("failover", false, "alias for --allow-failover")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *prompt == "" && len(remaining) > 0 {
		*prompt = strings.Join(remaining, " ")
	} else if len(remaining) > 0 {
		return fmt.Errorf("%w: ai complete accepts either --prompt or positional prompt text, not both", errUsage)
	}
	if strings.TrimSpace(*prompt) == "" {
		return fmt.Errorf("%w: --prompt or positional prompt text is required for `gofly ai complete`", errUsage)
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputText
	}
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	resolved, err := resolveAICompleteConfig(fs, aiCompleteConfigFlags{
		Provider:           *provider,
		Model:              *model,
		MaxInputTokens:     *maxInputTokens,
		MaxOutputTokens:    *maxOutputTokens,
		MaxTotalTokens:     *maxTotalTokens,
		RateLimitPerSecond: *rateLimitPerSecond,
		RateLimitBurst:     *rateLimitBurst,
		Timeout:            *timeoutText,
		ConfigPath:         *configPath,
		Dir:                *dir,
		AllowFailover:      *allowFailover || *failover,
	})
	if err != nil {
		return err
	}
	req := llm.Request{Provider: resolved.Provider, Model: resolved.Model, Prompt: *prompt, MaxOutputTokens: resolved.MaxOutputTokens, Metadata: map[string]string{"tool": "gofly", "command": "ai.complete"}}
	inputTokens := llm.EstimateTokens(*prompt)
	if *stream {
		if *dryRun || *plan {
			return printAIStreamPlanFor("ai.complete", "ai complete --stream", resolved, inputTokens, format == outputJSON || *jsonOutput)
		}
		return runAIStream(resolved, *prompt, format == outputJSON || *jsonOutput, "ai.complete", "ai.complete")
	}
	if *dryRun || *plan {
		return printAICompletePlan(resolved, inputTokens, format == outputJSON || *jsonOutput)
	}
	resp, providerSpec, budget, failoverUsed, failoverFrom, err := runAICompleteWithFailover(resolved, req, *prompt)
	if err != nil {
		return err
	}
	warnings := []string{}
	if providerSpec.Name == "noop" {
		warnings = append(warnings, "built-in noop provider does not call external LLM services or return generated text")
	}
	if providerSpec.RequiresSecrets {
		warnings = append(warnings, "provider credentials are resolved from environment variables and are never read from .gofly/config.json or included in output")
	}
	result := aiCompleteResult{
		Provider: providerSpec.Name,
		Model:    resolved.Model,
		Text:     resp.Text,
		Usage:    resp.Usage,
		Budget:   budget.Snapshot(),
		Governance: aiCompleteGovernance{
			ProviderMode:         providerSpec.Name,
			ProviderCapabilities: providerSpec.Capabilities,
			TelemetryFields:      aiLLMTelemetryFields(),
			FailoverProviders:    resolved.FailoverProviders,
			FailoverMode:         aiFailoverMode(resolved.FailoverProviders, resolved.AllowFailover),
			FailoverAllowed:      resolved.AllowFailover && len(resolved.FailoverProviders) > 0,
			FailoverUsed:         failoverUsed,
			FailoverFrom:         failoverFrom,
			IdempotencyKeySet:    resolved.AllowFailover && len(resolved.FailoverProviders) > 0,
			NetworkAccess:        providerSpec.NetworkAccess,
			RequiresSecrets:      providerSpec.RequiresSecrets,
			SecretSource:         "environment",
			Redacted:             true,
			BudgetEnforced:       resolved.MaxInputTokens > 0 || resolved.MaxOutputTokens > 0 || resolved.MaxTotalTokens > 0,
			RateLimited:          resolved.RateLimitPerSecond > 0,
			AuditLogged:          true,
		},
		Metadata: map[string]string{"configPath": resolved.ConfigPath},
		Warnings: warnings,
	}
	if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
		return printJSONEnvelope("ai.complete", result)
	}
	cliOutputfIf("provider=%s model=%s total_tokens=%d\n", result.Provider, result.Model, result.Usage.TotalTokens)
	if result.Text != "" {
		cliOutputlnIf(result.Text)
		return nil
	}
	cliOutputlnIf("(noop provider returned no text)")
	return nil
}

func runAICompleteWithFailover(resolved aiCompleteConfig, req llm.Request, prompt string) (llm.Response, llm.ProviderSpec, *llm.TokenBudget, bool, string, error) {
	budget := llm.NewTokenBudget(resolved.MaxInputTokens, resolved.MaxOutputTokens, resolved.MaxTotalTokens)
	auditor := llm.NewAuditLogger(slog.New(slog.NewJSONHandler(currentErr(), &slog.HandlerOptions{Level: slog.LevelInfo})), nil)
	options := aiGovernedProviderOptions(resolved, budget, auditor)
	ctx, cancel := aiExecutionContext(resolved)
	defer cancel()

	registry := llm.NewDefaultProviderRegistry()
	providers := []string{resolved.Provider}
	if resolved.AllowFailover {
		providers = append(providers, resolved.FailoverProviders...)
	}
	idempotencyKey := aiFailoverIdempotencyKey(prompt, resolved)
	var primaryErr error
	for index, providerName := range providers {
		attemptReq := req
		attemptReq.Provider = providerName
		attemptReq.Metadata = aiAttemptMetadata("ai.complete", index, resolved.Provider, providerName, idempotencyKey, resolved.AllowFailover)
		builtProvider, providerSpec, err := registry.Build(providerName, llm.ProviderConfig{
			Provider: providerName,
			Model:    resolved.Model,
			Secrets:  llm.EnvSecretResolver{},
			Metadata: attemptReq.Metadata,
		})
		if err != nil {
			return llm.Response{}, providerSpec, budget, index > 0, failoverFrom(index, resolved.Provider), err
		}
		providerClient := llm.NewGovernedProvider(builtProvider, options...)
		resp, err := providerClient.Complete(ctx, attemptReq)
		if err == nil {
			return resp, providerSpec, budget, index > 0, failoverFrom(index, resolved.Provider), nil
		}
		if index == 0 {
			primaryErr = err
		}
		if !shouldAttemptManualFailover(resolved, index, err) {
			return llm.Response{}, providerSpec, budget, index > 0, failoverFrom(index, resolved.Provider), err
		}
	}
	return llm.Response{}, llm.ProviderSpec{}, budget, false, "", primaryErr
}

func aiGovernedProviderOptions(resolved aiCompleteConfig, budget *llm.TokenBudget, auditor *llm.AuditLogger) []llm.Option {
	options := []llm.Option{llm.WithTokenBudget(budget), llm.WithAuditLogger(auditor), llm.WithCircuitBreaker(breaker.WithFailureThreshold(5), breaker.WithOpenTimeout(10*time.Second))}
	if resolved.RateLimitPerSecond > 0 {
		options = append(options, llm.WithRateLimiter(llm.NewRateLimiter(resolved.RateLimitPerSecond, resolved.RateLimitBurst)))
	}
	return options
}

func aiExecutionContext(resolved aiCompleteConfig) (context.Context, context.CancelFunc) {
	ctx := context.Background()
	if resolved.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, resolved.Timeout)
}

func shouldAttemptManualFailover(resolved aiCompleteConfig, index int, err error) bool {
	return resolved.AllowFailover && index == 0 && len(resolved.FailoverProviders) > 0 && isRetryableLLMError(err)
}

func isRetryableLLMError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *llm.ProviderHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Retryable()
	}
	return errors.Is(err, llm.ErrProviderRequestFailed) || errors.Is(err, llm.ErrRateLimited)
}

func failoverFrom(index int, primary string) string {
	if index == 0 {
		return ""
	}
	return primary
}

func aiFailoverIdempotencyKey(prompt string, resolved aiCompleteConfig) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		prompt,
		resolved.Provider,
		resolved.Model,
		strconv.Itoa(resolved.MaxInputTokens),
		strconv.Itoa(resolved.MaxOutputTokens),
		strconv.Itoa(resolved.MaxTotalTokens),
	}, "\x00")))
	return fmt.Sprintf("gofly-ai-%x", sum[:12])
}

func aiAttemptMetadata(command string, index int, primary, provider, idempotencyKey string, allowFailover bool) map[string]string {
	metadata := map[string]string{"tool": "gofly", "command": command, "provider_attempt": strconv.Itoa(index + 1)}
	if allowFailover {
		metadata["manual_failover_allowed"] = "true"
		metadata["idempotency_key"] = idempotencyKey
	}
	if index > 0 {
		metadata["manual_failover"] = "true"
		metadata["failover_from"] = primary
		metadata["failover_to"] = provider
	}
	return metadata
}

func aiStreamCommand(args []string) error {
	fs := flag.NewFlagSet("ai stream", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "prompt text")
	provider := fs.String("provider", "", "provider mode; use ai manifest to inspect available providers")
	model := fs.String("model", "", "model label")
	maxInputTokens := fs.Int("max-input-tokens", 0, "maximum cumulative input tokens")
	maxOutputTokens := fs.Int("max-output-tokens", 0, "maximum cumulative output tokens")
	maxTotalTokens := fs.Int("max-total-tokens", 0, "maximum cumulative total tokens")
	rateLimitPerSecond := fs.Int("rate-limit", 0, "maximum LLM calls per second; zero disables rate limiting")
	rateLimitBurst := fs.Int("rate-burst", 0, "LLM rate limit burst; zero uses rate-limit")
	timeoutText := fs.String("timeout", "", "provider call timeout, for example 2s or 500ms")
	configPath := fs.String("config", "", "gofly config file path")
	dir := fs.String("dir", ".", "service root used to resolve .gofly/config.json when --config is omitted")
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output newline-delimited JSON envelopes")
	dryRun := fs.Bool("dry-run", false, "print the governance plan without invoking the provider")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	allowFailover := fs.Bool("allow-failover", false, "manually retry retryable provider start failures against GOFLY_LLM_FAILOVER_PROVIDERS before emitting any stream events")
	failover := fs.Bool("failover", false, "alias for --allow-failover")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *prompt == "" && len(remaining) > 0 {
		*prompt = strings.Join(remaining, " ")
	} else if len(remaining) > 0 {
		return fmt.Errorf("%w: ai stream accepts either --prompt or positional prompt text, not both", errUsage)
	}
	if strings.TrimSpace(*prompt) == "" {
		return fmt.Errorf("%w: --prompt or positional prompt text is required for `gofly ai stream`", errUsage)
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputText
	}
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	resolved, err := resolveAICompleteConfig(fs, aiCompleteConfigFlags{
		Provider:           *provider,
		Model:              *model,
		MaxInputTokens:     *maxInputTokens,
		MaxOutputTokens:    *maxOutputTokens,
		MaxTotalTokens:     *maxTotalTokens,
		RateLimitPerSecond: *rateLimitPerSecond,
		RateLimitBurst:     *rateLimitBurst,
		Timeout:            *timeoutText,
		ConfigPath:         *configPath,
		Dir:                *dir,
		AllowFailover:      *allowFailover || *failover,
	})
	if err != nil {
		return err
	}
	inputTokens := llm.EstimateTokens(*prompt)
	if *dryRun || *plan {
		return printAIStreamPlan(resolved, inputTokens, format == outputJSON || *jsonOutput)
	}
	return runAIStream(resolved, *prompt, format == outputJSON || *jsonOutput, "ai.stream", "ai.stream")
}

func runAIStream(resolved aiCompleteConfig, prompt string, forceJSON bool, envelopeCommand, metadataCommand string) error {
	budget := llm.NewTokenBudget(resolved.MaxInputTokens, resolved.MaxOutputTokens, resolved.MaxTotalTokens)
	auditor := llm.NewAuditLogger(slog.New(slog.NewJSONHandler(currentErr(), &slog.HandlerOptions{Level: slog.LevelInfo})), nil)
	options := aiGovernedProviderOptions(resolved, budget, auditor)
	ctx, cancel := aiExecutionContext(resolved)
	defer cancel()
	registry := llm.NewDefaultProviderRegistry()
	providers := []string{resolved.Provider}
	if resolved.AllowFailover {
		providers = append(providers, resolved.FailoverProviders...)
	}
	idempotencyKey := aiFailoverIdempotencyKey(prompt, resolved)
	var stream <-chan llm.StreamEvent
	var providerSpec llm.ProviderSpec
	var failoverUsed bool
	var failoverSource string
	for index, providerName := range providers {
		metadata := aiAttemptMetadata(metadataCommand, index, resolved.Provider, providerName, idempotencyKey, resolved.AllowFailover)
		builtProvider, spec, err := registry.Build(providerName, llm.ProviderConfig{
			Provider: providerName,
			Model:    resolved.Model,
			Secrets:  llm.EnvSecretResolver{},
			Metadata: metadata,
		})
		if err != nil {
			return err
		}
		providerClient := llm.NewGovernedProvider(builtProvider, options...)
		req := llm.Request{Provider: providerName, Model: resolved.Model, Prompt: prompt, MaxOutputTokens: resolved.MaxOutputTokens, Metadata: metadata}
		stream, err = providerClient.Stream(ctx, req)
		if err == nil {
			providerSpec = spec
			failoverUsed = index > 0
			failoverSource = failoverFrom(index, resolved.Provider)
			break
		}
		if !shouldAttemptManualFailover(resolved, index, err) {
			return err
		}
	}
	if stream == nil {
		return fmt.Errorf("%w: stream was not created", llm.ErrProviderRequestFailed)
	}
	jsonStream := forceJSON || outputMode() == outputJSON
	governance := aiCompleteGovernance{
		ProviderMode:         providerSpec.Name,
		ProviderCapabilities: providerSpec.Capabilities,
		TelemetryFields:      aiLLMTelemetryFields(),
		FailoverProviders:    resolved.FailoverProviders,
		FailoverMode:         aiFailoverMode(resolved.FailoverProviders, resolved.AllowFailover),
		FailoverAllowed:      resolved.AllowFailover && len(resolved.FailoverProviders) > 0,
		FailoverUsed:         failoverUsed,
		FailoverFrom:         failoverSource,
		IdempotencyKeySet:    resolved.AllowFailover && len(resolved.FailoverProviders) > 0,
		NetworkAccess:        providerSpec.NetworkAccess,
		RequiresSecrets:      providerSpec.RequiresSecrets,
		SecretSource:         "environment",
		Redacted:             true,
		BudgetEnforced:       resolved.MaxInputTokens > 0 || resolved.MaxOutputTokens > 0 || resolved.MaxTotalTokens > 0,
		RateLimited:          resolved.RateLimitPerSecond > 0,
		AuditLogged:          true,
	}
	index := 0
	printedText := false
	for event := range stream {
		if event.Err != nil {
			if jsonStream && outputMode() != outputJSON {
				_ = printJSONLine(jsonEnvelope{OK: false, Command: envelopeCommand, Version: Version, Error: classifyJSONError(event.Err)})
			}
			return event.Err
		}
		result := aiStreamEventResult{Provider: providerSpec.Name, Model: resolved.Model, Index: index, Delta: event.Delta, Done: event.Done, Usage: event.Usage, Budget: budget.Snapshot(), Governance: governance}
		if jsonStream {
			if err := printJSONLine(jsonEnvelope{OK: true, Command: envelopeCommand, Version: Version, Data: result}); err != nil {
				return err
			}
		} else if event.Delta != "" {
			cliOutputIf(event.Delta)
			printedText = true
		}
		index++
	}
	if !jsonStream && printedText {
		cliOutputlnIf()
	}
	return nil
}

type aiCompleteConfigFlags struct {
	Provider           string
	Model              string
	AllowFailover      bool
	MaxInputTokens     int
	MaxOutputTokens    int
	MaxTotalTokens     int
	RateLimitPerSecond int
	RateLimitBurst     int
	Timeout            string
	ConfigPath         string
	Dir                string
}

func resolveAICompleteConfig(fs *flag.FlagSet, flags aiCompleteConfigFlags) (aiCompleteConfig, error) {
	path := flags.ConfigPath
	if path == "" {
		base := flags.Dir
		if base == "" {
			base = "."
		}
		path = filepath.Join(base, generator.DefaultConfigFile)
	}
	cfg, err := generator.LoadConfig(path)
	if err != nil {
		return aiCompleteConfig{}, err
	}
	resolved := aiCompleteConfig{Provider: "noop", Model: "noop", ConfigPath: path}
	if cfg != nil && cfg.LLM != nil {
		resolved.Provider = cfg.LLM.Provider
		resolved.Model = cfg.LLM.Model
		resolved.MaxInputTokens = cfg.LLM.MaxInputTokens
		resolved.MaxOutputTokens = cfg.LLM.MaxOutputTokens
		resolved.MaxTotalTokens = cfg.LLM.MaxTotalTokens
		resolved.RateLimitPerSecond = cfg.LLM.RateLimitPerSecond
		resolved.RateLimitBurst = cfg.LLM.RateLimitBurst
		if cfg.LLM.Timeout != "" {
			timeout, err := time.ParseDuration(cfg.LLM.Timeout)
			if err != nil {
				return aiCompleteConfig{}, fmt.Errorf("%w: invalid llm.timeout %q: %v", errUsage, cfg.LLM.Timeout, err)
			}
			resolved.Timeout = timeout
		}
	}
	if err := applyAICompleteEnv(&resolved); err != nil {
		return aiCompleteConfig{}, err
	}
	if err := applyAICompleteFlagOverlay(fs, flags, &resolved); err != nil {
		return aiCompleteConfig{}, err
	}
	return normalizeAICompleteConfig(resolved)
}

func applyAICompleteEnv(cfg *aiCompleteConfig) error {
	if value := os.Getenv("GOFLY_LLM_PROVIDER"); value != "" {
		cfg.Provider = value
	}
	if value := os.Getenv("GOFLY_LLM_MODEL"); value != "" {
		cfg.Model = value
	}
	if value := os.Getenv("GOFLY_LLM_FAILOVER_PROVIDERS"); value != "" {
		cfg.FailoverProviders = parseAIProviderList(value)
	}
	intEnvs := []struct {
		name string
		set  func(int)
	}{
		{name: "GOFLY_LLM_MAX_INPUT_TOKENS", set: func(v int) { cfg.MaxInputTokens = v }},
		{name: "GOFLY_LLM_MAX_OUTPUT_TOKENS", set: func(v int) { cfg.MaxOutputTokens = v }},
		{name: "GOFLY_LLM_MAX_TOTAL_TOKENS", set: func(v int) { cfg.MaxTotalTokens = v }},
		{name: "GOFLY_LLM_RATE_LIMIT", set: func(v int) { cfg.RateLimitPerSecond = v }},
		{name: "GOFLY_LLM_RATE_BURST", set: func(v int) { cfg.RateLimitBurst = v }},
	}
	for _, env := range intEnvs {
		value := os.Getenv(env.name)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%w: %s must be an integer", errUsage, env.name)
		}
		env.set(parsed)
	}
	if value := os.Getenv("GOFLY_LLM_TIMEOUT"); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("%w: GOFLY_LLM_TIMEOUT must be a duration", errUsage)
		}
		cfg.Timeout = timeout
	}
	return nil
}

func applyAICompleteFlagOverlay(fs *flag.FlagSet, flags aiCompleteConfigFlags, cfg *aiCompleteConfig) error {
	if flagProvided(fs, "provider") {
		cfg.Provider = flags.Provider
	}
	if flagProvided(fs, "model") {
		cfg.Model = flags.Model
	}
	if flagProvided(fs, "allow-failover") || flagProvided(fs, "failover") {
		cfg.AllowFailover = flags.AllowFailover
	}
	if flagProvided(fs, "max-input-tokens") {
		cfg.MaxInputTokens = flags.MaxInputTokens
	}
	if flagProvided(fs, "max-output-tokens") {
		cfg.MaxOutputTokens = flags.MaxOutputTokens
	}
	if flagProvided(fs, "max-total-tokens") {
		cfg.MaxTotalTokens = flags.MaxTotalTokens
	}
	if flagProvided(fs, "rate-limit") {
		cfg.RateLimitPerSecond = flags.RateLimitPerSecond
	}
	if flagProvided(fs, "rate-burst") {
		cfg.RateLimitBurst = flags.RateLimitBurst
	}
	if flagProvided(fs, "timeout") {
		cfg.Timeout = 0
		if flags.Timeout != "" {
			timeout, err := time.ParseDuration(flags.Timeout)
			if err != nil {
				return fmt.Errorf("%w: invalid --timeout %q: %v", errUsage, flags.Timeout, err)
			}
			cfg.Timeout = timeout
		}
	}
	return nil
}

func normalizeAICompleteConfig(cfg aiCompleteConfig) (aiCompleteConfig, error) {
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	if cfg.Provider == "" {
		cfg.Provider = "noop"
	}
	registry := llm.NewDefaultProviderRegistry()
	spec, ok := registry.Spec(cfg.Provider)
	if !ok {
		return aiCompleteConfig{}, fmt.Errorf("%w: %w: %q; available providers: %s", errUsage, llm.ErrProviderNotFound, cfg.Provider, strings.Join(registry.ProviderNames(), ","))
	}
	failoverProviders, err := normalizeAIFailoverProviders(registry, cfg.Provider, cfg.FailoverProviders)
	if err != nil {
		return aiCompleteConfig{}, err
	}
	cfg.FailoverProviders = failoverProviders
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = spec.DefaultModel
	}
	if cfg.MaxInputTokens < 0 || cfg.MaxOutputTokens < 0 || cfg.MaxTotalTokens < 0 {
		return aiCompleteConfig{}, fmt.Errorf("%w: token budgets must be non-negative", errUsage)
	}
	if cfg.RateLimitPerSecond < 0 || cfg.RateLimitBurst < 0 {
		return aiCompleteConfig{}, fmt.Errorf("%w: rate limit values must be non-negative", errUsage)
	}
	if cfg.RateLimitPerSecond == 0 {
		cfg.RateLimitBurst = 0
	}
	return cfg, nil
}

func parseAIProviderList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	providers := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			providers = append(providers, part)
		}
	}
	return providers
}

func normalizeAIFailoverProviders(registry *llm.ProviderRegistry, primary string, providers []string) ([]string, error) {
	if len(providers) == 0 {
		return nil, nil
	}
	seen := map[string]struct{}{primary: {}}
	normalized := make([]string, 0, len(providers))
	for _, provider := range providers {
		name := strings.ToLower(strings.TrimSpace(provider))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		if _, ok := registry.Spec(name); !ok {
			return nil, fmt.Errorf("%w: %w: failover provider %q; available providers: %s", errUsage, llm.ErrProviderNotFound, name, strings.Join(registry.ProviderNames(), ","))
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	return normalized, nil
}

func printAICompletePlan(config aiCompleteConfig, inputTokens int, forceJSON bool) error {
	registry := llm.NewDefaultProviderRegistry()
	spec, _ := registry.Spec(config.Provider)
	inputs := map[string]string{
		"provider":                config.Provider,
		"model":                   config.Model,
		"configPath":              config.ConfigPath,
		"estimatedInputTokens":    fmt.Sprint(inputTokens),
		"maxInputTokens":          fmt.Sprint(config.MaxInputTokens),
		"maxOutputTokens":         fmt.Sprint(config.MaxOutputTokens),
		"maxTotalTokens":          fmt.Sprint(config.MaxTotalTokens),
		"rateLimit":               fmt.Sprint(config.RateLimitPerSecond),
		"rateBurst":               fmt.Sprint(config.RateLimitBurst),
		"timeout":                 config.Timeout.String(),
		"networkAccess":           fmt.Sprint(spec.NetworkAccess),
		"requiresSecrets":         fmt.Sprint(spec.RequiresSecrets),
		"secretSource":            "environment",
		"providerCapabilities":    strings.Join(spec.Capabilities, ","),
		"providerSecretEnvVars":   strings.Join(spec.SecretEnvVars, ","),
		"providerConfigEnvVars":   strings.Join(spec.ConfigEnvVars, ","),
		"providerSecretsResolved": "not-checked-in-dry-run",
		"failoverMode":            aiFailoverMode(config.FailoverProviders, config.AllowFailover),
		"failoverProviders":       strings.Join(config.FailoverProviders, ","),
		"failoverEnvVar":          "GOFLY_LLM_FAILOVER_PROVIDERS",
		"failoverAutomatic":       "false",
		"failoverAllowed":         fmt.Sprint(config.AllowFailover && len(config.FailoverProviders) > 0),
		"failoverIdempotency":     aiFailoverIdempotencyDisclosure(config),
	}
	warnings := []string{"dry-run does not call an LLM provider and never prints raw prompt text"}
	warnings = append(warnings, aiFailoverWarnings(config.FailoverProviders)...)
	if spec.RequiresSecrets {
		warnings = append(warnings, "provider credentials are resolved from environment variables only and are not read from .gofly/config.json")
	}
	if spec.NetworkAccess {
		warnings = append(warnings, "provider may perform network access when dry-run is disabled; endpoint settings are disclosed only as environment variable names")
	}
	nextActions := []string{"run without --dry-run to execute the governed provider"}
	if spec.RequiresSecrets {
		nextActions = append([]string{"export the required provider secret environment variables before executing without --dry-run"}, nextActions...)
	}
	return printCLIPlan("ai.complete", cliPlan{
		Command:           "ai complete",
		DryRun:            true,
		MutatesFilesystem: false,
		Inputs:            inputs,
		Actions: []cliPlanAction{
			{Operation: "estimate-tokens", Target: "prompt", Description: "estimate input tokens without storing or printing prompt text", RiskLevel: "read"},
			{Operation: "apply-governance", Target: "github.com/imajinyun/gofly/core/llm", Description: "apply token budget, redaction and audit controls before provider invocation", RiskLevel: "read"},
			{Operation: "plan-provider-failover", Target: strings.Join(config.FailoverProviders, ","), Description: aiFailoverPlanDescription(config.FailoverProviders, config.AllowFailover), RiskLevel: "read"},
			{Operation: "invoke-provider", Target: config.Provider, Description: aiProviderPlanDescription(spec), RiskLevel: "read"},
		},
		Warnings:    warnings,
		NextActions: nextActions,
	}, forceJSON)
}

func printAIStreamPlan(config aiCompleteConfig, inputTokens int, forceJSON bool) error {
	return printAIStreamPlanFor("ai.stream", "ai stream", config, inputTokens, forceJSON)
}

func printAIStreamPlanFor(envelopeCommand, displayCommand string, config aiCompleteConfig, inputTokens int, forceJSON bool) error {
	registry := llm.NewDefaultProviderRegistry()
	spec, _ := registry.Spec(config.Provider)
	inputs := map[string]string{
		"provider":                config.Provider,
		"model":                   config.Model,
		"configPath":              config.ConfigPath,
		"estimatedInputTokens":    fmt.Sprint(inputTokens),
		"maxInputTokens":          fmt.Sprint(config.MaxInputTokens),
		"maxOutputTokens":         fmt.Sprint(config.MaxOutputTokens),
		"maxTotalTokens":          fmt.Sprint(config.MaxTotalTokens),
		"rateLimit":               fmt.Sprint(config.RateLimitPerSecond),
		"rateBurst":               fmt.Sprint(config.RateLimitBurst),
		"timeout":                 config.Timeout.String(),
		"networkAccess":           fmt.Sprint(spec.NetworkAccess),
		"requiresSecrets":         fmt.Sprint(spec.RequiresSecrets),
		"secretSource":            "environment",
		"providerCapabilities":    strings.Join(spec.Capabilities, ","),
		"providerSecretEnvVars":   strings.Join(spec.SecretEnvVars, ","),
		"providerConfigEnvVars":   strings.Join(spec.ConfigEnvVars, ","),
		"providerSecretsResolved": "not-checked-in-dry-run",
		"failoverMode":            aiFailoverMode(config.FailoverProviders, config.AllowFailover),
		"failoverProviders":       strings.Join(config.FailoverProviders, ","),
		"failoverEnvVar":          "GOFLY_LLM_FAILOVER_PROVIDERS",
		"failoverAutomatic":       "false",
		"failoverAllowed":         fmt.Sprint(config.AllowFailover && len(config.FailoverProviders) > 0),
		"failoverIdempotency":     aiFailoverIdempotencyDisclosure(config),
	}
	warnings := []string{"dry-run does not call an LLM provider and never prints raw prompt text", "JSON stream mode emits one JSON envelope per event"}
	warnings = append(warnings, aiFailoverWarnings(config.FailoverProviders)...)
	if spec.RequiresSecrets {
		warnings = append(warnings, "provider credentials are resolved from environment variables only and are not read from .gofly/config.json")
	}
	if spec.NetworkAccess {
		warnings = append(warnings, "provider may perform network access when dry-run is disabled; endpoint settings are disclosed only as environment variable names")
	}
	nextActions := []string{"run without --dry-run to execute the governed streaming provider"}
	if spec.RequiresSecrets {
		nextActions = append([]string{"export the required provider secret environment variables before executing without --dry-run"}, nextActions...)
	}
	return printCLIPlan(envelopeCommand, cliPlan{
		Command:           displayCommand,
		DryRun:            true,
		MutatesFilesystem: false,
		Inputs:            inputs,
		Actions: []cliPlanAction{
			{Operation: "estimate-tokens", Target: "prompt", Description: "estimate input tokens without storing or printing prompt text", RiskLevel: "read"},
			{Operation: "apply-governance", Target: "github.com/imajinyun/gofly/core/llm", Description: "apply token budget, redaction, event size limits and audit controls before provider streaming", RiskLevel: "read"},
			{Operation: "plan-provider-failover", Target: strings.Join(config.FailoverProviders, ","), Description: aiFailoverPlanDescription(config.FailoverProviders, config.AllowFailover), RiskLevel: "read"},
			{Operation: "invoke-stream-provider", Target: config.Provider, Description: aiProviderPlanDescription(spec), RiskLevel: "read"},
		},
		Warnings:    warnings,
		NextActions: nextActions,
	}, forceJSON)
}

func aiFailoverMode(providers []string, allow bool) string {
	if len(providers) == 0 {
		return "disabled"
	}
	if allow {
		return "manual"
	}
	return "plan-only"
}

func aiFailoverWarnings(providers []string) []string {
	if len(providers) == 0 {
		return nil
	}
	return []string{"GOFLY_LLM_FAILOVER_PROVIDERS is advisory and only disclosed in plans/governance; automatic provider switching is intentionally disabled"}
}

func aiFailoverPlanDescription(providers []string, allow bool) string {
	if len(providers) == 0 {
		return "no failover providers configured; runtime will not switch providers automatically"
	}
	if allow {
		return "manually retry retryable provider failures against declared fallback candidates with shared budget and audit metadata"
	}
	return "declare fallback candidates for operator review without automatic provider switching"
}

func aiFailoverIdempotencyDisclosure(config aiCompleteConfig) string {
	if !config.AllowFailover || len(config.FailoverProviders) == 0 {
		return "not-enabled"
	}
	return "stable per command execution and attached only to governed attempt metadata"
}

func aiProviderPlanDescription(spec llm.ProviderSpec) string {
	if spec.Name == "" {
		return "invoke selected provider when dry-run is disabled"
	}
	if spec.NetworkAccess {
		return "invoke network-capable provider when dry-run is disabled after environment-only secret and endpoint validation"
	}
	return "invoke deterministic built-in provider when dry-run is disabled"
}

func aiManifestCommand(args []string) error {
	fs := flag.NewFlagSet("ai manifest", flag.ContinueOnError)
	formatName := fs.String("format", outputJSON, "output format: json or text")
	schemaName := fs.String("schema", "", "output manifest schema: jsonschema")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("%w: ai manifest does not accept positional arguments: %s", errUsage, strings.Join(remaining, " "))
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputJSON
	}
	if format != outputJSON && format != outputText {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	schema := strings.ToLower(strings.TrimSpace(*schemaName))
	if schema != "" {
		if schema != "jsonschema" {
			return fmt.Errorf("%w: unsupported --schema %q", errUsage, *schemaName)
		}
		return printJSONEnvelope("ai.manifest.schema", buildAIToolManifestJSONSchema())
	}
	manifest := buildAIToolManifest()
	if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
		return printJSONEnvelope("ai.manifest", manifest)
	}
	cliOutputfIf("gofly AI tool manifest (%s)\n", manifest.SchemaVersion)
	for _, cmd := range manifest.Commands {
		cliOutputfIf("%s\t%s\tdry-run=%t\trisk=%s\n", cmd.Name, strings.Join(cmd.OutputFormats, ","), cmd.SupportsDryRun, cmd.RiskLevel)
	}
	return nil
}

func aiControlPlaneCommand(args []string) error {
	if printCommandHelp("ai control-plane", args) {
		return nil
	}
	fs := flag.NewFlagSet("ai control-plane", flag.ContinueOnError)
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	schemaName := fs.String("schema", "", "output control-plane schema: jsonschema")
	watch := fs.Bool("watch", false, "emit bounded snapshot watch events")
	maxEvents := fs.Int("max-events", 1, "maximum watch events to emit")
	timeoutName := fs.String("timeout", "2s", "watch timeout boundary")
	source := fs.String("source", "", "runtime control-plane snapshot URL, for example http://127.0.0.1:8080/admin/control-plane")
	adminToken := fs.String("admin-token", "", "bearer token for --source runtime admin endpoint; defaults to GOFLY_CONTROL_PLANE_TOKEN")
	fromChecksum := fs.String("from-checksum", "", "compare current snapshot checksum with a previous checksum")
	fromSnapshot := fs.String("from-snapshot", "", "compare current snapshot with a previous control-plane snapshot JSON file")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("%w: ai control-plane does not accept positional arguments: %s", errUsage, strings.Join(remaining, " "))
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputText
	}
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	schema := strings.ToLower(strings.TrimSpace(*schemaName))
	if schema != "" {
		if schema != "jsonschema" {
			return fmt.Errorf("%w: unsupported --schema %q", errUsage, *schemaName)
		}
		return printJSONEnvelope("ai.control_plane.schema", buildAIControlPlaneJSONSchema())
	}
	baseline, err := loadAIControlPlaneBaseline(strings.TrimSpace(*fromChecksum), strings.TrimSpace(*fromSnapshot))
	if err != nil {
		return err
	}
	manifest := buildAIControlPlaneManifest()
	provider, err := newAIControlPlaneProvider(strings.TrimSpace(*source), strings.TrimSpace(*adminToken))
	if err != nil {
		return err
	}
	jsonMode := *jsonOutput || outputMode() == outputJSON || format == outputJSON
	if *watch {
		return runAIControlPlaneWatch(provider, manifest, baseline, *maxEvents, *timeoutName, jsonMode)
	}
	snapshot, err := provider.Load(context.Background())
	if err != nil {
		return err
	}
	result := aiControlPlaneSnapshotResult{
		Source:         aiControlPlaneProviderSource(provider),
		Snapshot:       snapshot,
		Diff:           baseline.Diff(snapshot),
		AgentGuidance:  manifest.AgentGuidance,
		SecretBoundary: manifest.SecretBoundary,
	}
	result.ConsumerAction = controlplane.ConsumerActionForDiff(result.Diff)
	if jsonMode {
		return printJSONEnvelope("ai.control_plane", result)
	}
	cliOutputfIf("gofly AI control-plane snapshot\n")
	cliOutputfIf("source=%s version=%s checksum=%s\n", result.Source, result.Snapshot.Version, result.Snapshot.Checksum)
	if result.Diff.FromChecksum != "" {
		cliOutputfIf("diff changed=%t changeType=%s from=%s to=%s\n", result.Diff.Changed, result.Diff.ChangeType, result.Diff.FromChecksum, result.Diff.ToChecksum)
	}
	cliOutputfIf("consumerAction=%s fullReconcile=%t\n", result.ConsumerAction.Action, result.ConsumerAction.RequiresFullReconcile)
	metadataKeys := make([]string, 0, len(result.Snapshot.Metadata))
	for key := range result.Snapshot.Metadata {
		metadataKeys = append(metadataKeys, key)
	}
	sort.Strings(metadataKeys)
	for _, key := range metadataKeys {
		value := result.Snapshot.Metadata[key]
		cliOutputfIf("metadata.%s=%s\n", key, value)
	}
	for _, next := range result.AgentGuidance {
		cliOutputfIf("next: %s\n", next)
	}
	return nil
}

func loadAIControlPlaneBaseline(fromChecksum, fromSnapshotPath string) (aiControlPlaneBaseline, error) {
	if fromChecksum != "" && fromSnapshotPath != "" {
		return aiControlPlaneBaseline{}, fmt.Errorf("%w: --from-checksum and --from-snapshot are mutually exclusive", errUsage)
	}
	if fromSnapshotPath == "" {
		return aiControlPlaneBaseline{Checksum: fromChecksum}, nil
	}
	// #nosec G304 -- --from-snapshot reads an explicit local baseline file selected by the CLI caller.
	data, err := os.ReadFile(fromSnapshotPath)
	if err != nil {
		return aiControlPlaneBaseline{}, fmt.Errorf("read --from-snapshot %q: %w", fromSnapshotPath, err)
	}
	snapshot, err := controlplane.DecodeSnapshotJSON(data)
	if err != nil {
		return aiControlPlaneBaseline{}, fmt.Errorf("parse --from-snapshot %q: %w", fromSnapshotPath, err)
	}
	return aiControlPlaneBaseline{Checksum: snapshot.Checksum, Snapshot: snapshot, HasSnapshot: true}, nil
}

func (b aiControlPlaneBaseline) Diff(snapshot controlplane.Snapshot) controlplane.SnapshotDiff {
	if b.HasSnapshot {
		return controlplane.DiffSnapshots(b.Snapshot, snapshot)
	}
	return controlplane.DiffSnapshotChecksum(b.Checksum, snapshot)
}

func newAIControlPlaneProvider(source, token string) (controlplane.Provider, error) {
	if source == "" {
		return controlplane.StaticProvider{Name: "ai-manifest", Snapshot: defaultAIControlPlaneSnapshot()}, nil
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: --source must be an absolute http(s) URL", errUsage)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: --source supports only http and https URLs", errUsage)
	}
	if token == "" {
		token = os.Getenv("GOFLY_CONTROL_PLANE_TOKEN")
	}
	return httpControlPlaneProvider{
		URL:   parsed.String(),
		Token: token,
		Client: &http.Client{
			Timeout: 5 * time.Second,
		},
		WatchInterval: time.Second,
	}, nil
}

func aiControlPlaneProviderSource(provider controlplane.Provider) string {
	if sourceProvider, ok := provider.(controlplane.ProviderSource); ok {
		return sourceProvider.Source()
	}
	return "control-plane"
}

func (p httpControlPlaneProvider) Source() string {
	return p.URL
}

func (p httpControlPlaneProvider) Load(ctx context.Context) (controlplane.Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return controlplane.Snapshot{}, err
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return controlplane.Snapshot{}, fmt.Errorf("create control-plane source request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return controlplane.Snapshot{}, fmt.Errorf("fetch control-plane source %s: %w", p.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return controlplane.Snapshot{}, fmt.Errorf("fetch control-plane source %s: status %d", p.URL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return controlplane.Snapshot{}, fmt.Errorf("read control-plane source %s: %w", p.URL, err)
	}
	snapshot, err := controlplane.DecodeSnapshotJSON(data)
	if err != nil {
		return controlplane.Snapshot{}, fmt.Errorf("decode control-plane source %s: %w", p.URL, err)
	}
	return snapshot.WithChecksum(), nil
}

func (p httpControlPlaneProvider) Watch(ctx context.Context) (<-chan controlplane.SnapshotEvent, error) {
	if ctx == nil {
		return nil, errors.New("control-plane source watch context is nil")
	}
	interval := p.WatchInterval
	if interval <= 0 {
		interval = time.Second
	}
	out := make(chan controlplane.SnapshotEvent, 1)
	go func() {
		defer close(out)
		emit := func() bool {
			snapshot, err := p.Load(ctx)
			event := controlplane.SnapshotEvent{Snapshot: snapshot, Source: p.Source()}
			if err != nil {
				event = controlplane.SnapshotEvent{Source: p.Source(), Error: err.Error()}
			}
			select {
			case out <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if !emit() {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !emit() {
					return
				}
			}
		}
	}()
	return out, nil
}

func runAIControlPlaneWatch(provider controlplane.Provider, manifest aiControlPlaneManifest, baseline aiControlPlaneBaseline, maxEvents int, timeoutValue string, jsonMode bool) error {
	if maxEvents <= 0 {
		return fmt.Errorf("%w: --max-events must be positive", errUsage)
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(timeoutValue))
	if err != nil || timeout <= 0 {
		return fmt.Errorf("%w: --timeout must be a positive duration", errUsage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	providerSource := aiControlPlaneProviderSource(provider)
	events, err := provider.Watch(ctx)
	if err != nil {
		return err
	}
	deduped := controlplane.DeduplicateSnapshotEvents(ctx, events)
	previous := baseline.Snapshot
	previousChecksum := baseline.Checksum
	hasPreviousSnapshot := baseline.HasSnapshot
	for index := 0; index < maxEvents; {
		select {
		case event, ok := <-deduped:
			if !ok {
				return nil
			}
			if event.Source == "" {
				event.Source = providerSource
			}
			diff := controlplane.DiffSnapshots(previous, event.Snapshot)
			if !hasPreviousSnapshot && previousChecksum != "" {
				diff = controlplane.DiffSnapshotChecksum(previousChecksum, event.Snapshot)
			}
			result := aiControlPlaneWatchEventResult{
				Index:          index,
				Source:         event.Source,
				Snapshot:       event.Snapshot,
				Diff:           diff,
				ConsumerAction: controlplane.ConsumerActionForDiff(diff),
				Error:          event.Error,
				SecretBoundary: manifest.SecretBoundary,
			}
			if jsonMode {
				if err := printJSONLine(jsonEnvelope{OK: true, Command: "ai.control_plane.event", Version: Version, Data: result}); err != nil {
					return err
				}
			} else if result.Error != "" {
				cliOutputfIf("event=%d source=%s error=%s\n", result.Index, result.Source, result.Error)
			} else {
				cliOutputfIf("event=%d source=%s version=%s checksum=%s action=%s\n", result.Index, result.Source, result.Snapshot.Version, result.Snapshot.Checksum, result.ConsumerAction.Action)
			}
			previous = event.Snapshot
			previousChecksum = event.Snapshot.WithChecksum().Checksum
			hasPreviousSnapshot = true
			index++
		case <-ctx.Done():
			return nil
		}
	}
	return nil
}

func buildAIToolManifest() aiToolManifest {
	commands := []aiToolCommand{
		manifestCommand("ai manifest", []string{"tools manifest"}, "Print command schemas, side effects, risk levels and JSON output contract.", "gofly ai manifest [--format json|text] [--schema jsonschema] [--json]", map[string]aiInputProperty{
			"format": enumStringProperty("Output format.", outputJSON, outputText),
			"schema": enumStringProperty("Optional manifest schema output.", "jsonschema"),
			"json":   boolProperty("Print JSON envelope."),
		}, []string{outputJSON, outputText}, []string{"none"}, "read", true, false, []string{"gofly ai manifest --format json", "gofly ai manifest --schema jsonschema"}),
		manifestCommand("ai control-plane", nil, "Print or watch the deterministic AI control-plane snapshot with stable checksum, source and safe agent guidance.", "gofly ai control-plane [--format text|json] [--json] [--schema jsonschema] [--source <url>] [--admin-token <token>] [--from-checksum <sha256>|--from-snapshot <file>] [--watch --max-events <n> --timeout <duration>]", map[string]aiInputProperty{
			"format":        enumStringProperty("Output format.", outputText, outputJSON),
			"json":          boolProperty("Print JSON envelope."),
			"schema":        enumStringProperty("Optional control-plane output schema.", "jsonschema"),
			"source":        stringProperty("Optional runtime REST admin /control-plane URL used instead of the built-in AI manifest snapshot."),
			"admin-token":   stringProperty("Optional bearer token for --source runtime admin endpoint; GOFLY_CONTROL_PLANE_TOKEN is used when omitted."),
			"from-checksum": stringProperty("Previous snapshot checksum used to compute a lightweight changed/unchanged diff."),
			"from-snapshot": stringProperty("Previous control-plane snapshot JSON file used to compute semantic changedFields."),
			"watch":         boolProperty("Emit bounded snapshot watch events."),
			"max-events":    intProperty("Maximum watch events to emit."),
			"timeout":       stringProperty("Watch timeout boundary as a Go duration, for example 2s."),
		}, []string{outputText, outputJSON, "ndjson"}, []string{"none; reads built-in static control-plane metadata or an explicit runtime admin URL, optional previous snapshot file, and may open a bounded watch stream"}, "read", true, false, []string{"gofly ai control-plane --json", "gofly ai control-plane --schema jsonschema", "gofly ai control-plane --source http://127.0.0.1:8080/admin/control-plane --json", "gofly ai control-plane --from-snapshot previous-control-plane.json --json", "gofly ai control-plane --watch --max-events 1 --json"}),
		manifestCommand("ai plan", nil, "Plan an AI-first project scaffold from natural language using the built-in project template catalog. This command is deterministic and does not write files.", "gofly ai plan --prompt <requirement> [--kind service|rpc|worker|cli|ai-agent|rag|gateway] [--name <name>] [--module <module>] [--dir <dir>] [--format text|json] [--json]", map[string]aiInputProperty{
			"prompt": stringProperty("Natural language project requirement."),
			"kind":   stringProperty("Optional project kind hint, such as service, rpc, worker, cli, ai-agent, rag or gateway."),
			"name":   stringProperty("Project or service name used in the proposed command."),
			"module": stringProperty("Go module path used in the proposed command."),
			"dir":    stringProperty("Output directory used in the proposed command."),
		}, []string{outputText, outputJSON}, []string{"none; planning only"}, "read", true, false, []string{"gofly ai plan 'create a rag service with redis vector store' --json"}),
		manifestCommand("ai new", nil, "Plan or apply an AI-first project scaffold from natural language using the built-in project template catalog and local scaffold generators.", "gofly ai new --prompt <requirement> [--template <id>] [--kind service|rpc|worker|cli|ai-agent|rag|gateway] --name <name> --module <module> --dir <dir> [--dry-run|--plan|--apply] [--verify] [--format text|json] [--json]", map[string]aiInputProperty{
			"prompt":   stringProperty("Natural language project requirement."),
			"template": stringProperty("Explicit project template id, for example go-rag-service."),
			"kind":     stringProperty("Optional project kind hint, such as service, rpc, worker, cli, ai-agent, rag or gateway."),
			"name":     stringProperty("Project or service name."),
			"module":   stringProperty("Go module path."),
			"dir":      stringProperty("Output directory."),
			"dryRun":   boolProperty("Print the selected scaffold plan without writing files."),
			"apply":    boolProperty("Apply the planned scaffold with built-in generators."),
			"verify":   boolProperty("Run supported post-generation checks after --apply."),
		}, []string{outputText, outputJSON}, []string{"creates scaffold files under --dir when --apply is set", "may run local Go verification commands under --dir when --verify is set"}, "medium", true, true, []string{"gofly ai new 'create a rest api' --name hello --module example.com/hello --dir hello --dry-run --json", "gofly ai new --template go-rpc-grpc --name greeter --module example.com/greeter --dir greeter --apply --verify"}),
		manifestCommand("ai complete", nil, "Execute a governed completion through the provider registry with config/env/flag layering, token budget, redaction and audit controls. Use --stream as a compatible entry point for the ai stream event contract.", "gofly ai complete --prompt <text> [--stream] [--config .gofly/config.json] [--provider noop] [--model <model>] [--max-input-tokens <n>] [--max-output-tokens <n>] [--max-total-tokens <n>] [--rate-limit <n>] [--timeout <duration>] [--allow-failover|--failover] [--dry-run|--plan] [--format text|json] [--json]", map[string]aiInputProperty{
			"prompt":          stringProperty("Prompt text. It is redacted before provider calls and never included in plan output."),
			"config":          stringProperty("gofly config file. llm defaults are read before GOFLY_LLM_* env vars and CLI flags."),
			"dir":             stringProperty("Service root used to resolve .gofly/config.json when --config is omitted."),
			"provider":        enumStringProperty("Provider mode. Inspect llmGovernance.providers for available providers.", "noop", "openai-compatible"),
			"model":           stringProperty("Model label for audit and output."),
			"maxInputTokens":  intProperty("Maximum cumulative input tokens; zero means unlimited."),
			"maxOutputTokens": intProperty("Maximum cumulative output tokens; zero means unlimited."),
			"maxTotalTokens":  intProperty("Maximum cumulative total tokens; zero means unlimited."),
			"rateLimit":       intProperty("Maximum calls per second; zero disables rate limiting."),
			"rateBurst":       intProperty("Rate limit burst; zero uses rateLimit."),
			"timeout":         stringProperty("Provider call timeout, for example 2s or 500ms."),
			"dryRun":          boolProperty("Print governance plan without invoking the provider."),
			"stream":          boolProperty("Emit governed streaming completion events using the same contract as gofly ai stream."),
			"allowFailover":   boolProperty("Manual opt-in to retry retryable provider failures against GOFLY_LLM_FAILOVER_PROVIDERS with shared budget and audit metadata."),
		}, []string{outputText, outputJSON}, []string{"provider dependent; built-in noop has no filesystem or network side effects"}, "read", true, false, []string{"gofly ai complete --prompt 'hello' --max-total-tokens 32 --json"}),
		manifestCommand("ai stream", []string{"ai complete --stream"}, "Execute a governed streaming completion through the provider registry and emit text deltas or newline-delimited JSON event envelopes.", "gofly ai stream --prompt <text> [--config .gofly/config.json] [--provider noop|openai-compatible] [--model <model>] [--max-input-tokens <n>] [--max-output-tokens <n>] [--max-total-tokens <n>] [--rate-limit <n>] [--timeout <duration>] [--allow-failover|--failover] [--dry-run|--plan] [--format text|json] [--json]", map[string]aiInputProperty{
			"prompt":          stringProperty("Prompt text. It is redacted before provider calls and never included in plan output."),
			"config":          stringProperty("gofly config file. llm defaults are read before GOFLY_LLM_* env vars and CLI flags."),
			"dir":             stringProperty("Service root used to resolve .gofly/config.json when --config is omitted."),
			"provider":        enumStringProperty("Provider mode. Inspect llmGovernance.providers for available providers.", "noop", "openai-compatible"),
			"model":           stringProperty("Model label for audit and output."),
			"maxInputTokens":  intProperty("Maximum cumulative input tokens; zero means unlimited."),
			"maxOutputTokens": intProperty("Maximum cumulative output tokens; zero means unlimited."),
			"maxTotalTokens":  intProperty("Maximum cumulative total tokens; zero means unlimited."),
			"rateLimit":       intProperty("Maximum calls per second; zero disables rate limiting."),
			"rateBurst":       intProperty("Rate limit burst; zero uses rateLimit."),
			"timeout":         stringProperty("Provider call timeout, for example 2s or 500ms."),
			"dryRun":          boolProperty("Print governance plan without invoking the provider."),
			"allowFailover":   boolProperty("Manual opt-in to retry retryable provider start failures before any stream event is emitted."),
		}, []string{outputText, outputJSON}, []string{"provider dependent; openai-compatible may perform network access; JSON format emits newline-delimited envelopes"}, "read", true, false, []string{"gofly ai stream --prompt 'hello' --provider noop --json"}),
		manifestCommand("ai doctor", nil, "Run AI subsystem self-diagnostics: registered providers, environment variable status, secret resolution, failover configuration and config file status.", "gofly ai doctor [--json]", map[string]aiInputProperty{
			"json": boolProperty("print diagnostic report as JSON"),
		}, []string{outputText, outputJSON}, []string{"none; reads env vars and config file metadata without resolving secret values"}, "read", true, false, []string{"gofly ai doctor", "gofly ai doctor --json"}),
		manifestCommand("feature list", []string{"feature ls"}, "List registered scaffold features using neutral compatibility names.", "gofly feature list [--format text|json] [--json]", nil, []string{outputText, outputJSON}, []string{"none"}, "read", true, false, []string{"gofly feature list --json"}),
		manifestCommand("feature run", nil, "Preview files and data produced by one or more scaffold features without writing them.", "gofly feature run <feature-name> [features...] --name <service> --module <module> --dir <dir> [--style <style>] [--format text|json]", map[string]aiInputProperty{
			"feature": stringProperty("Feature names such as http-compat, rpc-compat or ecosystem-compat."),
			"name":    stringProperty("Service name used for template preview."),
			"module":  stringProperty("Go module path used for template preview."),
			"dir":     stringProperty("Service root directory used for path rendering."),
			"style":   enumStringProperty("Scaffold style.", "minimal", "basic", "production"),
		}, []string{outputText, outputJSON}, []string{"none; preview only"}, "read", true, false, []string{"gofly feature run ecosystem-compat --name hello --module example.com/hello --dir . --format json"}),
		manifestCommand("template list", []string{"template ls"}, "List built-in AI-first project templates and legacy file templates.", "gofly template list [--category <filter>] [--name <filter>] [--format text|json] [--json]", map[string]aiInputProperty{"category": stringProperty("Optional template category, kind, architecture, language or feature filter."), "name": stringProperty("Optional template id/name filter.")}, []string{outputText, outputJSON}, []string{"none"}, "read", true, false, []string{"gofly template list --json"}),
		manifestCommand("template inspect", []string{"template show", "template describe"}, "Inspect one AI-first project template from the catalog.", "gofly template inspect <template-id> [--format text|json] [--json]", map[string]aiInputProperty{"name": stringProperty("Project template id, for example go-rag-service.")}, []string{outputText, outputJSON}, []string{"none"}, "read", true, false, []string{"gofly template inspect go-rag-service --json"}),
		manifestCommand("new service", nil, "Create the golden-path production service scaffold with REST, RPC, OpenAPI, governance, admin control-plane and discovery.", "gofly new service <name> --module <module> [--dir <dir>] [--style production] [--dry-run|--plan]", apiServiceScaffoldProperties(), []string{outputText, outputJSON}, []string{"creates or overwrites files under --dir"}, "medium", true, true, []string{"gofly new service orders --module example.com/orders --dir orders --dry-run"}),
		manifestCommand("new api", []string{"api new"}, "Create an API service scaffold.", "gofly new api <name> --module <module> [--dir <dir>] [--style minimal|basic|production] [--dry-run|--plan]", apiServiceScaffoldProperties(), []string{outputText, outputJSON}, []string{"creates or overwrites files under --dir"}, "medium", true, true, []string{"gofly new api hello --module example.com/hello --dir hello --dry-run"}),
		manifestCommand("new rpc", []string{"rpc new"}, "Create an RPC service scaffold.", "gofly new rpc <name> --module <module> [--dir <dir>] [--style minimal|basic|production] [--profile gofly-ai|gozero-compatible|kitex-compatible] [--dry-run|--plan]", rpcServiceScaffoldProperties(), []string{outputText, outputJSON}, []string{"creates or overwrites files under --dir"}, "medium", true, true, []string{"gofly new rpc greeter --module example.com/greeter --dir greeter --profile kitex-compatible --dry-run"}),
		manifestCommand("api check", []string{"api validate"}, "Validate an .api file.", "gofly api check --api <service.api>", fileInputProperties("api"), []string{outputText}, []string{"reads API definition file"}, "read", true, false, []string{"gofly api check --api user.api"}),
		manifestCommand("api diff", nil, "Compare two .api files for route/type changes.", "gofly api diff --base <old.api> --target <new.api> [--format text|markdown|json]", map[string]aiInputProperty{"base": stringProperty("Old API file."), "target": stringProperty("New API file."), "format": enumStringProperty("Output format.", outputText, "markdown", outputJSON)}, []string{outputText, "markdown", outputJSON}, []string{"reads API definition files"}, "read", true, false, []string{"gofly api diff old.api new.api --format json"}),
		manifestCommand("rpc check", nil, "Validate protobuf syntax and generator support.", "gofly rpc check --src <service.proto>", fileInputProperties("src"), []string{outputText}, []string{"reads protobuf file"}, "read", true, false, []string{"gofly rpc check --src greeter.proto"}),
		manifestCommand("rpc descriptor", nil, "Compare runtime RPC descriptors for compatibility.", "gofly rpc descriptor --base <old-descriptor.json|url> --target <new-descriptor.json|url> [--format text|json]", map[string]aiInputProperty{"base": stringProperty("Old descriptor file, descriptor URL, or admin URL."), "target": stringProperty("New descriptor file or URL."), "format": enumStringProperty("Output format.", outputText, outputJSON)}, []string{outputText, outputJSON}, []string{"reads local files or fetches descriptor URLs when URL inputs are used"}, "medium", true, false, []string{"gofly rpc descriptor --base old.json --target new.json --format json"}),
		manifestCommand("plugin run", nil, "Run a built-in, cached, local or remote generation plugin.", "gofly plugin run <plugin> --name <service> --module <module> --dir <dir> [--dry-run|--plan] [--json]", map[string]aiInputProperty{"plugin": stringProperty("Plugin name, executable path, --remote, or --go-plugin input."), "name": stringProperty("Service name."), "module": stringProperty("Go module path."), "dir": stringProperty("Working/output directory."), "dryRun": boolProperty("Print a plan without downloading, executing plugins, writing files, or applying patches.")}, []string{outputText, outputJSON}, []string{"may execute external plugin processes", "may download remote plugins", "may write files under --dir"}, "high", true, true, []string{"gofly plugin run ./my-plugin --name hello --module example.com/hello --dir . --dry-run --json"}),
		manifestCommand("config init", nil, "Create .gofly/config.json.", "gofly config init --dir <service-dir> [--name <service>] [--module <module>] [--dry-run|--plan]", map[string]aiInputProperty{"dir": stringProperty("Service root directory."), "name": stringProperty("Service name."), "module": stringProperty("Go module path."), "dryRun": boolProperty("Print a plan without writing config.")}, []string{outputText, outputJSON}, []string{"writes .gofly/config.json under --dir"}, "low", true, true, []string{"gofly config init --dir . --name hello --module example.com/hello --dry-run"}),
		manifestCommand("config set", nil, "Update one value in .gofly/config.json.", "gofly config set <key> <value> --dir <service-dir> [--dry-run|--plan]", map[string]aiInputProperty{"key": stringProperty("Config key."), "value": stringProperty("Config value."), "dir": stringProperty("Service root directory."), "dryRun": boolProperty("Print a plan without writing config.")}, []string{outputText, outputJSON}, []string{"writes .gofly/config.json under --dir"}, "low", true, true, []string{"gofly config set style production --dir . --dry-run"}),
		manifestCommand("config clean", nil, "Remove .gofly/config.json if it exists.", "gofly config clean --dir <service-dir> [--dry-run|--plan]", map[string]aiInputProperty{"dir": stringProperty("Service root directory."), "dryRun": boolProperty("Print a plan without removing config.")}, []string{outputText, outputJSON}, []string{"removes .gofly/config.json under --dir"}, "medium", true, true, []string{"gofly config clean --dir . --dry-run"}),
	}
	for i := range commands {
		switch commands[i].Name {
		case "ai manifest":
			commands[i].OutputContract = aiManifestOutputContract()
		case "ai control-plane":
			commands[i].OutputContract = aiControlPlaneOutputContract()
		case "ai plan":
			commands[i].OutputContract = aiProjectPlanOutputContract()
		case "ai new":
			commands[i].OutputContract = aiProjectApplyOutputContract()
		case "ai complete":
			commands[i].OutputContract = aiCompleteOutputContract()
		case "ai stream":
			commands[i].OutputContract = aiStreamOutputContract("ai.stream")
		case "ai doctor":
			commands[i].OutputContract = aiDoctorOutputContract()
		}
	}

	for _, spec := range rootCommandManifestEntries() {
		commands = append(commands, manifestCommand(spec.Name, spec.Aliases, spec.Short, "gofly "+spec.Name+" [arguments]", nil, []string{outputText, outputJSON}, []string{"see command-specific manifest entries and help"}, inferTopLevelRisk(spec.Name), false, topLevelMayMutate(spec.Name), nil))
	}
	sort.SliceStable(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	registry := llm.NewDefaultProviderRegistry()
	return aiToolManifest{
		SchemaVersion: aiToolManifestSchemaVersion,
		Tool:          "gofly",
		Version:       Version,
		Description:   "Machine-readable command manifest for LLM/Agent callers. Prefer --output json or command --json flags and inspect the JSON envelope before acting on results.",
		Invocation:    "gofly <command> [arguments]",
		Docs:          buildAIManifestDocs(),
		Examples:      buildAIManifestExamples(),
		VerifyCommands: []string{
			"make docs-check",
			"make examples-smoke",
			"make test-generated-matrix",
			"make doc-manifest-sync-check",
		},
		Output: aiOutputSchema{
			Mode:        "json-envelope when --output json, --json or --format json is used",
			Envelope:    []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
			ErrorFields: []string{"code", "message", "retryable", "remediation", "details"},
		},
		ControlPlane: buildAIControlPlaneManifest(),
		LLMGovernance: aiLLMGovernance{
			Package:                "github.com/imajinyun/gofly/core/llm",
			Capabilities:           []string{"provider abstraction", "provider registry", "capability manifest", "provider plugin manifest contract", "model-level capability negotiation", "environment-only secret resolver", "token budget", "rate limiting", "prompt and metadata redaction", "structured audit logging", "stream event size limits", "no-op provider", "response caching", "cost-aware token accounting", "low-cardinality observability", "governance pipeline"},
			Resilience:             []string{"circuit breaker", "provider failover", "manual provider failover", "bounded provider responses", "context cancellation", "timeout propagation", "retryability classification", "low-cardinality error classes", "provider status code capture", "HTTP status class classification", "request coalescing"},
			ProviderPluginContract: buildAIProviderPluginContract(),
			TokenBudgetPolicy:      buildAITokenBudgetPolicy(),
			RateLimitPolicy: aiRateLimitPolicy{
				DefaultRate:  0,
				DefaultBurst: 0,
				EnvVarRate:   "GOFLY_LLM_RATE_LIMIT",
				EnvVarBurst:  "GOFLY_LLM_RATE_BURST",
				Strategy:     "token-bucket",
				Consequence:  "requests exceeding the rate limit receive ErrRateLimited and are not forwarded to the provider; configurable per invocation via --rate-limit and --rate-burst flags",
				Configurable: true,
				Scope:        "per-governed-provider-instance; each NewGovernedProvider call creates an independent token bucket",
			},
			OutputContractPolicy: buildAIOutputContractPolicy(),
			ErrorContractPolicy:  buildAIErrorContractPolicy(),
			DataSafetyPolicy:     buildAIDataSafetyPolicy(),
			ToolCallPolicy:       buildAIToolCallPolicy(),
			FailoverPolicy:       buildAIFailoverPolicy(registry),
			ResponseCachePolicy:  buildAIResponseCachePolicy(),
			ObservabilityPolicy:  buildAIObservabilityPolicy(),
			CostPolicy:           buildAICostPolicy(),
			GovernancePipeline:   buildAIGovernancePipeline(),
			AuditFields:          []string{"operation", "provider", "model", "status", "duration", "input_tokens", "output_tokens", "total_tokens", "metadata", "error", "error_class", "retryable", "provider_status_code", "stream_events", "trace_id", "request_id"},
			TelemetryFields:      aiLLMTelemetryFields(),
			DefaultMode:          "redact prompts and metadata before provider calls; never audit raw prompts or completions",
			Providers:            registry.Specs(),
		},
		FeatureLibrary: buildAIFeatureLibraryManifest(),
		Commands:       commands,
	}
}

func buildAIFeatureLibraryManifest() aiFeatureLibraryManifest {
	plugins := generator.ListProjectFeaturePluginContracts()
	return aiFeatureLibraryManifest{
		Mode:                 "deterministic built-in project feature plugins selected from project template feature tags",
		Deterministic:        true,
		AppliesUnderDirOnly:  true,
		DependencyPolicy:     "feature dependencies are reported in ai new apply results and nextActions for explicit review; they are not automatically added to the root module or generated go.mod",
		Features:             aiProjectFeatureNames(plugins),
		Templates:            aiProjectTemplateIDs(),
		VerifyAllowlist:      generator.ProjectFeatureVerifyAllowlist(),
		TemplateVerification: buildAITemplateVerificationContract(),
		ResultFields:         []string{"generatedFeatures", "dependencies", "configHints", "featureVerify", "verify", "nextActions"},
		Plugins:              plugins,
	}
}

func buildAIManifestDocs() []aiManifestLink {
	return []aiManifestLink{
		{Title: "AI manifest", Path: "docs/concepts/ai-manifest.md"},
		{Title: "CLI JSON contracts", Path: "docs/reference/cli-json-contracts.md"},
		{Title: "Stable API surface", Path: "docs/reference/api-surface.md"},
		{Title: "Compatibility policy", Path: "docs/reference/compatibility.md"},
		{Title: "P1 growth roadmap", Path: "docs/reference/p1-growth-roadmap.md"},
	}
}

func buildAIManifestExamples() []aiManifestLink {
	return []aiManifestLink{
		{Title: "Examples catalog", Path: "examples/README.md"},
		{Title: "AI governed service", Path: "examples/ai-governed-service/README.md"},
		{Title: "Microshop", Path: "examples/microshop/README.md"},
		{Title: "Production orders", Path: "examples/production-orders/README.md"},
	}
}

func aiProjectFeatureNames(plugins []generator.ProjectFeaturePluginContract) []string {
	names := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		names = append(names, plugin.Name)
	}
	sort.Strings(names)
	return names
}

func aiProjectTemplateIDs() []string {
	templates := generator.ListProjectTemplates()
	ids := make([]string, 0, len(templates))
	for _, tmpl := range templates {
		ids = append(ids, tmpl.ID)
	}
	sort.Strings(ids)
	return ids
}

func buildAIControlPlaneManifest() aiControlPlaneManifest {
	snapshot := defaultAIControlPlaneSnapshot().WithChecksum()
	return aiControlPlaneManifest{
		Package:          "github.com/imajinyun/gofly/core/controlplane",
		Purpose:          "versioned control-plane snapshots for AI agents to reason about runtime config, service discovery, governance policy, gateway routing, LLM and tool capabilities before acting",
		SnapshotVersion:  snapshot.Version,
		SnapshotChecksum: snapshot.Checksum,
		SchemaID:         aiControlPlaneSchemaID,
		SchemaCommand:    "gofly ai control-plane --schema jsonschema",
		SchemaChecksum:   aiControlPlaneJSONSchemaChecksum(),
		ProviderContract: []string{"Load(context.Context) (Snapshot, error)", "Watch(context.Context) (<-chan SnapshotEvent, error)", "Source() string when implemented"},
		SnapshotFields:   []string{"version", "checksum", "services", "configs", "policies", "updatedAt", "metadata"},
		EventFields:      []string{"snapshot", "source", "diff", "consumerAction", "error"},
		Capabilities: []string{
			"stable checksum independent of service ordering and updatedAt",
			"previous snapshot JSON decoding from raw snapshot, snapshot wrapper or ai control-plane envelope",
			"static provider for deterministic tests and local tools",
			"composite runtime provider for config, service discovery, governance policy and capability contributors",
			"runtime adapters for discovery snapshots, governance rule sets, raw JSON configs and capability metadata",
			"rpc policy runtime enforcement for client timeout, retry backoff with context cancellation, circuit breaker gates, balancer selection, load shedding, fallback and hedging",
			"control-plane contributor for rpc policy runtime state, cache counts and enforcement capabilities",
			"native REST admin control-plane endpoint with pluggable runtime contributors and sanitized REST runtime snapshots",
			"control-plane contributor for REST governance runtime cache counts across rate limiters, concurrency limiters and breakers",
			"generated project control-plane contributors for scaffold contract, sanitized runtime config and governance policy snapshots",
			"ai new --apply --verify runs generated project control-plane snapshot assertions when the scaffold exposes a snapshot contract test",
			"watch stream with context cancellation",
			"deduplicated snapshot events by checksum while preserving error events",
			"semantic diff classification mapped to stable consumer action policy",
			"consumer action dispatcher for runtime config planner, routing model, governance gates and capability cache refresh hooks",
		},
		ConsumerActions: controlplane.DefaultConsumerActions(),
		Determinism:     "StableChecksum canonicalizes services/endpoints/configs/metadata and excludes updatedAt so agents can detect semantic changes instead of timestamp churn",
		SecretBoundary:  "snapshots expose config metadata and raw JSON config blobs only from explicit providers; secret values must stay in environment-only resolvers and must not be copied into metadata",
		AgentGuidance: []string{
			"load one snapshot before mutating generated project configuration",
			"for generated projects, compare generated.* config blobs with scaffold artifacts and governance rules before rewriting code or policy files",
			"compare checksum before applying repeated governance or routing actions",
			"use consumerAction.action and consumerAction.scopes to narrow cache invalidation or choose full reconciliation",
			"treat SnapshotEvent.error as non-cacheable and actionable even when checksum is unchanged",
			"do not infer secret values from config metadata or provider names",
		},
		DefaultMetadata: snapshot.Metadata,
	}
}

func defaultAIControlPlaneSnapshot() controlplane.Snapshot {
	return controlplane.Snapshot{
		Version: controlplane.DefaultSnapshotVersion,
		Metadata: map[string]string{
			"config":                                "available",
			"controlplane.provider.composite":       "available",
			"discovery":                             "available",
			"governance":                            "available",
			"gateway":                               "planned",
			"rest.runtime":                          "available",
			"rest.governance.runtime":               "available",
			"llm":                                   "available",
			"tool":                                  "available",
			"generated.project.contract":            "available",
			"generated.project.verify.controlplane": "available",
		},
	}
}

func buildAITemplateVerificationContract() aiTemplateVerificationContract {
	templates := generator.ListProjectTemplates()
	validated := make([]string, 0, len(templates))
	for _, tmpl := range templates {
		if tmpl.VerifyE2EValidated {
			validated = append(validated, tmpl.ID)
		}
	}
	return aiTemplateVerificationContract{
		CatalogField:       "verifyE2EValidated",
		MatrixTarget:       "make test-generated-matrix",
		GovernanceRound:    "generated project verification matrix",
		CIRequired:         true,
		ZeroSkipRequired:   true,
		ValidatedTemplates: validated,
	}
}

func buildAITokenBudgetPolicy() aiTokenBudgetPolicy {
	return aiTokenBudgetPolicy{
		DefaultMaxInputTokens:  0,
		DefaultMaxOutputTokens: 0,
		DefaultMaxTotalTokens:  0,
		Configurable:           true,
		CLIFlags:               []string{"--max-input-tokens", "--max-output-tokens", "--max-total-tokens"},
		EnvVars:                []string{"GOFLY_LLM_MAX_INPUT_TOKENS", "GOFLY_LLM_MAX_OUTPUT_TOKENS", "GOFLY_LLM_MAX_TOTAL_TOKENS"},
		Enforcement:            "requests exceeding configured token budgets return ErrTokenBudgetExceeded before additional provider work is accepted",
		DeductionPoint:         "token usage is checked by the governed provider after provider usage accounting; dry-run only discloses configured limits",
		FailoverBudgetSharing:  "manual failover attempts share the same TokenBudget instance and idempotency key",
		StreamAccounting:       "streaming responses account provider-emitted usage snapshots; missing usage is represented as zero-valued usage and does not fabricate token counts",
		RejectionCode:          "token_budget_exceeded",
	}
}

func buildAIProviderPluginContract() aiProviderPluginContract {
	return aiProviderPluginContract{
		SchemaVersion:  llm.ProviderPluginManifestSchemaVersion,
		RequiredFields: []string{"schemaVersion", "provider.name", "provider.capabilities", "provider.requiresSecrets", "provider.secretEnvVars", "provider.configEnvVars", "models[].name", "models[].capabilities"},
		SafeFields:     []string{"provider display/default metadata", "environment variable names", "network access boolean", "provider-level capabilities", "model-level capabilities", "embedding dimensions"},
		SecretBoundary: "provider plugin manifests must disclose only secret environment variable names; secret values are resolved at build time from environment-backed SecretResolver",
	}
}

func buildAIOutputContractPolicy() aiOutputContractPolicy {
	return aiOutputContractPolicy{
		EnvelopeFields:          []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		ErrorFields:             []string{"code", "message", "retryable", "remediation", "details"},
		NextActions:             true,
		JSONMode:                "prefer --json, --output json or --format json for machine-readable calls; text output is human-oriented",
		SchemaValidation:        "manifest declares stable envelope and command output fields; command-specific JSON should be inspected before side effects",
		RetryableErrorSemantics: "retryable errors include low-cardinality classes and nextActions; non-retryable errors should not be retried without user/config changes",
		StreamSemantics:         "stream JSON output is newline-delimited JSON; each line is an independently parseable envelope",
		PartialFailureSemantics: "stream errors are emitted as a final error envelope when possible; failover is limited to failures before any stream event is emitted",
	}
}

func buildAIErrorContractPolicy() aiErrorContractPolicy {
	return aiErrorContractPolicy{
		CodeFormat: "UPPER_SNAKE_CASE stable JSON error.code values; callers must treat unknown future codes as non-retryable unless retryable is true",
		StableCodes: []string{
			"COMMAND_ERROR",
			"USAGE_ERROR",
			"LLM_TOKEN_BUDGET_EXCEEDED",
			"LLM_RATE_LIMITED",
			"LLM_PROVIDER_NOT_FOUND",
			"LLM_PROVIDER_SECRET_MISSING",
			"LLM_PROVIDER_ENDPOINT_REJECTED",
			"LLM_PROVIDER_CONFIG_INVALID",
			"LLM_PROVIDER_CAPABILITY_UNSUPPORTED",
			"LLM_PROVIDER_REQUEST_FAILED",
			"LLM_PROVIDER_RESPONSE_TOO_LARGE",
			"LLM_PROVIDER_ALREADY_REGISTERED",
		},
		RetryableCodes:    []string{"LLM_RATE_LIMITED", "LLM_PROVIDER_REQUEST_FAILED"},
		NonRetryableCodes: []string{"USAGE_ERROR", "LLM_TOKEN_BUDGET_EXCEEDED", "LLM_PROVIDER_NOT_FOUND", "LLM_PROVIDER_SECRET_MISSING", "LLM_PROVIDER_ENDPOINT_REJECTED", "LLM_PROVIDER_CONFIG_INVALID", "LLM_PROVIDER_CAPABILITY_UNSUPPORTED", "LLM_PROVIDER_RESPONSE_TOO_LARGE", "LLM_PROVIDER_ALREADY_REGISTERED"},
		ProviderStatusClasses: []string{
			"auth",
			"rate_limit",
			"client",
			"server",
			"unknown",
		},
		NextActionTypes:         []string{"retry", "run_doctor", "set_env", "choose_provider", "choose_model", "enable_failover", "reduce_prompt", "increase_budget", "inspect_manifest"},
		EnvelopePlacement:       "error details are duplicated only as structured error and top-level nextActions; command data remains omitted on failure",
		DetailsPolicy:           "details must stay low-cardinality and may include provider, statusCode and statusClass; raw provider bodies, prompts, completions and secret values are omitted",
		RetryableSemantics:      "retryable=true means the same command may be retried after waiting or resolving provider availability; retryable=false requires user/config/model changes first",
		ProviderFailureGuidance: "retryable provider request failures include nextActions for retry, optional manual failover and manifest inspection",
	}
}

func buildAIDataSafetyPolicy() aiDataSafetyPolicy {
	return aiDataSafetyPolicy{
		SecretResolution:    "environment-only SecretResolver; manifests disclose secret environment variable names but never values",
		Redaction:           "prompts and metadata are redacted before provider calls and audit logging",
		PromptLogging:       "disabled-by-default",
		ResponseLogging:     "disabled-by-default",
		MetadataLogging:     "redacted",
		SecretValueLogging:  "forbidden",
		SensitiveEnvVarMode: "presence/status only; values are never emitted by manifest or doctor output",
		AuditBoundary:       "audit records low-cardinality operational fields, token usage, status, retryability and provider attribution without raw prompt/completion content",
		SafeToExpose:        []string{"provider names", "model names", "capability names", "environment variable names", "token budget limits", "rate limit settings", "provider status classes"},
	}
}

func buildAIToolCallPolicy() aiToolCallPolicy {
	return aiToolCallPolicy{
		DefaultMode:                     "disabled-unless-model-and-command-contract-explicitly-enable-tool-call",
		RequiresModelCapability:         "tool-call",
		AllowedByDefault:                []string{},
		SideEffectToolsRequireApproval:  true,
		ArgumentSchemaValidation:        true,
		DryRunRequiredForMutation:       true,
		AuditToolArguments:              "redacted",
		RejectedToolCallCode:            "tool_call_rejected",
		UnsupportedCapabilityResolution: "select a model from eligibleToolCallSpecs or rerun without tool calling",
	}
}

func buildAIFailoverPolicy(registry *llm.ProviderRegistry) aiFailoverPolicy {
	configured := parseAIProviderList(os.Getenv("GOFLY_LLM_FAILOVER_PROVIDERS"))
	valid := make([]string, 0, len(configured))
	invalid := make([]string, 0)
	configuredSpecs := make([]llm.ProviderSpec, 0, len(configured))
	seen := map[string]struct{}{}
	for _, provider := range configured {
		name := strings.ToLower(strings.TrimSpace(provider))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		spec, ok := registry.Spec(name)
		if !ok {
			invalid = append(invalid, name)
			continue
		}
		valid = append(valid, name)
		configuredSpecs = append(configuredSpecs, spec)
	}
	return aiFailoverPolicy{
		EnvVar:                "GOFLY_LLM_FAILOVER_PROVIDERS",
		Mode:                  aiFailoverMode(valid, false),
		AutomaticSwitching:    false,
		ManualOptInFlags:      []string{"--allow-failover", "--failover"},
		ExecutionGuardrails:   []string{"manual opt-in is required", "only retryable provider failures are eligible", "failover attempts share the same token budget", "attempt metadata includes a stable idempotency key", "stream failover is limited to pre-event provider start failures"},
		ConfiguredProviders:   valid,
		InvalidProviders:      invalid,
		ConfiguredSpecs:       configuredSpecs,
		EligibleCompleteSpecs: registry.SpecsWithCapability("complete"),
		EligibleStreamSpecs:   registry.SpecsWithCapability("stream"),
		EligibleJSONModeSpecs: registry.SpecsWithModelCapability("json-mode"),
		EligibleToolCallSpecs: registry.SpecsWithModelCapability("tool-call"),
	}
}

func buildAIResponseCachePolicy() aiResponseCachePolicy {
	return aiResponseCachePolicy{
		DefaultTTL:         "5m",
		DefaultMaxSize:     256,
		CacheKeyComponents: []string{"provider", "model", "prompt", "maxOutputTokens"},
		Hash:               "SHA-256",
		Coalescing:         "request-level; concurrent requests for the same cache key share one inflight provider call",
		Observable:         true,
		CacheScope:         "in-process memory; per-CachingProvider instance",
		CacheUnsupported:   []string{"stream", "embed"},
	}
}

func buildAIObservabilityPolicy() aiObservabilityPolicy {
	return aiObservabilityPolicy{
		Signals:                []string{"structured audit log", "JSON envelope", "stream event envelope", "doctor remediation report"},
		LowCardinalityFields:   []string{"operation", "provider", "model", "status", "error_class", "retryable", "provider_status_class", "provider_status_code", "cache_status", "failover_enabled"},
		ForbiddenFields:        []string{"prompt", "completion", "messages[].content", "metadata raw values", "secret values", "authorization headers", "provider response body"},
		CorrelationFields:      []string{"trace_id", "request_id", "idempotency_key"},
		MetricFieldGuidance:    "emit counters and histograms using only low-cardinality labels; never label metrics with raw prompts, user input, URLs, headers or secret values",
		TraceFieldGuidance:     "trace attributes may include provider/model/status and token counts; raw prompt/completion content stays outside traces",
		AuditCorrelation:       "audit records and JSON envelopes share provider, model, usage, error_class, retryable, provider_status_code, trace_id and request_id fields when available",
		RedactionBoundary:      "redaction occurs before provider calls, audit logging, doctor output and manifest-safe metadata exposure",
		CardinalityGuardrails:  "provider status is reduced to stable classes for automation; high-cardinality details belong in redacted diagnostics, not metrics labels",
		ProviderStatusGuidance: "provider_status_code is optional and bounded to numeric/status class diagnostics; callers should aggregate by provider_status_class or error_class first",
	}
}

func buildAICostPolicy() aiCostPolicy {
	return aiCostPolicy{
		AccountingFields:   []string{"input_tokens", "output_tokens", "total_tokens", "cache_status", "provider", "model", "operation", "failover_attempt"},
		BudgetFields:       []string{"max_input_tokens", "max_output_tokens", "max_total_tokens", "used_input", "used_output", "used_total", "remain_total"},
		CurrencyMode:       "disabled-by-default; manifest exposes token accounting but does not invent currency estimates without an explicit pricing table",
		PricingSource:      "provider/model pricing must come from an operator-maintained table outside secret/config values; unknown prices are reported as unpriced",
		CostDisclosure:     "JSON outputs expose token usage and budget snapshots so agents can estimate cost externally before retrying or expanding prompts",
		FailoverDisclosure: "manual failover records provider/model and shared budget usage per attempt; agents should account fallback attempts as additive token/cost risk",
		CacheAccounting:    "cache hits avoid provider calls but still disclose cached usage; cache_status should be used to distinguish provider spend from served-from-cache responses",
		AgentGuidance: []string{
			"inspect token usage before retrying retryable errors",
			"prefer smaller prompts or explicit max-total-tokens when budget is unknown",
			"treat failover as potentially additional provider spend unless the failed attempt returned zero usage",
			"do not fabricate currency costs for unpriced providers",
		},
		UnpricedProviderPolicy: "token counts remain authoritative; currency fields should be omitted or marked unpriced until an explicit pricing source is configured",
	}
}

// buildAIGovernancePipeline returns the ordered 9-stage pipeline that every
// governed LLM call passes through. Optional stages are elided at runtime
// when their preconditions are not met.
func buildAIGovernancePipeline() []aiPipelineStage {
	return []aiPipelineStage{
		{Stage: "request-redaction", Description: "redact secrets and sensitive metadata from the incoming prompt before any further processing", Optional: true},
		{Stage: "rate-limit", Description: "token-bucket rate limiting; rejected with ErrRateLimited if the bucket is empty", Optional: true},
		{Stage: "token-budget", Description: "check cumulative token budget; rejected with token_budget_exceeded if over limit", Optional: true},
		{Stage: "response-cache", Description: "look up response cache; skip provider call and use cached response on hit; coalesce concurrent misses", Optional: true},
		{Stage: "circuit-breaker", Description: "reject call immediately if the provider circuit is open; allow probe requests for half-open recovery", Optional: false},
		{Stage: "provider-call", Description: "forward the request to the LLM provider with configured timeout, retry and failover wrappers", Optional: false},
		{Stage: "usage-accounting", Description: "record token usage (input, output, total) against the token budget and emit usage deltas", Optional: false},
		{Stage: "audit-log", Description: "emit a structured audit log entry with operation, provider, model, status, duration, tokens and error metadata", Optional: false},
		{Stage: "telemetry-emit", Description: "emit low-cardinality metrics and trace fields for observability pipeline; cache_status, error_class, provider_status_code", Optional: false},
	}
}

func rootCommandManifestEntries() []commandSpec {
	return []commandSpec{
		{Name: "version", Short: "Print version metadata."},
		{Name: "new", Short: "Scaffold new production, API, or RPC services."},
		{Name: "gen", Aliases: []string{"generate"}, Short: "Run unified code generators."},
		{Name: "handler", Short: "Generate or complete API handlers."},
		{Name: "rpc", Short: "Generate and validate RPC services."},
		{Name: "api", Short: "Generate and manage API definition files."},
		{Name: "model", Short: "Generate model repositories."},
		{Name: "docker", Short: "Generate Dockerfile assets."},
		{Name: "kube", Short: "Generate Kubernetes manifests."},
		{Name: "template", Short: "Manage local or remote generation templates."},
		{Name: "env", Short: "Inspect local toolchain environment."},
		{Name: "completion", Short: "Emit shell completion scripts."},
		{Name: "quickstart", Short: "Create runnable services quickly."},
		{Name: "migrate", Aliases: []string{"migration"}, Short: "Create SQL migration files."},
		{Name: "bug", Short: "Print diagnostic bug reports."},
		{Name: "upgrade", Short: "Print or run upgrade commands."},
		{Name: "config", Short: "Manage .gofly configuration."},
		{Name: "feature", Short: "List or preview scaffold features."},
		{Name: "plugin", Short: "List, install or run generation plugins."},
		{Name: "complete", Short: "Emit legacy completion scripts."},
		{Name: "release", Short: "Run release readiness checks."},
		{Name: "doctor", Short: "Diagnose local environment readiness."},
		{Name: "example", Aliases: []string{"examples"}, Short: "List or run built-in examples."},
		{Name: "ai", Aliases: []string{"tools"}, Short: "Emit machine-readable tool metadata for AI agents."},
	}
}

func aiManifestOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope by default; text summary only when --format text is explicitly requested",
		Envelope: []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		EventFields: []string{
			"schemaVersion", "tool", "version", "description", "invocation", "output", "controlPlane", "llmGovernance", "featureLibrary", "commands",
		},
		Semantics: map[string]string{
			"command": "ai.manifest",
			"schema":  "--schema jsonschema emits the JSON Schema contract for the manifest envelope data",
			"secrets": "provider secret values are never included; only secret environment variable names and resolution policy are exposed",
		},
	}
}

func aiControlPlaneOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json, --output json or --format json is used; newline-delimited JSON envelopes when --watch is used with JSON output; deterministic text snapshot otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		EventFields: []string{
			"source", "snapshot", "diff", "consumerAction", "agentGuidance", "secretBoundary", "index", "error",
			"snapshot.version", "snapshot.checksum", "snapshot.services", "snapshot.configs", "snapshot.policies", "snapshot.metadata",
			"diff.fromChecksum", "diff.toChecksum", "diff.changed", "diff.changeType", "diff.changedFields",
			"consumerAction.changeType", "consumerAction.action", "consumerAction.reason", "consumerAction.scopes", "consumerAction.requiresFullReconcile", "consumerAction.nextActions",
		},
		Semantics: map[string]string{
			"command":        "ai.control_plane",
			"watchCommand":   "ai.control_plane.event",
			"schema":         "--schema jsonschema emits the JSON Schema contract for snapshot, diff, consumerAction and watch event data",
			"watch":          "--watch emits a bounded event stream terminated by --max-events or --timeout; each JSON line is independently parseable",
			"diff":           "diff reports checksum equality for --from-checksum and semantic changedFields when both snapshots are available via --from-snapshot",
			"baseline":       "--from-snapshot accepts a raw Snapshot JSON object, a {snapshot:...} wrapper or a previous ai.control_plane envelope with data.snapshot; --from-checksum and --from-snapshot are mutually exclusive",
			"consumerAction": "consumerAction maps diff.changeType to a stable agent policy such as skip, load-baseline, refresh-routing-model, reload-governance-gates or full-reconcile",
			"determinism":    "snapshot checksum is stable across ordering and timestamp changes and excludes secret values",
			"secrets":        "control-plane output exposes capability metadata and secret boundaries only; secret values are never printed",
		},
	}
}

func buildAIControlPlaneJSONSchema() map[string]any {
	schema := buildAIControlPlaneJSONSchemaData()
	schema["xSchemaChecksum"] = stableJSONChecksum(schema)
	return schema
}

func aiControlPlaneJSONSchemaChecksum() string {
	return stableJSONChecksum(buildAIControlPlaneJSONSchemaData())
}

func stableJSONChecksum(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func buildAIControlPlaneJSONSchemaData() map[string]any {
	stringArraySchema := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	stringMapSchema := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
	rawConfigMapSchema := map[string]any{"type": "object", "additionalProperties": true}
	endpointSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"address"},
		"additionalProperties": false,
		"properties": map[string]any{
			"address":  map[string]any{"type": "string"},
			"weight":   map[string]any{"type": "integer"},
			"zone":     map[string]any{"type": "string"},
			"metadata": stringMapSchema,
		},
	}
	serviceSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"name"},
		"additionalProperties": false,
		"properties": map[string]any{
			"name":      map[string]any{"type": "string"},
			"endpoints": map[string]any{"type": "array", "items": endpointSchema},
			"metadata":  stringMapSchema,
		},
	}
	policySchema := map[string]any{"type": "object", "additionalProperties": true}
	snapshotSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"version", "checksum"},
		"additionalProperties": false,
		"properties": map[string]any{
			"version":   map[string]any{"type": "string"},
			"checksum":  map[string]any{"type": "string"},
			"services":  map[string]any{"type": "array", "items": serviceSchema},
			"configs":   rawConfigMapSchema,
			"policies":  map[string]any{"type": "array", "items": policySchema},
			"updatedAt": map[string]any{"type": "string", "format": "date-time"},
			"metadata":  stringMapSchema,
		},
	}
	diffSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"changed", "changeType"},
		"additionalProperties": false,
		"properties": map[string]any{
			"fromChecksum":  map[string]any{"type": "string"},
			"toChecksum":    map[string]any{"type": "string"},
			"changed":       map[string]any{"type": "boolean"},
			"changeType":    map[string]any{"type": "string", "enum": []string{"none", "initial-snapshot", "checksum-mismatch", "version-change", "service-discovery-change", "config-change", "policy-change", "metadata-change", "mixed-change", "checksum-change"}},
			"changedFields": stringArraySchema,
		},
	}
	consumerActionSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"changeType", "action", "reason", "requiresFullReconcile"},
		"additionalProperties": false,
		"properties": map[string]any{
			"changeType":            map[string]any{"type": "string"},
			"action":                map[string]any{"type": "string", "enum": []string{"skip", "load-baseline", "inspect-snapshot", "refresh-config-planner", "refresh-routing-model", "reload-governance-gates", "refresh-capability-cache", "full-reconcile"}},
			"reason":                map[string]any{"type": "string"},
			"scopes":                stringArraySchema,
			"requiresFullReconcile": map[string]any{"type": "boolean"},
			"nextActions":           stringArraySchema,
		},
	}
	snapshotResultSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"source", "snapshot", "diff", "consumerAction", "agentGuidance", "secretBoundary"},
		"additionalProperties": false,
		"properties": map[string]any{
			"source":         map[string]any{"type": "string"},
			"snapshot":       snapshotSchema,
			"diff":           diffSchema,
			"consumerAction": consumerActionSchema,
			"agentGuidance":  stringArraySchema,
			"secretBoundary": map[string]any{"type": "string"},
		},
	}
	watchEventSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"index", "diff", "consumerAction"},
		"additionalProperties": false,
		"properties": map[string]any{
			"index":          map[string]any{"type": "integer", "minimum": 0},
			"source":         map[string]any{"type": "string"},
			"snapshot":       snapshotSchema,
			"diff":           diffSchema,
			"consumerAction": consumerActionSchema,
			"error":          map[string]any{"type": "string"},
			"secretBoundary": map[string]any{"type": "string"},
		},
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  aiControlPlaneSchemaID,
		"title":                "gofly AI control-plane contract",
		"type":                 "object",
		"required":             []string{"snapshot", "diff", "consumerAction", "snapshotResult", "watchEvent"},
		"additionalProperties": false,
		"properties": map[string]any{
			"snapshot":       snapshotSchema,
			"diff":           diffSchema,
			"consumerAction": consumerActionSchema,
			"snapshotResult": snapshotResultSchema,
			"watchEvent":     watchEventSchema,
		},
	}
}

func aiProjectPlanOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json, --output json or --format json is used; deterministic text plan otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		EventFields: []string{
			"prompt", "projectType", "template", "features", "command", "riskLevel", "mutatesFilesystem", "dryRun", "verify", "warnings", "nextActions",
		},
		Semantics: map[string]string{
			"command":            "ai.plan",
			"determinism":        "uses deterministic local template matching and does not call an external LLM provider",
			"filesystemMutation": "never writes files; mutatesFilesystem is always false and dryRun is always true",
		},
	}
}

func aiProjectApplyOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json, --output json or --format json is used; deterministic text plan/apply summary otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		EventFields: []string{
			"plan", "applied", "outputDir", "executedCommand", "generatedFeatures", "dependencies", "configHints", "featureVerify", "verify", "verifyRan", "verifyPassed", "verification", "warnings", "nextActions", "mutatesFilesystem",
		},
		Semantics: map[string]string{
			"command":            "ai.new",
			"dryRunDefault":      "prints the selected scaffold plan without writing files unless --apply is set or --dry-run=false is explicitly used",
			"filesystemMutation": "writes scaffold files only under the validated --dir boundary when apply mode is enabled",
			"verification":       "--verify runs allowlisted local commands under --dir and reports every command result",
		},
	}
}

func aiCompleteOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:        "single JSON envelope for normal completion; newline-delimited JSON envelopes when --stream is set with JSON output",
		Envelope:    []string{"ok", "command", "version", "data", "error"},
		EventFields: []string{"provider", "model", "text", "usage", "budget", "governance"},
		Semantics: map[string]string{
			"stream": "when --stream is set, use the ai stream output contract with command ai.complete",
		},
	}
}

func aiLLMTelemetryFields() []string {
	return []string{"operation", "provider", "model", "status", "error_class", "retryable", "provider_status_code", "provider_status_class", "stream_events", "cache_status", "input_tokens", "output_tokens", "total_tokens"}
}

func aiStreamOutputContract(command string) *aiOutputContract {
	return &aiOutputContract{
		Mode:        "newline-delimited JSON; each line is one JSON envelope and is independently parseable",
		Envelope:    []string{"ok", "command", "version", "data", "error"},
		EventFields: []string{"provider", "model", "index", "delta", "done", "usage", "budget", "governance"},
		Semantics: map[string]string{
			"command": command,
			"delta":   "incremental text chunk; may be empty for usage or done events",
			"done":    "true only on stream termination events emitted by the provider/governance layer",
			"usage":   "token usage snapshot when the provider emits usage; omitted or zero-valued otherwise",
			"error":   "stream errors are emitted as a final error envelope in JSON stream mode before command failure when possible",
		},
	}
}

func aiDoctorOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json or --output json is used; human-readable diagnostic report otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		EventFields: []string{
			"version", "providers", "envVars", "secrets", "failover", "config", "cache", "telemetry", "cost", "summary",
		},
		Semantics: map[string]string{
			"command": "ai.doctor",
			"secrets": "reports secret presence and remediation without printing secret values",
			"status":  "diagnostic item status is one of ok, warn, fail or info; severity is present for actionable warnings/failures",
		},
	}
}

func buildAIToolManifestJSONSchema() map[string]any {
	stringArraySchema := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	linkSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"title", "path"},
		"additionalProperties": false,
		"properties": map[string]any{
			"title": map[string]any{"type": "string"},
			"path":  map[string]any{"type": "string"},
		},
	}
	linkArraySchema := map[string]any{"type": "array", "items": linkSchema}
	propertySchema := map[string]any{
		"type":                 "object",
		"required":             []string{"type", "description"},
		"additionalProperties": false,
		"properties": map[string]any{
			"type":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"enum":        stringArraySchema,
		},
	}
	outputContractSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"mode", "envelope"},
		"additionalProperties": false,
		"properties": map[string]any{
			"mode":        map[string]any{"type": "string"},
			"envelope":    stringArraySchema,
			"eventFields": stringArraySchema,
			"semantics":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		},
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://gofly.dev/schemas/ai-tool-manifest.schema.json",
		"title":                "gofly AI tool manifest",
		"type":                 "object",
		"required":             []string{"schemaVersion", "tool", "version", "description", "invocation", "docs", "examples", "verifyCommands", "output", "controlPlane", "llmGovernance", "featureLibrary", "commands"},
		"additionalProperties": false,
		"properties": map[string]any{
			"schemaVersion":  map[string]any{"type": "string", "const": aiToolManifestSchemaVersion},
			"tool":           map[string]any{"type": "string", "const": "gofly"},
			"version":        map[string]any{"type": "string"},
			"description":    map[string]any{"type": "string"},
			"invocation":     map[string]any{"type": "string"},
			"docs":           linkArraySchema,
			"examples":       linkArraySchema,
			"verifyCommands": stringArraySchema,
			"output": map[string]any{
				"type":                 "object",
				"required":             []string{"mode", "envelope", "errorFields"},
				"additionalProperties": false,
				"properties": map[string]any{
					"mode":        map[string]any{"type": "string"},
					"envelope":    stringArraySchema,
					"errorFields": stringArraySchema,
				},
			},
			"controlPlane":   map[string]any{"type": "object", "additionalProperties": true},
			"llmGovernance":  map[string]any{"type": "object", "additionalProperties": true},
			"featureLibrary": map[string]any{"type": "object", "additionalProperties": true},
			"commands": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"required":             []string{"name", "description", "usage", "inputSchema", "outputFormats", "sideEffects", "riskLevel", "supportsDryRun", "mutatesFilesystem"},
					"additionalProperties": false,
					"properties": map[string]any{
						"name":        map[string]any{"type": "string"},
						"aliases":     stringArraySchema,
						"description": map[string]any{"type": "string"},
						"usage":       map[string]any{"type": "string"},
						"inputSchema": map[string]any{
							"type":                 "object",
							"required":             []string{"type", "additionalProperties"},
							"additionalProperties": false,
							"properties": map[string]any{
								"type":                 map[string]any{"type": "string", "const": "object"},
								"properties":           map[string]any{"type": "object", "additionalProperties": propertySchema},
								"required":             stringArraySchema,
								"additionalProperties": map[string]any{"type": "boolean"},
							},
						},
						"outputContract":    outputContractSchema,
						"outputFormats":     stringArraySchema,
						"sideEffects":       stringArraySchema,
						"riskLevel":         map[string]any{"type": "string", "enum": []string{"read", "low", "medium", "high"}},
						"supportsDryRun":    map[string]any{"type": "boolean"},
						"mutatesFilesystem": map[string]any{"type": "boolean"},
						"examples":          stringArraySchema,
					},
				},
			},
		},
	}
}

func manifestCommand(name string, aliases []string, description, usage string, properties map[string]aiInputProperty, outputFormats, sideEffects []string, riskLevel string, supportsDryRun, mutatesFilesystem bool, examples []string) aiToolCommand {
	return aiToolCommand{
		Name:              name,
		Aliases:           aliases,
		Description:       description,
		Usage:             usage,
		InputSchema:       aiInputSchema{Type: "object", Properties: properties, AdditionalProperties: false},
		OutputFormats:     append([]string(nil), outputFormats...),
		SideEffects:       append([]string(nil), sideEffects...),
		RiskLevel:         riskLevel,
		SupportsDryRun:    supportsDryRun,
		MutatesFilesystem: mutatesFilesystem,
		Examples:          append([]string(nil), examples...),
	}
}

func apiServiceScaffoldProperties() map[string]aiInputProperty {
	return map[string]aiInputProperty{
		"name":   stringProperty("Service name."),
		"module": stringProperty("Go module path."),
		"dir":    stringProperty("Output directory."),
		"style":  enumStringProperty("Scaffold style.", "minimal", "basic", "production"),
		"dryRun": boolProperty("Print a plan without writing scaffold files, config, or plugin output."),
	}
}

func rpcServiceScaffoldProperties() map[string]aiInputProperty {
	props := apiServiceScaffoldProperties()
	props["profile"] = enumStringProperty("Generation profile.", "gofly-ai", "gozero-compatible", "kitex-compatible")
	return props
}

func fileInputProperties(name string) map[string]aiInputProperty {
	return map[string]aiInputProperty{name: stringProperty("Input file path.")}
}

func stringProperty(description string) aiInputProperty {
	return aiInputProperty{Type: "string", Description: description}
}

func boolProperty(description string) aiInputProperty {
	return aiInputProperty{Type: "boolean", Description: description}
}

func intProperty(description string) aiInputProperty {
	return aiInputProperty{Type: "integer", Description: description}
}

func enumStringProperty(description string, values ...string) aiInputProperty {
	return aiInputProperty{Type: "string", Description: description, Enum: append([]string(nil), values...)}
}

func inferTopLevelRisk(name string) string {
	switch name {
	case "version", "env", "bug", "doctor", "feature", "completion", "complete", "release", "ai":
		return "read"
	case "plugin", "template", "upgrade":
		return "high"
	case "new", "gen", "handler", "rpc", "api", "model", "docker", "kube", "quickstart", "migrate", "config", "example":
		return "medium"
	default:
		return "medium"
	}
}

func topLevelMayMutate(name string) bool {
	switch name {
	case "version", "env", "bug", "doctor", "feature", "completion", "complete", "release", "ai":
		return false
	default:
		return true
	}
}

func completionCommand(args []string) error {
	if printCommandHelp("completion", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly completion %s`", errUsage, completionShellUsage)
	}
	if len(args) > 1 {
		return fmt.Errorf("%w: completion accepts exactly one shell argument", errUsage)
	}
	shell := args[0]
	script, err := generator.GenerateCompletion(shell)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	cliOutput(script)
	return nil
}

// configCommand 处理 `gofly config init|show|get|set|clean`。
func configCommand(args []string) error {
	if printCommandHelp("config", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly config init|show|get|set|clean`", errUsage)
	}
	sub := args[0]
	rest := args[1:]
	fs := flag.NewFlagSet("config "+sub, flag.ContinueOnError)
	dir := fs.String("dir", ".", "service root directory")
	name := fs.String("name", "", "service name override")
	module := fs.String("module", "", "module override")
	style := fs.String("style", "", "style override: minimal|basic|production")
	key := fs.String("key", "", "config key (for get/set)")
	value := fs.String("value", "", "config value (for set)")
	dryRun := fs.Bool("dry-run", false, "print the planned filesystem changes without writing files")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	remaining, err := parseInterspersedFlags(fs, rest)
	if err != nil {
		return err
	}
	previewOnly := *dryRun || *plan
	if *key == "" && len(remaining) > 0 {
		*key = remaining[0]
	}
	positionalValueExplicit := false
	if *value == "" && len(remaining) > 1 {
		*value = remaining[1]
		positionalValueExplicit = true
	}
	valueExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "value" {
			valueExplicit = true
		}
	})
	valueExplicit = valueExplicit || positionalValueExplicit
	base := *dir
	if base == "" {
		base = "."
	}
	path := filepath.Join(base, generator.DefaultConfigFile)

	switch sub {
	case "init":
		cfg := generator.DefaultConfig(*name, *module)
		if *style != "" {
			cfg.Style = *style
		}
		if previewOnly {
			return printCLIPlan("config.init", configPlan("config init", path, true, map[string]string{"dir": base, "name": *name, "module": *module, "style": cfg.Style}, []cliPlanAction{{Operation: "write-config", Target: path, Description: "create or overwrite gofly config", RiskLevel: "low"}}))
		}
		if err := generator.SaveConfig(path, cfg); err != nil {
			return err
		}
		cliOutputf("wrote gofly config: %s\n", path)
		return nil
	case "show":
		cfg, err := generator.LoadConfig(path)
		if err != nil {
			return err
		}
		cliOutputln(cfg.String())
		return nil
	case "get":
		if *key == "" {
			return fmt.Errorf("%w: --key is required for `gofly config get`", errUsage)
		}
		cfg, err := generator.LoadConfig(path)
		if err != nil {
			return err
		}
		cliOutputln(getConfigField(cfg, *key))
		return nil
	case "set":
		if *key == "" {
			return fmt.Errorf("%w: --key is required for `gofly config set`", errUsage)
		}
		if *value == "" && (!valueExplicit || !isConfigFeaturesKey(*key)) {
			return fmt.Errorf("%w: --key and --value are required for `gofly config set`", errUsage)
		}
		cfg, err := generator.LoadConfig(path)
		if err != nil {
			return err
		}
		if err := setConfigField(cfg, *key, *value); err != nil {
			return err
		}
		if previewOnly {
			return printCLIPlan("config.set", configPlan("config set", path, true, map[string]string{"dir": base, "key": *key, "value": *value}, []cliPlanAction{{Operation: "update-config", Target: path, Description: "update one gofly config value", RiskLevel: "low"}}))
		}
		if err := generator.SaveConfig(path, cfg); err != nil {
			return err
		}
		cliOutputf("updated gofly config: %s\n", path)
		return nil
	case "clean":
		if previewOnly {
			return printCLIPlan("config.clean", configPlan("config clean", path, true, map[string]string{"dir": base}, []cliPlanAction{{Operation: "remove-config", Target: path, Description: "remove gofly config if it exists", RiskLevel: "medium"}}))
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clean gofly config: %w", err)
		}
		cliOutputf("removed gofly config: %s\n", path)
		return nil
	default:
		return fmt.Errorf("%w: expected `gofly config init|show|get|set|clean`", errUsage)
	}
}

func configPlan(command, path string, dryRun bool, inputs map[string]string, actions []cliPlanAction) cliPlan {
	if inputs == nil {
		inputs = map[string]string{}
	}
	inputs["path"] = path
	return cliPlan{
		Command:           command,
		DryRun:            dryRun,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions:           actions,
		NextActions:       []string{"rerun without --dry-run/--plan to apply these actions"},
	}
}

// featureCommand 暴露 `gofly feature list` 和 `gofly feature run`。
// `run` 用于开发者测试某个已注册的 feature 对特定目录作用（不写文件，打印会生成的文件列表）。
func featureCommand(args []string) error {
	if printCommandHelp("feature", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly feature list|run`", errUsage)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list", "ls":
		fs := flag.NewFlagSet("feature list", flag.ContinueOnError)
		formatName := fs.String("format", "text", "output format: text or json")
		jsonOutput := fs.Bool("json", false, "output JSON")
		if _, err := parseInterspersedFlags(fs, rest); err != nil {
			return err
		}
		names := generator.ListFeatures()
		if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
			return printJSONEnvelope("feature.list", featureListPreview{Features: names})
		}
		if len(names) == 0 {
			cliOutputlnIf("(no registered features)")
			return nil
		}
		for _, n := range names {
			cliOutputlnIf(n)
		}
		return nil
	case "run":
		fs := flag.NewFlagSet("feature run", flag.ContinueOnError)
		name := fs.String("name", "", "service name")
		module := fs.String("module", "", "module path")
		dir := fs.String("dir", ".", "service directory")
		style := fs.String("style", "basic", "service style")
		featureFlag := fs.String("feature", "", "feature names to enable, comma-separated")
		featuresFlag := fs.String("features", "", "alias for --feature")
		formatName := fs.String("format", "text", "output format: text or json")
		jsonOutput := fs.Bool("json", false, "output JSON")
		feature := ""
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			feature = rest[0]
			rest = rest[1:]
		}
		remaining, err := parseInterspersedFlags(fs, rest)
		if err != nil {
			return err
		}
		if feature == "" && len(remaining) > 0 {
			feature = remaining[0]
			remaining = remaining[1:]
		}
		featureNames := splitCSV(joinCSV(feature, strings.Join(remaining, ","), *featureFlag, *featuresFlag))
		if len(featureNames) == 0 {
			err := fmt.Errorf("%w: expected `gofly feature run <feature-name>`", errUsage)
			if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
				_ = printJSONError("feature.run", err)
			}
			return err
		}
		if err := generator.ValidateFeatureNames(featureNames); err != nil {
			if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
				_ = printJSONError("feature.run", err)
			}
			return err
		}
		scope := generator.ExtensionScope{
			Name:   *name,
			Module: *module,
			Style:  *style,
			Dir:    *dir,
			Data:   map[string]string{"Name": *name, "Module": *module},
		}
		files, data, err := generator.ApplyFeatureNames(featureNames, scope, map[string]string{}, map[string]string{})
		if err != nil {
			return err
		}
		preview := buildFeatureRunPreview(featureNames, files, data)
		if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
			return printJSONEnvelope("feature.run", preview)
		}
		for _, file := range preview.Files {
			cliOutputfIf("# file: %s (%d bytes)\n", file.Path, file.Bytes)
		}
		if len(preview.Data) > 0 {
			cliOutputlnIf("# data:")
			for _, item := range preview.Data {
				cliOutputfIf("  %s = %s\n", item.Key, item.Value)
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: expected `gofly feature list|run`", errUsage)
	}
}

type featureListPreview struct {
	Features []string `json:"features"`
}

type featureRunPreview struct {
	Features []string             `json:"features"`
	Files    []featureFilePreview `json:"files"`
	Data     []featureDataPreview `json:"data,omitempty"`
}

type featureFilePreview struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

type featureDataPreview struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type pluginListOutput struct {
	Internal  []string                    `json:"internal"`
	Installed []generator.InstalledPlugin `json:"installed"`
}

type pluginRegistrySearchOutput struct {
	Registry string                          `json:"registry"`
	Query    string                          `json:"query,omitempty"`
	Plugins  []generator.PluginRegistryEntry `json:"plugins"`
}

type pluginRunOutput struct {
	Plugins []pluginRunResult `json:"plugins"`
}

type pluginRunResult struct {
	Plugin  string `json:"plugin"`
	Message string `json:"message,omitempty"`
	Files   int    `json:"files"`
	Patches int    `json:"patches"`
}

type pluginUninstallOutput struct {
	Remote string `json:"remote"`
	Path   string `json:"path"`
}

func buildFeatureRunPreview(names []string, files map[string]string, data map[string]string) featureRunPreview {
	preview := featureRunPreview{
		Features: append([]string(nil), names...),
		Files:    make([]featureFilePreview, 0, len(files)),
		Data:     make([]featureDataPreview, 0, len(data)),
	}
	filePaths := make([]string, 0, len(files))
	for path := range files {
		filePaths = append(filePaths, path)
	}
	sort.Strings(filePaths)
	for _, path := range filePaths {
		preview.Files = append(preview.Files, featureFilePreview{Path: path, Bytes: len(files[path])})
	}
	dataKeys := make([]string, 0, len(data))
	for key := range data {
		dataKeys = append(dataKeys, key)
	}
	sort.Strings(dataKeys)
	for _, key := range dataKeys {
		preview.Data = append(preview.Data, featureDataPreview{Key: key, Value: data[key]})
	}
	return preview
}

// pluginCommand 暴露 `gofly plugin list|search|install|uninstall|run`。
func pluginCommand(args []string) error {
	if printCommandHelp("plugin", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly plugin list|search|install|uninstall|run`", errUsage)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list", "ls":
		fs := flag.NewFlagSet("plugin list", flag.ContinueOnError)
		formatName := fs.String("format", "text", "output format: text or json")
		jsonOutput := fs.Bool("json", false, "output JSON")
		if _, err := parseInterspersedFlags(fs, rest); err != nil {
			return err
		}
		internal := generator.ListInternalPlugins()
		installed, err := generator.ListInstalledPlugins()
		if err != nil {
			return err
		}
		if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
			return printJSON(pluginListOutput{Internal: internal, Installed: installed})
		}
		if len(internal) == 0 && len(installed) == 0 {
			cliOutputln("(no registered internal plugins; external plugins are discovered at runtime)")
			return nil
		}
		for _, n := range internal {
			cliOutputf("internal\t%s\n", n)
		}
		for _, p := range installed {
			cliOutputf("cached\t%s@%s\t%s\tsha256:%s\n", p.Remote, p.Version, p.Binary, p.BinaryDigest)
		}
		return nil
	case "search":
		fs := flag.NewFlagSet("plugin search", flag.ContinueOnError)
		registry := fs.String("registry", "", "plugin registry JSON URL or path")
		query := fs.String("query", "", "search query")
		formatName := fs.String("format", "text", "output format: text or json")
		jsonOutput := fs.Bool("json", false, "output JSON")
		remaining, err := parseInterspersedFlags(fs, rest)
		if err != nil {
			return err
		}
		fillNameFromArgs(query, remaining)
		if *registry == "" {
			return fmt.Errorf("%w: --registry <url-or-path> is required for `gofly plugin search`", errUsage)
		}
		index, err := generator.LoadPluginRegistryIndex(*registry)
		if err != nil {
			return err
		}
		matches := generator.FilterPluginRegistryEntries(index.Plugins, *query)
		if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
			return printJSON(pluginRegistrySearchOutput{Registry: *registry, Query: *query, Plugins: matches})
		}
		if len(matches) == 0 {
			cliOutputln("(no plugins matched)")
			return nil
		}
		for _, plugin := range matches {
			cliOutputf("%s@%s\t%s\t%s\n", plugin.Name, plugin.Version, plugin.Remote, plugin.Description)
		}
		return nil
	case "install":
		fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
		remote := fs.String("remote", "", "remote plugin as <repo-or-url>@<version>")
		jsonOutput := fs.Bool("json", false, "output JSON")
		remaining, err := parseInterspersedFlags(fs, rest)
		if err != nil {
			return err
		}
		fillNameFromArgs(remote, remaining)
		if *remote == "" {
			return fmt.Errorf("%w: --remote <repo-or-url>@<version> is required for `gofly plugin install`", errUsage)
		}
		sp := spinner.New()
		if isQuiet() || *jsonOutput || outputMode() == outputJSON {
			sp.Disable()
		}
		sp.Start("installing plugin...")
		info, err := generator.InstallRemotePlugin(*remote)
		sp.Stop()
		if err != nil {
			return err
		}
		if *jsonOutput {
			return printJSON(info)
		}
		cliOutputf("installed plugin %s@%s\nhash: %s\npath: %s\n", info.Remote, info.Version, info.Hash, info.Binary)
		if info.BinaryDigest != "" {
			cliOutputf("sha256: %s\n", info.BinaryDigest)
		}
		return nil
	case "uninstall", "remove", "rm":
		fs := flag.NewFlagSet("plugin uninstall", flag.ContinueOnError)
		remote := fs.String("remote", "", "remote plugin as <repo-or-url>@<version>")
		jsonOutput := fs.Bool("json", false, "output JSON")
		remaining, err := parseInterspersedFlags(fs, rest)
		if err != nil {
			return err
		}
		fillNameFromArgs(remote, remaining)
		if *remote == "" {
			return fmt.Errorf("%w: --remote <repo-or-url>@<version> is required for `gofly plugin uninstall`", errUsage)
		}
		dir, err := generator.UninstallRemotePlugin(*remote)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return printJSON(pluginUninstallOutput{Remote: *remote, Path: dir})
		}
		cliOutputf("uninstalled plugin cache: %s\n", dir)
		return nil
	case "run":
		fs := flag.NewFlagSet("plugin run", flag.ContinueOnError)
		name := fs.String("name", "", "service name")
		module := fs.String("module", "", "module path")
		dir := fs.String("dir", ".", "service directory")
		command := fs.String("command", "service", "plugin command: service|handler|model")
		remote := fs.String("remote", "", "remote plugin as <repo-or-url>@<version>")
		goPlugin := fs.String("go-plugin", "", "plugin executable or directory to traverse")
		jsonOutput := fs.Bool("json", false, "output JSON")
		dryRun := fs.Bool("dry-run", false, "print the planned plugin execution without executing plugins or writing files")
		plan := fs.Bool("plan", false, "alias for --dry-run")
		plugin := ""
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			plugin = rest[0]
			rest = rest[1:]
		}
		remaining, err := parseInterspersedFlags(fs, rest)
		if err != nil {
			return err
		}
		if plugin == "" && len(remaining) > 0 {
			plugin = remaining[0]
		}
		previewOnly := *dryRun || *plan
		plugins := []string(nil)
		if *remote != "" {
			if previewOnly {
				plugins = append(plugins, *remote)
			} else {
				sp := spinner.New()
				if isQuiet() || *jsonOutput || outputMode() == outputJSON {
					sp.Disable()
				}
				sp.Start("resolving plugin...")
				info, err := generator.ResolveRemotePlugin(*remote)
				sp.Stop()
				if err != nil {
					return err
				}
				plugins = append(plugins, info.Binary)
			}
		}
		if *goPlugin != "" {
			if previewOnly {
				plugins = append(plugins, *goPlugin)
			} else {
				resolved, err := generator.ResolveGoPluginPaths(*goPlugin)
				if err != nil {
					return err
				}
				plugins = append(plugins, resolved...)
			}
		}
		if plugin != "" {
			plugins = append(plugins, plugin)
		}
		if len(plugins) == 0 {
			return fmt.Errorf("%w: expected `gofly plugin run <plugin-name-or-path>` or --remote/--go-plugin", errUsage)
		}
		if previewOnly {
			return printCLIPlan("plugin.run", pluginRunPlan(*command, *dir, *name, *module, *remote, *goPlugin, plugins), *jsonOutput)
		}
		runner := generator.NewPluginRunner()
		req := generator.PluginRequest{
			Command: *command,
			Service: *name,
			Module:  *module,
			Dir:     *dir,
		}
		results := make([]pluginRunResult, 0, len(plugins))
		for _, plugin := range plugins {
			resp, err := runner.Run(plugin, req)
			if err != nil {
				return err
			}
			if resp.Message != "" && !*jsonOutput {
				errorf("[gofly] plugin %s: %s\n", plugin, resp.Message)
			}
			writtenFiles, err := resp.WriteFiles(*dir)
			if err != nil {
				return err
			}
			if err := resp.ApplyPatches(*dir); err != nil {
				return err
			}
			results = append(results, pluginRunResult{Plugin: plugin, Message: resp.Message, Files: writtenFiles, Patches: len(resp.Patches)})
		}
		if *jsonOutput {
			return printJSON(pluginRunOutput{Plugins: results})
		}
		return nil
	default:
		return fmt.Errorf("%w: expected `gofly plugin list|search|install|uninstall|run`", errUsage)
	}
}

func pluginRunPlan(commandName, dir, name, module, remote, goPlugin string, plugins []string) cliPlan {
	inputs := map[string]string{
		"command": commandName,
		"dir":     dir,
		"name":    name,
		"module":  module,
	}
	if remote != "" {
		inputs["remote"] = remote
	}
	if goPlugin != "" {
		inputs["goPlugin"] = goPlugin
	}
	if len(plugins) > 0 {
		inputs["plugins"] = strings.Join(plugins, ",")
	}
	return cliPlan{
		Command:           "plugin run",
		DryRun:            true,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions: []cliPlanAction{
			{Operation: "resolve-plugins", Target: strings.Join(plugins, ","), Description: "resolve configured plugin inputs", RiskLevel: "medium"},
			{Operation: "execute-plugins", Target: strings.Join(plugins, ","), Description: "execute plugins with the requested plugin command", RiskLevel: "high"},
			{Operation: "apply-plugin-output", Target: dir, Description: "write files and apply patches returned by plugins", RiskLevel: "high"},
		},
		Warnings:    []string{"dry-run does not download remote plugins, execute plugin binaries, write files, or apply patches"},
		NextActions: []string{"rerun without --dry-run/--plan to apply these actions"},
	}
}

// getConfigField 返回 Config 的简单字段（用于 `gofly config get <key>`）。
func getConfigField(cfg *generator.Config, key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "servicename", "service-name", "service":
		return cfg.ServiceName
	case "module":
		return cfg.Module
	case "style":
		return cfg.Style
	case "templatedir", "template-dir", "templates":
		return cfg.TemplateDir
	case "goversion", "go-version":
		return cfg.GoVersion
	case "features":
		return strings.Join(cfg.Features, ",")
	case "rpc.plugins", "rpc-plugins":
		if cfg.RPC != nil {
			return strings.Join(cfg.RPC.Plugins, ",")
		}
		return ""
	case "rpc.transport", "rpc-transport":
		if cfg.RPC != nil {
			return cfg.RPC.Transport
		}
		return ""
	case "rpc.profile", "rpc-profile", "profile":
		if cfg.RPC != nil {
			return cfg.RPC.Profile
		}
		return ""
	case "api.plugins", "api-plugins":
		if cfg.API != nil {
			return strings.Join(cfg.API.Plugins, ",")
		}
		return ""
	case "api.profile", "api-profile":
		if cfg.API != nil {
			return cfg.API.Profile
		}
		return ""
	case "model.style", "model-style":
		if cfg.Model != nil {
			return cfg.Model.Style
		}
		return ""
	case "model.ignorecolumns", "model.ignore-columns", "model-ignore-columns":
		if cfg.Model != nil {
			return strings.Join(cfg.Model.IgnoreColumns, ",")
		}
		return ""
	case "model.typesmap", "model.types-map", "model-types-map":
		if cfg.Model != nil {
			return encodeStringMap(cfg.Model.TypesMap)
		}
		return ""
	case "model.cache", "model-cache":
		if cfg.Model != nil && cfg.Model.Cache {
			return "true"
		}
		return "false"
	case "model.strict", "model-strict":
		if cfg.Model != nil && cfg.Model.Strict {
			return "true"
		}
		return "false"
	case "llm.provider", "llm-provider":
		if cfg.LLM != nil {
			return cfg.LLM.Provider
		}
		return ""
	case "llm.model", "llm-model":
		if cfg.LLM != nil {
			return cfg.LLM.Model
		}
		return ""
	case "llm.maxinputtokens", "llm.max-input-tokens", "llm-max-input-tokens":
		if cfg.LLM != nil {
			return fmt.Sprint(cfg.LLM.MaxInputTokens)
		}
		return "0"
	case "llm.maxoutputtokens", "llm.max-output-tokens", "llm-max-output-tokens":
		if cfg.LLM != nil {
			return fmt.Sprint(cfg.LLM.MaxOutputTokens)
		}
		return "0"
	case "llm.maxtotaltokens", "llm.max-total-tokens", "llm-max-total-tokens":
		if cfg.LLM != nil {
			return fmt.Sprint(cfg.LLM.MaxTotalTokens)
		}
		return "0"
	case "llm.ratelimit", "llm.rate-limit", "llm-rate-limit":
		if cfg.LLM != nil {
			return fmt.Sprint(cfg.LLM.RateLimitPerSecond)
		}
		return "0"
	case "llm.rateburst", "llm.rate-burst", "llm-rate-burst":
		if cfg.LLM != nil {
			return fmt.Sprint(cfg.LLM.RateLimitBurst)
		}
		return "0"
	case "llm.timeout", "llm-timeout":
		if cfg.LLM != nil {
			return cfg.LLM.Timeout
		}
		return ""
	default:
		if cfg.Extra != nil {
			if v, ok := cfg.Extra[key]; ok {
				return v
			}
		}
		return ""
	}
}

func isConfigFeaturesKey(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), "features")
}

// setConfigField 写入 Config 的简单字段（用于 `gofly config set <key> <value>`）。
func setConfigField(cfg *generator.Config, key, value string) error {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "servicename", "service-name", "service":
		cfg.ServiceName = value
	case "module":
		cfg.Module = value
	case "style":
		cfg.Style = value
	case "templatedir", "template-dir", "templates":
		cfg.TemplateDir = value
	case "goversion", "go-version":
		cfg.GoVersion = value
	case "features":
		features := splitCSV(value)
		if err := generator.ValidateFeatureNames(features); err != nil {
			return err
		}
		cfg.Features = features
	case "rpc.plugins", "rpc-plugins":
		if cfg.RPC == nil {
			cfg.RPC = &generator.RPCConfig{}
		}
		cfg.RPC.Plugins = splitCSV(value)
	case "rpc.transport", "rpc-transport":
		if cfg.RPC == nil {
			cfg.RPC = &generator.RPCConfig{}
		}
		cfg.RPC.Transport = value
	case "rpc.profile", "rpc-profile", "profile":
		if cfg.RPC == nil {
			cfg.RPC = &generator.RPCConfig{}
		}
		cfg.RPC.Profile = value
	case "api.plugins", "api-plugins":
		if cfg.API == nil {
			cfg.API = &generator.APIConfig{}
		}
		cfg.API.Plugins = splitCSV(value)
	case "api.profile", "api-profile":
		if cfg.API == nil {
			cfg.API = &generator.APIConfig{}
		}
		cfg.API.Profile = value
	case "model.style", "model-style":
		ensureModelConfig(cfg).Style = value
	case "model.ignorecolumns", "model.ignore-columns", "model-ignore-columns":
		ensureModelConfig(cfg).IgnoreColumns = splitCSV(value)
	case "model.typesmap", "model.types-map", "model-types-map":
		ensureModelConfig(cfg).TypesMap = parseKeyValueCSV(value)
	case "model.cache", "model-cache":
		ensureModelConfig(cfg).Cache = parseBoolString(value)
	case "model.strict", "model-strict":
		ensureModelConfig(cfg).Strict = parseBoolString(value)
	case "llm.provider", "llm-provider":
		ensureLLMConfig(cfg).Provider = value
	case "llm.model", "llm-model":
		ensureLLMConfig(cfg).Model = value
	case "llm.maxinputtokens", "llm.max-input-tokens", "llm-max-input-tokens":
		v, err := parseNonNegativeIntConfigValue("llm.maxInputTokens", value)
		if err != nil {
			return err
		}
		ensureLLMConfig(cfg).MaxInputTokens = v
	case "llm.maxoutputtokens", "llm.max-output-tokens", "llm-max-output-tokens":
		v, err := parseNonNegativeIntConfigValue("llm.maxOutputTokens", value)
		if err != nil {
			return err
		}
		ensureLLMConfig(cfg).MaxOutputTokens = v
	case "llm.maxtotaltokens", "llm.max-total-tokens", "llm-max-total-tokens":
		v, err := parseNonNegativeIntConfigValue("llm.maxTotalTokens", value)
		if err != nil {
			return err
		}
		ensureLLMConfig(cfg).MaxTotalTokens = v
	case "llm.ratelimit", "llm.rate-limit", "llm-rate-limit":
		v, err := parseNonNegativeIntConfigValue("llm.rateLimitPerSecond", value)
		if err != nil {
			return err
		}
		ensureLLMConfig(cfg).RateLimitPerSecond = v
	case "llm.rateburst", "llm.rate-burst", "llm-rate-burst":
		v, err := parseNonNegativeIntConfigValue("llm.rateLimitBurst", value)
		if err != nil {
			return err
		}
		ensureLLMConfig(cfg).RateLimitBurst = v
	case "llm.timeout", "llm-timeout":
		if value != "" {
			if _, err := time.ParseDuration(value); err != nil {
				return fmt.Errorf("%w: invalid llm.timeout %q: %v", errUsage, value, err)
			}
		}
		ensureLLMConfig(cfg).Timeout = value
	default:
		if cfg.Extra == nil {
			cfg.Extra = map[string]string{}
		}
		cfg.Extra[key] = value
	}
	return nil
}

func ensureModelConfig(cfg *generator.Config) *generator.ModelConfig {
	if cfg.Model == nil {
		cfg.Model = &generator.ModelConfig{}
	}
	if cfg.Model.TypesMap == nil {
		cfg.Model.TypesMap = map[string]string{}
	}
	return cfg.Model
}

func ensureLLMConfig(cfg *generator.Config) *generator.LLMConfig {
	if cfg.LLM == nil {
		cfg.LLM = &generator.LLMConfig{Provider: "noop", Model: "noop"}
	}
	return cfg.LLM
}

func parseNonNegativeIntConfigValue(name, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", errUsage, name)
	}
	return parsed, nil
}

func parseBoolString(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
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
