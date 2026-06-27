# CLI JSON Contracts Reference

This reference defines the machine-readable CLI outputs that gofly treats as automation contracts. Human-readable output can change for clarity; scripts and agents should use `--json`, `--format json`, or the global `--output json` flag where documented.

The governed command surface is tracked in
[`cli-command-surface.json`](cli-command-surface.json). That manifest maps
registered command families, aliases, help topics, JSON contract surfaces, and
follow-up CLI governance tasks.

The executable golden set is tracked in
[`cli-json-contract-goldens.json`](cli-json-contract-goldens.json). That
manifest lists the release-blocking JSON cases and the gate that keeps stdout
JSON-only for machine consumers.

The command surface manifest also records closed CLI governance work. The
`stdio-error-discipline` closeout links the P9-3 aiflow subtasks to tests that
protect stdout/stderr separation, stable usage exit code `2`, and
`USAGE_ERROR` JSON envelopes for flag parsing failures.

## Stability rules

- Existing JSON fields keep their type and meaning within a minor release line.
- New fields are additive and may appear at any object level.
- Field removals, renames, or type changes require a breaking-change note and a migration path.
- Array ordering is stable only when this page names it as stable; otherwise consumers should key by identifiers such as `name`, `path`, `service`, or `method`.
- Errors emitted while `--output json` is active use the standard error envelope.
- Flag parsing failures, including unknown flags and missing flag values, are
  usage errors: text mode writes the diagnostic to stderr and global JSON mode
  writes a single `USAGE_ERROR` envelope to stdout.

## Standard envelope

Commands that opt into the gofly JSON envelope use this shape:

```json
{
  "ok": true,
  "command": "version",
  "data": {}
}
```

Stable fields:

| Field | Type | Stability | Notes |
| --- | --- | --- | --- |
| `ok` | boolean | Stable | `true` for successful command execution. |
| `command` | string | Stable | Canonical command name, such as `version` or `ai.control-plane`. |
| `data` | object | Stable | Command-specific payload. New nested fields are additive. |

Standard error envelope under global JSON mode:

```json
{
  "ok": false,
  "command": "api check",
  "error": {
    "code": "usage",
    "message": "api file is required"
  }
}
```

Stable error fields are `ok`, `command`, `error.code`, and `error.message`.

## Scaffold plan contract

Generator-facing commands that write projects or generated artifacts expose the
same planning fields whether the command is applied or dry-run:

| Field | Type | Stability | Notes |
| --- | --- | --- | --- |
| `command` | string | Stable | Canonical plan command, for example `new api`, `rpc gen`, or `model gen`. |
| `dryRun` | boolean | Stable | `true` means no filesystem mutation was performed by this command. |
| `mutatesFilesystem` | boolean | Stable | Signals whether applying the plan writes files. |
| `inputs` | object | Stable keys are command-specific | Includes normalized CLI inputs such as `dir`, `module`, `style`, `profile`, and contract paths. |
| `actions` | array | Stable item fields | Each item has `operation`, `target`, `description`, and `riskLevel`. |
| `generatedFiles` | integer | Stable | Count of generated files visible under the output boundary after an applied command; `0` is valid for dry-run or no-output plans. |
| `pluginEffects` | array | Stable item fields | Present when plugins are configured. Each item has `name`, `executed`, `files`, `patches`, and optional `note`. |
| `warnings` | array | Additive | Non-blocking automation notes, including dry-run plugin or remote-template limitations. |
| `nextActions` | array | Additive | Suggested follow-up commands for humans or automation. |

## Stable command contracts

| Command | JSON mode | Stable payload |
| --- | --- | --- |
| `gofly version --json` | Envelope | `tool`, `version`, `commit`, `built_at`, `go_version`, `goos`, `goarch` |
| `gofly doctor --json` | Raw object | `version`, `go`, `os`, `arch`, `checks`, `summary`, `nextActions`; warn/fail checks include `fix_hint` and/or `nextActions`. |
| `gofly env --json`, `gofly env check --json` | Raw object | Environment/toolchain keys and check status objects. |
| `gofly bug --json` | Raw object | `tool`, `version`, `environment`, `checks`, `supportBundle`, `nextActions`. `supportBundle.schema` is `gofly.support_bundle.v1`. |
| `gofly upgrade --json` | Raw object | `command`, `target`, `module`, `version`, `execute`, `output` |
| `gofly release check --json` | Envelope | Release gate results, strictness, warnings, failure summaries, and structured `error.remediation` on blockers. |
| `gofly example list --json` | Raw object | Example names, descriptions, and runnable metadata. |
| `gofly feature list --json` | Raw object | Feature identifiers and descriptions. |
| `gofly feature run --format json` | Raw object | Planned/generated files, feature verification commands, config hints, and next actions. |
| `gofly new service --json`, `gofly new api --json`, `gofly new rpc --json` | Envelope | `command`, `dryRun`, `mutatesFilesystem`, `inputs`, `actions`, `generatedFiles`, `pluginEffects`, `warnings`, `nextActions`. |
| `gofly model gen --json` | Envelope | `command`, `dryRun`, `mutatesFilesystem`, `inputs`, `actions`, `generatedFiles`, `nextActions`. |
| `gofly plugin list --json` | Raw object | Built-in and cached plugin inventory. |
| `gofly plugin search --json` | Raw object | Registry search results and plugin metadata. |
| `gofly plugin install --json` | Raw object | Version-pinned remote identity, binary path, and digest metadata. |
| `gofly plugin uninstall --json` | Raw object | Removed plugin identity and status. |
| `gofly plugin run --json` | Raw object | Plugin execution summary, generated files, verification commands, and errors. |
| `gofly ai manifest --json` | Envelope | Tool manifest, schemas, side effects, governance metadata, and command descriptors. |
| `gofly ai manifest --schema jsonschema` | Raw schema | JSON Schema with `$schema`, `$id`, `title`, `xSchemaChecksum`, `properties`, `required`. |
| `gofly ai doctor --json` | Envelope | `version`, `providers`, `envVars`, `secrets`, `failover`, `config`, `cache`, `telemetry`, `cost`, `summary`. |
| `gofly ai plan --json` | Envelope | Dry-run project plan, chosen template, actions, warnings, and next actions. |
| `gofly ai new --json` | Envelope | Plan/apply summary, generated features, config hints, verification commands, and verification results. |
| `gofly ai complete --json` | Envelope | Governance plan or completion result, token budget decisions, provider status, and audit-safe metadata. |
| `gofly ai stream --json` | NDJSON envelopes | Stream events; each line is an independent JSON envelope. |
| `gofly ai control-plane --json` | Envelope | See [Control-Plane Contracts](control-plane-contracts.md). |
| `gofly ai control-plane --schema jsonschema` | Raw schema | Control-plane JSON Schema with checksum metadata. |

## API command JSON outputs

| Command | Stable fields | Notes |
| --- | --- | --- |
| `gofly api route --format json` | Route entries with service, method, path, handler, and middleware metadata. | Consumers should key by `method` + `path`. |
| `gofly api diff --format json` | Changed routes/types, compatibility classification, and summaries. | `--base` and `--target` identify compared API files. |
| `gofly api breaking` with JSON/global JSON mode | Breaking-change report and incompatible route/type changes. | Exit code remains non-zero when breaking changes are detected. |
| `gofly api doc --json` or `--format json` | OpenAPI JSON. | OpenAPI fields follow the generated OpenAPI version. |

Validation commands such as `api check` have stable exit codes and human text, but no stable success JSON payload unless global JSON error reporting is active.

## RPC command JSON outputs

| Command | Stable fields | Notes |
| --- | --- | --- |
| `gofly rpc idl --format json` | IDL report: syntax, imports/includes, services, methods, messages, enums. | Consumers should key methods by service + method. |
| `gofly rpc deps --format json` | Same IDL report with imports/includes populated. | Text mode remains human-oriented. |
| `gofly rpc breaking --format json` | Breaking-change report for protobuf services/messages/enums. | Exit code remains non-zero for incompatible changes. |
| `gofly rpc descriptor --format json` | Descriptor diff and compatibility classification. | Remote descriptor sources may be file or URL based. |
| `gofly rpc doc --json` or `--format openapi` | OpenAPI JSON generated from protobuf HTTP transcoding metadata. | YAML and markdown outputs are not JSON contracts. |

Validation commands such as `rpc check` and `rpc lint` have stable exit codes and human text, but no stable success JSON payload unless global JSON error reporting is active.

## Troubleshooting and support bundle contract

`gofly doctor --json`, `gofly release check --json --strict`, and
`gofly bug --json` form the stable troubleshooting loop for CI and support
automation. The contract is intentionally additive: consumers should preserve
unknown fields and key checks by `name`.

The machine-readable product surface is
[`dx-support-bundle.json`](dx-support-bundle.json), schema
`gofly.dx_support_bundle.v1`.

`gofly bug --json` is the support bundle entry point. It emits
`supportBundle.schema: "gofly.support_bundle.v1"`, a redaction policy, and the
commands an adopter should attach when reporting a local generation, runtime, or
release failure. The bundle must never include secrets; redact Authorization,
Cookie, Set-Cookie, token, secret, password, and provider credential values
before sharing.

Generated project verification failures use the
`gofly.generated_project_failure_report.v1` shape: command, status, bounded
output, error, and next actions. This lets CI and agents tell users which
verification command failed and which rerun command is safe. Verification
output is bounded to 4096 bytes, status values are `passed`, `failed`, or
`skipped`, and rerun guidance is carried by the `nextActions` field so support
automation can attach the failure report without scraping terminal text.
The product surface records these as `outputLimitBytes: 4096` and
`rerunGuidanceField: "nextActions"` in
[`dx-support-bundle.json`](dx-support-bundle.json).

The DX troubleshooting gate verifies this contract with real CLI output:

```sh
make dx-troubleshooting-check
```

## Consumer guidance

1. Prefer explicit JSON flags over parsing text.
2. Treat missing optional fields as zero values.
3. Ignore unknown fields.
4. Compare semantic identifiers instead of array positions unless ordering is documented.
5. Pin gofly versions when depending on experimental fields or compatibility-profile-specific output.

Related policy: [Compatibility Policy](compatibility.md) and [Stable API Surface Reference](api-surface.md).
