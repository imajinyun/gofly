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
type GetUserRequest struct {
    ID string `path:"id" validate:"required"`
}

rest.Route{
    Method:      http.MethodGet,
    Path:        "/users/{id}",
    Summary:     "get user",
    OperationID: "getUser",
    Parameters:  []rest.Parameter{{Name: "id", In: "path", Required: true, Schema: rest.StringSchema()}},
    Responses: map[string]rest.Response{
        "200": rest.JSONResponse("the user", rest.StructSchema(User{})),
        "400": rest.JSONErrorResponse("Invalid request"),
        "404": rest.JSONErrorResponse("User not found"),
    },
}
```

Use `rest.StructSchema` for request/response DTOs that use gofly's portable validation tags. It links `required`, `min`, `max`, and `oneof` metadata into the generated OpenAPI schema. Use `rest.DefaultErrorResponses()` for the standard `400` and `500` REST error envelopes, then add domain-specific responses such as `404` or `409`.

## Production configuration

- keep OpenAPI enabled on internal or public REST services as needed;
- review generated contracts during API changes;
- use `gofly api diff` and `gofly api breaking` before release.
