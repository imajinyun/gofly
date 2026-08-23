# Examples

Each directory is a standalone module. Copy it with
`make examples-copyable-check` or follow
[standalone examples](../docs/how-to/standalone-examples.md).

Run the repository smoke:

```sh
make examples-smoke
```

| Example | What it proves |
| --- | --- |
| [getting-started/restserver](getting-started/restserver) | Minimal REST server with health, metrics, and OpenAPI |
| [getting-started/rpcserver](getting-started/rpcserver) | Minimal RPC greeter |
| [production/production-orders](production/production-orders) | Compact production reference: REST, RPC, saga, outbox |
| [production/microshop](production/microshop) | Multi-service topology with a gateway |
| [migration/gozero-basic](migration/gozero-basic) | go-zero migration entry report |
| [migration/migration-proof](migration/migration-proof) | Migration evidence JSON |
| [ai-first/ai-governed-service](ai-first/ai-governed-service) | Control-plane snapshot for agents |
| [http/http-middleware](http/http-middleware) | JWT, CORS, CSRF, SSE, WebSocket, OpenAPI |
| [http/middlewares](http/middlewares) | Middleware catalog library |
| [http/observability](http/observability) | Prometheus / Grafana / OTel compose |
| [microservices/rpc-idl-matrix](microservices/rpc-idl-matrix) | Proto and Thrift streaming plus balancers |
| [microservices/config-discovery](microservices/config-discovery) | Config and discovery wiring |
| [microservices/gateway-discovery-rpc](microservices/gateway-discovery-rpc) | Gateway plus discovery plus RPC |
| [microservices/resilience](microservices/resilience) | Retry, breaker, and limit drill |
| [microservices/saga](microservices/saga) | Saga compensation |
| [microservices/outbox-mq](microservices/outbox-mq) | Outbox relay into MQ |
| [microservices/mq-worker](microservices/mq-worker) | MQ worker |
| [microservices/custom-mux-sink](microservices/custom-mux-sink) | Application-owned RPC mux sink |
| [goctl-model/cache-local](goctl-model/cache-local) | Local cache model helpers |
| [goctl-model/model-gorm](goctl-model/model-gorm) | GORM-style generated model in an isolated module |
| [ecosystem/plugin-ecosystem](ecosystem/plugin-ecosystem) | Plugin registry and template |
| [deploy/k8s](deploy/k8s) | Kubernetes manifest starter |

More context: [docs/index.md](../docs/index.md),
[go-zero migration](../docs/reference/from-go-zero-migration.md).
