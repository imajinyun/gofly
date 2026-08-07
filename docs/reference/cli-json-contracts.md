# CLI JSON Contracts

## Standard envelope

Stable JSON commands use a top-level envelope with `ok`, `command`, `version`,
`data`, and optional `error` fields. Error envelopes use `error.code`,
`error.message`, and when remediation is known, `error.remediation`.

## Stable command contracts

- `gofly doctor --json` reports environment checks, `summary`, and
  `nextActions`.
- `gofly bug --json` reports a redacted `supportBundle` with schema
  `gofly.support_bundle.v1`.
- `gofly release check --json --strict` reports release gate checks and
  `error.remediation` when strict mode blocks promotion.
- `gofly ai new --json --apply --verify` reports bounded generated project
  verification through `data.verification`, `data.verifyRan`,
  `data.verifyPassed`, and `data.nextActions`.
- `gofly ai control-plane --json` reports stable control-plane snapshots.
- `gofly api diff --format json` reports API contract changes.
- `gofly rpc descriptor --format json` reports RPC descriptor differences.

Generated project failure reports use
`gofly.generated_project_failure_report.v1`. Their `output` field is bounded by
`outputLimitBytes` and the `rerunGuidanceField` is `nextActions`.

DX support bundles use `gofly.dx_support_bundle.v1`. Remediation handoff uses
`gofly.remediation_handoff.v1`; troubleshooting adoption uses
`gofly.troubleshooting_adoption_loop.v1`; the aggregate remediation workflow
uses `gofly.remediation_loop_contract.v1`.

`commitPolicy` stays outside automated remediation: Aiflow may queue tasks and
produce bounded failure reports, but the current agent or a human owns commits.
