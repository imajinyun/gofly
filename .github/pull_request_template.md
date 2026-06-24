## Summary

- 

## Type

- [ ] Bug fix
- [ ] Feature
- [ ] Governance / observability
- [ ] Generator / template
- [ ] CI / release / docs

## Change Level

- [ ] L0 docs/comments only
- [ ] L1 single-package change
- [ ] L2 subsystem change
- [ ] L3 full-repository governance, release, dependency, CI, or cross-module change

## Validation

- [ ] `make ci-fast` for default build/test/tidy gates
- [ ] `make ci` for full local CI gates when touching framework, generator, CI, release, or governance paths
- [ ] `make governance-10-rounds` for governance or release-impacting changes
- [ ] `make dependency-upgrade-check` when `go.mod` or `go.sum` changed; CI integration matrix covers Docker-backed dependency behavior
- [ ] `make release-snapshot` when release archive, checksum, archive SBOM, or GoReleaser packaging changed
- [ ] `make examples-smoke` when examples or docs links changed
- [ ] `make docs-link-check` when Markdown links changed
- [ ] Benchmarks or fuzz tests updated when behavior/performance changed

## Validation Evidence

- Commands run:
- Failed, skipped, or downgraded gates:
- Environment limits:

## CI / Release Governance

- [ ] Required GitHub Actions checks are expected to remain stable, or branch protection and `docs/operations/production-checklist.md` were updated together
- [ ] Release image evidence includes GHCR publish permission, canonical registry digest, release-image Trivy/build evidence, SBOM, and attestation verification when release paths changed
- [ ] Any skipped governance gate has a documented compensating gate and is not required for tag releases

## Compatibility

- [ ] Public API compatibility checked
- [ ] Generated code compatibility considered
- [ ] Config migration documented if needed
- [ ] No new required runtime dependency added to core framework

## Compatibility Impact

- Public Go API:
- CLI flags or JSON output:
- OpenAPI, proto, thrift, or RPC descriptors:
- Plugin protocol or registry:
- Generated project layout/config:
- Migration or deprecation notes:

## Generated Output Diff

- [ ] None
- [ ] Formatting only
- [ ] Feature addition
- [ ] Compatibility fix
- [ ] Breaking change with migration notes
- Diff summary:

## Operational Notes

- Metrics/logs/traces added or updated:
- Rollout or rollback considerations:
