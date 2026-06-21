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
