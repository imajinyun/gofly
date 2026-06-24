# gofly Examples

This directory contains runnable examples for the major gofly runtime paths. Each example is a standalone Go module with a local `replace github.com/gofly/gofly => ../..` directive so users can copy the directory into another workspace and then replace the dependency with a released gofly version.

## Standalone module contract

Every example directory that contains Go source must include its own `go.mod`. This keeps examples copyable and prevents example-only dependencies from leaking into the root module. Example-specific `README.md` files are used when an example needs standalone operational guidance, while the matrix below remains the shared index for the full examples set. CI enforces the copy-out contract with `make examples-copyable-check` and `make examples-smoke`.

When copying an example out of the repository:

```bash
cp -R examples/restserver /tmp/restserver-demo
cd /tmp/restserver-demo
go mod edit -dropreplace github.com/gofly/gofly
go get github.com/gofly/gofly@latest
go test ./...
```

Validate the copy-out contract for every example:

```bash
make examples-copyable-check
```

## Matrix

| Example | Purpose | Command | Ports | Verify | Expected output |
| --- | --- | --- | --- | --- | --- |
| `restserver` | REST routing, OpenAPI, health and metrics | `go run ./examples/restserver` | `8080` | `curl -s localhost:8080/healthz` | HTTP 200 health response and `/users/{id}` JSON payloads |
| `middlewares` | Copyable HTTP middleware catalog for generated services | import `github.com/gofly/gofly/examples/middlewares` | none | `go test -C examples/middlewares ./...` | Productization catalog for JWT, CORS, CSRF, sessions, OpenTelemetry, Prometheus, SSE, WebSocket and validation |
| `middleware-demo` | Focused route-per-middleware demo with catalog and OpenAPI exposure | `go run ./examples/middleware-demo` | `8086` | `curl -s localhost:8086/middleware/catalog` | JSON catalog plus runnable endpoints for each reusable middleware |
| `http-middleware` | Combined HTTP middleware chain for browser/API workloads | `go run ./examples/http-middleware` | `8085` | `curl -s localhost:8085/token` | JWT, CORS, CSRF, session, tracing, metrics, SSE, WebSocket and validation behavior |
| `rpcserver` | RPC service registration and HTTP transport | `go run ./examples/rpcserver` | `8081` | `curl -s -X POST localhost:8081/examples.greeter.Greeter/SayHello -d '{"name":"gofly"}'` | Greeting response from `SayHello` |
| `rpc-idl-matrix` | Copyable RPC IDL adoption matrix | `go run ./examples/rpc-idl-matrix` | none | `go run -C examples/rpc-idl-matrix .` | Proto/thrift fixtures, unary/server-streaming/client-streaming/bidirectional streaming, interceptors, resolver updates and load-balancing JSON |
| `config-discovery` | Profile config layering and service discovery | `go run ./examples/config-discovery` | none | command output | Merged config plus resolved `orders` endpoint |
| `gateway-discovery-rpc` | Gateway route wiring from discovery snapshots | `go run ./examples/gateway-discovery-rpc` | none | command output | Discovery snapshot/register events and route target summary |
| `mq-worker` | MQ publish, retrying consume and stats snapshot | `go run ./examples/mq-worker` | none | command output | Retry log, received message and broker stats |
| `observability` | Metrics, health, admin and request tracing basics | `go run ./examples/observability` | `8080`, `8081` | `curl -s localhost:8081/debug/metrics.json` | JSON metrics snapshot; optional Prometheus/Grafana/OTel stack |
| `production-orders` | REST/RPC, config, discovery, MQ, outbox, saga, resilience and observability | `go run ./examples/production-orders` | `8090`, `8091`, `8092` | `curl -s -X POST localhost:8090/orders -d '{"sku":"coffee","quantity":2}'` | Accepted order response and fulfillment worker log |
| `microshop` | Five-service shop topology with per-service admin control-plane | `go run ./examples/microshop describe` | `8100`-`8104` | `go run ./examples/microshop gateway` + `curl -s localhost:8100/v1/checkout` | Service topology or gateway JSON response |
| `ai-governed-service` | AI-readable runtime drift demo with governed state and control-plane | `go run ./examples/ai-governed-service` | `8200` | `curl -s -H 'Authorization: Bearer ai-token' localhost:8200/admin/control-plane` | Control-plane snapshot with checksum and governance metadata |
| `resilience` | Limiter, breaker and retry behavior under mixed outcomes | `go run ./examples/resilience` | none | command output | `results: ok=... rejected=... breaker-open=... failed=...` |
| `model-gorm` | Optional GORM model generation mode | `go run ./examples/model-gorm` | none | command output | GORM unique lookup and repository pattern checklist |
| `model-mongo` | Optional Mongo driver model generation mode | `go run ./examples/model-mongo` | none | command output | Mongo unique lookup and repository pattern checklist |
| `outbox-mq` | Transactional outbox style MQ publishing | `go run ./examples/outbox-mq` | none | command output | `published topic=hello.jobs key=verify body=hello` |
| `saga` | Saga compensation flow | `go run ./examples/saga` | none | command output | Saga error plus compensation log |
| `k8s` | Kubernetes deployment and service discovery checklist | `go run ./examples/k8s` | none | command output | Kubernetes resolver and manifest checklist |

## End-to-End Commands

Build every example module:

```bash
make examples-check
```

Smoke test runnable example contracts:

```bash
make examples-smoke
```

Run the REST example:

```bash
go run ./examples/restserver
curl -s localhost:8080/healthz
curl -s localhost:8080/users/42
```

Run the HTTP middleware matrix:

```bash
go test -C examples/middlewares ./...
go test -C examples/middleware-demo ./...
go run -C examples/http-middleware .
curl -s localhost:8085/token
curl -s localhost:8085/openapi.json
```

Run the RPC IDL matrix:

```bash
go test -C examples/rpc-idl-matrix ./...
go run -C examples/rpc-idl-matrix .
```

Run the observability example:

```bash
go run ./examples/observability
curl -s localhost:8080/users/42
curl -s localhost:8081/debug/metrics.json
curl -s localhost:8081/debug/healthz
```

Run the observability local stack:

```bash
docker compose -f examples/observability/docker-compose.yaml up
```

Run the production-style composition example:

```bash
go run ./examples/production-orders
curl -s -X POST localhost:8090/orders -H 'Content-Type: application/json' -d '{"sku":"coffee","quantity":2}'
curl -s localhost:8091/debug/metrics.json
```

Run Phase 6 ecosystem examples:

```bash
go run ./examples/microshop describe
go run ./examples/ai-governed-service expected
make examples-smoke
```

Run the standalone examples:

```bash
go run ./examples/gateway-discovery-rpc
go run ./examples/config-discovery
go run ./examples/microshop describe
go run ./examples/ai-governed-service expected
go run ./examples/mq-worker
go run ./examples/model-gorm
go run ./examples/model-mongo
go run ./examples/outbox-mq
go run ./examples/saga
go run ./examples/k8s
```

## Notes

- `model-gorm`, `model-mongo` and `k8s` intentionally demonstrate generation and deployment patterns without requiring local GORM, MongoDB or Kubernetes services.
- Docker-backed examples and integration scenarios are covered by `go test -tags=integration ./...` instead of long-running example processes.
- Service examples should keep their listening ports documented here so CI and tutorials can avoid collisions.
