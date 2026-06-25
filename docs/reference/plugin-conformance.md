# Plugin Conformance

Schema: `gofly.plugin_conformance.v1`

Plugin adoption requires a conformance suite, not only a registry example. The
suite covers registry JSON schema, plugin manifest schema, digest validation,
least permission checks, a compatibility runner, and a failure isolation report.

## Required cases

| Case | Expected result | Reason |
| --- | --- | --- |
| old protocol | reject | The host supports protocol `1`; protocol `0` must not run. |
| current protocol | accept | Protocol `1` is the current compatible protocol. |
| future protocol | reject unless current is also declared | Future-only plugins require a host upgrade. |
| malicious path | reject | Generated files and patches must stay relative to the project root. |
| digest mismatch | reject | Downloaded plugin bytes must match the registry digest. |
| permission escape | reject | Permissions must be the least permission set for the declared output. |
| failure isolation | accept as reportable failure | Plugin failures must not leave partial host writes. |

Run:

```sh
make plugin-conformance-check
go test -shuffle=on ./cmd/gofly/internal/generator -run 'TestPluginProtocolCompatibilityMatrix|TestPluginRegistryIndexValidationAndFiltering|TestPluginProtocolSchemaContract'
```
