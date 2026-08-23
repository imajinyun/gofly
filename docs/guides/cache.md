# Cache and model runtime

This page is part of the **DB and cache productization matrix**. The machine-readable
contract lives in `docs/reference/db-cache-productization.json` and is gated by
`make db-cache-productization-check`.

## Local and Redis-backed model cache

`SQLStore` plus `NewCluster` cover SQL read/write routing. Generated repositories
can wrap those stores with a **Redis-backed model cache** (`NewRedisModel`,
`RedisCachedOrderRepo`, `UpdateWithInvalidate`) while `GOFLY_CACHE_DISABLED`
keeps local and tests deterministic.

The `p10StorageCacheProductization` closeout records **SQL outbox**, cache stats,
and `WritePrometheus` evidence. Rows still marked **planned**
(`migration-runner`, `production-redis-integration`) stay out of release notes
until they have implementation paths and tests.
