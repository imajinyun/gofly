# Governance

Governance rules apply production policies outside business handlers. A rule matches a request and returns a policy for timeout, retry, breaker, rate limit, concurrency, canary routing, headers, or metadata.

## Match fields

| Field | Example | Notes |
| --- | --- | --- |
| `transport` | `rest`, `rpc`, `gateway`, `mq` | Selects runtime surface |
| `service` | `orders` | Logical service name |
| `method` | `GET`, `SayHello` | HTTP method or RPC method |
| `path` | `/orders/{id}` | HTTP path or route |
| `tags` | `env=prod` | Optional routing labels |

## Example rule

```json
{
  "rules": [
    {
      "name": "orders-rest-read",
      "transport": "rest",
      "service": "orders",
      "method": "GET",
      "path": "/orders/{id}",
      "policy": {
        "timeout": "2s",
        "retry": { "attempts": 2, "backoff": "100ms", "statuses": [503] },
        "breaker": { "enabled": true, "minRequests": 20, "failureRatio": 0.5 },
        "rateLimit": { "rate": 200, "burst": 400 }
      }
    }
  ]
}
```

## Production guidance

- Prefer explicit rules per transport and service.
- Set timeouts before retries; retries without deadlines can amplify outages.
- Use breakers for unstable downstreams.
- Use rate limits at ingress and concurrency limits around expensive handlers.
- Keep generated `etc/governance.json` under review with the service code.

See [operations/production-checklist](../operations/production-checklist.md) before release.

## Resilience drill

`examples/resilience` emits a deterministic `gofly.resilience_drill.v1` report
for rate-limit rejection, retry attempts, breaker-open fast fail, and recovery
back to a closed breaker state.

Run:

```sh
go run -C examples/resilience . --json
make resilience-drill-check
```
