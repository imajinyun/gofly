# Generated Upgrade Dry-Run

Schema: `gofly.generated_upgrade_dry_run.v1`

Generated-project compatibility already proves old, current, and future inputs
can generate and compile. The upgrade dry-run contract adds adopter-facing
evidence for upgrades: which fixtures represent each profile, which plugin
protocol profile is in scope, what generated snapshot metadata must remain
visible, and where temporary generated projects may be created. The
machine-readable manifest is
[`generated-upgrade-dry-run.json`](generated-upgrade-dry-run.json).

Validation:

```bash
make generated-upgrade-dry-run-check
```

## Fixture Profiles

| Profile | API | Proto | Service config | Plugin profile | Snapshot expectation |
| --- | --- | --- | --- | --- | --- |
| old | `testdata/generated-compat/v0.1/orders.api` | `testdata/generated-compat/v0.1/greeter.proto` | `testdata/generated-compat/v0.1/service-config.json` | legacy-safe protocol `1` | additive files and compatibility shims only |
| current | `testdata/generated-compat/current/orders.api` | `testdata/generated-compat/current/greeter.proto` | `testdata/generated-compat/current/service-config.json` | current protocol `1` | deterministic regeneration with no unexplained diff |
| future | `testdata/generated-compat/future/orders.api` | `testdata/generated-compat/future/greeter.proto` | `testdata/generated-compat/future/service-config.json` | future-tolerant protocol `1` | explainable diffs and safe ignored unsupported fields |

## Artifact Boundary

Upgrade dry-runs must write generated projects only to temporary directories such
as `.tmp-test/generated-upgrade-dry-run` or `$TMPDIR/gofly-generated-upgrade-*`.
Generated runtime project trees are volatile evidence and must not be committed.

Durable evidence should stay limited to:

- profile fixture paths;
- plugin registry/profile metadata;
- generated snapshot metadata expectations;
- explainable diff and rollback reports added by later gates.

## Explainable Diff Report

Every upgrade dry-run report must classify generated output changes with the
`diffReportContract.categories` values from the manifest:

| Category | Meaning | Release handling |
| --- | --- | --- |
| `deterministic-repeat-generation` | The same profile generated twice has no content diff after volatile paths and timestamps are normalized. | Required pass/fail evidence for every profile. |
| `compatible-addition` | Generated files, fields, handlers, comments, or metadata were added without deleting or changing an existing public contract. | Review and keep a rollback note. |
| `formatting-only` | The diff is limited to gofmt, imports, whitespace, or generated comment normalization. | Review the normalized diff and confirm no semantic token changed. |
| `breaking-candidate` | Files, public symbols, JSON/OpenAPI/proto fields, config keys, or plugin protocol behavior were deleted, renamed, or changed. | Block release until migrated, reverted, or explicitly accepted as a breaking version boundary. |

Each profile entry must include a `diffReport` object with:

- `repeatGeneration`: the deterministic generation requirement;
- `categories`: the accepted categories for that profile;
- `summary`: the human-readable review expectation;
- `rollbackNote`: the action an adopter can take when the generated output is
  not acceptable.

Adopters should review upgrade output in this order:

1. Generate the same profile twice and confirm the
   `deterministic-repeat-generation` category passes.
2. Compare the previous and upgraded generated project snapshots.
3. Classify every file diff into one of the manifest categories.
4. Treat any unclassified diff as a `breaking-candidate` until a migration note
   or rollback note explains it.

Profile-specific rollback notes are part of the manifest so generated-upgrade
automation can surface the next action without requiring users to read the full
roadmap.

## Relationship To Generated Version Compatibility

`make generated-version-compat-check` remains the executable compatibility
matrix. The upgrade dry-run manifest reuses the same fixture roots so future
automation can compare a generated project before and after a gofly upgrade
without inventing a second source of truth.
