#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "runtime-slo.json"
missing = []

required_signals = {
    "latency",
    "errors",
    "traffic",
    "saturation",
    "cache",
    "governance-decisions",
    "trace-log-correlation",
}


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


def make_target_names(makefile):
    return set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.M))


if not manifest_path.is_file():
    missing.append("docs/reference/runtime-slo.json is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

makefile = read_text(root / "Makefile")
docs = read_text(root / "docs" / "reference" / "runtime-slo.md")
docs_index = read_text(root / "docs" / "index.md")
readme = read_text(root / "README.md")
operations = read_text(root / "docs" / "operations" / "observability.md")
example_readme = read_text(root / "examples" / "observability" / "README.md")
example_main = read_text(root / "examples" / "observability" / "main.go")
governance_report = read_text(root / "bin" / "scripts" / "governance-report.sh")

target_names = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")

require(manifest.get("schema") == "gofly.runtime_slo.v1", "schema must be gofly.runtime_slo.v1")
require("runtime-slo-check" in target_names, "Makefile must expose runtime-slo-check")
require("runtime-slo-check" in docs_check_line, "docs-check must depend on runtime-slo-check")
require("check-runtime-slo.sh" in makefile, "Makefile must call check-runtime-slo.sh")

signals = manifest.get("goldenSignals") or []
actual_signals = {item.get("id") for item in signals if isinstance(item, dict)}
require(actual_signals == required_signals, f"golden signals drifted: missing={sorted(required_signals - actual_signals)} extra={sorted(actual_signals - required_signals)}")

for item in signals:
    if not isinstance(item, dict):
        missing.append(f"golden signal entry must be an object: {item!r}")
        continue
    signal = item.get("id", "")
    for field in ("id", "slo", "evidence", "queries"):
        require(bool(item.get(field)), f"{signal or '<missing>'}: {field} is required")
    if signal:
        require(signal in docs, f"runtime-slo.md must document signal {signal!r}")
    for evidence in item.get("evidence") or []:
        path = root / evidence
        require(path.exists(), f"{signal}: evidence path is missing: {evidence}")
    for query in item.get("queries") or []:
        query_text = str(query)
        require(
            query_text in docs or query_text in example_readme or query_text in example_main,
            f"{signal}: query or contract {query_text!r} must be documented",
        )

verification = manifest.get("verification") or {}
require(verification.get("gate") == "make runtime-slo-check", "verification.gate must be make runtime-slo-check")
require(verification.get("observabilityExample") == "go test -C examples/observability ./...", "observability example gate mismatch")
require(verification.get("productionGate") == "make p1-growth-check", "production gate mismatch")

for needle in [
    "request id",
    "trace id",
    "latency",
    "in-flight requests",
    "/admin/control-plane",
]:
    require(needle in operations, f"docs/operations/observability.md missing {needle!r}")

for needle in [
    "gofly_requests_total",
    "gofly_errors_total",
    "gofly_route_duration_seconds_bucket",
    "trace_id",
    "request_id",
    "traceparent",
    "Prometheus",
    "Grafana",
    "OpenTelemetry Collector",
]:
    require(needle in example_readme or needle in example_main, f"observability example missing {needle!r}")

assets = {
	"examples/observability/grafana-dashboard.json": [
		"Request Rate",
		"Error Ratio",
		"Route Latency P95",
	],
	"examples/observability/prometheus.yaml": [
		"host.docker.internal:8081",
		"/debug/metrics",
	],
    "examples/observability/otel-collector.yaml": [
        "otlp",
        "debug",
    ],
    "k8s/servicemonitor.yaml": [
        "kind: ServiceMonitor",
        "port: metrics",
    ],
    "charts/gofly/templates/servicemonitor.yaml": [
        "kind: ServiceMonitor",
        ".Values.serviceMonitor.enabled",
    ],
}
for path_text, needles in assets.items():
    text = read_text(root / path_text)
    for needle in needles:
        require(needle in text, f"{path_text}: missing {needle!r}")

for path_text, needles in {
    "docs/reference/runtime-slo.md": [
        "gofly.runtime_slo.v1",
        "runtime-slo.json",
        "make runtime-slo-check",
        "go test -C examples/observability ./...",
        "make p1-growth-check",
    ],
    "docs/index.md": ["reference/runtime-slo.md"],
    "README.md": ["docs/reference/runtime-slo.md"],
}.items():
    text = {"docs/index.md": docs_index, "README.md": readme}.get(path_text, docs)
    for needle in needles:
        require(needle in text, f"{path_text}: missing {needle!r}")

for needle in [
    "runtime-slo.json",
    "gofly.runtime_slo.v1",
    "runtimeSLO",
    "make runtime-slo-check",
]:
    require(needle in governance_report, f"governance-report.sh must expose {needle!r}")

if missing:
    print("runtime SLO check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print(f"runtime SLO governance ok: {len(signals)} golden signals")
PY
