# Migrating From go-zero

schema: gofly.go_zero_migration_guide.v1

This guide maps existing go-zero and `goctl` projects to gofly generation
surfaces. It is not a claim of full go-zero replacement. The supported path is
an evidence-backed migration workflow that keeps go-zero-compatible generation,
fixture replay, and rollback gates runnable while teams move selected surfaces.

## Migration Map

| go-zero surface | gofly migration surface | Evidence |
| --- | --- | --- |
| `.api` REST contracts | `gofly api gen --file <service.api> --dir <dir> --profile gozero-compatible` | `docs/reference/goctl-generator-compatibility.json`, `docs/reference/goctl-api-flag-parity.json`, `docs/reference/goctl-real-project-replay.json` |
| `api/http/app/svc/types` layout | `gofly new api <name> --profile gozero-compatible` | `docs/reference/goctl-generator-compatibility.json` |
| `etc/<service>-api.yaml` | generated `etc/<service>.json` plus explicit REST profile | `docs/reference/rest-middleware-profiles.md` |
| REST middleware and auth | generated REST routes plus route/middleware compatibility evidence | `docs/reference/rest-middleware-profiles.md`, `docs/reference/goctl-real-project-replay.json` |
| zRPC `.proto` | `gofly rpc gen` / `gofly rpc protoc` with compatibility matrix review | `docs/reference/zrpc-proto-compatibility.json`, `docs/reference/goctl-rpc-protoc-parity.json` |
| model/cache generation | `gofly model gen --style go_zero` and replay fixtures | `docs/reference/goctl-real-project-replay.json`, `docs/reference/goctl-model-parity-replay.json` |
| multi-language API clients | `gofly api client --language <language>` | `docs/reference/api-client-generation.md` |
| production service scaffold | `gofly new service --style production` | `docs/reference/generated-service-layout.md` |
| generated upgrade proof | repeat generation, diff classification, and rollback evidence | `docs/reference/generated-upgrade-dry-run.json` |

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
`api format --declare`. The `--declare` flag is intentionally documented as
`compat-accepted` until a real goctl formatter oracle fixture proves
declare-specific formatter semantics.

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
one `model` directory and keeps repeated DDL-driven generation focused on the
generated model and repository files.

Model migration parity is tracked by
`docs/reference/goctl-model-parity-replay.json` and validated by
`make goctl-model-parity-replay-check`. The contract covers migration-critical
options such as cache generation, strict validation, ignored columns, table
prefix trimming, table filters, database/schema selection, datasource aliases,
and Mongo type/cache/prefix inputs. It does not claim byte-for-byte goctl model
layout parity while `model-layout-difference` remains an accepted oracle
category.

## zRPC Compatibility Boundaries

Before migrating zRPC code, check the matrix in
`docs/reference/zrpc-proto-compatibility.json`:

- `multiple-services`: supported
- `streaming-rpc`: supported
- `client-wrapper`: supported
- `external-proto-imports`: supported for local imports
- `google-well-known-types`: supported for common WKT mappings

`docs/reference/goctl-rpc-protoc-parity.json` separately tracks goctl-style
`rpc protoc` flags. Standard protoc mode forwards include paths, `go_out`,
`go-grpc_out`, `go_opt`, and `go-grpc_opt` as argv entries. The built-in
`--plugin gofly` path maps `--multiple`, `--client=false`, `--module`, and
`--name-from-filename` into explicit gofly plugin options. External plugin names
are accepted for migration compatibility but are not executed by default until a
separate security contract covers command path validation, output limits,
timeouts, and audit evidence.

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
