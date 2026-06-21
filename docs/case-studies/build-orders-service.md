# Case Study: Building a Governed Orders Service

This case study maps the `examples/production-orders` service to a production adoption path.

## Problem

Teams often start with a small REST service, then add RPC calls, service discovery, retries, event publishing, and observability through separate libraries. That makes runtime behavior hard to inspect and hard for AI agents to verify.

## Gofly Path

The production orders example keeps the service compact while proving the complete runtime chain:

1. REST accepts `POST /orders` requests.
2. Discovery resolves the inventory RPC endpoint.
3. Retry and breaker wrappers protect inventory reservation.
4. Saga compensation records the failure path.
5. Outbox publishes an `orders.created` event after commit.
6. Metrics, health, OpenAPI, and admin endpoints expose runtime state.

## Verification

```sh
go test ./examples/production-orders
go run ./examples/production-orders
curl -s -X POST localhost:8090/orders -H 'Content-Type: application/json' -d '{"sku":"coffee","quantity":2}'
```

## Outcome

The service demonstrates a production-shaped baseline without external dependencies. Replace in-memory discovery, MQ, and outbox storage with Consul/etcd, Kafka/RabbitMQ, and SQL adapters when deploying across multiple processes.
