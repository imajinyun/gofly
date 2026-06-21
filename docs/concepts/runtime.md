# Runtime

The runtime is the part of gofly that starts servers, applies configuration, and exposes health and admin surfaces.

## Default ports

| Surface | Default in generated production service | Purpose |
| --- | --- | --- |
| REST | `:8080` | Public HTTP API |
| RPC | `:8081` | Service-to-service RPC |
| Admin | `:9090` | Diagnostics and control plane |

## Health endpoints

REST services expose:

```sh
curl http://127.0.0.1:8080/startupz
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

Use `/startupz` for startup probes, `/healthz` for liveness, and `/readyz` for dependencies.

## Runtime configuration

Generated services keep runtime settings in `etc/<service>.yaml`. Production capabilities should be configured there instead of hard-coded in handlers.

```yaml
server:
  rest:
    port: 8080
  rpc:
    port: 8081
admin:
  port: 9090
  pathPrefix: /admin
```

## Local verification

```sh
go run ./cmd/orders
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:9090/admin/control-plane
```
