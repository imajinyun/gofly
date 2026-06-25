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
- `BenchmarkRPCStreamGovernance` records stream governance overhead.
- resolver/balancer smoke tests must cover discovery updates and routing
  choices before migration.
- Kitex boundary docs must include a rollback note and coexistence strategy.

Run:

```sh
make rpc-boundary-check
BENCH_PATTERN=BenchmarkRPC make bench-stat
make bench-evidence-check
```
