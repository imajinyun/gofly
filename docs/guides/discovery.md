# Discovery Guide

Discovery lets services register themselves and resolve upstream instances without hard-coded addresses.

## Minimal examples

```sh
go run ./examples/config-discovery
go run ./examples/gateway-discovery-rpc
```

## Providers

The provider status is intentionally explicit. Only `implemented` providers
have discovery adapter code, tests, and release gates. `planned` and
`config-only` rows are adoption targets, not supported discovery runtimes.

| Provider | Status | Use case | Failover / rollback boundary |
| --- | --- | --- | --- |
| `memory` | implemented | local development, tests, generated golden path | in-process snapshot and watcher cleanup; roll back networked smoke tests to memory when external registries are unavailable |
| `consul` | implemented | existing Consul-based environments | validate Consul health checks and watch behavior in the deployment topology; roll traffic back to the previous Consul registration path if watches diverge |
| `etcdv3` | implemented | etcd-backed service registration | validate lease and watch behavior with the Docker-backed integration matrix; keep the previous etcd registration path until generated-service smoke passes |
| `nacos` | config-only | Nacos-backed configuration source only | do not route service discovery traffic to Nacos until a `core/discovery/nacos` adapter and integration evidence exist |
| `dns` | planned | future resolver-only profile | no registration or watch guarantees are advertised until resolver semantics and TTL behavior are tested |
| `kubernetes` | planned | future Service or EndpointSlice profile | keep using platform service discovery directly until endpoint watching and policy evidence exist |
| `static` | planned | future file or config-backed profile | keep static endpoints in application configuration until reload, validation, and generated-service smoke coverage exist |

## Discovery adapter matrix

The machine-readable matrix lives at
[`docs/reference/discovery-adapter-matrix.json`](../reference/discovery-adapter-matrix.json)
and is checked by:

```sh
make discovery-adapter-matrix-check
```

The matrix records provider status, implementation and test evidence,
capabilities, failover behavior, rollback notes, and release gates. Promote a
planned row to implemented only after code, tests, documentation, and gates
land together.

The P10 closeout is recorded as `p10DiscoveryAdapterCloseout`. It keeps memory,
Consul, etcdv3, Nacos, DNS, Kubernetes, and static discovery rows tied to
provider classification, register/resolve/watch/lease capabilities, failover,
dependency boundaries, smoke gates, and rollback notes. Planned or config-only
providers remain non-routing evidence until implementation and integration
criteria are met.

The P13 closeout is recorded as `p13DiscoveryFailoverCloseout`. It makes
resolver update, stale endpoint, registry unavailable, zone/tag/version
filtering, and rollback-note evidence release-blocking for the matrix. Memory,
Consul, and etcdv3 rows are backed by concrete tests. Nacos remains config-only,
while DNS, Kubernetes, and static discovery remain planned and must not be
advertised as routable providers until their promotion boundaries pass.

## Production configuration

```yaml
discovery:
  provider: memory
  address: ""
  endpoints: []
```

Switch the provider and endpoints when moving from local smoke to a shared environment.

## Change events

Resolvers that support `Watch` publish an initial `snapshot` event and then
incremental events when instances are registered, deregistered, updated, or
expired. Each event includes the full filtered instance list plus a `changes`
section:

| Field | Meaning |
| --- | --- |
| `added` | Instances newly visible for the watched service |
| `removed` | Instances no longer visible for the watched service |
| `updated` | Instances whose endpoint identity stayed stable but metadata, weight, zone, version, status, tags, or other fields changed |
| `unchanged` | Instances present in both snapshots with identical normalized state |

RPC clients use these events to refresh their runtime discovery snapshot and to
close idle HTTP transport connections when an endpoint is removed. The next
governance slice extends the same event path to endpoint-scoped connection-pool
cleanup and load-balancer cache invalidation.

## Verification

- register one instance;
- resolve it from a client or gateway;
- watch updates when instances change;
- confirm control-plane metadata reflects discovery wiring;
- run `make discovery-adapter-matrix-check` before advertising a provider as supported.
