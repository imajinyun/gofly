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

## Gate

Run:

```sh
make deprecation-lifecycle-check
```

The gate validates:

- `activeDeprecations` entries are complete and unique;
- `deprecatedMarkers` match production Go files containing `Deprecated:`;
- active production Go deprecations cannot exist without lifecycle metadata;
- lifecycle docs, stable-surface docs, compatibility policy, release docs, and
  Makefile targets remain synchronized.

There are currently no active deprecations, so both `activeDeprecations` and
`deprecatedMarkers` are empty by design. Adding the first `Deprecated:` marker
must update the manifest in the same change.
