# RPC Boundary Governance

Schema: `gofly.rpc_boundary.v1`

gofly RPC, `rpc/grpc`, and Kitex should coexist by workload shape rather than
by framework preference.

Machine-readable Tier 1 promotion evidence lives in
`docs/reference/rpc-tier1-evidence.json` and is validated by
`make rpc-boundary-check`.
Each evidence row includes a `decisionBoundary` and `rollbackOrEscalation`
entry so adopters can decide whether to use gofly RPC, `rpc/grpc`, or keep an
existing Kitex/gRPC path for the workload.
The transport boundary contract lives in
`docs/reference/rpc-transport-boundary.json` with schema
`gofly.rpc_transport_boundary.v1`. It explicitly forbids Kitex or gRPC-Go
transport parity claims unless transport-layer benchmark, protocol, streaming,
resolver, balancer, deadline, retry, release, and rollback evidence exists.

RPC latency ratchet policy lives in `bench/budget-ratchet.json`. RPC unary and
stream rows are listed as promotion candidates, not blocking tracked rows, until
the policy has enough trend confidence to promote a specific benchmark. The
current blocker is explicit: the latest unary smoke exceeded the allocation
baseline, and mode-specific stream rows do not yet have published five-sample
baselines. HTTP/OpenAPI budget rows remain the blocking regression surface.

`promotionReadiness` in the Tier 1 evidence manifest keeps the current state
machine-readable: `rpc`, `rpc/grpc`, `gateway`, and `app` are still Tier 2
surfaces targeting Tier 1. Promotion is blocked until one complete release train
attaches RPC boundary and benchmark evidence, and until at least one RPC unary or
stream row moves from `rpcPolicy.candidates` into blocking budget coverage.

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
| Deadline and error-code mapping | deadline, validation, auth, and unavailable errors map to stable gofly RPC or gRPC codes | `go test -shuffle=on ./rpc/... ./rpc/grpc/...` |
| Retry and balancer contract | retryable statuses, resolver updates, and routing policies are rehearsed before migration | `go test -shuffle=on ./rpc/... ./rpc/grpc/... && go test -C examples/rpc-idl-matrix ./...` |
| go-zero coexistence | generated service migration keeps policy, discovery, rollback routing, and control-plane evidence visible | `make generated-upgrade-dry-run-check && make rpc-boundary-check` |

Current promotion blockers:

- `rpc-release-train-missing`: no completed release train has attached the full
  RPC boundary and benchmark evidence set yet.
- `rpc-budget-report-only`: RPC unary and stream benchmarks remain candidates in
  `bench/budget-ratchet.json`; latency-critical methods should keep their Kitex
  or existing gRPC rollback path until blocking budget evidence exists.
- `transport-parity-forbidden`: docs and release notes must not imply Kitex,
  gRPC-Go, Netpoll, TTHeader, or Thrift transport parity while gofly only has
  governed RPC and compatibility evidence.

## Transport Boundary Contract

gofly should be positioned as governed service glue, generated control-plane
evidence, REST ingress, and `rpc/grpc` compatibility where the listed gates
pass. It is not a replacement for Kitex transport internals or native gRPC-Go
ecosystem behavior on every workload.

| Boundary | Keep existing framework when | gofly role |
| --- | --- | --- |
| Kitex hot path | Netpoll, TTHeader, Thrift, generated Kitex clients, or latency-critical transport behavior is required. | REST ingress, generated service glue, governance, release gates, and non-hot-path RPC. |
| gRPC-Go ecosystem | Native gRPC health, auth, tracing, retry service config, streaming, or generated clients are required. | `rpc/grpc` compatibility, governance interceptors, and control-plane integration. |
| go-zero coexistence | zrpc discovery, timeout, retry, breaker, rate-limit, or generated layout semantics must stay active. | Generated-service migration, policy comparison, discovery visibility, and rollback routing rehearsal. |

## Deadline and error-code mapping

Tier 1 promotion requires stable error mapping before teams replace existing
RPC clients. gofly RPC maps request validation failures to `invalid_argument`,
policy or context timeouts to `deadline_exceeded`, unavailable upstreams to
`unavailable`, and auth failures to `unauthenticated`. The HTTP transport keeps
the same envelope while exposing the matching status code, including
`http.StatusGatewayTimeout` for deadline expiry.

`rpc/grpc` keeps native gRPC code semantics: missing credentials return
`Unauthenticated`, authorization failures return `PermissionDenied`, policy
timeouts return `DeadlineExceeded`, and retryable upstream failures return
`Unavailable`. If sampled production behavior depends on a different error
taxonomy, keep the existing gRPC-Go, Kitex, or go-zero path in place until a
compatibility shim and tests are added.

## Retry and balancer contract

Retry and routing policy are migration-sensitive. gofly RPC only treats
explicit retryable failures such as unavailable upstreams as retry candidates;
validation and caller-cancelled deadlines must not be retried. Balancer evidence
must include round-robin, weighted round-robin, P2C, consistent-hash, and
health-aware routing before the client replaces a service mesh or existing
client-side routing stack.

`rpc/grpc` follows the same governance intent while preserving gRPC ecosystem
behavior. Resolver updates and balancer choices must be tested with the target
service discovery adapter before traffic is shifted.

## go-zero coexistence

go-zero migrations should treat generated services as a coexistence rollout,
not an in-place rewrite. Keep the previous go-zero service registered during
the first gofly rollout, port timeout, retry, breaker, and rate-limit policy
into governance config, compare generated OpenAPI/control-plane output, and
verify rollback routing through discovery or gateway configuration.

The rollback trigger is concrete: if generated output, control-plane evidence,
or RPC boundary evidence diverges from the migration rehearsal, keep the
go-zero endpoint live and route traffic back while the fixture or policy gap is
fixed.

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
make bench-regression-check
make bench-evidence-check
```
