# Config Guide

gofly config supports local files, layered providers, and environment-aware profiles.

## Minimal example

```sh
go run ./examples/config-discovery
```

## What the example shows

- local config file loading;
- profile-aware provider composition;
- discovery config living next to service config;
- runtime inspection of resolved values.

## Production configuration

| Concern | Config |
| --- | --- |
| Service ports | `server.rest.port`, `server.rpc.port`, `admin.port` |
| Environment profile | profile provider options or env-driven overlay |
| Discovery | `discovery.provider`, `discovery.address`, `discovery.endpoints` |
| Governance file | `etc/governance.json` |

## Recommendation

- Keep one checked-in baseline config file per service.
- Put environment-specific overrides in deployment config, not handler code.
- Validate config through generated tests and `go test ./...`.
