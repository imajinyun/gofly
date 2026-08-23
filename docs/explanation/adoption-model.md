# Explanation: adoption model

gofly is for teams that want generated microservice structure, runtime
governance, and machine-readable operator state in one Go-native toolkit.

## What to adopt first

1. **Tier 0 golden path.** `gofly new service --style production` is the
   default shape. Treat its layout as an external contract.
2. **One runnable example.** Copy `examples/production/production-orders` or
   `examples/getting-started/restserver` before writing a custom template.
3. **Control-plane and CLI JSON.** Operators and agents should read
   `/admin/control-plane` and `gofly --json` envelopes instead of scraping
   logs.
4. **goctl-compatible migration only where evidence exists.** Use
   [from-go-zero-migration.md](../reference/from-go-zero-migration.md). gofly
   is a migration path, not a goctl replacement.

## What not to adopt yet

- RPC as a Kitex or gRPC-Go throughput substitute. Unary and stream latency
  stay report-only in [benchmark-matrix.md](../reference/benchmark-matrix.md).
- Preview templates, remote plugins, and experimental mux sinks as production
  contracts. Those are Tier 2.
- Byte-for-byte goctl model directory layout. gofly keeps a `model` / `repo`
  split on purpose.

## Why this shape

Code generation without governance produces services that are hard to operate.
Governance without generation produces a pile of libraries. gofly keeps both
behind the same CLI, then exposes the result as snapshots that tests and
agents can diff.
