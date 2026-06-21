# Observability Example

This example demonstrates a production-style observability loop for a gofly
service:

- REST traffic on `:8080`
- Admin diagnostics on `:8081/debug/*`
- Prometheus metrics at `/debug/metrics`
- JSON metrics at `/debug/metrics.json`
- Health probes at `/debug/healthz`, `/debug/readyz`, `/debug/startupz`
- pprof endpoints under `/debug/pprof/*`
- Structured logs that include `trace_id` and `request_id`

## Run the Service

From the repository root:

```bash
go run ./examples/observability
```

In another terminal, generate traffic:

```bash
curl -s localhost:8080/users/42
curl -s localhost:8080/users/42 -H 'traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01'
curl -s localhost:8081/debug/healthz
curl -s localhost:8081/debug/readyz
curl -s localhost:8081/debug/metrics.json
curl -s localhost:8081/debug/metrics | grep gofly_requests_total
```

The service logs should include an entry similar to:

```text
rpc call simulated trace_id=... span_id=...
```

## Run the Local Stack

The local stack starts Prometheus, Grafana, and OpenTelemetry Collector. Keep the
Go service running on the host, then start the stack:

```bash
docker compose -f examples/observability/docker-compose.yaml up
```

Open:

- Prometheus: <http://localhost:9090>
- Grafana: <http://localhost:3000>
- OTel Collector OTLP gRPC: `localhost:4317`
- OTel Collector OTLP HTTP: `localhost:4318`

Grafana uses anonymous admin access for local development only. The dashboard is
provisioned from `grafana-dashboard.json` and reads from the Prometheus data
source.

## Verify Prometheus

Generate traffic while the stack is running:

```bash
for i in $(seq 1 20); do
  curl -s localhost:8080/users/$i >/dev/null
done
```

Query Prometheus:

```promql
sum(rate(gofly_requests_total[5m])) by (route)
histogram_quantile(0.95, sum(rate(gofly_route_duration_seconds_bucket[5m])) by (le, route))
sum(rate(gofly_errors_total[5m])) / clamp_min(sum(rate(gofly_requests_total[5m])), 1)
```

## Verify Grafana

Open <http://localhost:3000/d/gofly-observability/gofly-observability> and check:

- Request rate by route
- Error ratio
- Route latency P95

## Verify OTel Collector

The example service does not export OTLP spans directly; it demonstrates gofly's
internal W3C trace propagation and log correlation. The collector config is
included so services that enable OTLP exporters can reuse the same local
endpoint:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 go run ./examples/observability
```

The collector prints received telemetry through the `debug` exporter.

## Production Notes

- Do not expose pprof endpoints without authentication in production.
- Keep Prometheus labels bounded; use route patterns instead of raw URLs.
- Prefer JSON logs and carry `trace_id` / `request_id` through every boundary.
- Use a real trace backend in production instead of the collector debug exporter.
