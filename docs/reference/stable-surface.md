# Stable Surface Governance

Schema: `gofly.stable_surface.v1`

This page defines the v1 candidate surface that production adopters may depend
on before the final v1 tag. It is intentionally narrow: a surface becomes a v1
candidate only when it has compatibility documentation, tests, release-note
handling, and deprecation rules.

## v1 candidate surfaces

| Surface | Candidate status | Required evidence |
| --- | --- | --- |
| `rest` | v1 candidate | Binding, validation, error envelope, OpenAPI, middleware, and health tests. |
| `core/governance` | v1 candidate | Rule matching, policy JSON, diagnostics, and runtime decision tests. |
| `core/controlplane` | v1 candidate | Snapshot, checksum, diff, consumer action, and secret-boundary tests. |
| CLI JSON | v1 candidate | Stable envelope, command-specific payload docs, and JSON contract tests. |
| generated production service | v1 candidate | Generated project smoke, production-check, and compatibility fixture coverage. |

The validation entry point is:

```sh
make stable-surface-check
```

This target is release-blocking for v1 candidate surfaces. It runs:

- public Go API compatibility through `check-public-api.sh`;
- CLI JSON golden contract tests for scaffold, IDL, version, AI manifest,
  doctor, release, and RPC descriptor outputs;
- `core/controlplane` snapshot, watch, load, checksum, and diff contract tests;
- `rest` OpenAPI, default error response, and runtime control-plane golden
  tests;
- generated production service compile smoke and generated OpenAPI error
  envelope fixture checks.

## Tier 2 to Tier 1 criteria

`rpc`, `gateway`, and `app` remain Tier 2 until each surface satisfies all of
the following:

- public Go API has compatibility tests or an API compatibility report;
- runtime JSON fields are documented as additive-only;
- generated or example adoption path exists and is covered by smoke tests;
- deprecation notes name the replacement, coexistence window, and removal
  trigger;
- release note template includes upgrade, rollback, and compatibility impact.

## Deprecation and release note contract

Any v1 candidate change must include one of these release note classifications:

- `compatible-addition`: additive API, field, command, or generated asset;
- `behavioral-fix`: existing contract preserved, incorrect behavior fixed;
- `deprecation`: old surface still works and has a documented migration path;
- `breaking-candidate`: requires explicit v1 governance approval before merge.

Every deprecation must include:

- the old API, JSON field, command flag, or generated asset;
- the replacement surface;
- the first release containing both old and new forms;
- the coexistence window;
- rollback guidance for services pinned to the old surface.
