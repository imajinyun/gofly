package generator

import (
	"fmt"
	"strings"
)

func GenerateCompletion(shell string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "bash":
		return `# bash completion for gofly
_gofly_completion() {
  local cur prev commands
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  local cmd="${COMP_WORDS[1]}"
  local sub="${COMP_WORDS[2]}"

  # File path completion for flag arguments that expect files or directories.
  case "$prev" in
    --api|--src|--file|--idl|--base|--target|--config)
      COMPREPLY=($(compgen -f -- "$cur"))
      return 0
      ;;
    --dir)
      COMPREPLY=($(compgen -d -- "$cur"))
      return 0
      ;;
  esac

  commands="version new gen generate handler rpc api model docker kube template quickstart migrate migration env bug upgrade config feature plugin completion complete release doctor example examples ai tools"
  case "$cmd" in
    new) commands="api rpc" ;;
    gen) commands="handler rpc api rest middleware model gateway" ;;
    handler) commands="gen complete" ;;
    rpc) commands="new idl inspect thrift thrift2proto client server middleware lint deps gen protoc check doc docs swagger openapi breaking descriptor plugin template tpl" ;;
    generate) commands="handler rpc api rest middleware model gateway" ;;
    api) commands="new go gen check validate breaking break format fmt doc docs swagger client ts typescript js javascript dart java kotlin kt types route routes import diff plugin middleware" ;;
    model) commands="gen mysql pg postgres postgresql mongo" ;;
    kube) commands="deploy deployment service svc ingress ing configmap cm job" ;;
    template) commands="init list ls clean update revert" ;;
    env) commands="check install" ;;
    config) commands="init show get set clean" ;;
    feature) commands="list ls run" ;;
    plugin) commands="list ls install uninstall remove rm run" ;;
    completion) commands="bash zsh fish powershell pwsh" ;;
    complete) commands="handler" ;;
    ai|tools) commands="manifest plan new complete stream doctor" ;;
  esac
  case "$cmd:$sub" in
    rpc:template|rpc:tpl) commands="init list ls clean update revert" ;;
    model:mysql|model:pg|model:postgres|model:postgresql) commands="ddl datasource" ;;
    complete:handler) commands="bash zsh fish powershell pwsh" ;;
  esac
  COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
}
complete -F _gofly_completion gofly
`, nil
	case "zsh":
		return `#compdef gofly
_gofly() {
  local -a commands
  commands=(
    'version:print version metadata'
    'new:scaffold a new api or rpc service'
    'gen:unified generator entrypoint'
    'generate:unified generator entrypoint alias'
    'handler:generate or complete api handler boilerplate'
    'rpc:rpc-file operations'
    'api:api-file operations'
    'model:model generation operations'
    'docker:generate Dockerfile'
    'kube:generate Kubernetes manifests'
    'template:manage local templates'
    'quickstart:create runnable service quickly'
    'migrate:create SQL migrations'
    'migration:create SQL migrations'
    'env:print and check local toolchain environment'
    'bug:print diagnostic bug report'
    'upgrade:print or execute upgrade command'
    'config:manage .gofly/config.json'
    'feature:list or preview scaffold features'
    'plugin:list, install or run gofly plugins'
    'completion:emit shell completion scripts'
    'complete:emit shell completion scripts'
    'release:run release readiness checks'
    'doctor:diagnose local environment readiness'
    'example:list or run built-in examples'
    'examples:list or run built-in examples alias'
    'ai:emit AI tool manifest'
    'tools:emit AI tool manifest alias'
  )
  case "$words[2]" in
    new) commands=('api:create API service' 'rpc:create RPC service') ;;
    gen|generate) commands=('handler:generate REST handler' 'rpc:generate RPC code' 'api:generate REST code' 'rest:generate REST code' 'middleware:generate middleware skeletons' 'model:generate model code' 'gateway:generate API gateway') ;;
    handler) commands=('gen:generate REST handler' 'complete:append missing methods') ;;
    rpc) commands=('new:create RPC service' 'idl:inspect IDL metadata' 'inspect:inspect IDL metadata alias' 'thrift:convert thrift to proto skeleton' 'thrift2proto:convert thrift alias' 'client:generate RPC client wrapper' 'server:generate RPC server stubs' 'middleware:generate gRPC middleware' 'lint:lint IDL contract' 'deps:list IDL imports' 'gen:generate RPC code' 'protoc:run protoc plugins' 'check:validate proto' 'doc:generate OpenAPI docs' 'docs:generate OpenAPI docs alias' 'swagger:generate OpenAPI docs alias' 'openapi:generate OpenAPI docs alias' 'breaking:compare compatibility' 'descriptor:compare runtime descriptors' 'plugin:run RPC plugin' 'template:manage templates' 'tpl:manage templates alias') ;;
    api) commands=('new:create API service' 'go:generate REST code' 'gen:generate REST code' 'check:validate API' 'validate:validate API' 'breaking:detect breaking changes' 'break:detect breaking changes' 'format:format API' 'fmt:format API' 'doc:generate docs' 'docs:generate docs' 'swagger:generate OpenAPI' 'client:generate client' 'ts:generate TypeScript client' 'typescript:generate TypeScript client' 'js:generate JavaScript client' 'javascript:generate JavaScript client' 'dart:generate Dart client' 'java:generate Java client' 'kotlin:generate Kotlin client' 'kt:generate Kotlin client' 'types:generate DTOs' 'route:print routes' 'routes:print routes' 'import:convert OpenAPI' 'diff:compare APIs' 'plugin:run plugin' 'middleware:generate middleware') ;;
    model) commands=('gen:generate SQL model' 'mysql:MySQL model mode' 'pg:PostgreSQL model mode' 'postgres:PostgreSQL model mode' 'postgresql:PostgreSQL model mode' 'mongo:Mongo model mode') ;;
    kube) commands=('deploy:generate Deployment' 'deployment:generate Deployment' 'service:generate Service' 'svc:generate Service' 'ingress:generate Ingress' 'ing:generate Ingress' 'configmap:generate ConfigMap' 'cm:generate ConfigMap' 'job:generate Job') ;;
    template) commands=('init:write default templates' 'list:list templates' 'ls:list templates' 'clean:remove templates' 'update:refresh templates' 'revert:restore templates') ;;
    env) commands=('check:check dependencies' 'install:print installation guidance') ;;
    config) commands=('init:create config' 'show:print config' 'get:read value' 'set:update value' 'clean:remove config') ;;
    feature) commands=('list:list features' 'ls:list features' 'run:preview feature') ;;
    plugin) commands=('list:list plugins' 'ls:list plugins' 'install:install remote plugin' 'uninstall:uninstall remote plugin' 'remove:uninstall remote plugin' 'rm:uninstall remote plugin' 'run:run plugin') ;;
    completion) commands=('bash:bash completion' 'zsh:zsh completion' 'fish:fish completion' 'powershell:powershell completion' 'pwsh:powershell completion alias') ;;
    complete) commands=('handler:emit shell completion scripts') ;;
    ai|tools) commands=('manifest:print AI tool manifest' 'plan:plan AI-first project scaffold' 'new:plan or apply AI-first project scaffold' 'complete:run governed noop completion' 'stream:run governed streaming completion' 'doctor:run AI subsystem diagnostics') ;;
  esac
  case "$words[2]:$words[3]" in
    rpc:template|rpc:tpl) commands=('init:write templates' 'list:list templates' 'ls:list templates' 'clean:remove templates' 'update:refresh templates' 'revert:restore templates') ;;
    model:mysql|model:pg|model:postgres|model:postgresql) commands=('ddl:generate from DDL' 'datasource:generate from datasource') ;;
    complete:handler) commands=('bash:bash completion' 'zsh:zsh completion' 'fish:fish completion' 'powershell:powershell completion' 'pwsh:powershell completion alias') ;;
  esac
  _describe -t commands 'gofly command' commands
}
compdef _gofly gofly
`, nil
	case "fish":
		return `complete -c gofly -f -a "version\tPrint version metadata\nnew\tScaffold a new service\ngen\tUnified generator\ngenerate\tUnified generator alias\nhandler\tHandler generator and completer\nrpc\tRPC file operations\napi\tAPI file operations\nmodel\tModel generation\ndocker\tGenerate Dockerfile assets\nkube\tGenerate Kubernetes manifests\ntemplate\tManage templates\nquickstart\tCreate a runnable service\nmigrate\tCreate SQL migrations\nmigration\tCreate SQL migrations alias\nenv\tCheck toolchain environment\nbug\tPrint diagnostic bug report\nupgrade\tPrint or run upgrade commands\nconfig\tManage .gofly/config.json\nfeature\tList or preview scaffold features\nplugin\tList, install or run plugins\ncompletion\tEmit shell completion scripts\ncomplete\tEmit legacy completion scripts\nrelease\tRun release readiness checks\ndoctor\tDiagnose local environment\nexample\tList or run built-in examples\nexamples\tList or run built-in examples alias\nai\tEmit AI tool manifest\ntools\tEmit AI tool manifest alias"
complete -c gofly -n '__fish_seen_subcommand_from new' -a "api\tCreate an API service\nrpc\tCreate an RPC service"
complete -c gofly -n '__fish_seen_subcommand_from gen' -a "handler\tGenerate REST handler\nrpc\tGenerate RPC code\napi\tGenerate REST code\nrest\tGenerate REST code alias\nmiddleware\tGenerate middleware skeletons\nmodel\tGenerate model code\ngateway\tGenerate API gateway"
complete -c gofly -n '__fish_seen_subcommand_from generate' -a "handler\tGenerate REST handler\nrpc\tGenerate RPC code\napi\tGenerate REST code\nrest\tGenerate REST code alias\nmiddleware\tGenerate middleware skeletons\nmodel\tGenerate model code\ngateway\tGenerate API gateway"
complete -c gofly -n '__fish_seen_subcommand_from handler' -a "gen\tGenerate REST handler\ncomplete\tAppend missing methods"
complete -c gofly -n '__fish_seen_subcommand_from rpc' -a "new\tCreate RPC service\nidl\tInspect IDL metadata\ninspect\tInspect IDL metadata alias\nthrift\tConvert thrift to proto\nthrift2proto\tConvert thrift alias\nclient\tGenerate client wrapper\nserver\tGenerate server stubs\nmiddleware\tGenerate gRPC middleware\nlint\tLint IDL contract\ndeps\tList IDL imports\ngen\tGenerate RPC code\nprotoc\tRun protoc plugins\ncheck\tValidate proto syntax\ndoc\tGenerate OpenAPI docs\ndocs\tGenerate OpenAPI docs alias\nswagger\tGenerate OpenAPI docs alias\nopenapi\tGenerate OpenAPI docs alias\nbreaking\tCompare compatibility\ndescriptor\tCompare runtime descriptors\nplugin\tRun RPC plugin\ntemplate\tManage RPC templates\ntpl\tManage RPC templates alias"
complete -c gofly -n '__fish_seen_subcommand_from api' -a "new\tCreate API service\ngo\tGenerate REST code\ngen\tGenerate REST code\ncheck\tValidate API syntax\nvalidate\tValidate API alias\nbreaking\tDetect breaking changes\nbreak\tDetect breaking changes alias\nformat\tFormat API file\nfmt\tFormat API file alias\ndoc\tGenerate API docs\ndocs\tGenerate API docs alias\nswagger\tGenerate OpenAPI spec\nclient\tGenerate API client\nts\tGenerate TypeScript client\ntypescript\tGenerate TypeScript client alias\njs\tGenerate JavaScript client\njavascript\tGenerate JavaScript client alias\ndart\tGenerate Dart client\njava\tGenerate Java client\nkotlin\tGenerate Kotlin client\nkt\tGenerate Kotlin client alias\ntypes\tGenerate DTOs\nroute\tPrint routes\nroutes\tPrint routes alias\nimport\tConvert OpenAPI to API\ndiff\tCompare API files\nplugin\tRun API plugin\nmiddleware\tGenerate API middleware"
complete -c gofly -n '__fish_seen_subcommand_from model' -a "gen\tGenerate model code\nmysql\tMySQL model mode\npg\tPostgreSQL model mode\npostgres\tPostgreSQL model mode alias\npostgresql\tPostgreSQL model mode alias\nmongo\tMongo model mode"
complete -c gofly -n '__fish_seen_subcommand_from kube' -a "deploy\tGenerate Deployment\ndeployment\tGenerate Deployment alias\nservice\tGenerate Service\nsvc\tGenerate Service alias\ningress\tGenerate Ingress\ning\tGenerate Ingress alias\nconfigmap\tGenerate ConfigMap\ncm\tGenerate ConfigMap alias\njob\tGenerate Job"
complete -c gofly -n '__fish_seen_subcommand_from template' -a "init\tWrite default templates\nlist\tList templates\nls\tList templates alias\nclean\tRemove templates\nupdate\tRefresh templates\nrevert\tRestore default templates"
complete -c gofly -n '__fish_seen_subcommand_from env' -a "check\tCheck dependencies\ninstall\tPrint install guidance"
complete -c gofly -n '__fish_seen_subcommand_from config' -a "init\tCreate config file\nshow\tPrint config\nset\tUpdate config value\nget\tRead config value\nclean\tRemove config file"
complete -c gofly -n '__fish_seen_subcommand_from feature' -a "list\tList available features\nls\tList features alias\nrun\tPreview feature output"
complete -c gofly -n '__fish_seen_subcommand_from plugin' -a "list\tList plugins\nls\tList plugins alias\ninstall\tInstall remote plugin\nuninstall\tUninstall plugin\nremove\tUninstall plugin alias\nrm\tUninstall plugin alias\nrun\tRun plugin"
complete -c gofly -n '__fish_seen_subcommand_from completion' -a "bash\tBash completion\nzsh\tZsh completion\nfish\tFish completion\npowershell\tPowerShell completion\npwsh\tPowerShell completion alias"
complete -c gofly -n '__fish_seen_subcommand_from complete' -a "handler\tEmit completion scripts"
complete -c gofly -n '__fish_seen_subcommand_from ai' -a "manifest\tPrint AI tool manifest\nplan\tPlan AI-first project scaffold\nnew\tPlan or apply AI-first project scaffold\ncomplete\tRun governed noop completion\nstream\tRun governed streaming completion\ndoctor\tRun AI subsystem diagnostics"
complete -c gofly -n '__fish_seen_subcommand_from tools' -a "manifest\tPrint AI tool manifest alias\nplan\tPlan AI-first project scaffold alias\nnew\tPlan or apply AI-first project scaffold alias\ncomplete\tRun governed noop completion alias\nstream\tRun governed streaming completion alias\ndoctor\tRun AI subsystem diagnostics alias"
complete -c gofly -n '__fish_seen_subcommand_from rpc; and __fish_seen_subcommand_from template' -a "init\tWrite default templates\nlist\tList templates\nls\tList templates alias\nclean\tRemove templates\nupdate\tRefresh templates\nrevert\tRestore default templates"
complete -c gofly -n '__fish_seen_subcommand_from rpc; and __fish_seen_subcommand_from tpl' -a "init\tWrite default templates\nlist\tList templates\nls\tList templates alias\nclean\tRemove templates\nupdate\tRefresh templates\nrevert\tRestore default templates"
complete -c gofly -n '__fish_seen_subcommand_from model; and __fish_seen_subcommand_from mysql' -a "ddl\tGenerate from DDL\ndatasource\tGenerate from datasource"
complete -c gofly -n '__fish_seen_subcommand_from model; and __fish_seen_subcommand_from pg' -a "ddl\tGenerate from DDL\ndatasource\tGenerate from datasource"
complete -c gofly -n '__fish_seen_subcommand_from model; and __fish_seen_subcommand_from postgres' -a "ddl\tGenerate from DDL\ndatasource\tGenerate from datasource"
complete -c gofly -n '__fish_seen_subcommand_from model; and __fish_seen_subcommand_from postgresql' -a "ddl\tGenerate from DDL\ndatasource\tGenerate from datasource"
complete -c gofly -n '__fish_seen_subcommand_from complete; and __fish_seen_subcommand_from handler' -a "bash\tBash completion\nzsh\tZsh completion\nfish\tFish completion\npowershell\tPowerShell completion\npwsh\tPowerShell completion alias"
`, nil
	case "powershell", "pwsh":
		return `Register-ArgumentCompleter -Native -CommandName gofly -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $words = $commandAst.CommandElements | ForEach-Object { $_.Value }
  $cmd = if ($words.Count -gt 1) { $words[1] } else { "" }
  $sub = if ($words.Count -gt 2) { $words[2] } else { "" }
  $commands = @("version", "new", "gen", "generate", "handler", "rpc", "api", "model", "docker", "kube", "template", "quickstart", "migrate", "migration", "env", "bug", "upgrade", "config", "feature", "plugin", "completion", "complete", "release", "doctor", "example", "examples", "ai", "tools")
  switch ($cmd) {
    "new" { $commands = @("api", "rpc") }
    "gen" { $commands = @("handler", "rpc", "api", "rest", "middleware", "model", "gateway") }
    "generate" { $commands = @("handler", "rpc", "api", "rest", "middleware", "model", "gateway") }
    "handler" { $commands = @("gen", "complete") }
    "rpc" { $commands = @("new", "idl", "inspect", "thrift", "thrift2proto", "client", "server", "middleware", "lint", "deps", "gen", "protoc", "check", "doc", "docs", "swagger", "openapi", "breaking", "descriptor", "plugin", "template", "tpl") }
    "api" { $commands = @("new", "go", "gen", "check", "validate", "breaking", "break", "format", "fmt", "doc", "docs", "swagger", "client", "ts", "typescript", "js", "javascript", "dart", "java", "kotlin", "kt", "types", "route", "routes", "import", "diff", "plugin", "middleware") }
    "model" { $commands = @("gen", "mysql", "pg", "postgres", "postgresql", "mongo") }
    "kube" { $commands = @("deploy", "deployment", "service", "svc", "ingress", "ing", "configmap", "cm", "job") }
    "template" { $commands = @("init", "list", "ls", "clean", "update", "revert") }
    "env" { $commands = @("check", "install") }
    "config" { $commands = @("init", "show", "get", "set", "clean") }
    "feature" { $commands = @("list", "ls", "run") }
    "plugin" { $commands = @("list", "ls", "install", "uninstall", "remove", "rm", "run") }
    "completion" { $commands = @("bash", "zsh", "fish", "powershell", "pwsh") }
    "complete" { $commands = @("handler") }
    "ai" { $commands = @("manifest", "plan", "new", "complete", "stream", "doctor") }
    "tools" { $commands = @("manifest", "plan", "new", "complete", "stream", "doctor") }
  }
  switch ("$($cmd):$sub") {
    "rpc:template" { $commands = @("init", "list", "ls", "clean", "update", "revert") }
    "rpc:tpl" { $commands = @("init", "list", "ls", "clean", "update", "revert") }
    "model:mysql" { $commands = @("ddl", "datasource") }
    "model:pg" { $commands = @("ddl", "datasource") }
    "model:postgres" { $commands = @("ddl", "datasource") }
    "model:postgresql" { $commands = @("ddl", "datasource") }
    "complete:handler" { $commands = @("bash", "zsh", "fish", "powershell", "pwsh") }
  }
  $commands |
    Where-Object { $_ -like "$wordToComplete*" }
}
`, nil
	default:
		return "", fmt.Errorf("unsupported completion shell %q", shell)
	}
}
