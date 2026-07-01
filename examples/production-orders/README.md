# Production Orders Reference App

Schema: `gofly.reference_app.v1`

This example composes the main gofly runtime capabilities into one service:

- REST API for order creation
- RPC service for inventory reservation
- Profile-based configuration
- In-memory service discovery
- In-memory MQ worker
- cache boundary placeholder for read-through order lookups
- Outbox relay for event publication
- Saga compensation around the business workflow
- Limiter, retry, and circuit breaker protection
- observability through metrics, health probes, pprof, structured logs, and
  trace propagation
- K8s deployment assets and rollback notes in the root `k8s/` and `charts/`
  directories

The example is dependency-free: it uses in-memory adapters so it can run in CI
and local development without Docker.

Docker-backed mode renders the same reference app against a production topology:

- SQL outbox endpoint through Postgres
- Redis cache and Redis Stream endpoint
- Kafka and RabbitMQ broker endpoints
- Consul, etcd, and Nacos discovery endpoints
- OpenTelemetry collector endpoint
- machine-readable `topology_evidence` entries with component, mode, endpoint,
  validation, and fallback note fields

## Smoke Modes

Memory mode is the default and does not require Docker:

```bash
REFERENCE_APP_MODE=memory make reference-app-smoke
```

Docker mode is reserved for CI or local environments that intentionally run the
integration stack:

```bash
REFERENCE_APP_MODE=docker make reference-app-smoke
```

When Docker is available, the smoke gate starts `examples/production-orders/compose.yaml`
and runs the HTTP smoke script. When Docker is unavailable, the gate still
verifies the static production topology contract and reports the fallback.
The runtime evidence is written under the ignored `.aiflow/` directory as
`gofly.reference_app_smoke_report.v1`; Docker mode is considered live proof
only when `liveCompose=true`, while tool or dependency skips must keep an
explicit fallback reason and must not be used as deployment-promotion evidence.

## Run

From the repository root:

```bash
go run ./examples/production-orders
```

The process starts:

| Component | Port | Purpose |
| --- | --- | --- |
| REST | `8090` | Public order API, OpenAPI, `/admin/control-plane` |
| Admin | `8091` | Metrics, health and pprof |
| RPC | `8092` | Inventory reservation service |

## Create an Order

```bash
curl -s -X POST localhost:8090/orders \
  -H 'Content-Type: application/json' \
  -d '{"sku":"coffee","quantity":2}'
```

Expected response:

```json
{
  "id": "order-001",
  "status": "accepted",
  "trace_id": "...",
  "request_id": "..."
}
```

The logs show the internal chain:

```text
inventory reserved
fulfillment worker received order
```

## Exercise Failure Paths

Inventory failure triggers saga compensation:

```bash
curl -s -X POST localhost:8090/orders \
  -H 'Content-Type: application/json' \
  -d '{"sku":"sold-out","quantity":1}'
```

Rate limiting can be observed by sending a burst:

```bash
for i in $(seq 1 20); do
  curl -s -X POST localhost:8090/orders \
    -H 'Content-Type: application/json' \
    -d '{"sku":"coffee","quantity":1}' &
done
wait
```

## Observe

```bash
curl -s localhost:8091/debug/healthz
curl -s localhost:8091/debug/readyz
curl -s localhost:8091/debug/metrics.json
curl -s localhost:8091/debug/metrics | grep gofly_requests_total
curl -s -H 'Authorization: Bearer orders-token' localhost:8090/admin/control-plane
curl -s localhost:8090/topology
```

OpenAPI and Swagger UI are exposed by the REST server:

```bash
curl -s localhost:8090/openapi.json
open http://localhost:8090/docs
```

## Design Notes

- The order workflow uses `saga` to compensate inventory/order state if a step fails.
- The outbox relay publishes `orders.created` after the order workflow commits.
- The MQ worker consumes the event and logs trace metadata carried in headers.
- The REST route uses low-cardinality route metrics and propagates trace/request IDs.
- Replace the in-memory adapters with Redis/RabbitMQ/Kafka, SQL outbox storage,
  and external discovery for production deployments. Docker-backed mode exposes
  those endpoints through `/topology` so operators can verify the selected
  production profile before routing traffic.
- The `/topology` response includes `topology_evidence` so CI and operators can
  verify that SQL outbox, Redis cache, Kafka, RabbitMQ, Redis Stream, Consul,
  etcd, Nacos, and OpenTelemetry collector endpoints were selected by the
  active profile.
- Rollback by keeping the previous deployment active, disabling the new gateway
  route, and replaying unpublished outbox entries after the old service is
  healthy.
