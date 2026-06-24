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

## Security

Do not open public issues for suspected vulnerabilities. Follow `SECURITY.md` so maintainers can coordinate a fix before disclosure.

Do not include secrets in tests, docs, fixtures, logs, control-plane snapshots, plugin registries, or generated configs. Store secret names as environment variable names only.
