# Generated Version Compatibility

`make generated-version-compat-check` validates generated project snapshots for
`old`, `current`, and `future` profiles. The matrix lives in
`testdata/generated-compat/matrix.json` and exists to keep generated project
snapshots reproducible across scaffold evolution.

The compatibility contract is:

- `old`: legacy fixture inputs must still generate, compile, and run generated
  project smoke tests. Expected changes must be explainable diffs.
- `current`: current fixture inputs must regenerate with no unexplained diff.
- `future`: future fixture inputs may expose experimental fields, but generated
  output must compile and report explainable diffs instead of writing unsafe
  artifacts.

Generated project snapshots are always created under temporary directories.
They must not be committed. Generated-only dependencies must stay in the
generated project `go.mod` or in an isolated temporary module, never in the
gofly root module.

Compatibility reports use schema `gofly.generated_version_compat_report.v1`.
Every report must include generated file counts, `go test ./...` evidence,
repeat-generation diff status, the expected profile diff, and verification
commands. Promotion is blocked unless repeat generation is clean or the diff is
classified as an expected `compatible-addition`, `formatting-only`, or
`breaking-candidate` with a rollback note.
