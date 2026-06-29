# HTTP Middleware Example

This example productizes the P1 HTTP middleware matrix for gofly. It shows how a single service wires:

- JWT bearer authentication
- CORS preflight and credentialed browser requests
- CSRF double-submit cookie protection
- signed sessions with `HttpOnly` cookies
- OpenTelemetry trace propagation through `traceparent`
- Prometheus metrics at `/metrics`
- Server-Sent Events at `/events`
- WebSocket upgrade at `/ws`
- request binding and validation with a stable JSON error response

## Migration DX

Use this example when replacing Gin or go-zero HTTP middleware one concern at a time. Keep the old chain active until `go -C examples/http-middleware run . --describe`, `go -C examples/http-middleware test ./...`, `make examples-smoke`, `make api-example-consistency-check`, and `make p1-growth-check` all pass in the target branch.

Recommended ordering:

1. recover, request-id, trace, log, metrics
2. security headers, CORS, max-body, timeout
3. session, CSRF, JWT, validation
4. handler, SSE/WebSocket bounds

Gin mapping: Gin auth middleware maps to `rest.BearerAuthMiddleware`, `gin-contrib/cors` maps to `rest.CORSConfig`, Gin CSRF/session middleware maps to `rest.CSRFConfig` and the signed session middleware in this example, and Gin SSE/WebSocket handlers map to `Context.SSE` and `Context.WebSocket`.

go-zero mapping: go-zero JWT, CORS, trace, log, Prometheus, and custom browser-safety middleware map to gofly route middleware plus `/metrics`, OpenAPI response schemas, and the `/middleware/catalog` control-plane surface.

Failure modes to rehearse: missing JWT, credentialed CORS preflight drift, missing or mismatched CSRF token, unsigned session cookie, high-cardinality Prometheus labels, traceparent loss, SSE cancellation, and WebSocket message-size or timeout violations.

## Run

From the repository root:

```bash
go run ./examples/http-middleware
```

Fetch local demo credentials:

```bash
curl -i localhost:8085/token
```

Use the returned token and CSRF cookie to create an order:

```bash
curl -i -X POST localhost:8085/orders \
  -H 'Authorization: Bearer <token>' \
  -H 'X-CSRF-Token: <csrf-cookie-value>' \
  -H 'Content-Type: application/json' \
  -b 'gofly_demo_csrf=<csrf-cookie-value>; gofly_demo_session=<session-cookie-value>' \
  -d '{"sku":"tea","quantity":2}'
```

Observe realtime and metrics endpoints:

```bash
curl -N localhost:8085/events
curl -s localhost:8085/metrics | grep gofly_requests_total
open http://localhost:8085/docs
```

## Production notes

- Replace demo secrets with environment-specific values from a secret manager.
- Keep CSRF cookies `Secure` in production HTTPS deployments.
- Keep Prometheus labels bounded; use route patterns instead of raw URLs.
- Authenticate WebSocket and SSE routes when they expose user data.
- Treat validation failures as client errors and return the standard `rest.ErrorResponse` envelope.
