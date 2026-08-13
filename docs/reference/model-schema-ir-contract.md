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
| `Tables` | `[]SQLTable` with columns, primary key, unique and non-unique indexes |

## Pipeline stages

`generateModelFromSchemaIR(ir, opts)` is the only internal entrypoint and runs
two stages with a single options struct (`modelSchemaGenerationOptions`):

1. `prepareModelSchemaIR` — table filter, prefix trim, ignore columns,
   `TypesMap` application, strict type validation. Mutates only `ir.Tables`;
   metadata fields are preserved.
2. `emitModelSchemaIR` — package default (`model`), module inference,
   import-module computation, style normalization, go_zero layout writes,
   gorm dependency handling.

### Error semantics (pinned by `TestGenerateModelFromSchemaIRBoundaries`)

| Condition | Stage | Error |
| --- | --- | --- |
| No tables after prepare | emit | `model table is required` |
| `Strict` and a requested table is missing | prepare | `strict model generation: requested table not found` |
| `Strict` and a column type is unknown | prepare | `strict model generation: unknown column type "<type>" for <table>.<column>; configure types_map or disable --strict` |

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
| `tables[].name` | table name after prefix trim (order matters) |
| `tables[].primaryKey` | `SQLTable.PrimaryKey` |
| `tables[].columnCount` | surviving column count (omit or `0` to skip) |
| `tables[].absentColumns` | columns removed by `ignoreColumns` |
| `tables[].uniqueIndexes` | composite unique indexes that must exist |
| `tables[].indexes` | non-unique indexes that must exist |

The same fixture also drives generation directly
(`TestGenerateModelFromReplaySchemaIRCompiles`): fixture -> raw IR ->
`generateModelFromSchemaIR` -> `go mod tidy` + `go test ./...` in the
generated module, without going through a fake datasource entrypoint.
