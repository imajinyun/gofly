## Summary

<!-- What changed and why. -->

## Change Level

- [ ] L0 docs/comments
- [ ] L1 single-package change
- [ ] L2 subsystem change
- [ ] L3 full-repository governance

## Compatibility Impact

- [ ] Public API compatibility checked
- [ ] Generated code compatibility considered
- [ ] CLI/JSON/control-plane contract unchanged or documented
- [ ] No compatibility impact

## Generated Output Diff

- [ ] none
- [ ] formatting
- [ ] feature addition
- [ ] compatibility fix
- [ ] breaking change (include migration notes)

## Validation Evidence

Commands that passed:

- [ ] `go test -shuffle=on ./...` or the matching package tests
- [ ] `make ci-fast`
- [ ] `make examples-smoke`
- [ ] `make docs-link-check`
- [ ] `make docs-check`
- [ ] `make ci` (for L2/L3 or generated-output changes)
- [ ] `make governance-10-rounds` (for L3 / release)

## Rollout or rollback considerations

<!-- How to pin, revert, or disable the change if a gate fails after merge. -->
