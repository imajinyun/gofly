# Migrating From go-zero

schema: gofly.go_zero_migration_guide.v1

This guide maps existing go-zero and `goctl` projects to gofly generation
surfaces. It is not a claim of full go-zero replacement. The supported path is
an evidence-backed migration workflow that keeps go-zero-compatible generation,
fixture replay, and rollback gates runnable while teams move selected surfaces.

## Migration Map

| go-zero surface | gofly migration surface | Evidence |
| --- | --- | --- |
| `.api` REST contracts | `gofly api gen --file <service.api> --dir <dir> --profile gozero-compatible` | `docs/reference/goctl-generator-compatibility.json`, `docs/reference/goctl-real-project-replay.json` |
| `handler/logic/svc/types` layout | `gofly new api <name> --profile gozero-compatible` | `docs/reference/goctl-generator-compatibility.json` |
| `etc/<service>-api.yaml` | generated `etc/<service>.json` plus explicit REST profile | `docs/reference/rest-middleware-profiles.md` |
| REST middleware and auth | generated REST routes plus route/middleware compatibility evidence | `docs/reference/rest-middleware-profiles.md`, `docs/reference/goctl-real-project-replay.json` |
| zRPC `.proto` | `gofly rpc gen` / `gofly rpc protoc` with compatibility matrix review | `docs/reference/zrpc-proto-compatibility.json` |
| model/cache generation | `gofly model gen --style go_zero` and replay fixtures | `docs/reference/goctl-real-project-replay.json` |
| multi-language API clients | `gofly api client --language <language>` | `docs/reference/api-client-generation.md` |
| production service scaffold | `gofly new service --style production` | `docs/reference/generated-service-layout.md` |
| generated upgrade proof | repeat generation, diff classification, and rollback evidence | `docs/reference/generated-upgrade-dry-run.json` |

## Recommended Path

1. Capture the current go-zero `.api`, `.proto`, SQL DDL, and config files.
2. Replay REST and model generation with the go-zero-compatible profile.
3. Start with `examples/migration/gozero-basic`, then run
   `make goctl-generator-compat-check` and
   `make goctl-real-project-replay-check`.
4. For zRPC services, inspect `docs/reference/zrpc-proto-compatibility.json`
   before claiming parity. External proto imports and google well-known types
   are currently degraded boundaries.
5. Move callers gradually behind routing or discovery. Keep the original go-zero
   service active until generated smoke and rollback evidence pass.
6. For production adoption, run `make generated-service-layout-check` and
   `make generated-upgrade-dry-run-check` before release promotion.

## ServiceContext Mapping

go-zero projects typically wire dependencies through `svc.ServiceContext`.
gofly preserves that mental model in the `gozero-compatible` profile:

- `internal/svc/servicecontext.go` is the generated dependency entrypoint.
- `internal/handler/routes.go` receives `svcCtx`.
- `internal/logic/*logic.go` is constructed with `svcCtx`.
- `internal/types/types.go` keeps request and response DTOs.

The default gofly production scaffold uses the same concept with additional
runtime dependencies such as MQ and config hot reload. The path name may differ
for the default profile, but the dependency entrypoint remains `ServiceContext`.

## zRPC Compatibility Boundaries

Before migrating zRPC code, check the matrix in
`docs/reference/zrpc-proto-compatibility.json`:

- `multiple-services`: supported
- `streaming-rpc`: supported
- `client-wrapper`: supported
- `external-proto-imports`: degraded
- `google-well-known-types`: degraded

Do not describe degraded rows as full goctl or zRPC parity in docs, release
notes, or user-facing migration claims.

## Verification

Use these gates for migration evidence:

```sh
make goctl-generator-compat-check
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
