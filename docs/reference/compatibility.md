# Compatibility policy

This policy covers public Go APIs, CLI flags, JSON envelopes, generated
layouts, and control-plane snapshots. Field contracts are in
[cli-json-contracts.md](cli-json-contracts.md) and
[control-plane-contracts.md](control-plane-contracts.md). Tiers are listed in
[api-surface.md](api-surface.md).

## Adoption tier policy

- **Tier 0** breaking changes require migration notes and
  `make generated-service-layout-check`.
- **Tier 1** may add JSON fields and generated files. Removing fields,
  renaming paths, or changing types needs a compatibility window.
- **Tier 2** preview surfaces may change faster but must stay path-safe and
  deterministic.
- **Tier 3** report-only evidence (RPC latency, oracle layout diffs) must not
  be described as product guarantees.

## Deprecation and migration window

Deprecated surfaces keep working for at least one minor release line after the
`Deprecated:` marker appears in godoc, CLI help, or release notes. During that
window:

1. Old flags and JSON fields still parse.
2. New fields are additive.
3. Generated projects on the previous profile still compile.

RPC tier-1 promotion and byte-for-byte goctl layout are outside this window
until their blocking gates say otherwise.

## Operational endpoint namespace migration

All framework-provided operational endpoints are served under the shared
`/gofly` prefix, defined by the `core/endpoint` path constants. This isolates
diagnostics from business routes so the whole surface can be firewalled or
exposed as one namespace.

| Surface | Legacy path | Canonical path |
| --- | --- | --- |
| Liveness probe | `GET /healthz` | `GET /gofly/healthz` |
| Readiness probe | `GET /readyz` | `GET /gofly/readyz` |
| Startup probe | `GET /startupz` | `GET /gofly/startupz` |
| Prometheus metrics | `GET /metrics` | `GET /gofly/metrics` |
| JSON metrics | `GET /metrics.json` | `GET /gofly/metrics.json` |
| REST admin prefix | `/admin/*` (default) | `/gofly/admin/*` (default) |
| Control plane | `GET /admin/control-plane` | `GET /gofly/admin/control-plane` |
| Runtime profiling | `/debug/pprof/*` | `/gofly/debug/pprof/*` |
| HTTP-RPC admin | `/rpc/admin/*` | `/gofly/rpc/admin/*` |
| gRPC admin mux | `/healthz`, `/metrics`, `/governance/*` | `/gofly/healthz`, `/gofly/metrics`, `/gofly/admin/governance/*` |
| Gateway admin | `/_gofly/gateway/*` | `/gofly/admin/gateway/*` |

Migration notes:

1. Explicit `AdminConfig.PathPrefix` values keep working, so projects that
   pinned a custom prefix do not change until they opt in.
2. The generated admin server defaults its prefix to `/gofly`; generated
   configs now emit `"pathPrefix": "/gofly"` and `"pathPrefix": "/gofly/admin"`.
3. `gofly rpc descriptor` still accepts legacy `/rpc/admin/descriptors` URLs
   for services pinned to older runtimes while emitting canonical
   `/gofly/rpc/admin/descriptors` URLs by default.
4. Kubernetes probes, ServiceMonitor scrape paths, Prometheus configs, and
   smoke scripts must be updated to the canonical paths together with the
   runtime upgrade.
5. Custom `PathPrefix` on `rest.AdminConfig` remains supported; pprof now
   always mounts under `/gofly/debug/pprof` with the same token/localhost
   protection regardless of the admin prefix.
