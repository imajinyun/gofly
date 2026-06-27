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

## DB and cache productization matrix

The model generator is part of the DB/cache productization contract at
[`docs/reference/db-cache-productization.json`](../reference/db-cache-productization.json).
Validate it with:

```sh
make db-cache-productization-check
```

The matrix ties generated SQL/GORM repositories to `SQLStore`, `NewCluster`,
SQL outbox adoption, Redis-backed model cache boundaries, temp-module compile
evidence, and planned migration-runner work. Generated-only dependencies remain
owned by the generated project module, not the gofly root module.

For go-zero `sqlx` migrations, the same matrix requires `goZeroAlignment`
evidence before adopter docs can claim parity. Generated repositories must keep
read/write strategy, transaction examples, Redis-backed cache contracts,
observability, local smoke tests, and rollback guidance connected to
`make framework-gap-check`.

## Recommendation

Pair model generation with migration review and a service-level example that exercises one real read/write path.
