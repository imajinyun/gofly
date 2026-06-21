# Security

Security defaults focus on keeping generated services predictable and minimizing accidental exposure.

## Production rules

| Area | Rule |
| --- | --- |
| Admin endpoints | bind internally and require a token if exposed beyond localhost |
| Secrets | keep secrets out of generated source and test snapshots |
| TLS | use explicit TLS config for public or cross-network traffic |
| Request bodies | configure max body bytes for public REST endpoints |
| Governance | use rate limits and concurrency limits for expensive paths |
| Logs | redact credentials, tokens, cookies, and authorization headers |

## Generated service checklist

```sh
go test ./...
gofly release check --strict
curl http://127.0.0.1:9090/admin/control-plane
```

## Admin token guidance

If an admin endpoint must be reachable outside localhost, configure a bearer token and put the listener behind private networking. Do not publish `/admin/*` directly to the internet.

## Dependency checks

The repository security gates use `govulncheck` and `gosec`. Run the project-level security target before release when dependencies or generated templates change.
