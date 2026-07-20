#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(".").resolve()
missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def read_text(relative):
    path = root / relative
    require(path.is_file(), f"{relative} is missing")
    return path.read_text(encoding="utf-8") if path.is_file() else ""


delivery = read_text("rpc/mux_export_delivery.go")
sink_set = read_text("rpc/mux_sink_set.go")
admin = read_text("rpc/admin.go")
prometheus = read_text("examples/http/observability/prometheus.yaml")
alerts = read_text("examples/http/observability/rpc-mux-sink-alerts.yaml")
compose = read_text("examples/http/observability/docker-compose.yaml")
dashboard_text = read_text("examples/http/observability/grafana-dashboard.json")

try:
    dashboard = json.loads(dashboard_text)
except json.JSONDecodeError as exc:
    dashboard = {}
    missing.append(f"grafana dashboard is invalid JSON: {exc}")

for needle in (
    "gofly_rpc_mux_diagnosis_exporter_delivery_duration_seconds",
    "gofly_rpc_mux_diagnosis_exporter_consecutive_failures",
    "gofly_rpc_mux_diagnosis_exporter_breaker_open",
    '"sink"',
):
    require(needle in delivery, f"delivery governance missing {needle!r}")

for needle in (
    "RPCMuxDiagnosisSinkSetSnapshot",
    "LastReloadError",
    "LastSuccessAt",
    "ConsecutiveFailures",
):
    require(needle in sink_set or needle in delivery, f"sink runtime introspection missing {needle!r}")

require('details["sinkSet"]' in admin, "RPC control-plane snapshot must expose sinkSet")
require("rpcMuxDiagnosisSinkSetStatus" in admin, "RPC control-plane snapshot must derive sink health status")
require("rpc-mux-sink-alerts.yaml" in prometheus, "Prometheus must load mux sink alert rules")
require("rpc-mux-sink-alerts.yaml" in compose, "observability compose must mount mux sink alert rules")

for needle in (
    "GoflyRPCMuxSinkBreakerOpen",
    "GoflyRPCMuxSinkDeliveryDrops",
    "GoflyRPCMuxSinkDeliveryP99High",
    'outcome=~"dropped|backpressure|breaker_open"',
    "histogram_quantile(0.99",
):
    require(needle in alerts, f"mux sink alerts missing {needle!r}")

titles = {
    panel.get("title")
    for panel in dashboard.get("panels") or []
    if isinstance(panel, dict)
}
for title in (
    "Mux Sink Delivery P99",
    "Mux Sink Consecutive Failures",
    "Mux Sink Breaker State",
):
    require(title in titles, f"grafana dashboard missing {title!r}")

if missing:
    print("RPC mux sink SLO check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("RPC mux sink SLO assets OK")
PY
