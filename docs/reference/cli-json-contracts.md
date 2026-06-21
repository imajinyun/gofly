# CLI JSON Contracts Reference

This reference defines the machine-readable CLI outputs that gofly treats as automation contracts. Human-readable output can change for clarity; scripts and agents should use `--json`, `--format json`, or the global `--output json` flag where documented.

## Stability rules

- Existing JSON fields keep their type and meaning within a minor release line.
- New fields are additive and may appear at any object level.
- Field removals, renames, or type changes require a breaking-change note and a migration path.
- Array ordering is stable only when this page names it as stable; otherwise consumers should key by identifiers such as `name`, `path`, `service`, or `method`.
- Errors emitted while `--output json` is active use the standard error envelope.

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

## Stable command contracts

| Command | JSON mode | Stable payload |
| --- | --- | --- |
| `gofly version --json` | Envelope | `tool`, `version`, `commit`, `built_at`, `go_version`, `goos`, `goarch` |
| `gofly doctor --json` | Raw object | `version`, `go`, `os`, `arch`, `checks`, `summary` |
| `gofly env --json`, `gofly env check --json` | Raw object | Environment/toolchain keys and check status objects. |
| `gofly bug --json` | Raw object | `tool`, `version`, `environment`, `checks` |
| `gofly upgrade --json` | Raw object | `command`, `target`, `module`, `version`, `execute`, `output` |
| `gofly release check --json` | Raw object | Release gate results, strictness, warnings, and failure summaries. |
| `gofly example list --json` | Raw object | Example names, descriptions, and runnable metadata. |
| `gofly feature list --json` | Raw object | Feature identifiers and descriptions. |
| `gofly feature run --format json` | Raw object | Planned/generated files, feature verification commands, config hints, and next actions. |
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

## Consumer guidance

1. Prefer explicit JSON flags over parsing text.
2. Treat missing optional fields as zero values.
3. Ignore unknown fields.
4. Compare semantic identifiers instead of array positions unless ordering is documented.
5. Pin gofly versions when depending on experimental fields or compatibility-profile-specific output.

Related policy: [Compatibility Policy](compatibility.md) and [Stable API Surface Reference](api-surface.md).
