# Generated models and SQL repositories

This page is part of the **DB and cache productization matrix**. Keep
`docs/reference/db-cache-productization.json` and `make db-cache-productization-check`
in lockstep with generator output.

## SQL repositories

Generated SQL repositories use `SQLStore`, `NewCluster`, `FindWhere`,
`storage.SelectWhere`, `ListAfter`, and `UpdateWithVersion`. Transactions go
through `SQLStore.Transact` / `Cluster.Transact`. Event publication uses the
**SQL outbox** rather than dual-writing from handlers.

## Cache-aside models

When `--cache` is set, the generator emits a **Redis-backed model cache** and
invalidation helpers. See `p10StorageCacheProductization` for the evidence rows.
Capabilities still **planned** must not be advertised as production-ready.

## Database migrations

Model generation reads schema and writes Go code. Use [versioned SQL migrations](migrations.md)
to generate initial SQL pairs, validate history, and explicitly apply or roll back
database changes with `gofly migrate`. Model generation never applies migrations.
