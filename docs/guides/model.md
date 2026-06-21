# Model Generation Guide

Use gofly model generation when you want generated Go types and repository-facing structures from SQL schema or datasource metadata.

## Runnable examples

```sh
go run ./examples/model-gorm
go run ./examples/model-mongo
```

These examples are lightweight pattern demos. They document how generated model layers are intended to be consumed.

## CLI entrypoints

```sh
gofly gen model --ddl schema.sql --dir internal/model
```

## Production guidance

| Concern | Guidance |
| --- | --- |
| SQL source | keep DDL versioned with migrations |
| Output dir | generate into service-local model packages |
| Dependencies | generated project dependencies stay in generated project `go.mod` |
| Verification | compile generated code and run `go test ./...` |

## Recommendation

Pair model generation with migration review and a service-level example that exercises one real read/write path.
