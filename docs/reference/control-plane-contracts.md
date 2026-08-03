# Control-Plane Contracts

This reference defines the stable control-plane fields consumed by operators,
release checks, generated smoke tests, and AI agents.

## Runtime Endpoint

Generated production services expose the runtime snapshot at:

```text
GET /admin/control-plane
```

The response is a `gofly-control-plane.v1` snapshot with stable `version`,
`checksum`, `services`, `configs`, `policies`, and `metadata` fields. Secret
values must not be copied into this endpoint; callers should use it for runtime
state, schema discovery, and drift detection only.

## Generated RPC Mux Warning Contract

Generated services expose the RPC mux warning schema directly in:

```text
configs.generated.rpcMuxConfigWarningSchema
```

The schema id is `gofly.rpc_mux_config_warning.v1`. It describes warning objects
with `schema`, `field`, `message`, `current`, and `recommended` fields.

Warnings are exposed through:

```text
configs.generated.rpcMuxConfigWarnings
```

Compatibility note: this field remains `[]string` during the current
compatibility window. Each string is a JSON object conforming to
`gofly.rpc_mux_config_warning.v1`. This keeps existing `[]string` consumers
working while allowing new callers to decode each entry as a structured warning
object. A future breaking-version boundary may promote this field to `[]object`
after release notes and support-bundle guidance name the migration path.

When RPC mux config is within recommended bounds,
`configs.generated.rpcMuxConfigWarnings` is omitted. The schema remains present
so callers do not have to infer the contract from docs or source code.

## Schema Checksums

Generated services expose schema drift evidence in:

```text
configs.generated.controlPlaneSchemaChecksums
```

The map currently carries:

```text
generated.rpcMuxConfigWarningSchema
generated.rpcMuxOperatorAuditSchemas
aiManifestSchema
```

Callers should use these checksum values to detect schema drift before comparing
warning payloads, audit schemas, or generated support-bundle guidance.

## Support Bundle Fields

`gofly bug --json` points users to include generated control-plane evidence from
`/admin/control-plane` after redaction. A useful support bundle for generated RPC
mux warning issues includes:

```text
supportBundle
supportBundle.redaction
configs.generated.rpcMuxConfigWarningSchema
configs.generated.rpcMuxConfigWarnings
configs.generated.controlPlaneSchemaChecksums
```

## Diff Object

Diffs produced by `gofly ai control-plane --from-snapshot` compare stable
checksums and report changed fields. A schema-only change should appear as a
config change naming `configs.generated.controlPlaneSchemaChecksums` or the
specific schema config key.

## Consumer Action Object

Consumers should treat a warning-schema checksum change as a reason to refresh
their parser or generated support-bundle adapter. They should not infer secret
values from `configs` or `metadata`; the control-plane `secretBoundary` remains
the source of truth for this rule.
