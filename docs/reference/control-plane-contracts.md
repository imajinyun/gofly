# Control-Plane Contracts Reference

This reference defines the stable JSON contract for runtime control-plane snapshots. The same snapshot model is used by the REST admin endpoint and by AI-facing CLI commands so operators, agents, and release gates can diff runtime state without parsing logs.

## Surfaces

| Surface | Contract | Notes |
| --- | --- | --- |
| `GET /admin/control-plane` | Stable JSON response in trusted admin networks. | Mounted by REST/admin integrations. |
| `gofly ai control-plane --json` | JSON envelope with snapshot, diff, consumer action, and guidance. | Can read local runtime metadata or remote admin endpoint snapshots. |
| `gofly ai control-plane --watch --json` | NDJSON envelope events. | Each line is a complete event envelope. |
| `gofly ai control-plane --schema jsonschema` | Raw JSON Schema. | Schema includes checksum metadata for tooling. |

## Snapshot object

The stable snapshot payload is represented by `controlplane.Snapshot`:

```json
{
  "version": "gofly-control-plane.v1",
  "checksum": "<sha256>",
  "services": [],
  "configs": {},
  "policies": [],
  "updatedAt": "2026-06-21T00:00:00Z",
  "metadata": {}
}
```

Stable fields:

| Field | Type | Stability | Notes |
| --- | --- | --- | --- |
| `version` | string | Stable | Snapshot schema/version identifier. Default is `gofly-control-plane.v1`. |
| `checksum` | string | Stable | SHA-256 checksum over canonical snapshot content. `updatedAt` is excluded. |
| `services` | array | Stable | Service discovery snapshot. New endpoint metadata keys are additive. |
| `configs` | object | Stable | JSON objects keyed by configuration source/name. Values must be valid JSON. |
| `policies` | array | Stable | Governance rules using the `core/governance` JSON shape. |
| `updatedAt` | string | Stable | RFC3339 timestamp for observation time; not part of checksum semantics. |
| `metadata` | object | Stable | String metadata for runtime capabilities and contributor state. |

### Service entries

```json
{
  "name": "orders",
  "endpoints": [
    {
      "address": "127.0.0.1:8080",
      "weight": 100,
      "zone": "local",
      "metadata": {}
    }
  ],
  "metadata": {}
}
```

Stable service fields are `name`, `endpoints`, and `metadata`. Stable endpoint fields are `address`, `weight`, `zone`, and `metadata`.

## CLI envelope payload

`gofly ai control-plane --json` uses the standard CLI envelope:

```json
{
  "ok": true,
  "command": "ai.control-plane",
  "data": {
    "source": "runtime",
    "snapshot": {},
    "diff": {},
    "consumerAction": {},
    "agentGuidance": [],
    "secretBoundary": "admin metadata masks credentials"
  }
}
```

Stable `data` fields:

| Field | Type | Stability | Notes |
| --- | --- | --- | --- |
| `source` | string | Stable | Snapshot source such as `runtime` or a remote admin URL. |
| `snapshot` | object | Stable | The snapshot object described above. |
| `diff` | object | Stable | Semantic diff against a previous checksum or snapshot. |
| `consumerAction` | object | Stable | Recommended consumer action for the diff. |
| `agentGuidance` | array | Stable | Safe next steps for AI/tool callers. |
| `secretBoundary` | string | Stable | Human-readable statement of masking/secret boundaries. |

Watch mode adds a stable `index` field and may include `error` when a bounded watch event fails.

## Diff object

```json
{
  "fromChecksum": "<sha256>",
  "toChecksum": "<sha256>",
  "changed": true,
  "changeType": "policy-change",
  "changedFields": ["policies"]
}
```

Stable fields are `fromChecksum`, `toChecksum`, `changed`, `changeType`, and `changedFields`.

Stable `changeType` values:

| Value | Meaning |
| --- | --- |
| `none` | Checksums match; no semantic change. |
| `initial-snapshot` | No previous checksum/snapshot was provided. |
| `checksum-mismatch` | Only checksum comparison was possible. |
| `config-change` | `configs` changed. |
| `service-discovery-change` | `services` changed. |
| `policy-change` | `policies` changed. |
| `metadata-change` | `metadata` changed. |
| `mixed-change` | Multiple semantic fields changed. |

## Consumer action object

```json
{
  "changeType": "policy-change",
  "action": "reload-governance-gates",
  "reason": "governance policy changed",
  "scopes": ["policy"],
  "requiresFullReconcile": false,
  "nextActions": []
}
```

Stable fields are `changeType`, `action`, `reason`, `scopes`, `requiresFullReconcile`, and `nextActions`. New action values are additive; consumers should fall back to full reconciliation for unknown non-`none` changes.

## Compatibility rules

- Top-level snapshot fields listed here are additive-only within a minor release line.
- Contributor-specific metadata keys may be added at any time but must not repurpose existing keys.
- `checksum` is stable for semantic content and intentionally ignores `updatedAt`.
- Sensitive values must be masked before publication. Tokens, passwords, API keys, cookies, and authorization headers must not appear in snapshots.
- Remote admin endpoints must stay behind the admin trust boundary and should use bearer-token protection outside local-only development.

Related policy: [Compatibility Policy](compatibility.md), [Stable API Surface Reference](api-surface.md), and [CLI JSON Contracts](cli-json-contracts.md).
