# Troubleshooting Workflow

Use this workflow when generated projects, local environments, or release gates
fail and a support bundle is needed.

1. Run `gofly doctor --json` and inspect `nextActions` plus any `fix_hint`
   fields.
2. Run `gofly release check --json --strict` before release promotion. Use
   `error.remediation` and `data.checks` to identify the blocking gate.
3. Run `gofly bug --json` to collect a redacted support bundle.
4. For generated project verification failure, attach the bounded
   `gofly.generated_project_failure_report.v1` fields from
   `gofly ai new --json --apply --verify`.

Support bundle content must be redacted before handoff. Generated project
verification failure output is capped at 4096 bytes and should point users to
`nextActions` for the exact rerun command.

Remediation handoff uses `gofly.remediation_handoff.v1`.
Troubleshooting adoption uses `gofly.troubleshooting_adoption_loop.v1`.
The aggregate remediation workflow uses `gofly.remediation_loop_contract.v1`.

Aiflow may queue diagnostics and patch plans, but it must not create commits.
The current agent or a human owns staging, commits, and final gate evidence.
