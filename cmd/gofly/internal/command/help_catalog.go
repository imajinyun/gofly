package command

import "strings"

func commandHelpFor(command string) commandHelp {
	command = canonicalHelpTopic(command)
	if help, ok := apiCommandHelp(command); ok {
		return help
	}
	if help, ok := rpcCommandHelp(command); ok {
		return help
	}
	if help, ok := modelCommandHelp(command); ok {
		return help
	}
	if help, ok := pluginCommandHelp(command); ok {
		return help
	}
	if help, ok := aiCommandHelp(command); ok {
		return help
	}

	switch command {
	case "new service":
		return commandHelp{Name: "new service", Short: "Create the golden-path production service scaffold.", Usage: "gofly new service <name> --module <module> [--dir <dir>] [flags]", Flags: []string{"--name <name>                  service name", "--module <module>              Go module path", "--dir <dir>                    output directory", "--style production             defaults to production", "--discovery memory|consul|etcdv3", "--discovery-address <addr>      discovery address", "--discovery-endpoints <list>    discovery endpoints", "--home, --remote, --branch      template source options", "--feature <names>               feature names to enable", "--plugin <paths>                plugin executables", "--json                         emit scaffold result as JSON"}, Examples: []string{"gofly new service orders --style production --module example.com/orders", "gofly new service orders --module example.com/orders --discovery memory --dir orders --json"}}
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
	case "handler":
		return commandHelp{
			Name:     "handler",
			Short:    "Generate or complete API handlers.",
			Usage:    "gofly handler gen|complete [arguments]",
			Commands: []helpCommand{{Name: "gen", Short: "generate REST handler skeletons"}, {Name: "complete", Short: "append missing methods to an existing handler Go file"}},
			Examples: []string{"gofly handler gen CreateOrder --dir . --path v1/order", "gofly handler complete --file internal/svc/service.go --method HealthCheck"},
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
	case "bug":
		return commandHelp{Name: "bug", Short: "Print diagnostic bug reports.", Usage: "gofly bug [--json]", Flags: []string{"--json  print bug report as JSON"}, Examples: []string{"gofly bug", "gofly bug --json"}}
	case "upgrade":
		return commandHelp{Name: "upgrade", Short: "Print or run upgrade commands.", Usage: "gofly upgrade [--version <version>] [--module <module>] [--project-dir <dir>] [--execute] [--json]", Flags: []string{"--version <version>  version to install", "--module <module>    module path to install", "--project-dir <dir>  generated project directory for diff/verify commands", "--dir <dir>          alias for --project-dir", "--execute            execute go install instead of printing the command", "--json               print upgrade plan/result as JSON"}, Examples: []string{"gofly upgrade --version latest", "gofly upgrade --version v1.2.3 --json", "gofly upgrade --project-dir ./orders --version latest"}}
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
