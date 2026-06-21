# OpenAPI Guide

gofly REST can expose OpenAPI and Swagger UI directly from registered routes.

## Minimal example

```sh
go run ./examples/restserver
curl http://127.0.0.1:8080/openapi.json
open http://127.0.0.1:8080/docs
```

## Generated service path

Production scaffolds also include a checked-in API contract under `docs/openapi.yaml`.

## Route-level metadata

Populate route metadata when you register routes:

```go
rest.Route{
    Method:      http.MethodGet,
    Path:        "/users/{id}",
    Summary:     "get user",
    OperationID: "getUser",
}
```

## Production configuration

- keep OpenAPI enabled on internal or public REST services as needed;
- review generated contracts during API changes;
- use `gofly api diff` and `gofly api breaking` before release.
