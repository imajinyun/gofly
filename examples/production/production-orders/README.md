# Production orders

schema: gofly.reference_app.v1

This is the compact production reference app. It wires REST, RPC, saga, outbox,
MQ, config, discovery, cache, observability, and a K8s-oriented topology
contract. Memory mode stays local; Docker mode names SQL outbox, Redis cache,
Kafka, RabbitMQ, Redis Stream, Consul, etcd, Nacos, and OpenTelemetry collector
endpoints. Keep a rollback path so the original process stays routable when a
drill fails.

## Run

```sh
go test ./...
go run .
```

Smoke the topology evidence:

```sh
REFERENCE_APP_MODE=memory make reference-app-smoke
REFERENCE_APP_MODE=docker make reference-app-smoke
```

`GET /topology` returns `topology_evidence` plus a fallback note for each
component when Docker dependencies are unavailable.

## Related

- [Examples catalog](../../README.md)
- [Documentation index](../../../docs/index.md)
- [Production checklist](../../../docs/operations/production-checklist.md)
