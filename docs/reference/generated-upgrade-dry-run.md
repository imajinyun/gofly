# Generated Upgrade Dry-Run

Schema: `gofly.generated_upgrade_dry_run.v1`

Generated-project compatibility already proves old, current, and future inputs
can generate and compile. The upgrade dry-run contract adds adopter-facing
evidence for upgrades: which fixtures represent each profile, which plugin
protocol profile is in scope, what generated snapshot metadata must remain
visible, and where temporary generated projects may be created. The
machine-readable manifest is
[`generated-upgrade-dry-run.json`](generated-upgrade-dry-run.json).
Framework-specific migration fidelity evidence lives in
[`migration-fidelity-matrix.json`](migration-fidelity-matrix.json) and is
validated by the same gate.
Long-term scaffold compatibility is captured in
[`generated-scaffold-long-term-compatibility.json`](generated-scaffold-long-term-compatibility.json)
with schema `gofly.generated_scaffold_long_term_compatibility.v1`. It binds
old, current, and future generated profiles to go-zero and Kratos scaffold
expectations, goctl-compatible edge cases, diff classification, generated
project dependency boundaries, temporary-project smoke, and rollback actions.

Validation:

```bash
make generated-upgrade-dry-run-check
```

## Goctl-compatible generator matrix

The goctl-alignment contract lives at
[`docs/reference/goctl-generator-compatibility.json`](goctl-generator-compatibility.json)
and is checked by:

```bash
make goctl-generator-compat-check
```

The matrix records the implemented `gozero-compatible` scaffold profile,
accepted goctl-style flags such as `name-from-filename`, `go_opt`,
`go-grpc_opt`, and `go_grpc_opt`, API tooling compatibility for `api format`,
`api import`, `api route`, and `api diff`, generated-version fixtures, upgrade
diff categories, and route layout boundaries. It also records compatibility
boundaries that must not drift: do not add unrelated JSON envelope flags, do
not change `api route` or `api diff` format validation semantics, keep plugin
and middleware positional arguments as names, and keep generated project
dependencies out of the root module.

The upgrade dry-run manifest embeds this matrix through the
`goctlGeneratorCompatibility` section. That section makes the goctl-compatible
surface part of the upgrade rehearsal rather than a standalone checklist: a
dry-run must fail when the `gozero-compatible` profile, goctl-style flags, API
tooling, route layout, generated-version fixtures, or generated dependency
boundaries drift from the compatibility matrix.

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
- profile dependency policy: generated-project-only dependencies must stay in
  the generated module or an isolated temporary test module and must never be
  added to the gofly root module;
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

Each profile entry must also include a `dependencyPolicy` object with:

- `owner`: always `generated-project-dependencies`;
- `allowedLocation`: `generated project go.mod or isolated temporary test
  module`;
- `rootModulePolicy`: `must-not-add-generated-only-dependencies`;
- `verificationGates`: a generated compatibility gate plus a dependency
  boundary gate such as `make root-dependency-policy-check` or
  `make dependency-upgrade-evidence-check`;
- `rollbackOrEscalation`: the action to take if generated dependencies escape
  into the root module or if the generated project fails its compile smoke.

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

## Upgrade Rehearsal

The `upgradeRehearsal` section defines the adopter upgrade path as a sequence of
machine-checkable steps:

| Step | Phase | Gate |
| --- | --- | --- |
| `inventory-current-project` | baseline | `make generated-version-compat-check` |
| `regenerate-dry-run` | generation | `make generated-upgrade-dry-run-check` |
| `goctl-compatibility-review` | generation | `make goctl-generator-compat-check` |
| `dependency-boundary-review` | dependency | `make dependency-upgrade-evidence-check` |
| `release-evidence-review` | release | `make governance-report-check` |
| `adopter-smoke-and-rollback` | verification | `make framework-gap-check` |

Each step records evidence files, expected output, failure class, and
`rollbackOrEscalation` so an adopter can rehearse an upgrade without committing
generated runtime artifacts.

## Relationship To Generated Version Compatibility

`make generated-version-compat-check` remains the executable compatibility
matrix. The upgrade dry-run manifest reuses the same fixture roots so future
automation can compare a generated project before and after a gofly upgrade
without inventing a second source of truth.

The P9 historical fixture matrix is captured in the
`p9HistoricalFixtureMatrix` section of
[`generated-upgrade-dry-run.json`](generated-upgrade-dry-run.json). It makes the
old, current, and future fixture profiles release-blocking evidence rather than
only a smoke exercise. For each profile, the gate must create temporary
generated projects, apply the local gofly module replacement, run `go test
./...`, generate the same profile twice, require a clean `diff -ru`, and emit a
`gofly.generated_version_compat_report.v1` report with generated file count,
test status, repeat diff status, and expected diff explanation. Runtime reports
and generated project directories remain temporary evidence and must not be
committed.

The P10 goctl generator fidelity closeout is captured in
`p10GoctlGeneratorFidelity`. It links the gozero-compatible scaffold profile,
goctl-style flags, `.api` import/route/diff behavior, proto import handling,
alias collision boundaries, repeat-diff classification, generated dependency
boundaries, and rollback notes into one blocking contract. Promotion requires
`make generated-upgrade-dry-run-check` plus the goctl compatibility gate, and
runtime generated projects must remain volatile evidence under `.tmp-test` or
temporary directories.

The P11 live upgrade proof is captured in `p11LiveUpgradeProof`. It keeps the
old, current, and future profiles tied to a realistic adopter upgrade path:
generate a temporary service project, apply the local module replacement inside
that generated project only, run `go test ./...`, generate the same profile
twice, require a clean repeat diff, classify adopter-facing diffs, and keep
generated-only dependencies out of the root module. P11 does not commit live
generated projects or reports; those artifacts remain runtime evidence under
ignored temporary paths.

The P12 real branch replay contract is captured in `p12RealBranchReplay`. It
raises the dry-run expectation from temporary project generation to a traceable
adopter branch replay: record the repository, branch, base commit, selected
profile, generator version, previous generated snapshot, replay worktree,
classified diff report, smoke result, and rollback action before template
promotion. The replay must run in a temporary worktree under
`GENERATED_VERSION_COMPAT_TMPDIR` or the local temp directory, must keep generated
runtime artifacts out of the gofly repository, and must treat unclassified branch
diffs as `breaking-candidate` until a migration note or rollback note explains
the change. Branch worktrees, generated projects, replay reports, and diff
outputs remain ignored runtime evidence and must never be committed.

The P13 goctl generator maturity closeout is captured in
`p13GoctlGeneratorMaturity`. It turns the goctl-level generator claim into a
blocking contract across `.api` import, `.api` format, `.api` validation,
historical old/current/future fixtures, real adopter branch replay,
multi-language client generation, generated project `go test ./...`, clean
repeat-generation diff classification, and root module dependency hygiene. The
section cross-checks the goctl compatibility capability matrix, P12 branch replay
minimum fields, diff categories, generated version report fields, and generated
dependency boundary before generator maturity can be promoted. P13 runtime
worktrees, generated projects, reports, and diff outputs remain ignored evidence
under `.tmp-test`, `GENERATED_VERSION_COMPAT_TMPDIR`, or the local temp
directory and must never be committed.

## Migration Fidelity Matrix

The migration fidelity matrix ties generated upgrade expectations to adopter
paths that teams compare against Gin, go-zero, Kratos, and Kitex. Each path must
declare:

- a runnable example directory;
- comparison or case-study documentation;
- the generated dry-run profile used as the upgrade fixture;
- accepted diff categories from `diffReportContract.categories`;
- smoke gates that validate the path;
- a rollback note and a compatibility caveat.

The current paths are:

| Framework | Example | Dry-run profile | Primary gates |
| --- | --- | --- | --- |
| Gin | `examples/restserver` | `current` | `go test -C examples/restserver ./...`, `make migration-docs-check` |
| go-zero | `examples/production-orders` | `old` | `make generated-version-compat-check`, `make reference-app-smoke` |
| Kratos | `examples/microshop` | `current` | `go test -C examples/microshop ./...`, `make adopter-decision-check` |
| Kitex | `examples/rpc-idl-matrix` | `future` | `go test -C examples/rpc-idl-matrix ./...`, `make rpc-boundary-check` |

Any new migration path must be added to the matrix before docs can claim it as
adopter-ready. The gate checks that examples and docs exist, dry-run profiles are
valid, every path includes deterministic regeneration, and rollback guidance is
present.

## Long-Term Scaffold Compatibility

The long-term compatibility contract keeps generated scaffold trust aligned with
the executable version matrix:

- `old`, `current`, and `future` profiles must all keep fixture paths, expected
  diffs, required diff categories, smoke gates, and rollback notes;
- go-zero alignment is represented by the `gozero-compatible` profile and
  goctl-style command compatibility;
- Kratos alignment stays focused on generated project boundaries, lifecycle
  evidence, and migration fidelity rather than changing import paths;
- generated-project-only dependencies must stay in the generated module or an
  isolated temporary module, never in the gofly root module;
- adopters should treat unclassified diffs as `breaking-candidate` until a
  migration note or rollback note explains the change.
