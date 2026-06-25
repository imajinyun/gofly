# Fuzz Robustness

Schema: `gofly.fuzz_robustness.v1`

gofly keeps fuzzing focused on external input surfaces where malformed input
would otherwise become parser or binding crashes. The current blocking contract
is machine-readable in [`fuzz-robustness.json`](fuzz-robustness.json).

## Covered surfaces

| Surface | Target | Package | Scope |
| --- | --- | --- | --- |
| parser | `FuzzParseAPI` | `./cmd/gofly/internal/generator/` | `.api` contract parser panic-free behavior |
| parser | `FuzzParseProto` | `./cmd/gofly/internal/generator/` | `.proto` contract parser panic-free behavior |
| REST binding | `FuzzBindJSON` | `./rest/` | malformed JSON request binding |
| REST binding | `FuzzBindQuery` | `./rest/` | malformed query parameter binding |

## Gates

`make fuzz-robustness-check` is a fast static gate. It verifies the manifest,
the Go fuzz target functions, the CI `bench + fuzz smoke` commands, the explicit
`make fuzz-smoke` entry point, and the release documentation stay synchronized.

`make fuzz-smoke` runs each bounded fuzz target for 20 seconds:

```sh
make fuzz-robustness-check
make fuzz-smoke
```

`docs-check` depends on `make fuzz-robustness-check` so fuzz coverage cannot
silently drift when public parser or REST binding surfaces change. The longer
fuzz execution remains explicit through `make fuzz-smoke` and the required CI
`bench + fuzz smoke` job.
