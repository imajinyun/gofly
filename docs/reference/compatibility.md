# Compatibility Policy

gofly is still pre-1.0, but production adopters need to know which surfaces are safe to build on. This policy defines the stability contract for v0.x releases.

## Stability levels

| Level | Meaning | Change policy |
| --- | --- | --- |
| Stable | Intended for application code and automation. | No removals or incompatible semantic changes within the same minor release line. Deprecations are documented for at least one minor release before removal. |
| Evolving | Supported, but still gaining features. | Additive changes may happen in patch releases. Breaking changes require changelog notes and migration instructions. |
| Experimental | Incubating APIs or generated assets. | May change between patch releases. Do not depend on these without pinning a version. |
| Internal | Implementation detail. | No compatibility promise. |

## Stable public surfaces

| Surface | Stability | Notes |
| --- | --- | --- |
| `rest` package | Stable | Server construction, route registration, context binding/response helpers, health, metrics, and OpenAPI hooks. |
| `rpc` package | Evolving | Lightweight RPC server/client, descriptors, governance hooks, and HTTP transport are supported; advanced compatibility adapters remain evolving. |
| `gateway` package | Evolving | Route-to-discovery wiring and downstream RPC routing are supported; policy expansion is additive. |
| `core/governance` package | Stable | Rule matching, timeout, retry, breaker, rate limit, concurrency, canary, and fallback policy shapes are stable JSON/runtime contracts. |
| `core/controlplane` package | Stable | Snapshot envelope, contributor model, and published runtime metadata shape are stable; new fields are additive. |
| `core/observability` package | Evolving | Metrics, tracing, and profiling helpers are supported; exporter-specific details may change. |
| `cmd/gofly` CLI human output | Evolving | Human-readable text may change for clarity. Scripts should prefer JSON output where available. |
| `cmd/gofly` CLI JSON output | Stable | Existing fields are additive-only unless marked experimental in command help or docs. |
| Generated service layout | Evolving | Generated projects compile and run across patch releases. File names may receive additive changes. |
| Generated examples under `examples/` | Experimental | Examples are runnable contracts, but their directory layout may evolve while being split into standalone modules. |

## Experimental surfaces

- AI agent scaffolds and manifest extensions not listed in `docs/concepts/ai-manifest.md`.
- Kitex compatibility adapter files generated under compatibility profiles.
- Prototype plugin extensions outside the published SPI schema.
- Long-form examples and case-study code until each example has its own `go.mod` and smoke target.

## CLI and JSON compatibility rules

- New JSON fields may be added at any time.
- Existing JSON fields keep their type and meaning within a minor release line.
- Field removals, renames, or type changes must be detected by contract tests and documented as breaking.
- Human output is not a scripting contract unless the command explicitly documents it as such.

## Control-plane compatibility rules

- `/admin/control-plane` keeps a stable top-level envelope for service identity, runtime status, contributors, and diagnostics.
- Contributors may add new keys, but should not repurpose existing keys.
- Policy and descriptor contributors must preserve method/service identifiers so agents can diff snapshots across deploys.

## Package classification checklist

Before marking a package stable:

1. It has direct tests for nil/zero-value behavior and error paths.
2. It has at least one documented example or generated service path using it.
3. Its JSON or public Go API has compatibility tests or clear changelog coverage.
4. It avoids exposing types from experimental dependencies as required API parameters.

## v0.x promise

Within v0.x, gofly optimizes for fast product learning while keeping production-facing contracts predictable. Stable surfaces receive migration notes before breaking changes. Experimental surfaces are intentionally labeled so teams can decide whether to pin, wrap, or avoid them.
