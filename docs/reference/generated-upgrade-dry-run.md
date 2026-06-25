# Generated Upgrade Dry-Run

Schema: `gofly.generated_upgrade_dry_run.v1`

Generated-project compatibility already proves old, current, and future inputs
can generate and compile. The upgrade dry-run contract adds adopter-facing
evidence for upgrades: which fixtures represent each profile, which plugin
protocol profile is in scope, what generated snapshot metadata must remain
visible, and where temporary generated projects may be created. The
machine-readable manifest is
[`generated-upgrade-dry-run.json`](generated-upgrade-dry-run.json).

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

## Relationship To Generated Version Compatibility

`make generated-version-compat-check` remains the executable compatibility
matrix. The upgrade dry-run manifest reuses the same fixture roots so future
automation can compare a generated project before and after a gofly upgrade
without inventing a second source of truth.
