# Case Study: Detecting AI Control-Plane Drift

This case study uses `examples/ai-governed-service` to show how AI agents can compare expected runtime contracts with a live control-plane snapshot.

## Problem

Generated code and deployment manifests can pass review while the running service drifts through configuration, policy, or route changes. Humans and AI agents need a stable runtime surface to compare against.

## Gofly Path

The example exposes:

- `GET /v1/state` as a governed business route.
- `GET /admin/control-plane` as the machine-readable runtime snapshot.
- `go run ./examples/ai-governed-service expected` as the compact expected contract.

## Verification

```sh
go run ./examples/ai-governed-service
curl -s localhost:8200/v1/state
curl -s -H 'Authorization: Bearer ai-token' localhost:8200/admin/control-plane
go run ./examples/ai-governed-service expected
```

## Outcome

The live snapshot gives agents a stable object to diff for service name, admin configuration, governance rules, OpenAPI routes, and checksum changes before approving a rollout.
