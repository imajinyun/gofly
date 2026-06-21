# Migrating from Kitex

Kitex is a high-performance RPC framework. gofly does not try to replace specialized RPC performance work; it focuses on end-to-end service delivery with REST, RPC, governance, discovery, OpenAPI, and admin diagnostics.

## Mapping

| Kitex | gofly |
| --- | --- |
| IDL-first RPC | gofly RPC or `rpc/grpc` integration |
| client/server middleware | RPC middleware plus governance rules |
| service discovery | `core/discovery` providers |
| performance benchmarks | `benchmarks/` and `make bench-smoke` |
| operations | `/admin/control-plane` and observability docs |

## Migration steps

1. Keep high-throughput Kitex services where raw RPC performance is the primary goal.
2. Use gofly for services that need REST+RPC composition, generated admin metadata, and release governance.
3. If migrating a method, wrap the domain handler behind gofly RPC or gRPC.
4. Add benchmark rows before and after migration using `make bench-stat`.
5. Keep Kitex as an optional benchmark extension if your repo already has generated Kitex fixtures.

## Recommendation

Use gofly and Kitex together when Kitex owns latency-critical internal RPC and gofly owns generated service scaffolding, REST ingress, governance, and control-plane transparency.
