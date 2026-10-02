# Middleware Preset Generation

Schema: `gofly.middleware_presets.v1`

Acceptance gate: `make middleware-preset-check`

`gofly api middleware` has two deliberately separate modes:

- positional names and `--api` generate project-owned middleware skeletons;
- `--preset` installs complete, maintained implementations into
  `internal/middleware`.

The explicit boundary preserves existing commands such as
`gofly api middleware Auth --dir .`. Preset selection does not infer behavior
from a middleware name and does not support an `--option` alias.

## Commands

```sh
gofly api middleware --list
gofly api middleware --list --json
gofly api middleware --preset auth --dir /path/to/service
gofly api middleware --preset auth,cors,csrf --dir /path/to/service
gofly api middleware --preset all --dir /path/to/service --dry-run --json
```

Preset generation installs source files only. It does not edit routes,
configuration, service context, secrets, or `go.mod`. The command reports the
written and unchanged files so the caller can wire middleware explicitly.

## Failure-mode inventory

1. Unknown, empty, or duplicate preset selections must be handled
   deterministically; unknown names fail with the available preset list.
2. `--preset` must reject positional names and `--api` so skeleton generation
   cannot silently change meaning.
3. `--dry-run` requires `--preset` and must not create directories or files.
4. The target must be an existing Go module; the command must not run `go get`
   or mutate module dependencies.
5. Absolute preset paths, root escapes, symlink parents, and symlink leaf
   targets must be rejected.
6. Existing identical files are unchanged. Existing different files block the
   entire operation; preset generation never overwrites project-owned code.
7. A conflict in any selected preset must be detected before the first write.
   A later filesystem failure must roll back files created by the operation.
8. Repeating the same command must produce no content diff.
9. Every registered preset, and the complete `all` selection, must produce
   gofmt-clean code that compiles in an isolated temporary module.
10. Generated authentication and session code must not contain demo secrets or
    silently enable security behavior.
11. Text and JSON list/plan/result output must remain deterministic and must
    distinguish `planned`, `written`, and `unchanged` files.
12. The existing positional and API-declaration skeleton flows must remain
    backward compatible.

## Wiring boundary

The first release does not provide `--wire`. Function-style REST middleware,
gozero-compatible constructor middleware, and helpers such as SSE or WebSocket
have different integration contracts. Automatic route or configuration edits
require a separate profile-aware design and compatibility window.
