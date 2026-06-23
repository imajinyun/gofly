# Discovery Guide

Discovery lets services register themselves and resolve upstream instances without hard-coded addresses.

## Minimal examples

```sh
go run ./examples/config-discovery
go run ./examples/gateway-discovery-rpc
```

## Providers

| Provider | Use case |
| --- | --- |
| `memory` | local development, tests, generated golden path |
| `consul` | existing Consul-based environments |
| `etcdv3` | etcd-backed service registration |

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
- confirm control-plane metadata reflects discovery wiring.
