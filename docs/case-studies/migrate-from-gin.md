# Case Study: Moving a Gin-Style Service into Gofly

This case study describes a low-risk migration path for teams that already have Gin-style handlers and want gofly governance and control-plane visibility.

## Problem

Router-only services are easy to start but usually need separate work for OpenAPI, rate limiting, circuit breaking, metrics, health checks, release checks, and AI-readable runtime state.

## Baseline

Use this case when a Gin-style service is still healthy enough to migrate incrementally. The minimum baseline is:

- handlers have separable business logic rather than all logic embedded in `gin.Context` callbacks;
- route patterns and request/response examples are known;
- deployment can run old and new handlers side by side during a canary.

## Migration Path

1. Keep handler business logic in small functions that accept typed input and return typed output.
2. Wrap each handler with `rest.Route` and `func(*rest.Context)`.
3. Enable production middleware defaults through `rest.Config`.
4. Attach governance rules for latency, rate limits, and canary headers.
5. Expose `/admin/control-plane` and compare the snapshot in CI or release gates.

## Adoption plan

| Step | Change | Exit criteria |
| --- | --- | --- |
| 1 | Port one read-only route to `examples/restserver` style. | Old and new responses match for sampled requests. |
| 2 | Add OpenAPI metadata and health checks. | `/openapi.json` and `/healthz` are stable in CI. |
| 3 | Move middleware concerns into gofly middleware or governance. | Rate limit, timeout, and metrics behavior is visible in tests or staging. |
| 4 | Enable admin control-plane capture. | Release review includes a snapshot diff. |

## Verification

```sh
make examples-copyable-check
go run ./examples/restserver
curl -s localhost:8080/openapi.json
curl -s localhost:8080/healthz
make docs-check
```

Use [Migrating from Gin](../comparisons/gin.md) for the route and handler mapping.

## Rollback

Keep the Gin route active behind the existing router until the gofly route has matching responses, metrics, and error behavior. Roll back by removing the traffic split or feature flag for the migrated route; avoid changing request/response JSON names during the first step.

## Outcome

The migrated service keeps Go HTTP ergonomics while gaining generated contracts, governance policy, and runtime metadata that can be inspected by operators and AI agents.
