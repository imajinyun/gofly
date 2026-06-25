# Coverage Trend Evidence

Schema: `gofly.coverage_trend.v1`

Coverage is already enforced by `make cover-check`; this contract makes the
threshold, ratchet, cache-isolation behavior, and volatile artifact boundary
visible to release reviewers and governance automation. The machine-readable
manifest is [`coverage-trend.json`](coverage-trend.json).

## Gates

Run the lightweight contract gate whenever governance docs or coverage policy
change:

```sh
make coverage-trend-check
```

Run the measuring gate before release, dependency upgrades, generated-output
changes, and broad refactors:

```sh
make cover-check
```

`docs-check` depends on `make coverage-trend-check`, so documentation and
release evidence cannot drift from the coverage policy.

## Policy

| Policy | Value | Source |
| --- | --- | --- |
| Minimum threshold | `60%` | `COVERAGE_THRESHOLD` in `Makefile` |
| Ratchet floor | `90%` | `COVERAGE_RATCHET` in `Makefile` |
| Blocking measurement gate | `make cover-check` | `bin/scripts/coverage-check.sh` |
| Evidence contract gate | `make coverage-trend-check` | `bin/scripts/check-coverage-trend.sh` |

`make cover-check` forces uncached test execution with `GOFLAGS=-count=1`,
uses isolated `GOCACHE` and `GOTMPDIR` defaults when callers do not provide
them, sanitizes the generated coverage profile before `go tool cover -func`,
and fails when measured coverage is below the higher of the threshold or
ratchet.

## Artifact Boundary

`coverage.out` is intentionally volatile. It should be produced by local or CI
coverage runs and uploaded as CI evidence when needed, but it must not become a
tracked governance source of truth. Durable release evidence should reference
the command output, the ratchet values, and this manifest instead.
