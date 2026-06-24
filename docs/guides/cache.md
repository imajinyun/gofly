# Cache Guide

Use gofly cache when you need a typed local cache, loader-based fill, negative caching, bloom protection, or tiered L1/L2 behavior.

## Current adoption path

Start with the runnable cache example:

```sh
go test -C examples/cache-local ./...
go run -C examples/cache-local .
```

The example emits a stable `gofly.cache_local.v1` JSON report covering typed local cache behavior, loader fill, negative cache, bloom protection, tiered L1/L2 cache, cache-disabled mode, stats, and Prometheus output.

## Smallest local cache

```go
c := cache.New[string, string](time.Minute)
c.Set("user:42", "ada")
value, ok := c.Get("user:42")
```

## Production capabilities

| Capability | API |
| --- | --- |
| Loader fill | `cache.WithLoader` |
| Negative cache | negative-cache option |
| Bloom protection | bloom option |
| Typed model cache | `cache.NewModel` |
| Tiered cache | `cache.NewTiered` |
| Governance bypass | `GOFLY_CACHE_DISABLED=true` or explicit disabled options |

## Recommendation

- keep cache TTLs explicit per domain;
- use a loader for read-through paths;
- use tiered cache only when L2 latency reduction matters;
- set `GOFLY_CACHE_DISABLED=true` in governance runs that must bypass cache state;
- benchmark before introducing cross-service cache complexity.
