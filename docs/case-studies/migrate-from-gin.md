# Case Study: Moving a Gin-Style Service into Gofly

This case study describes a low-risk migration path for teams that already have Gin-style handlers and want gofly governance and control-plane visibility.

## Problem

Router-only services are easy to start but usually need separate work for OpenAPI, rate limiting, circuit breaking, metrics, health checks, release checks, and AI-readable runtime state.

## Migration Path

1. Keep handler business logic in small functions that accept typed input and return typed output.
2. Wrap each handler with `rest.Route` and `func(*rest.Context)`.
3. Enable production middleware defaults through `rest.Config`.
4. Attach governance rules for latency, rate limits, and canary headers.
5. Expose `/admin/control-plane` and compare the snapshot in CI or release gates.

## Verification

```sh
go run ./examples/restserver
curl -s localhost:8080/openapi.json
curl -s localhost:8080/healthz
```

## Outcome

The migrated service keeps Go HTTP ergonomics while gaining generated contracts, governance policy, and runtime metadata that can be inspected by operators and AI agents.
