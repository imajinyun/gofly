package command

import "sort"

func buildAIToolManifestCommands() []aiToolCommand {
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
		manifestCommand("api cleanup stale", []string{"api clean stale"}, "Read .gofly/stale-api-files.json and report stale go-zero-compatible API/app files; deletes only when --execute is set.", "gofly api cleanup stale --dir <project> [--execute] [--json]", map[string]aiInputProperty{"dir": stringProperty("Project root containing .gofly/stale-api-files.json."), "execute": boolProperty("Delete validated stale .go files under internal/api/http, internal/app, or legacy handler/logic directories; omitted means report-only."), "json": boolProperty("Print JSON envelope.")}, []string{outputText, outputJSON}, []string{"reads .gofly/stale-api-files.json", "report-only by default", "--execute removes only validated stale .go files under internal/api/http, internal/app, or legacy handler/logic directories"}, "medium", true, true, []string{"gofly api cleanup stale --dir .", "gofly api cleanup stale --dir . --execute --json"}),
		manifestCommand("rpc check", nil, "Validate protobuf syntax and generator support.", "gofly rpc check --src <service.proto>", fileInputProperties("src"), []string{outputText}, []string{"reads protobuf file"}, "read", true, false, []string{"gofly rpc check --src greeter.proto"}),
		manifestCommand("rpc protoc", nil, "Run standard protoc Go plugins and optionally forward validated non-gofly plugin argv with explicit operator opt-in.", "gofly rpc protoc <service.proto> [--I <paths>] [--go_out <dir>] [--go-grpc_out <dir>] [--plugin <name>] [--allow-external-plugin] [--extra <args>]", map[string]aiInputProperty{
			"src":                   stringProperty("Protobuf source file; also accepted positionally."),
			"dir":                   stringProperty("Output directory used when go_out or go-grpc_out are omitted."),
			"I":                     stringProperty("Comma-separated proto include paths."),
			"proto_path":            stringProperty("Comma-separated proto include paths."),
			"go_out":                stringProperty("protoc-gen-go output directory."),
			"go-grpc_out":           stringProperty("protoc-gen-go-grpc output directory."),
			"plugin":                stringProperty("Optional protoc plugin name or path. Non-gofly plugins are not forwarded unless allow-external-plugin is true."),
			"allow-external-plugin": boolProperty("Explicit opt-in to forward validated non-gofly --plugin argv to protoc."),
			"extra":                 stringProperty("Comma-separated extra protoc arguments such as plugin-specific *_out flags."),
			"timeout":               stringProperty("Maximum protoc execution time as a Go duration."),
		}, []string{outputText}, []string{"runs protoc as an external process", "forwards non-gofly plugin argv only when --allow-external-plugin is set", "rejects flag-like plugin values, URL schemes, whitespace, control characters and shell metacharacters", "does not execute shell strings"}, "high", false, true, []string{"gofly rpc protoc greeter.proto -I . --go_out . --go-grpc_out .", "gofly rpc protoc greeter.proto --plugin protoc-gen-openapi --allow-external-plugin --extra --openapi_out=."}),
		manifestCommand("rpc descriptor", nil, "Compare runtime RPC descriptors for compatibility.", "gofly rpc descriptor --base <old-descriptor.json|url> --target <new-descriptor.json|url> [--format text|json]", map[string]aiInputProperty{"base": stringProperty("Old descriptor file, descriptor URL, or admin URL."), "target": stringProperty("New descriptor file or URL."), "format": enumStringProperty("Output format.", outputText, outputJSON)}, []string{outputText, outputJSON}, []string{"reads local files or fetches descriptor URLs when URL inputs are used"}, "medium", true, false, []string{"gofly rpc descriptor --base old.json --target new.json --format json"}),
		manifestCommand("gateway profile validate", []string{"gateway profile diff"}, "Validate a candidate gateway transcode profile against current config and report additive or breaking mapping contract changes.", "gofly gateway profile validate --config <gateway.json> --candidate <profile.json> [--format text|json] [--json]", map[string]aiInputProperty{"config": stringProperty("Gateway config JSON containing transcodeProfiles."), "candidate": stringProperty("Candidate transcode profile JSON."), "format": enumStringProperty("Output format.", outputText, outputJSON), "json": boolProperty("Print JSON envelope.")}, []string{outputText, outputJSON}, []string{"reads gateway config and candidate profile files"}, "read", true, false, []string{"gofly gateway profile validate --config etc/edge.json --candidate etc/edge-profile-candidate.json --json"}),
		manifestCommand("gateway aggregation validate", []string{"gateway aggregation diff"}, "Validate a candidate BFF aggregation contract against current gateway config or compare old/new OpenAPI x-gofly-aggregation contracts.", "gofly gateway aggregation validate --config <gateway.json> --route <route> --candidate <aggregation.json> [--openapi-base old.json --openapi-candidate new.json] [--format text|markdown|sarif|json] [--json]", map[string]aiInputProperty{"config": stringProperty("Gateway config JSON containing routes."), "route": stringProperty("Route name or route key to compare."), "candidate": stringProperty("Candidate aggregation JSON."), "openapi-base": stringProperty("Base OpenAPI JSON containing x-gofly-aggregation."), "openapi-candidate": stringProperty("Candidate OpenAPI JSON containing x-gofly-aggregation."), "format": enumStringProperty("Output format.", outputText, "markdown", "sarif", outputJSON), "json": boolProperty("Print JSON envelope.")}, []string{outputText, "markdown", "sarif", outputJSON}, []string{"reads gateway config and candidate aggregation files", "or reads base/candidate OpenAPI files"}, "read", true, false, []string{"gofly gateway aggregation validate --config etc/edge.json --route bff-home --candidate etc/edge-aggregation-candidate.json --json", "gofly gateway aggregation validate --openapi-base old-openapi.json --openapi-candidate new-openapi.json --route home --format markdown", "gofly gateway aggregation validate --openapi-base old-openapi.json --openapi-candidate new-openapi.json --route home --format sarif"}),
		manifestCommand("release check", nil, "Run release readiness checks and report machine-readable release gate evidence.", "gofly release check [--api-base <old.api> --api-target <new.api>] [--rpc-base <old.proto> --rpc-target <new.proto>] [--changelog CHANGELOG.md] [--evidence <check>] [--strict] [--json]", map[string]aiInputProperty{"api-base": stringProperty("Base .api file for breaking detection."), "api-target": stringProperty("Target .api file for breaking detection."), "rpc-base": stringProperty("Base protobuf file for breaking detection."), "rpc-target": stringProperty("Target protobuf file for breaking detection."), "changelog": stringProperty("Changelog file to inspect."), "evidence": stringProperty("Optional check name to return only that check's evidence."), "strict": boolProperty("Treat warnings as blockers."), "json": boolProperty("Print JSON envelope.")}, []string{outputText, outputJSON}, []string{"reads local contract, changelog, module, generated gateway evidence files", "reads RPC mux adapter report-only evidence", "reads generated RPC mux retry smoke evidence"}, "read", true, false, []string{"gofly release check --json", "gofly release check --json --evidence gateway-aggregation-contract", "gofly release check --json --evidence rpc-mux-adapter-evidence", "gofly release check --json --evidence generated-rpc-mux-retry-smoke"}),
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
		case "gateway profile validate":
			commands[i].OutputContract = gatewayProfileValidateOutputContract()
		case "gateway aggregation validate":
			commands[i].OutputContract = gatewayAggregationValidateOutputContract()
		case "release check":
			commands[i].OutputContract = releaseCheckOutputContract()
		}
	}

	for _, spec := range rootCommandManifestEntries() {
		commands = append(commands, manifestCommand(spec.Name, spec.Aliases, spec.Short, "gofly "+spec.Name+" [arguments]", nil, []string{outputText, outputJSON}, []string{"see command-specific manifest entries and help"}, inferTopLevelRisk(spec.Name), false, topLevelMayMutate(spec.Name), nil))
	}
	sort.SliceStable(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return commands
}
