# gofly Roadmap

This roadmap is the public adoption view. Runnable evidence stays in
`docs/reference/` and `Makefile` gates. It is not a claim that gofly replaces
goctl, Kitex, or gRPC-Go.

## v0.2 Production proof

The production golden path is already the Tier 0 contract:

- `gofly new service --style production` layout in `docs/reference/generated-service-layout.md`
- `examples/production/production-orders` as the compact production reference app
- generated `/healthz` and `/admin/control-plane` smoke
- Helm, Kustomize-ready deploy starters, and `bin/production-check.sh`

Hold: RPC unary and stream latency stay report-only. Do not promote RPC to
tier-1 in release notes.

## v0.5 Ecosystem preview

Current preview surfaces, still faster-moving than Tier 0:

- Plugin registry, remote templates, and feature previews
- `examples/ecosystem/plugin-ecosystem`
- AI-governed workflows (`gofly ai manifest`, `gofly ai control-plane`)
- HTTP middleware catalog and RPC IDL matrix examples
- Benchmark trend artifacts under `bench/` (`make bench-evidence-check`)

CLI package splits stay preflight-gated. Help, doctor, and release already live
behind adapters. The next candidate is the `config` family; this roadmap does
not authorize moving those files until `P22-18-command-config-family-preflight`
runs as its own change.

## v1.0 Compatibility

v1.0 is the compatibility freeze, not a performance contest:

- Stable CLI flags and JSON output
- Stable control-plane schema migration policy
- Generated project compatibility policy (`old` / `current` / `future` fixtures)
- goctl-compatible migration path with blocking replay gates
- RPC tier-1 promotion only after release-train and budget evidence exist

Until that evidence lands, docs must keep the current hold: gofly RPC
coexists with Kitex and gRPC-Go; it does not claim throughput parity.

## Validation gates

Use these commands before promoting a roadmap claim:

```sh
make community-growth-check
make docs-taxonomy-check
make migration-docs-check
make p1-growth-check
make contract-docs-check
make examples-smoke
make generated-service-layout-check
make goctl-real-project-replay-check
make bench-evidence-check
make governance-10-rounds
```
