# Production Checklist

Use this checklist before merging or releasing a gofly service.

## Build and tests

```sh
make ci-fast
make security
make release-artifacts-check
```

For framework changes:

```sh
make ci-fast
make test-generated-matrix
make bench-smoke
```

## Required CI checks

Treat these GitHub Actions jobs as branch-protection required checks for `main` and release tags:

- `build & test (go 1.26)` and `build & test (go stable)` for the root build, unit, generated matrix, control-plane smoke, coverage, and CLI version gates.
- `golangci-lint`, `security (govulncheck + gosec)`, `supply-chain lint + OSV`, `CodeQL security analysis`, and `dependency review` for static, vulnerability, workflow, action-pin, and pull-request dependency gates.
- `dependency upgrade validation` for dependency PRs; it runs `go mod verify`, `govulncheck`, and Docker-backed integration tests when `go.mod` or `go.sum` changes.
- `contract / api+rpc (check + breaking)`, `governance gates`, `bench + fuzz smoke`, and `integration tests (...)` for compatibility, governance, performance-smoke, fuzz, and Docker-backed subsystem coverage.
- `docker build + trivy` and `OSSF Scorecard` for container scan evidence and supply-chain posture.
- `release (tagged)` for tag releases; it must depend on all required pre-release jobs and upload release, Docker digest, Trivy, SBOM, and provenance evidence.

Required-check maintenance rules:

- Keep job names stable. If a job is split or renamed, update branch protection and this checklist in the same change.
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
