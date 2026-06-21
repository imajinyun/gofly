# MQ Guide

Use gofly MQ to build workers, async pipelines, retries, and event-driven integrations without locking into one broker too early.

## Minimal example

```sh
go run ./examples/mq-worker
```

The example demonstrates an in-memory broker, subscription handling, retry behavior, and broker stats.

## Production configuration

| Concern | Config |
| --- | --- |
| Broker backend | choose memory/Kafka/RabbitMQ/Redis Stream adapter |
| Consumer retry | subscription retry settings |
| Message headers | use headers for tracing, tenancy, or canary state |
| Governance | MQ-specific policies if your worker path needs them |

## Verification

- publish one message;
- observe the worker log;
- inspect stats after processing;
- add integration tests only when a real broker is required.
