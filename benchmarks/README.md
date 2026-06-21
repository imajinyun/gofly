# gofly benchmarks

This directory contains reproducible microbenchmarks for Phase 2 performance transparency. The goal is not to claim gofly is always the fastest framework; the goal is to keep benchmark scenarios explicit, runnable, and comparable across releases.

## Suites

HTTP benchmarks cover:

- hello route dispatch
- path parameters
- JSON binding
- five-layer middleware chain
- gofly OpenAPI disabled/enabled route table overhead
- gofly REST governance disabled/enabled client overhead

HTTP candidates:

- `net/http`
- gofly REST
- Gin
- Echo
- Chi
- Fiber
- Hertz

RPC benchmarks cover:

- gofly RPC unary call over the real HTTP RPC client/server path
- gRPC-Go unary call over `bufconn`

Kitex is optional. Downstream services that already carry generated Kitex fixtures can add a `kitex` sub-benchmark under `BenchmarkRPCUnary` without making Kitex a required root dependency for every gofly checkout.

## Local reproduction

Run a fast smoke pass:

```sh
make bench-smoke
```

Run statistically useful samples and save raw output to `bench/current.txt`:

```sh
make bench-stat
```

Compare `bench/current.txt` with `bench/baseline.txt`:

```sh
make bench-compare
```

Generate a Markdown trend artifact at `bench/summary.md`:

```sh
make bench-trend
```

For release notes, include `bench/summary.md` with the release artifacts and paste statistically significant `benchstat` rows into the release description.

## Notes on fairness

- Benchmarks use in-process test transports where possible to avoid network noise.
- Server benchmarks allocate a fresh request/recorder per iteration because request bodies and response state are single-use.
- gofly default production middleware is disabled in cross-framework HTTP route benchmarks so that framework dispatch, binding, and middleware chain costs are compared explicitly.
- The OpenAPI and governance benchmarks are gofly-specific because those capabilities are part of gofly's product surface rather than common built-ins across all compared routers.
