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

The target `go.mod` must parse successfully and explicitly require
`github.com/imajinyun/gofly`, unless the target is the framework module itself.
A `replace` directive alone does not add a dependency. The parser validates
declared version syntax; the installer performs no network lookup or dependency
upgrade and does not infer an unpublished minimum release. Use a runtime version
compatible with the selected templates and verify the target project compiles.

`--json` emits one error envelope on stdout for usage, flag and operational
failures, without a duplicate stderr message. The global `--output json` flow
remains supported. The AI manifest describes `api middleware`, its preset/list
options, dry-run boundary and file-write effects. Legacy skeleton success keeps
its existing output behavior.

## Failure-mode inventory

1. Unknown, empty, or duplicate preset selections must be handled
   deterministically; unknown names fail with the available preset list.
2. `--preset` must reject positional names and `--api` so skeleton generation
   cannot silently change meaning.
3. `--dry-run` requires `--preset` and must not create directories or files.
4. The target must be an existing Go module with an explicit gofly dependency,
   or the framework module itself; malformed module/version declarations and
   replace-only declarations fail before writing. The command must not run
   `go get` or mutate module dependencies.
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

## Runtime safety and upgrading existing copies

Session identity comes from a verified signed cookie or the private request
context populated by `SessionMiddleware`. An incoming `HeaderName` value is
never authentication evidence. After establishing an identity, the middleware
projects the verified ID into that header for downstream compatibility.
`SessionID` also works on the first request, before the client has a cookie.
Context identity is bound to the cookie name and a fingerprint of the signing
key, so another session configuration cannot reuse it without verification.
Leave `DefaultID` empty for production sessions; it is a deterministic fixture
option, not an identity source for authenticated users.

This corrects the previous behavior that trusted a client-supplied session ID
header and signed it. Clients relying on unsigned headers must transition to
signed cookies or application-controlled authentication. No implicit trusted
proxy bypass is added.

Session cookies now default to `Secure`, `HttpOnly` and `SameSite=Lax`.
Plaintext local development must explicitly set `AllowInsecureCookies=true`;
`Secure=true` and `SameSite=None` always retain Secure cookies. This tightens
the previous zero-value Secure behavior, so review HTTP-only development
configuration when upgrading. The API key header name has a G101 annotation
because it is a protocol identifier, not an embedded credential.

The stability preset preserves HTTP flushing, connection hijacking, HTTP/2
push delegation and ResponseController unwrapping. Its recorded response status
follows the first final header, including an implicit 200 from Write or Flush,
so a late WriteHeader cannot incorrectly trip the breaker.
ResponseController flushing also preserves backend errors through FlushError;
the legacy Flusher method retains its error-free interface.

Existing customized files are still protected from overwrites. To upgrade,
generate the selected presets into a temporary project using the same gofly
dependency, review the diff and merge the session/stability changes into the
application. Do not remove customized middleware just to bypass the conflict.

## Verification artifacts

`make middleware-preset-check` verifies that its regex selects tests in every
target package, including the process entrypoint and help package. It compiles
each preset individually and the full bundle. The generated-project fixture in
`testdata/api/middleware-presets` exercises real HTTP session requests, SSE
delivery before handler completion, WebSocket upgrade and response-status
handling with the race detector enabled. JSON test events are retained in
`.tmp-test/middleware-presets/runtime.jsonl` (override with
`GOFLY_MIDDLEWARE_REPORT_DIR`).
