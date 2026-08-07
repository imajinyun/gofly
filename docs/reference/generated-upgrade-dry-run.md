# Generated Upgrade Dry Run

Schema: `gofly.generated_upgrade_dry_run.v1`

Gate: `make generated-upgrade-dry-run-check`

The generated upgrade dry run is the release-facing contract for generated
project adoption. It joins generated output governance, code generation
governance, generated project smoke tests, and compatibility reports into one
blocking gate.

## Diff Categories

- `deterministic-repeat-generation`: repeat generation must be clean and
  reproducible.
- `compatible-addition`: generated output adds compatible files, fields, or
  configuration while old inputs still compile.
- `formatting-only`: generated output changes formatting without behavior or
  contract changes.
- `breaking-candidate`: generated output may break adopters and requires a
  rollbackNote plus release review.

Every diff report includes `rollbackNote` and uses
`gofly.generated_version_compat_report.v1` when fixture replay is involved.

## Goctl-compatible generator matrix

Evidence is stored in `docs/reference/goctl-generator-compatibility.json` and
validated by `make goctl-generator-compat-check`. The matrix covers
`gozero-compatible`, goctl-style flags, `api format`, `api import`,
`api route`, `api diff`, and deterministic-repeat-generation boundaries.

## Closeout Contracts

The manifest records the following blocking-contract sections:

- `p9HistoricalFixtureMatrix`
- `p10GoctlGeneratorFidelity`
- `p11LiveUpgradeProof`
- `p12RealBranchReplay`
- `p13GoctlGeneratorMaturity`
- `p14GeneratorAdopterReplayEvidence`

`p14GeneratorAdopterReplayEvidence` intentionally remains
`hold-until-replay-evidence-attached` until real branch replay rows are attached.
Runtime evidence, `.tmp-test` outputs, `GENERATED_VERSION_COMPAT_TMPDIR`, and
local temp directory outputs must never be committed.
