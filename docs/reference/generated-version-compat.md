# Generated Version Compatibility

Schema: `gofly.generated_version_compat.v1`

Generated projects are a v1 candidate adoption surface. This matrix keeps old,
current, and future contract inputs visible so the generator can evolve without
silently breaking existing services.

Validate the matrix with:

```sh
make generated-version-compat-check
```

The gate creates a temporary project for every profile, runs `gofly new
service` with the profile's `.api`, `.proto`, and service config inputs, applies
a local module replacement for the current framework checkout, runs `go mod
tidy`, verifies repeat generation with a clean diff, and then executes `go test
./...` in the generated project. It writes a machine-readable
`gofly.generated_version_compat_report.v1` report with generated file counts,
repeat-generation diff status, test status, and the expected diff explanation.

## Profiles

| Profile | Fixtures | Expected diff | Verification |
| --- | --- | --- | --- |
| old | `testdata/generated-compat/v0.1/orders.api`, `greeter.proto`, `service-config.json` | Additive generated files and compatibility shims only. | Generate, compile, run generated smoke tests. |
| current | `testdata/generated-compat/current/orders.api`, `greeter.proto`, `service-config.json` | No unexplained diff from current templates. | Generate, compile, run generated smoke tests. |
| future | `testdata/generated-compat/future/orders.api`, `greeter.proto`, `service-config.json` | Experimental fields must be labeled and ignored safely by current profiles. | Generate, compile, report explainable diffs. |

## generated project snapshots

Each profile records the contract inputs and a short expected snapshot summary
in `testdata/generated-compat/matrix.json`. Snapshot checks must explain whether
the diff is:

- formatting only;
- compatible feature addition;
- compatibility shim;
- experimental future field ignored by the current generator;
- breaking candidate requiring release governance.

The compatibility gate does not require real historical tags in local smoke
mode. Release CI may expand the same matrix by checking the current generator
against previous tagged fixtures.
