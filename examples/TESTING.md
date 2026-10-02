# Example Testing Contract

The directories below `examples/` are independent Go modules. Repository-root
Go package patterns do not cross those module boundaries, so every example
gate must discover modules and execute Go commands from each module directory.

## Failure-mode inventory

The example governance gates must detect the following failures:

1. An example contains Go source but has no owning `go.mod`.
2. A discovered module is absent from `examples/catalog.json`, or the catalog
   points to a missing module.
3. A catalog name or path is duplicated, absolute, or escapes `examples/`.
4. A multi-package module is built into a single output file. In particular,
   `gosky` contains a command plus internal packages and must use a directory
   output for `go build ./...` or build an explicit command package.
5. A module passes in the checkout only because a relative replacement or
   repository-local path is available; the copyable gate must build a copy in
   a temporary directory.
6. Unit, race, vet, or build checks accidentally use the root module and skip
   nested example modules.
7. A smoke command hangs, opens a fixed port without a bound, reaches the
   network unexpectedly, or depends on a persistent user cache.
8. A fixture or integration harness is placed under `examples/` even though it
   is not intended to be read, copied, and run by an adopter.
9. A supporting package creates a second standalone module beside the runnable
   example that owns it, causing duplicate dependency files and split gates.

## Test layers

- Module tests remain next to the code they verify.
- `examples-smoke` orchestrates module tests and bounded, machine-readable
  behavior checks; it does not replace module-local assertions.
- `examples-copyable-check` verifies that every catalogued module works after
  being copied outside the repository tree.
- External-service tests use the `integration` build tag and document their
  required environment variables.
- Fixture data belongs under `testdata/`; executable cross-module integration
  harnesses belong under `tests/integration/`.

All temporary build and test outputs must stay in a one-time directory and be
removed when the gate exits.
