# Compatibility policy

This policy covers public Go APIs, CLI flags, JSON envelopes, generated
layouts, and control-plane snapshots. Field contracts are in
[cli-json-contracts.md](cli-json-contracts.md) and
[control-plane-contracts.md](control-plane-contracts.md). Tiers are listed in
[api-surface.md](api-surface.md).

## Adoption tier policy

- **Tier 0** breaking changes require migration notes and
  `make generated-service-layout-check`.
- **Tier 1** may add JSON fields and generated files. Removing fields,
  renaming paths, or changing types needs a compatibility window.
- **Tier 2** preview surfaces may change faster but must stay path-safe and
  deterministic.
- **Tier 3** report-only evidence (RPC latency, oracle layout diffs) must not
  be described as product guarantees.

## Deprecation and migration window

Deprecated surfaces keep working for at least one minor release line after the
`Deprecated:` marker appears in godoc, CLI help, or release notes. During that
window:

1. Old flags and JSON fields still parse.
2. New fields are additive.
3. Generated projects on the previous profile still compile.

RPC tier-1 promotion and byte-for-byte goctl layout are outside this window
until their blocking gates say otherwise.
