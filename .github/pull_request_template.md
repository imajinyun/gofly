## Summary

- 

## Type

- [ ] Bug fix
- [ ] Feature
- [ ] Governance / observability
- [ ] Generator / template
- [ ] CI / release / docs

## Validation

- [ ] `go test ./...`
- [ ] `go test -race ./...`
- [ ] `golangci-lint run ./...`
- [ ] `go test -tags=integration -count=1 ./...`
- [ ] `make examples-smoke` when examples or docs links changed
- [ ] `make docs-link-check` when Markdown links changed
- [ ] Benchmarks or fuzz tests updated when behavior/performance changed

## Compatibility

- [ ] Public API compatibility checked
- [ ] Generated code compatibility considered
- [ ] Config migration documented if needed
- [ ] No new required runtime dependency added to core framework

## Operational Notes

- Metrics/logs/traces added or updated:
- Rollout or rollback considerations:
