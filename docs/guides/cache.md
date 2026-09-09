# Cache and model runtime

This page is part of the **DB and cache productization matrix**. The machine-readable
contract lives in `docs/reference/db-cache-productization.json` and is gated by
`make db-cache-productization-check`.

## Local and Redis-backed model cache

`SQLStore` plus `NewCluster` cover SQL read/write routing. Generated repositories
can wrap those stores with a **Redis-backed model cache** (`NewRedisModel`,
`RedisCachedOrderRepo`, `UpdateWithInvalidate`) while `GOFLY_CACHE_DISABLED`
keeps local and tests deterministic.

## Redis failure semantics

`core/kv/redis` is a compatibility adapter over
[`github.com/redis/go-redis/v9`](https://github.com/redis/go-redis). It keeps
the stable gofly `kv.RedisClient` surface while delegating connection pooling,
Redis Cluster routing, authentication, context deadlines, and stream protocol
handling to the maintained v9 client. Existing `Addr` configuration remains a
single-node client; `Addrs` or `Cluster` selects v9 cluster routing. The adapter
uses RESP2 negotiation and disables v9 identity writes, retaining compatibility
with legacy Redis servers and RESP2-only proxies.

Cluster seed addresses are trimmed, deduplicated, and retained in configuration
order. Redis Cluster rejects logical database selection, so `NewChecked` rejects
any non-zero DB configuration before dialing. `maintNotifications` defaults to
`disabled`; it may be set to `auto` or `enabled` only with `protocol: 3`, making
the additional connection handshake an explicit production choice.

`masterName` enables Sentinel failover, with `Addrs` treated as Sentinel seeds
and optional sentinel-specific ACL credentials. `readOnly`, `routeByLatency`,
and `routeRandomly` are explicit replica-routing choices for Cluster or Sentinel
deployments; the latter two are mutually exclusive. gofly keeps them disabled
by default because replica reads can be stale. Sentinel failover currently does
not support v9 maintenance notifications, so that combination is rejected at
configuration validation time.

`redis.ErrNil` remains gofly's public cache-miss signal. A missing key or TTL
maps to it; a persistent key returns a zero TTL. The adapter keeps command and
error counters and translates v9 pool totals into active/idle snapshot values.

Following go-zero's v9 integration, normal Redis commands and pipelines pass
through a circuit-breaker hook. Redis misses (`redis.Nil`) and canceled calls
are accepted outcomes, while repeated backend failures open the breaker. The
connection handshake (`HELLO`) and `BLPOP` are excluded: a proxy capability
mismatch or intentional blocking wait must not poison cache availability. Set
`disableBreaker` only when an application provides equivalent external Redis
resilience controls. Set `breakerAdaptive: true` to select the same Google SRE
adaptive throttling model used by go-zero: the breaker uses a rolling request
and accept ratio and probabilistically sheds calls during sustained Redis
failures. The consecutive-failure breaker remains the compatibility default.

For stream consumers, `redisstream.Broker` detects the v9 gofly client and
creates one dedicated one-connection client per blocking reader. `XREADGROUP`
therefore cannot exhaust the pool used by cache reads and writes. Use
`NewChecked` when configuration must be validated at startup or when an eager
`PING` is required; it validates Cluster database use, builds TLS through
`security.TLSConfig`, and preserves certificate verification unless an explicit
local-development TLS opt-out is supplied.

The Redis hook emits low-cardinality command/pipeline latency and error-class
metrics and OTel client spans. Commands slower than `slowThreshold` (100ms by
default, matching go-zero; set a negative duration to disable) emit a warning
and increment `gofly_redis_slow_commands_total`. Slow logs intentionally record
only operation, duration, and threshold: Redis keys, values, and Lua arguments
are never logged. Pool saturation is exported through
`gofly_redis_pool_connections` (`active`, `idle`, and `max`) plus monotonic
hit, miss, wait, timeout, and stale-connection counters. Pool metrics use only
the bounded topology label (`node`, `cluster`, `sentinel`, or
`sentinel-cluster`) and never expose endpoints. `Snapshot` retains the same
statistics for control-plane diagnostics.

Two go-zero behaviors are intentionally not copied. gofly keeps `maxRetries`
at `-1` by default because retry is governed at the application/RPC layer; an
additional default of three Redis retries would multiply attempts and delay
breaker feedback. Applications can opt in with an explicit positive value.
gofly also creates one client pool per `New`/`NewChecked` call rather than
sharing a process-global client by address. This preserves explicit ownership
and deterministic `Close` semantics; applications that want one pool should
construct one client and inject it into all consumers.

`DiagnosticsSnapshot` adds a safe operational view: topology class, seed count,
RESP protocol, maintenance-notification policy, replica routing, TLS presence,
breaker state, and pool statistics. It intentionally omits endpoints, Redis and
Sentinel credentials, TLS file paths, server names, keys, values, and scripts.
Applications that expose a control-plane snapshot can opt in with
`redis.ControlPlaneContributor{Client: client, Name: "cache"}`; it writes
the same sanitized view under `runtime.redis.cache` without taking ownership of
the client or changing its close lifecycle.

TieredCache is availability-oriented: an L2 read failure is treated as a miss,
the loader still runs, and a failed L2 write does not discard the loaded L1
value. RedisModelCache is intentionally stricter: after a repository load, a
Redis write-back failure is returned so generated cache-aside repositories do
not silently claim a successful cache update. Disable either cache path with
GOFLY_CACHE_DISABLED when direct source-of-truth reads are required.

The `p10StorageCacheProductization` closeout records **SQL outbox**, cache stats,
and `WritePrometheus` evidence. Rows still marked **planned**
(`migration-runner`, `production-redis-integration`) stay out of release notes
until they have implementation paths and tests.
