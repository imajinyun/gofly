# go-zero / goctl alignment

gofly is a goctl-compatible migration path, not a drop-in replacement for
go-zero. SQL access is `SQLStore` / `NewCluster` rather than go-zero `sqlx`.
Caching uses the typed `cache` package and generated Redis cache-aside
repositories instead of go-zero `cache`.

## Rollback plan

If generated SQL or cache output diverges from a goctl project, keep the
existing go-zero datastore, pin `REFERENCE_APP_MODE=memory`, and rerun
`make goctl-model-parity-replay-check` before promoting the gofly model.

## Reference app

Copy the production orders example at `examples/production/production-orders`.
It proves REST, RPC, SQL outbox, and cache topology without claiming go-zero
sqlx parity.
