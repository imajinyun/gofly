# Case Study: Detecting AI Control-Plane Drift

This case study uses `examples/ai-governed-service` to show how AI agents can compare expected runtime contracts with a live control-plane snapshot.

## Problem

Generated code and deployment manifests can pass review while the running service drifts through configuration, policy, or route changes. Humans and AI agents need a stable runtime surface to compare against.

## Baseline

Use this case when a service already ships generated contracts or deployment manifests, but release reviewers cannot prove that the running process matches them. The minimum baseline is:

- one expected contract file or command output committed with the release;
- a protected admin endpoint or trusted network path;
- a release job that can fetch JSON from the running service.

## Gofly Path

The example exposes:

- `GET /v1/state` as a governed business route.
- `GET /admin/control-plane` as the machine-readable runtime snapshot.
- `go run ./examples/ai-governed-service expected` as the compact expected contract.

## Adoption plan

| Step | Change | Exit criteria |
| --- | --- | --- |
| 1 | Run the expected-contract command in CI. | CI stores a small JSON object for review. |
| 2 | Fetch `/admin/control-plane` from a staging deployment. | Snapshot includes service name, governance rules, admin path, and checksum. |
| 3 | Compare expected fields before rollout approval. | Drift is classified as expected change, config error, or rollout blocker. |

## Verification

```sh
make examples-copyable-check
go run ./examples/ai-governed-service
curl -s localhost:8200/v1/state
curl -s -H 'Authorization: Bearer ai-token' localhost:8200/admin/control-plane
go run ./examples/ai-governed-service expected
make docs-check
```

The field-level contract is defined in [Control-plane contracts](../reference/control-plane-contracts.md).

## Rollback

If the live checksum or governance rules differ from the approved contract, stop promotion and keep serving the previous deployment. Do not patch the expected contract after deployment; update it in a new reviewed change so agents and humans see the drift reason.

## Outcome

The live snapshot gives agents a stable object to diff for service name, admin configuration, governance rules, OpenAPI routes, and checksum changes before approving a rollout.
