## Summary

- 

## Type

- [ ] Bug fix
- [ ] Feature
- [ ] Governance / observability
- [ ] Generator / template
- [ ] CI / release / docs

## Validation

- [ ] `make ci-fast` for default build/test/tidy gates
- [ ] `make ci` for full local CI gates when touching framework, generator, CI, release, or governance paths
- [ ] `make governance-10-rounds` for governance or release-impacting changes
- [ ] `make dependency-upgrade-check` when `go.mod` or `go.sum` changed; CI integration matrix covers Docker-backed dependency behavior
- [ ] `make release-snapshot` when release archive, checksum, archive SBOM, or GoReleaser packaging changed
- [ ] `make examples-smoke` when examples or docs links changed
- [ ] `make docs-link-check` when Markdown links changed
- [ ] Benchmarks or fuzz tests updated when behavior/performance changed

## CI / Release Governance

- [ ] Required GitHub Actions checks are expected to remain stable, or branch protection and `docs/operations/production-checklist.md` were updated together
- [ ] Release image evidence includes GHCR publish permission, canonical registry digest, release-image Trivy/build evidence, SBOM, and attestation verification when release paths changed
- [ ] Any skipped governance gate has a documented compensating gate and is not required for tag releases

## Compatibility

- [ ] Public API compatibility checked
- [ ] Generated code compatibility considered
- [ ] Config migration documented if needed
- [ ] No new required runtime dependency added to core framework

## Operational Notes

- Metrics/logs/traces added or updated:
- Rollout or rollback considerations:
