# Compatibility Policy

gofly is still pre-1.0, but production adopters need to know which surfaces are safe to build on. This policy defines the stability contract for v0.x releases.

## Stability levels

| Level | Meaning | Change policy |
| --- | --- | --- |
| Stable | Intended for application code and automation. | No removals or incompatible semantic changes within the same minor release line. Deprecations are documented for at least one minor release before removal. |
| Evolving | Supported, but still gaining features. | Additive changes may happen in patch releases. Breaking changes require changelog notes and migration instructions. |
| Experimental | Incubating APIs or generated assets. | May change between patch releases. Do not depend on these without pinning a version. |
| Internal | Implementation detail. | No compatibility promise. |

## Adoption tier policy

The [Stable API Surface Reference](api-surface.md) defines Tier 0 through Tier 3 adoption surfaces. Release notes must name any tier promotion, demotion, or breaking migration.

Rules:

- Tier 0 generated production services must keep compiling and running across patch releases.
- Tier 1 surfaces are the default automation and application contract. Breaking changes require a deprecation period, migration guide, and release note.
- Tier 2 surfaces may evolve, but a breaking change must include a changelog entry, explicit migration steps, and a generated-project or subsystem smoke test update.
- Tier 3 surfaces must be labeled experimental in docs, command help, generated files, or JSON fields.
- Any downgrade from a higher tier must state the affected surface, original tier, reason, user impact, and rollback or pinning guidance.

## Stable public surfaces

| Surface | Stability | Notes |
| --- | --- | --- |
| `rest` package | Stable | Server construction, route registration, context binding/response helpers, health, metrics, and OpenAPI hooks. |
| `rpc` package | Evolving | Lightweight RPC server/client, descriptors, governance hooks, and HTTP transport are supported; advanced compatibility adapters remain evolving. |
| `gateway` package | Evolving | Route-to-discovery wiring and downstream RPC routing are supported; policy expansion is additive. |
| `core/governance` package | Stable | Rule matching, timeout, retry, breaker, rate limit, concurrency, canary, and fallback policy shapes are stable JSON/runtime contracts. |
| `core/controlplane` package | Stable | Snapshot envelope, contributor model, and published runtime metadata shape are stable; new fields are additive. |
| `core/observability` package | Evolving | Metrics, tracing, and profiling helpers are supported; exporter-specific details may change. |
| `spi` package | Stable | Extension interfaces and generator plugin request/response structs are the public compatibility boundary for third-party extensions. |
| `cmd/gofly` CLI human output | Evolving | Human-readable text may change for clarity. Scripts should prefer JSON output where available. |
| `cmd/gofly` CLI JSON output | Stable | Existing fields are additive-only unless marked experimental in command help or docs. |
| Generated service layout | Evolving | Generated projects compile and run across patch releases. File names may receive additive changes. |
| Generated examples under `examples/` | Experimental | Examples are runnable contracts, but their directory layout may evolve while being split into standalone modules. |

## Plugin ecosystem compatibility

The external generator plugin protocol is versioned separately from Go packages. The current host protocol is `1`, while the public in-process SPI package uses the `gofly.spi.v1` contract name.

Registry entries for third-party plugins must include:

- `name`, `remote`, and `version` for reproducible discovery and install identity;
- `protocol` for host/plugin negotiation;
- `manifest.compatibleVersions` for old/current/future protocol compatibility checks;
- `manifest.capabilities` and `manifest.permissions` for auditable file and patch behavior;
- `checksum` in `sha256:<hex>` format for binary provenance;
- `source` for reviewable source or release metadata.

The copyable example at `examples/plugin-ecosystem` verifies the current registry contract, compatibility matrix, code-generation plugin output, post-generation patching output, and third-party template directory metadata.

## Experimental surfaces

- AI agent scaffolds and manifest extensions not listed in `docs/concepts/ai-manifest.md`.
- Kitex compatibility adapter files generated under compatibility profiles.
- Prototype plugin extensions outside the published SPI schema.
- Long-form examples and case-study code until each example has its own `go.mod` and smoke target.

## Deprecation and migration window

Stable and Tier 1 surfaces follow this deprecation process:

1. Mark the old API, JSON field, command flag, or generated asset with `Deprecated:` in Go docs, command help, or reference docs.
2. Add a migration note that names the replacement surface and the first release where both old and new forms coexist.
3. Keep the deprecated form for at least one minor release line unless it exposes a security issue.
4. Add or update a compatibility test, CLI JSON contract fixture, generated-service smoke test, or control-plane contract check before removal.
5. Record the removal in release notes with the last supported version and rollback guidance.

Security exceptions can shorten the window, but the release must state the risk, mitigation, and upgrade path.

## CLI and JSON compatibility rules

See [CLI JSON Contracts](cli-json-contracts.md) for the stable command list, JSON envelope, and field-level rules.

- New JSON fields may be added at any time.
- Existing JSON fields keep their type and meaning within a minor release line.
- Field removals, renames, or type changes must be detected by contract tests and documented as breaking.
- Human output is not a scripting contract unless the command explicitly documents it as such.

## Control-plane compatibility rules

See [Control-Plane Contracts](control-plane-contracts.md) for the snapshot, diff, consumer-action, and watch-event contracts.

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
