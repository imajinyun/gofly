# Installation

## Requirements

- Go 1.26 or newer.
- Network access for `go install` and module downloads.
- Optional: `benchstat` for performance comparisons.

## Install the CLI

```sh
go install github.com/gofly/gofly/cmd/gofly@latest
gofly version
```

For local development from a checkout:

```sh
make build
./bin/gofly version
```

## Verify the toolchain

```sh
gofly doctor
gofly help new service
```

`gofly doctor` checks the local Go environment and common project assumptions. Use `gofly help <command>` as the source of truth for CLI flags.

## CI usage

Generated projects should run:

```sh
go test ./...
```

The gofly repository uses:

```sh
make ci-fast
make test-generated-matrix
make bench-smoke
```

Use `make bench-trend` before releases when you need a Markdown performance artifact.
