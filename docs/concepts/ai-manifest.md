# AI manifest

The AI manifest describes gofly capabilities in a machine-readable form so agents and automation can discover safe commands, inputs, outputs, risks, and generated files.

## Inspect the manifest

```sh
gofly ai manifest --json
```

Useful checks:

```sh
gofly ai manifest --json | python3 -m json.tool
gofly ai manifest --json | grep 'new service'
```

## How agents use it

Agents should prefer manifest-declared commands instead of guessing CLI flags. For example, the manifest exposes `new service` as the canonical production scaffold command, including dry-run/plan support and output modes.

The manifest also lists documentation links, runnable example links, and verification commands. This lets agents start from the same source of truth that `make docs-check` validates instead of keeping a separate capability inventory.

## Feature and template sync

The `featureLibrary.features` field exposes built-in feature plugins such as `auth-jwt`, `postgres-repository`, and `redis-cache`.

The `featureLibrary.templates` field exposes scaffold templates such as `go-rest-minimal`, `go-rag-service`, and `go-rpc-grpc`.

Documentation that names these features or templates is checked by `make doc-manifest-sync-check`, which runs `gofly ai manifest --format json` and verifies that the documented names still exist in the machine-readable manifest.

## Template and profile trust

Template/profile trust is indexed in
[`../reference/template-profile-trust.json`](../reference/template-profile-trust.json)
with schema `gofly.template_profile_trust.v1`. The matrix links each
manifest-exposed project template to its purpose, generated-output guarantees,
dependency boundary, verification commands, and AI manifest fields. It also
links historical generated upgrade profiles (`old`, `current`, and `future`) to
repeat-generation guarantees and rollback-note requirements.

`make doc-manifest-sync-check` validates that the trust matrix stays aligned
with `featureLibrary.templates`,
`featureLibrary.templateVerification.validatedTemplates`, and
[`../reference/generated-upgrade-dry-run.json`](../reference/generated-upgrade-dry-run.json).

## Production use

- Use `--dry-run` before writing generated files in automation.
- Use JSON output where available for CI and agents.
- Treat high-risk commands as requiring review when they overwrite files or change release state.
- Run `make doc-manifest-sync-check` after changing manifest metadata, examples, scaffold templates, or feature-library documentation.

## Related docs

- [Quickstart](../getting-started/quickstart.md)
- [Control plane](control-plane.md)
- [Production checklist](../operations/production-checklist.md)
