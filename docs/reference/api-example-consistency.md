# API Example Consistency

Schema: `gofly.api_example_consistency.v1`

Public API documentation should not drift away from runnable examples or package
examples. The consistency manifest is
[`api-example-consistency.json`](api-example-consistency.json).
Adopter-facing example health metadata is tracked in
[`examples-health-index.json`](examples-health-index.json).

## Gate

```sh
make api-example-consistency-check
```

The gate validates that each tracked public surface has:

- a documentation page;
- a runnable example directory or machine-readable evidence artifact;
- a package `Example...` test or script-level contract test;
- a Makefile gate that exercises the example or evidence.
- example health metadata for copyability, smoke commands, ports, output schema,
  runtime mode, and risk notes.

`docs-check` depends on this gate so new public surfaces must stay connected to
copyable examples and executable contracts.
