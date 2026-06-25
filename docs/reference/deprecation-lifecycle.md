# Deprecation Lifecycle

Schema: `gofly.deprecation_lifecycle.v1`

The deprecation lifecycle contract makes stable-surface compatibility
machine-checkable. The source of truth is
[`deprecation-lifecycle.json`](deprecation-lifecycle.json).

## Policy

Every stable or Tier 1 deprecation must include:

| Field | Meaning |
| --- | --- |
| `surface` | Public API, CLI JSON field, command flag, generated asset, or runtime JSON contract being changed. |
| `oldSurface` | The deprecated name or behavior. |
| `replacement` | The preferred surface that exists during the coexistence window. |
| `firstDeprecatedVersion` | First release where the old and new forms coexist. |
| `minimumRemovalVersion` | Earliest release where the old form can be removed. |
| `coexistenceWindow` | Must be `one minor release line` unless a security exception is documented. |
| `rollbackGuidance` | How adopters pin, revert, or keep using the old surface during migration. |
| `validationGate` | Contract test, smoke test, or compatibility gate proving the old and new behavior. |
| `releaseNoteClassification` | Must be `deprecation` for active deprecation entries. |

Security exceptions may shorten the window only when the manifest also records
`risk`, `mitigation`, and `upgradePath`.

## Support Lifecycle

The `supportLifecycle` section turns stable-surface policy into an adopter-facing
support contract. Each entry must name:

| Field | Meaning |
| --- | --- |
| `surface` | The API, JSON, generated-output, runtime, or package family covered by the support promise. |
| `tier` | The adoption tier that controls how conservative the change policy must be. |
| `owner` | The accountable maintainer group for compatibility decisions and release evidence. |
| `supportWindow` | How long the surface remains supported after a deprecation starts. |
| `compatibilityClass` | `stable` for Tier 0 or Tier 1 surfaces, `evolving` for Tier 2 promotion candidates. |
| `sunsetTrigger` | The evidence required before the old surface can be removed or demoted. |
| `releaseNoteEvidence` | The release document that must record compatibility impact and migration status. |
| `validationGate` | The local gate that proves the support promise for the surface. |
| `rollbackGuidance` | The pin, rollback, or migration fallback adopters can use if the new surface fails. |

Current support lifecycle entries cover the `rest` v1 candidate, governance and
control-plane contracts, CLI JSON, generated production service output, and the
`rpc`/`gateway`/`app` Tier 1 promotion candidates.

## Gate

Run:

```sh
make deprecation-lifecycle-check
```

The gate validates:

- `activeDeprecations` entries are complete and unique;
- `deprecatedMarkers` match production Go files containing `Deprecated:`;
- active production Go deprecations cannot exist without lifecycle metadata;
- `supportLifecycle` entries include owner, support window, compatibility
  class, sunset trigger, release-note evidence, validation gate, and rollback
  guidance;
- lifecycle docs, stable-surface docs, compatibility policy, release docs, and
  Makefile targets remain synchronized.

There are currently no active deprecations, so both `activeDeprecations` and
`deprecatedMarkers` are empty by design. Adding the first `Deprecated:` marker
must update the manifest in the same change.
