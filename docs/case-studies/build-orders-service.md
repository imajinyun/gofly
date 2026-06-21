# Case Study: Building a Governed Orders Service

This case study maps the `examples/production-orders` service to a production adoption path.

## Problem

Teams often start with a small REST service, then add RPC calls, service discovery, retries, event publishing, and observability through separate libraries. That makes runtime behavior hard to inspect and hard for AI agents to verify.

## Baseline

Use this path when the current service already has an order-like workflow with at least one downstream dependency and one asynchronous side effect. The minimum baseline before adoption is:

- one documented inbound request, such as `POST /orders`;
- one downstream call that needs timeout, retry, or breaker protection;
- one observable event or outbox handoff;
- a release check that can run `go test ./...`.

## Gofly Path

The production orders example keeps the service compact while proving the complete runtime chain:

1. REST accepts `POST /orders` requests.
2. Discovery resolves the inventory RPC endpoint.
3. Retry and breaker wrappers protect inventory reservation.
4. Saga compensation records the failure path.
5. Outbox publishes an `orders.created` event after commit.
6. Metrics, health, OpenAPI, and admin endpoints expose runtime state.

## Adoption plan

| Step | Change | Exit criteria |
| --- | --- | --- |
| 1 | Run `examples/production-orders` unchanged. | Local `go test` and `go run` succeed. |
| 2 | Replace the in-memory order handler with the team's domain logic. | REST response shape and OpenAPI remain stable. |
| 3 | Move downstream calls behind governed RPC clients. | Retry and breaker outcomes are visible in logs or metrics. |
| 4 | Replace in-memory adapters with production discovery, MQ, and storage. | `/admin/control-plane` reports the intended service, policy, and dependency state. |

## Verification

```sh
go test ./examples/production-orders
make examples-copyable-check
go run ./examples/production-orders
curl -s -X POST localhost:8090/orders -H 'Content-Type: application/json' -d '{"sku":"coffee","quantity":2}'
curl -s -H 'Authorization: Bearer orders-token' localhost:8090/admin/control-plane
```

Before release, also run `make docs-check` and compare the service contract against [Stable API Surface Reference](../reference/api-surface.md).

## Rollback

Keep the previous handler and deployment manifest until the gofly service passes smoke tests, control-plane capture, and one canary rollout. Roll back by routing traffic to the previous service version; the outbox event name should remain unchanged during the first migration to avoid consumer rewrites.

## Outcome

The service demonstrates a production-shaped baseline without external dependencies. Replace in-memory discovery, MQ, and outbox storage with Consul/etcd, Kafka/RabbitMQ, and SQL adapters when deploying across multiple processes.
