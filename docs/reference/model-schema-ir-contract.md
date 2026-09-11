# Model Schema IR Contract

Fixture schema: `gofly.goctl_datasource_replay_fixture.v1` (`schemaContract` section)

Enforced by: `go test ./cmd/gofly/internal/generator -run 'TestModelSchemaIR|TestGoctlDatasourceReplayFixtureModelSchemaIR|TestGenerateModelFromSchemaIR'`

`ModelSchemaIR` is the single intermediate representation for model code
generation. Every supported source produces a raw IR, and all generation flows
through one internal entrypoint:

```
DDL file        -> ParseSQLModels          -> raw ModelSchemaIR (source=ddl)        -> generateModelFromSchemaIR
Datasource      -> introspectSQLTables     -> raw ModelSchemaIR (source=datasource) -> generateModelFromSchemaIR
Replay fixture  -> rawModelSchemaIRFromReplayFixture -> raw ModelSchemaIR (source=replay) -> generateModelFromSchemaIR
```

## IR shape

| Field | Meaning |
| --- | --- |
| `Source` | `ddl`, `datasource`, or `replay` |
| `Dialect` | normalized `storage.Dialect` (`question`, `mysql`, `postgres`) |
| `Driver` | original datasource driver name (empty for DDL) |
| `Database` / `Schema` | introspection scope, trimmed; empty when unset |
| `Tables` | `[]SQLTable` with columns, default expressions, primary key, unique and non-unique indexes |

## Pipeline stages

`generateModelFromSchemaIR(ir, opts)` is the only internal entrypoint and runs
two stages with a single options struct (`modelSchemaGenerationOptions`):

1. `prepareModelSchemaIR` — table filter, separate cache namespace, write-ignored
   column markers, output conflict validation, legacy `TypesMap` application,
   structured `TypeOverrides` application, and strict type validation. Physical table names and readable columns are preserved;
   prepared column slices do not alias the input schema.
2. `emitModelSchemaIR` — package default (`model`), module inference,
   import-module computation, style normalization, go_zero layout writes,
   gorm dependency handling.

### Error semantics (pinned by `TestGenerateModelFromSchemaIRBoundaries`)

| Condition | Stage | Error |
| --- | --- | --- |
| No tables after prepare | emit | `model table is required` |
| `Strict` and a requested table is missing | prepare | `strict model generation: requested table not found` |
| `Strict` and a column type is unknown | prepare | `strict model generation: unknown column type "<type>" for <table>.<column>; configure types_map or disable --strict` |
| Prepared tables map to the same table name, Go type, generated entity/repo file, or go_zero facade file | prepare | `model schema output conflict: tables "<a>" and "<b>" both map to <kind> "<name>"` |

On any error the entrypoint writes nothing: no `model/` or `repo/` directory
is created.

## Replay fixture `schemaContract` section

Replay fixtures under `testdata/goctl-datasource-replay/*/replay.json` carry an
optional `schemaContract` object that pins the prepared IR semantics, asserted
by `TestGoctlDatasourceReplayFixtureModelSchemaIR`. Empty fields mean
"not asserted".

| Field | Asserted against |
| --- | --- |
| `source`, `dialect`, `driver`, `database`, `schemaName` | IR metadata after prepare |
| `tables[].name` | physical table name, unchanged by cache prefix (order matters) |
| `tables[].primaryKey` | `SQLTable.PrimaryKey` |
| `tables[].columnCount` | readable column count (omit or `0` to skip) |
| `tables[].writeIgnoredColumns` | retained columns marked `WriteIgnored` for insert/update exclusion |
| `tables[].uniqueIndexes` | composite unique indexes that must exist |
| `tables[].indexes` | non-unique indexes that must exist |

`SQLColumn.DefaultExpr` is metadata collected from DDL or information schema. It is never used to synthesize Go values or interpolate generated SQL; database defaults remain authoritative.

`ModelConfig.TypeOverrides` is applied after the legacy `TypesMap` for the same
normalized SQL type. The selected value is `nullableType`, `unsignedType`, then
`type`; an explicitly selected nullable type is not pointer-wrapped. `importPath`
is optional, but when present must be a canonical import-path-like value and is
emitted once with the entity imports.

For the go_zero SQL facade, `Insert` returns `sql.Result`, while `Update` and
`Delete` remain error-only. The cache facade preserves those signatures and
invalidates a successfully updated or deleted primary-key entry.

SQL model `--home` optionally supplies `model-entity.tpl`; it is resolved under
the local root through the same symlink-rejecting file boundary as generated
output. Only the fixed entity placeholder set is accepted, and the rendered Go
source must pass gofmt. Model `--remote` and `--branch` are accepted compatibility
inputs but do not download or execute templates until a pinned remote-template
contract exists.

With cache-enabled `go_zero` SQL output, the facade primary-key cache prefix is
`<prefix>:<physical-table>:<primary-column>`, so the final model cache key matches
goctl's `<prefix>:<table>:<primary-column>:<value>` form. This contract does not
extend to gofly's advanced unique/list/count/version caches.

The same fixture also drives generation directly
(`TestGenerateModelFromReplaySchemaIRCompiles`): fixture -> raw IR ->
`generateModelFromSchemaIR` -> `go mod tidy` + `go test ./...` in the
generated module, without going through a fake datasource entrypoint.
