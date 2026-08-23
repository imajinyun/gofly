# Contributing to gofly

Thank you for helping improve gofly. This guide is the human companion to
`bin/scripts/check-community-growth.sh`. Quality-gate commands in `Makefile`
remain the source of truth.

## Local Setup

1. Install Go 1.26 or newer.
2. Clone the repository and run `make build`.
3. Confirm the CLI: `./bin/gofly version`.

Do not write generated projects, plugin caches, or `go.mod` tidy experiments
into the repository root. Use `t.TempDir()`, `.tmp-test`, or another temporary
directory.

## Development Checks

Run the smallest gate that matches the change:

```sh
go test -shuffle=on ./...
make examples-smoke
make docs-check
make test-generated-matrix
make governance-10-rounds
```

`GOFLAGS` defaults include `-count=1`. Do not disable shuffle to hide order
coupling.

## Validation Levels

Use the level that matches the blast radius:

| Level | When to use it | Minimum evidence |
| --- | --- | --- |
| L0 docs/comments | Comments, `AGENTS.md`, or explanatory text | Script help or the related make target still exists |
| L1 single-package change | One Go package | `go test -shuffle=on <pkg>` and `go vet <pkg>` |
| L2 subsystem change | generator, CLI, cache, RPC, REST, or similar trees | package tests, race, vet, and the matching make gate |
| L3 full-repository governance | cross-module, `go.mod`, CI, scripts, or release | `make governance-10-rounds` |

## Generated Project Changes

Generator, template, plugin, and `gofly new service` changes must keep output
inside the target project root. Generated-only dependencies such as GORM stay
in the generated project's `go.mod`.

Pull requests that touch generated output must name the Generated output diff
type: formatting, feature addition, compatibility fix, or breaking change.

## Documentation and Examples

- Keep docs in English.
- Prefer runnable examples under `examples/` over prose-only claims.
- New docs must have a Makefile or `bin/scripts/` gate.
- Example modules must keep their own `go.mod` and pass `make examples-smoke`.

## Pull Requests

Every pull request must state:

- Change level (`L0` through `L3`)
- Validation evidence (commands that passed)
- Compatibility impact (public API, CLI JSON, generated layout, or none)
- Generated output diff type, or `none`

Use `.github/pull_request_template.md`. Do not merge with failing required
checks.

## Security

Report vulnerabilities through [SECURITY.md](SECURITY.md) or GitHub's private
security advisory flow. Do not open a public issue for credential leaks,
path-escape bugs, or plugin download flaws.
