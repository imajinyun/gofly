# Observability

gofly services should expose logs, metrics, traces, health checks, and admin diagnostics from the first production run.

## Minimal example

```sh
go run ./examples/observability
curl http://127.0.0.1:8080/users/42
curl http://127.0.0.1:9090/debug/metrics.json
curl http://127.0.0.1:9090/debug/healthz
```

## Production signals

| Signal | What to capture |
| --- | --- |
| Logs | request id, trace id, route, status, latency |
| Metrics | request count, error count, latency, in-flight requests |
| Traces | W3C `traceparent` propagation across REST/RPC |
| Health | `/startupz`, `/healthz`, `/readyz` |
| Admin | `/admin/control-plane`, metrics JSON, pprof when enabled |

## Configuration

- enable metrics middleware on REST;
- keep admin endpoints on an internal port;
- export OpenTelemetry data only to trusted collectors;
- use sampling in high-throughput services.

See `examples/observability/README.md` for Prometheus, Grafana, and OTel Collector local stack notes.
