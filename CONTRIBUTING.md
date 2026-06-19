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
go test ./...
```

## Development Checks

Run focused tests for the package you changed, then run the repository gates that apply to your change:

```sh
go test ./...
go test -race ./...
go test -tags=integration -count=1 ./...
make tidy
COVERAGE_THRESHOLD=60 make cover-check
API_BASE_REF=origin/main make api-compat
```

Generator changes must also compile generated artifacts in a temporary module. Keep generated code free of unnecessary framework-core dependencies.

## Pull Requests

- Keep PRs focused on one behavior or governance improvement.
- Include tests for new public behavior and generated-code changes.
- Update documentation, examples, and config templates when user-visible behavior changes.
- Call out public API changes, migration notes, and any compatibility risk.

## Security

Do not open public issues for suspected vulnerabilities. Follow `SECURITY.md` so maintainers can coordinate a fix before disclosure.
