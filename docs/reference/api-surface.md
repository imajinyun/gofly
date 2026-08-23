# Reference: stable API surface

This page is the human index for adoption tiers. Detailed field contracts stay
in [cli-json-contracts.md](cli-json-contracts.md) and
[control-plane-contracts.md](control-plane-contracts.md). Generated layout
rules stay in [generated-service-layout.md](generated-service-layout.md).

## Stable adoption tiers

| Tier | Name | Stability |
| --- | --- | --- |
| Tier 0 | Golden path | `gofly new service --style production` layout, health, control-plane, and production-check scripts. Breaking changes need migration notes. |
| Tier 1 | Compatibility surfaces | API, RPC, model, and gateway generation plus CLI JSON envelopes. Additive fields are allowed; removals and type changes are breaking. |
| Tier 2 | Preview | AI templates, remote plugins, feature previews, custom mux sinks. Deterministic and path-safe, but allowed to change faster. |
| Tier 3 | Experiments | Report-only performance claims, oracle layout diffs, and RPC tier-1 promotion evidence. Not a product promise. |

## Command map

- Scaffold: `gofly quickstart`, `gofly new service --style production`
- REST: `gofly api gen`, `gofly api diff`, `gofly api breaking`
- RPC: `gofly rpc gen`, `gofly rpc protoc`, `gofly rpc check`
- Model: `gofly model mysql ddl`, `gofly model mysql datasource`
- Operators: `gofly doctor --json`, `gofly release check --strict`
- Agents: `gofly ai manifest --json`, `gofly ai control-plane --json`

Compatibility policy: [compatibility.md](compatibility.md).
