# Gateway Guide

Use gofly gateway when you need a controlled ingress that can route to REST or RPC backends, apply discovery, and expose runtime snapshots.

## Current runnable example

```sh
go run ./examples/gateway-discovery-rpc
```

This example demonstrates service discovery registration and route intent for gateway-backed RPC access.

## Core configuration

```yaml
listen: :8080
routes:
  - method: GET
    path: /api/users/{id}
    upstream: users
```

## Production configuration

| Concern | Config |
| --- | --- |
| Listener | gateway config `listen` |
| Upstreams | route `upstream` and discovery service names |
| Transcoding | route transcode config when exposing RPC as REST |
| Governance | transport `gateway` rules |
| Admin/control plane | keep admin port separate |

## Verification

- confirm the route resolves a discovered upstream;
- check `/admin/control-plane` for runtime metadata;
- run benchmark smoke when gateway hot paths change.

## Note

The example set currently demonstrates gateway discovery flow more strongly than a full standalone gateway runtime. If you are documenting gateway adoption, use the config model and `examples/gateway-discovery-rpc` together.
