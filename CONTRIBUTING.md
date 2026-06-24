# Contributing

Thanks for helping improve gofly. This project is a Go microservice framework, so changes should preserve runtime stability, generated-code compatibility, and production-safe defaults.

## Prerequisites

- Go 1.26 or newer.
- Docker, when running tests with `-tags=integration`.
- `make`, `git`, and a POSIX shell for repository scripts.

## Local Setup

```sh
git clone https://github.com/gofly/gofly.git
cd gofly
go mod download
go test -shuffle=on ./...
```

## Development Checks

Run focused tests for the package you changed, then run the repository gates that apply to your change:

```sh
go test -shuffle=on ./...
go test -shuffle=on -race ./...
go test -tags=integration -count=1 ./...
make tidy
make docs-check
make examples-smoke
make governance-10-rounds
COVERAGE_THRESHOLD=60 make cover-check
API_BASE_REF=origin/main make api-compat
```

## Validation Levels

Use the smallest level that fully covers the changed surface, then record the exact commands in the pull request.

| Level | Use when | Minimum local evidence |
| --- | --- | --- |
| L0 docs/comments | Markdown, comments, or explanatory copy only | `make docs-check` |
| L1 single-package change | Code or tests are limited to one package | `go test -shuffle=on ./path/to/pkg` and `go vet ./path/to/pkg` |
| L2 subsystem change | Generator, CLI, REST, RPC, cache, examples, or governance scripts | Targeted subtree tests, relevant script check, and `make docs-check` when docs are touched |
| L3 full-repository governance | Release, dependency, CI, public API, generated output, or cross-module behavior | `make governance-10-rounds`, plus `make ci` when release or CI surfaces changed |

Generated output, CLI JSON, plugin protocols, OpenAPI/proto/thrift descriptors, public Go APIs, and control-plane fields are compatibility surfaces. If one changes, include the compatibility impact and migration or deprecation notes in the pull request.

## Generated Project Changes

Generator changes must also compile generated artifacts in a temporary module. Keep generated code free of unnecessary framework-core dependencies.

```sh
go test -shuffle=on ./cmd/gofly/internal/generator ./cmd/gofly/internal/command
make test-generated-matrix
make examples-copyable-check
```

Generated-project-only dependencies must stay in the generated project or example module. Do not add them to the root `go.mod` unless the root module imports them directly.

## Documentation and Examples

Documentation should be written in English unless a task explicitly targets another language. User-facing docs, examples, and migration notes should include the command that proves the behavior still works.

```sh
make docs-check
make examples-smoke
make examples-copyable-check
```

## Pull Requests

- Keep PRs focused on one behavior or governance improvement.
- Include tests for new public behavior and generated-code changes.
- Update documentation, examples, and config templates when user-visible behavior changes.
- Call out public API changes, migration notes, and any compatibility risk.
- Select the Change level in the PR template and include validation evidence for that level.
- Record the Generated output diff type: none, formatting only, feature addition, compatibility fix, or breaking change with migration notes.
- Explain failed, skipped, or downgraded gates with the environment limit and compensating gate.

## Security

Do not open public issues for suspected vulnerabilities. Follow `SECURITY.md` so maintainers can coordinate a fix before disclosure.

Do not include secrets in tests, docs, fixtures, logs, control-plane snapshots, plugin registries, or generated configs. Store secret names as environment variable names only.
