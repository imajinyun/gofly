# Migrating From go-zero

schema: gofly.go_zero_migration_guide.v1

This guide maps existing go-zero and `goctl` projects to gofly generation
surfaces. gofly provides a goctl-compatible migration path, not a full goctl replacement. The supported path is an evidence-backed migration workflow that
keeps go-zero-compatible generation, fixture replay, and rollback gates
runnable while teams move selected surfaces.

## Migration Map

| go-zero surface | gofly migration surface | Evidence |
| --- | --- | --- |
| `.api` REST contracts | `gofly api gen --file <service.api> --dir <dir> --profile gozero-compatible` | `docs/reference/goctl-generator-compatibility.json`, `docs/reference/goctl-api-flag-parity.json`, `docs/reference/goctl-real-project-replay.json` |
| `api/http/app/svc/types` layout | `gofly new api <name> --profile gozero-compatible` | `docs/reference/goctl-generator-compatibility.json` |
| `etc/<service>-api.yaml` | generated `etc/<service>.json` plus explicit REST profile | `docs/reference/rest-middleware-profiles.md` |
| REST middleware and auth | generated REST routes plus route/middleware compatibility evidence | `docs/reference/rest-middleware-profiles.md`, `docs/reference/goctl-real-project-replay.json` |
| zRPC `.proto` and runnable service | `gofly new rpc <name> --module <module> --profile gozero-compatible` for a native gRPC service; `gofly rpc gen` for bindings; `gofly rpc protoc` for standard protobuf stubs | `docs/reference/zrpc-proto-compatibility.json`, `docs/reference/goctl-rpc-protoc-parity.json` |
| model/cache generation | `gofly model gen --style go_zero` and replay fixtures | `docs/reference/goctl-real-project-replay.json`, `docs/reference/goctl-model-parity-replay.json` |
| multi-language API clients | `gofly api client --language <language>` | `docs/reference/api-client-generation.md` |
| production service scaffold | `gofly new service --style production` | `docs/reference/generated-service-layout.md` |
| generated upgrade proof | repeat generation, diff classification, and rollback evidence | `docs/reference/generated-upgrade-dry-run.json` |

## 30-minute migration path

This is the shortest evidence-backed path from an existing go-zero service to a
gofly-compatible generated surface. It is not a full goctl replacement.

1. Install gofly and keep the original go-zero service running.

   ```sh
   go install github.com/imajinyun/gofly/cmd/gofly@latest
   gofly version
   ```

2. Copy `examples/migration/gozero-basic` and read its JSON report:

   ```sh
   go test -C examples/migration/gozero-basic ./...
   go -C examples/migration/gozero-basic run .
   ```

3. Replay the REST contract with the go-zero-compatible profile:

   ```sh
   gofly api gen --file service.api --dir ./migrated --profile gozero-compatible
   ```

4. Replay models with the go_zero style:

   ```sh
   gofly model mysql ddl --src schema.sql --dir ./internal --style go_zero
   ```

5. For a runnable native gRPC service, inspect
   `docs/reference/zrpc-proto-compatibility.json`, then generate the
   gozero-compatible profile:

   ```sh
   gofly new rpc service --module example.com/service --profile gozero-compatible
   ```

   Use `gofly rpc gen --transport grpc` when only gofly gRPC bindings are
   needed, or `gofly rpc protoc` when only standard protobuf/gRPC stubs are
   needed. The default production `gofly new rpc` profile remains the gofly
   HTTP-RPC runtime; the profile flag is therefore an explicit transport
   contract during migration.

6. Run the migration gates before switching traffic:

   ```sh
   make goctl-api-flag-parity-check
   make goctl-model-parity-replay-check
   make goctl-generator-compat-check
   make goctl-real-project-replay-check
   ```

7. Keep the original go-zero process routable until those gates and the
   generated module `go test ./...` pass. Rollback by discarding the generated
   directory and pinning the previous gozero-compatible generator behavior.

Time-box: steps 1-2 should take a few minutes; steps 3-6 depend on contract
size but should complete inside thirty minutes for a single API plus one
model package.

## Recommended Path

1. Capture the current go-zero `.api`, `.proto`, SQL DDL, and config files.
2. Replay REST and model generation with the go-zero-compatible profile.
3. Start with `examples/migration/gozero-basic`, then run
   `make goctl-api-flag-parity-check`,
   `make goctl-model-parity-replay-check`,
   `make goctl-generator-compat-check`, and
   `make goctl-real-project-replay-check`.
4. For zRPC services, inspect `docs/reference/zrpc-proto-compatibility.json`
   and `docs/reference/goctl-rpc-protoc-parity.json` before claiming parity.
   Local proto imports, common google well-known types, multiple services,
   streaming RPCs, generated client wrappers, and migration-critical
   `rpc protoc` flags have dedicated compatibility evidence.
5. Move callers gradually behind routing or discovery. Keep the original go-zero
   service active until generated smoke and rollback evidence pass.
6. For production adoption, run `make generated-service-layout-check` and
   `make generated-upgrade-dry-run-check` before release promotion.

## ServiceContext Mapping

go-zero projects typically wire dependencies through `svc.ServiceContext`.
gofly preserves that mental model in the `gozero-compatible` profile:

- `internal/svc/servicecontext.go` is the generated dependency entrypoint.
- `internal/api/http/routes.go` receives `svcCtx`.
- `internal/app/*logic.go` is constructed with `svcCtx`.
- `internal/types/types.go` keeps request and response DTOs.

The default gofly production scaffold uses the same concept with additional
runtime dependencies such as MQ and config hot reload. The path name may differ
for the default profile, but the dependency entrypoint remains `ServiceContext`.

## API Flag Parity

API flag migration parity is tracked by
`docs/reference/goctl-api-flag-parity.json` and validated by
`make goctl-api-flag-parity-check`. The contract covers `api go --test`,
`api go --type-group`, `api format --stdin`, and compatibility acceptance for
`api format --declare`. The `--declare` flag skips missing type declaration
checks during formatting, matching goctl's migration-critical formatter
behavior without claiming a full formatter clone.

## Model Layout

The `go_zero` model profile keeps generated table structs separate from
repository code. When `--dir` points at a project subdirectory such as
`internal`, generated files use this layout:

- `internal/model/<table>_gen.go` contains generated table structs, column
  constants, and `TableName` metadata.
- `internal/model/vars.go` contains shared model-level compatibility variables
  such as `ErrNotFound`.
- `internal/repo/<table>.go` contains generated repository methods.
- `internal/repo/<table>model.go` and `internal/repo/<table>model_gen.go`
  contain the go-zero-style model facade and default repository-backed
  implementation.

For example:

```sh
gofly model mysql ddl \
  --src approvals.sql \
  --dir ./internal \
  --module example.com/hello \
  --style go_zero
```

This layout intentionally avoids putting every go-zero-style model artifact in
one `model` directory. Existing `repo/<table>model.go` and `model/vars.go` are
create-only extension files: regeneration preserves their contents and permissions.
Entity files, `repo/<table>.go`, and `repo/<table>model_gen.go` remain generator-owned;
place custom repository methods in a separate file rather than editing them.

For SQL model commands, `--prefix` is a cache namespace prefix (default `cache`),
not a table-name transformation. Physical table names, entity names, and filenames
retain the source table prefix. `--ignore-columns` excludes columns from inserts
and updates but retains them in entities, reads, and index metadata. The CLI
excludes `create_at,created_at,create_time,update_at,updated_at,update_time` by
default; pass `--ignore-columns=""` to disable that default. SQL batches and upserts
use the same write-column selection; GORM marks ignored fields read-only.

Migration from older gofly output requires reviewing renamed prefixed model files
and updating imports/type references. Old generated files are not deleted
automatically; merge custom changes before removing stale output. Redis cache
namespaces change, including table-scoped list/count/version keys, so expect cold
caches and avoid mixed-version writers sharing old and new namespaces.
This does not establish byte-for-byte goctl cache-key or Model API compatibility.

Model migration parity is tracked by
`docs/reference/goctl-model-parity-replay.json` and validated by
`make goctl-model-parity-replay-check`. This gate executes extension-preservation,
generated SQL read/write, and datasource schema tests rather than only checking
that flags exist. DDL and datasource metadata preserve auto-increment, strict
unsigned primary-key mapping, and database default expressions; generated SQL leaves
database defaults authoritative. The go_zero `Insert` facade returns `sql.Result`.
The matching SQL facade keeps `Update` and `Delete` error-only, consistent with
goctl; cache-enabled facades invalidate the affected primary-key entry after a
successful mutation.
For a database type whose Go representation varies by shape, preserve the legacy
`model.typesMap` and add `model.typeOverrides` in `.gofly/config.json`; override
selection is nullable first, then unsigned, then base type. An optional `importPath`
is validated before it is emitted into entity imports, for example:

```json
{
  "model": {
    "typeOverrides": {
      "bigint": {
        "type": "int64",
        "unsignedType": "uint64",
        "nullableType": "sql.NullInt64",
        "importPath": "database/sql"
      }
    }
  }
}
```

For cache-enabled model generation, `NewCached<Type>Model(conn, opts...)` returns
the same Model interface, caches reads, invalidates after writes, and declines to
reuse the cache across a transaction session. Normal repository convenience methods
remain error-only. For Mongo migration, use `--style go_zero_mongo`: it is an
opt-in v2 profile that emits goctl-shaped `New<Type>Model` constructors, BSON
ObjectIDs, `Update` result and `Delete` count contracts, and go-zero `mon`/`monc`
runtime wiring. Mongo accepts comma-separated types, `--easy` emits a deterministic
`<Type>CollectionName` constant, and `--prefix` contributes to generated cache
namespaces without changing the type name. Existing `driver` output remains a
separate direct-collection style. Full goctl replacement is not claimed; structural
`model-layout-difference` remains an accepted oracle category.

For cache-enabled SQL `go_zero` facades, primary-key entries use the goctl-shaped
namespace `prefix:table:primary_column:value`. The richer gofly unique/list/count/
version caches keep their own encoding and must not share a Redis namespace with
a goctl writer until a dedicated advanced-key oracle is introduced.

For SQL model customization, `--home <dir>` executes a local
`model-entity.tpl` for generated entity files. The supported placeholders are
`{{.Package}}`, `{{.Imports}}`, `{{.Type}}`, `{{.Table}}`,
`{{.CachePrefix}}`, `{{.Columns}}`, and `{{.Fields}}`; output is gofmt-checked.
The template root and target file must not traverse symlinks. `--remote` and
`--branch` remain accepted goctl-compatible inputs but do not trigger remote model
template execution unless `--template-sha256` supplies the exact 64-character
lowercase digest of the remote `model-entity.tpl`. The remote path reads only that
fixed file inside a temporary root; a missing or mismatched digest writes no model
output.

## zRPC Compatibility Boundaries

Before migrating zRPC code, check the matrix in
`docs/reference/zrpc-proto-compatibility.json`:

- `multiple-services`: supported
- `streaming-rpc`: supported
- `client-wrapper`: supported
- `external-proto-imports`: supported for local imports
- `google-well-known-types`: supported for common WKT mappings
- `runnable-grpc-layout`: supported for `new rpc --profile gozero-compatible`
- `grpc-runtime-golden-path`: supported for discovery lifecycle, health, safe client defaults, and selectable P2C-EWMA or consistent-hash balancing

The generic bearer/RBAC interceptor remains the default authentication model.
Projects migrating go-zero app/token credentials can opt into
`NewRedisTokenValidator` and `WithAppTokenCredentials`; transport security is
required unless the client explicitly enables development-only insecure mode.

`docs/reference/goctl-rpc-protoc-parity.json` separately tracks goctl-style
`rpc protoc` flags. Standard protoc mode forwards include paths, `go_out`,
`go-grpc_out`, `go_opt`, and `go-grpc_opt` as argv entries. The built-in
`--plugin gofly` path maps `--multiple`, `--client=false`, `--module`, and
`--name-from-filename` into explicit gofly plugin options. External plugin names
are accepted without execution by default and are forwarded to `protoc` only
when `--allow-external-plugin` is explicitly set; plugin values are passed as
argv entries, not shell strings.

Do not describe future matrix rows as full goctl or zRPC parity until their
status is supported by the matrix gate.

## Verification

Use these gates for migration evidence:

```sh
make goctl-generator-compat-check
make goctl-api-flag-parity-check
make goctl-rpc-protoc-parity-check
make goctl-model-parity-replay-check
make goctl-real-project-replay-check
make zrpc-proto-compatibility-check
make api-client-generation-check
make rest-profile-check
make generated-service-layout-check
make generated-upgrade-dry-run-check
go test -C examples/migration/gozero-basic ./...
```

If default Go build cache permissions fail locally, rerun with temporary cache
directories:

```sh
GOCACHE=/private/tmp/gofly-cache-migration GOTMPDIR=/private/tmp/gofly-tmp-migration make goctl-generator-compat-check
```

## Rollback

Rollback by pinning the previous gozero-compatible generator behavior, keeping
the source go-zero service routable, and discarding generated output that does
not pass the replay matrix or generated upgrade dry-run gates.
