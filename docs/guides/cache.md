# Cache Guide

Use gofly cache when you need a typed local cache, loader-based fill, negative caching, bloom protection, or tiered L1/L2 behavior.

## Current adoption path

There is not yet a dedicated runnable `examples/cache-*` service. For now, adopt from the package APIs and integrate into an existing service.

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

## Recommendation

- keep cache TTLs explicit per domain;
- use a loader for read-through paths;
- use tiered cache only when L2 latency reduction matters;
- benchmark before introducing cross-service cache complexity.
