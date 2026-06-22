# Production Checklist

Use this checklist before merging or releasing a gofly service.

## Build and tests

```sh
make ci
make governance-10-rounds
make dependency-upgrade-check
```

For framework changes:

```sh
make ci
make test-generated-matrix
make generated-control-plane-smoke
make bench-smoke
```

Before publishing a tag release, also run a local release snapshot when GoReleaser is available:

```sh
make release-snapshot
```

## Required CI checks

Treat these GitHub Actions jobs as branch-protection required checks for `main` and release tags:

- `build & test (go 1.26)` and `build & test (go stable)` for the root build, unit, generated matrix, control-plane smoke, coverage, and CLI version gates.
- `golangci-lint`, `security (govulncheck + gosec)`, `supply-chain lint + OSV`, `CodeQL security analysis`, and `dependency review` for static, vulnerability, workflow, action-pin, and pull-request dependency gates.
- `dependency upgrade validation` for dependency PRs; it runs `go mod verify` and `govulncheck` when `go.mod` or `go.sum` changes, while Docker-backed coverage is provided by the required `integration tests (...)` matrix.
- `branch protection required-check audit` to detect drift between the configured `main` branch protection checks and this checklist.
- `contract / api+rpc (check + breaking)`, `governance gates`, `bench + fuzz smoke`, and `integration tests (...)` for compatibility, governance, performance-smoke, fuzz, and Docker-backed subsystem coverage.
- `docker build + trivy` and `OSSF Scorecard` for container scan evidence and supply-chain posture.
- `release (tagged)` for tag releases; it must depend on all required pre-release jobs and upload release, Docker digest, Trivy, SBOM, and provenance evidence.

Required-check maintenance rules:

- Keep job names stable. If a job is split or renamed, update branch protection and this checklist in the same change.
- `branch protection required-check audit` verifies the configured default-branch required-check list against this checklist on scheduled and default-branch push CI runs; missing project checks fail, extra external checks are reported as informational.
- GitHub Actions `uses:` entries must stay pinned to 40-character commit SHAs; `make actions-pin-check` enforces this.
- Reports and evidence artifacts should write to runner temp or explicit artifact directories, not the repository root.

## Runtime

- [ ] REST, RPC, and admin ports are explicit.
- [ ] `/healthz` and `/readyz` return expected status.
- [ ] `/admin/control-plane` is reachable only from trusted networks.
- [ ] generated smoke tests pass.

## Governance

- [ ] timeouts exist for slow paths.
- [ ] retry attempts are bounded.
- [ ] breakers protect unstable downstreams.
- [ ] rate and concurrency limits protect public or expensive endpoints.

## Config and discovery

- [ ] config files are versioned and reviewed.
- [ ] environment overrides are documented.
- [ ] discovery provider and endpoints are correct for the target environment.

## Observability and security

- [ ] logs include request id and trace id.
- [ ] metrics and traces are exported to trusted backends.
- [ ] admin token or private networking is configured.
- [ ] secrets are not present in source, logs, or snapshots.
