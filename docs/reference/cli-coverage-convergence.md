# CLI coverage convergence

This inventory was recorded before adding the command workflow tests. It covers
offline workflows through `command.ExecuteWithIO`, including argument dispatch,
file loading, validation, output formatting, and filesystem effects. The tests
introduce no coverage exclusions or ratchet changes. Companion fixes for defects
found by these workflows are described below.

## Failure-mode inventory

| Workflow | Failure mode or boundary | Expected invariant |
| --- | --- | --- |
| Gateway profile validation | Missing, malformed, or structurally invalid current configuration; missing or malformed candidate | Return the relevant input error in one JSON error envelope, without reporting successful validation. |
| Gateway profile validation | Unknown flags or output formats | Reject unsupported CLI inputs without changing either source file. |
| Gateway profile text output | Identical, breaking, and invalid candidates | Distinguish compatible, breaking, and invalid reports; expose the offending mapping or validation error. |
| Gateway aggregation validation | Positional config/candidate paths | Resolve the same configuration as named flags and preserve route selection. |
| Gateway aggregation validation | Missing files, malformed wrappers/direct objects, invalid current routes, unknown flags or formats | Return an actionable error with one JSON envelope; preserve inputs. |
| Gateway aggregation reports | Identical, breaking, and invalid candidates in text/Markdown | Show accurate status, mapping changes, validation errors, and an explicit empty diff. |
| Gateway OpenAPI validation | Missing/malformed documents, invalid imports on either side, or absent aggregation route | Distinguish input/import/route errors; a SARIF import failure must identify the failing source document. |
| Gateway OpenAPI reports | Query mapping, required-field, and body-template contract changes | SARIF classifies the corresponding rule, severity, and source location; text output exposes the same breaking result. |
| API stale cleanup | Missing/malformed report or unsupported report version | Stop before deleting any file. |
| API stale cleanup | Blank/duplicate entries and already-removed files | Plans are deduplicated; repeated execution reports skipped files without failing or deleting unrelated files. |
| API stale cleanup | Non-Go paths, paths outside supported directories, routes.go, absolute paths, or traversal | Validate the entire plan before deletion, preserving even a valid file that precedes an invalid entry. |
| API stale cleanup | A listed .go path is a nonempty directory | Surface the removal error and preserve its contents. |
| API stale cleanup | Unknown operation or flag | Report a usage error before filesystem work. |
| Middleware catalog and preset plan | Text catalog, dry-run, quiet mode | Catalog names remain discoverable; a plan lists target files while leaving the project unchanged. |
| Middleware parameters | List combined with generation, unknown preset/API file/flag, invalid bool, --json=false, or -- terminator | Reject invalid requests; local JSON intent yields one error envelope, while false/terminated JSON flags do not request JSON. |
| Middleware JSON output | Output writer fails while reporting a command error | Preserve both the original usage failure and the writer failure. The initial probe exposed a defect now covered by the central output regressions described below. |
| Example commands | list/ls, unknown flag/name, positional name after flags | Dispatch correctly and return usable catalog or usage errors without launching an example. |

Release post-generation corruption paths and vanished-working-directory failures
are excluded: deterministic CLI reproduction would require artificial internal
fault injection. The tests use real local input files and command dispatch; they
do not mock validators or directly invoke private implementation helpers.

## Isolation and evidence

Every mutable project and fixture lives below `t.TempDir()`. Gateway targets are
inert configuration values; validation never starts a listener or contacts them.
Commands do not run dependency downloads, read user HOME, or use a persistent
plugin cache. Tests execute sequentially because command I/O is process-scoped.
These offline command workflows run in the default suite so the existing
coverage gate includes them, following the repository's E2E-first requirement.

Each invocation writes its arguments, stdout, stderr, and returned error to
`t.ArtifactDir()` for repeatable review. Pass `-artifacts` and `-outputdir` to
retain those files after the run. This note specifies verification for the
existing gate; it does not introduce a new governance target or automation.

```bash
GOFLAGS=-count=1 go test -shuffle=on -run '^TestCLICoverage' \
  -artifacts -outputdir=/tmp/gofly-cli-evidence \
  -coverpkg=./cmd/gofly/internal/command/...,./gateway/...,./cmd/gofly/internal/generator/... \
  -coverprofile=cli-coverage.out ./cmd/gofly/internal/command
GOFLAGS=-count=1 go test -shuffle=on -race -run '^TestCLICoverage' \
  ./cmd/gofly/internal/command
go vet ./cmd/gofly/internal/command
```

Coverage comparison must match the exact source-block location and statement
count against the prior profile. Only a known block changing from zero to a
positive counter contributes to the union gain. Unknown or changed blocks must
be reported separately rather than changing the denominator.

## Findings resolved by companion security changes

The output-writer probe found that `printJSON` discarded the error returned by
`cliOutputln`. An `ExecuteWithIO` middleware catalog request could return success
when its writer rejected the JSON, and a usage failure could be marked as already
reported even though the JSON was not written. The companion change now checks
JSON writes in `json_error.go`; `cli_output_failure_test.go` exercises both
successful-command and error-reporting failures. The isolated initial failing
probe remains separate from the passing coverage evidence.

A real `api cleanup stale --execute` invocation also reproduced a parent-symlink
escape: `project/internal/app` pointed to a sibling directory, and cleanup
removed that sibling's file. Only disposable temporary fixtures were used. The
companion cleanup change validates targets before deletion and removes files
through `os.Root`. `api_cleanup_symlink_test.go` preserves the regression, and
`api-cleanup-safety.md` records the safety invariants and verification.

Because these fixes change source blocks in `api_cleanup_command.go` and
`json_error.go`, a historical profile must not be merged into their new blocks
by line number alone. Report the gain in unchanged files separately and use a
fresh full run for the final repository coverage ratio.

## Verified outcome

The full governance run on Go 1.26.9 measured **44,179 / 48,795 statements =
90.5400143457%**, passing the unchanged **90.5%** ratchet. The previous profile
was 44,082 / 48,790 = 90.3504816561%; the five additional source statements
come from the companion production fixes. No source blocks were removed to
improve the ratio.

Both MySQL and PostgreSQL migration E2Es passed. Their merged counters preserve
every source block and statement count from the fresh baseline profile. The
repeatable profiles and database reports are written to
`.tmp-test/migration-coverage/` by the existing coverage gate. Keep the ratchet
at 90.5% until a later run establishes a larger, stable margin.
