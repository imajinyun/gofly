# Cleanup and JSON output failure contracts

This inventory precedes the regression tests and fixes. Both defects were
reproduced through the CLI entry point using disposable fixtures.

| Boundary | Failure mode | Required invariant |
| --- | --- | --- |
| Stale API cleanup | An allowed parent directory is a symlink to a sibling outside the project | Dry-run and execute reject the plan; the outside file remains intact. |
| Whole cleanup plan | A regular file precedes a symlink-parent target in the report | Validate every target before deleting any file; the regular file also remains intact. |
| Removal confinement | Parent links can change after validation | Actual removal uses an opened `os.Root`, not an absolute filesystem path. |
| CLI JSON success output | The provided stdout writer fails | Return an error and nonzero exit status, rather than successful completion without a JSON response. |
| CLI JSON error output | A usage failure is followed by an output-writer failure | Preserve both errors; do not mark the envelope as already reported. |

The command workflows use real local files and injected CLI output streams.
They retain evidence in the standard test artifact directory and require no
external service or user configuration. Existing cleanup tests cover regular
file removal, repeat execution, missing targets, and invalid lexical paths.

Verification through the existing test and coverage gates:

```sh
go test -count=1 -shuffle=on ./cmd/gofly/internal/command \
  -run 'TestExecuteAPICleanupSymlinkBoundary|TestExecuteJSONOutputFailure|TestCLICoverage'
```
