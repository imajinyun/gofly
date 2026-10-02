# Examples

Every directory listed in [catalog.json](catalog.json) is a standalone Go
module. The catalog is the machine-readable inventory; this page groups the
same modules by adopter intent. Testing and fixture-placement rules are in
[TESTING.md](TESTING.md).

Run the repository gates:

```sh
make examples-layout-check
make examples-smoke
make examples-copyable-check
```

## Canonical reference projects

| Example | What it proves |
| --- | --- |
| [gosky](gosky) | Canonical generated API/RPC service with authorization, persistence, operations, and deployment assets; see the [reference contract](../docs/reference/gosky-reference-project.md) |
| [production/production-orders](production/production-orders) | Compact REST, RPC, saga, and outbox reference retained for compatibility |
| [production/microshop](production/microshop) | Multi-service topology retained for compatibility |

## Getting started

| Example | What it proves |
| --- | --- |
| [getting-started/restserver](getting-started/restserver) | Minimal REST server with health, metrics, and OpenAPI |
| [getting-started/rpcserver](getting-started/rpcserver) | Minimal RPC greeter |

## Focused recipes

| Example | What it proves |
| --- | --- |
| [ai-first/ai-governed-service](ai-first/ai-governed-service) | Control-plane snapshot for agents |
| [ecosystem/plugin-ecosystem](ecosystem/plugin-ecosystem) | Plugin registry and template |
| [goctl-model/cache-local](goctl-model/cache-local) | Local cache model helpers |
| [http/http-middleware](http/http-middleware) | Runnable JWT, CORS, CSRF, SSE, WebSocket, OpenAPI, and reusable middleware catalog |
| [http/observability](http/observability) | Prometheus, Grafana, and OpenTelemetry wiring |
| [microservices/config-discovery](microservices/config-discovery) | Config and discovery wiring |
| [microservices/custom-mux-sink](microservices/custom-mux-sink) | Application-owned RPC mux sink |
| [microservices/gateway-discovery-rpc](microservices/gateway-discovery-rpc) | Gateway discovery and RPC routing boundary |
| [microservices/mq-worker](microservices/mq-worker) | MQ worker |
| [microservices/outbox-mq](microservices/outbox-mq) | Outbox relay into MQ |
| [microservices/resilience](microservices/resilience) | Retry, breaker, and rate-limit drill |
| [microservices/rpc-idl-matrix](microservices/rpc-idl-matrix) | Proto and Thrift streaming plus balancers |
| [microservices/saga](microservices/saga) | Saga compensation |

## Compatibility evidence

These modules remain copyable for compatibility, but they emit evidence rather
than representing the preferred application layout. New full-service behavior
belongs in `gosky`.

| Example | What it proves |
| --- | --- |
| [deploy/k8s](deploy/k8s) | Historical Kubernetes checklist output |
| [goctl-model/model-gorm](goctl-model/model-gorm) | Historical GORM-style model output shape |
| [migration/gozero-basic](migration/gozero-basic) | go-zero migration entry report |
| [migration/migration-proof](migration/migration-proof) | Migration evidence JSON |

More context: [documentation index](../docs/index.md),
[standalone examples](../docs/how-to/standalone-examples.md), and
[go-zero migration](../docs/reference/from-go-zero-migration.md).
