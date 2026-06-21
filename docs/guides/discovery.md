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

## Verification

- register one instance;
- resolve it from a client or gateway;
- watch updates when instances change;
- confirm control-plane metadata reflects discovery wiring.
