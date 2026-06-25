# RPC Boundary Governance

Schema: `gofly.rpc_boundary.v1`

gofly RPC, `rpc/grpc`, and Kitex should coexist by workload shape rather than
by framework preference.

## Decision table

| Runtime | Use when | Keep out |
| --- | --- | --- |
| gofly RPC | You need lightweight service-to-service calls, runtime governance, descriptors, and generated control-plane evidence. | Ultra-low-latency hot paths that already depend on generated Kitex clients. |
| `rpc/grpc` | You need gRPC ecosystem compatibility, interceptors, health checks, auth, and trace propagation. | Services that only need simple JSON RPC and no protobuf/gRPC toolchain. |
| Kitex | You already have latency-critical Kitex services or generated IDL workflows. | End-to-end REST/RPC/control-plane/governance delivery owned by gofly. |

## Required evidence

- `BenchmarkRPCUnary` compares gofly RPC and gRPC-Go unary cost.
- `BenchmarkRPCServerStreamGovernance`, `BenchmarkRPCClientStreamGovernance`,
  and `BenchmarkRPCBidiStreamGovernance` record mode-specific stream governance
  overhead. `BenchmarkRPCStreamGovernance` remains as the aggregate release
  trend entry.
- resolver/balancer smoke tests must cover discovery updates and routing
  choices before migration.
- Kitex boundary docs must include a rollback note and coexistence strategy.

## Tier 1 promotion checklist

`rpc`, `rpc/grpc`, `gateway`, and `app` stay Tier 2 until these checks pass for
at least one release train:

| Capability | Evidence | Gate |
| --- | --- | --- |
| Unary contract | gofly RPC vs gRPC-Go benchmark evidence | `BENCH_PATTERN=BenchmarkRPCUnary make bench-stat` |
| Server stream | server-stream governance benchmark and IDL matrix smoke | `BENCH_PATTERN=BenchmarkRPCServerStreamGovernance make bench-stat` |
| Client stream | client-stream governance benchmark and IDL matrix smoke | `BENCH_PATTERN=BenchmarkRPCClientStreamGovernance make bench-stat` |
| Bidi stream | bidi-stream governance benchmark and IDL matrix smoke | `BENCH_PATTERN=BenchmarkRPCBidiStreamGovernance make bench-stat` |
| Resolver updates | registry resolver removes unhealthy/standby endpoint from the report | `go test -C examples/rpc-idl-matrix ./...` |
| Balancer routing | round-robin, weighted round-robin, P2C, consistent hash, and health-aware picks | `go run -C examples/rpc-idl-matrix .` |
| Kitex coexistence | rollback note keeps Kitex on hot paths while gofly owns governance surfaces | `make rpc-boundary-check` |

## gRPC compatibility matrix

| Surface | Expected behavior | Evidence |
| --- | --- | --- |
| Health | gRPC health service reports `SERVING`; gofly admin health remains separate. | `BenchmarkRPCUnary/grpc_go` and `rpc/grpc` server tests |
| Auth | Unary and streaming interceptors map missing or invalid bearer tokens to `Unauthenticated`. | `rpc/grpc/auth_test.go` |
| Tracing | Unary and stream interceptors inject/extract trace metadata without breaking payloads. | `rpc/grpc/trace_test.go` and `rpc/grpc` governance tests |
| Governance | Unary and stream policies enforce timeout, retry, concurrency, and breaker behavior. | `rpc/grpc/governance_test.go` |

Run:

```sh
make rpc-boundary-check
BENCH_PATTERN=BenchmarkRPC make bench-stat
make bench-evidence-check
```
