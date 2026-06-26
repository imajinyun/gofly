package command

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
