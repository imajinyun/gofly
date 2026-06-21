# Microshop Example

`microshop` runs five small gofly REST services with identical production defaults: request IDs, tracing, logging, metrics, health checks, OpenAPI, and `/admin/control-plane`.

```sh
go run ./examples/microshop describe
go run ./examples/microshop gateway
```

In another terminal:

```sh
curl -s localhost:8100/v1/checkout
curl -s -H 'Authorization: Bearer microshop-token' localhost:8100/admin/control-plane
```

Each service can also run through Compose:

```sh
docker compose -f examples/microshop/compose.yaml up --build
```
