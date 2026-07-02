#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "runtime-slo.json"
runbook_path = root / "docs" / "reference" / "operator-runbook-drills.json"
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
required_drills = {
    "health-probe-failure",
    "metrics-regression",
    "trace-correlation-break",
    "resilience-policy-regression",
    "control-plane-drift",
    "rollback-decision",
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
if not runbook_path.is_file():
    missing.append("docs/reference/operator-runbook-drills.json is missing")
    runbook = {}
else:
    runbook = json.loads(runbook_path.read_text(encoding="utf-8"))

makefile = read_text(root / "Makefile")
docs = read_text(root / "docs" / "reference" / "runtime-slo.md")
docs_index = read_text(root / "docs" / "index.md")
readme = read_text(root / "README.md")
operations = read_text(root / "docs" / "operations" / "observability.md")
troubleshooting = read_text(root / "docs" / "operations" / "troubleshooting.md")
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

require(runbook.get("schema") == "gofly.operator_runbook_drills.v1", "operator runbook drills schema mismatch")
require(runbook.get("sourceOfTruth") == "docs/reference/runtime-slo.json", "operator runbook sourceOfTruth mismatch")
require(runbook.get("acceptanceGate") == "make runtime-slo-check", "operator runbook acceptanceGate mismatch")
drills = runbook.get("drills") or []
actual_drills = {item.get("id") for item in drills if isinstance(item, dict)}
require(actual_drills == required_drills, f"operator drills drifted: missing={sorted(required_drills - actual_drills)} extra={sorted(actual_drills - required_drills)}")
incident_rehearsals = runbook.get("incidentRehearsals") or []
required_incidents = {
    "rollout-readiness-incident",
    "latency-error-regression-incident",
    "governance-policy-incident",
    "release-gate-incident",
}
actual_incidents = {item.get("id") for item in incident_rehearsals if isinstance(item, dict)}
require(actual_incidents == required_incidents, f"incident rehearsals drifted: missing={sorted(required_incidents - actual_incidents)} extra={sorted(actual_incidents - required_incidents)}")
for item in incident_rehearsals:
    if not isinstance(item, dict):
        missing.append(f"incident rehearsal entry must be an object: {item!r}")
        continue
    incident = item.get("id", "")
    for field in (
        "id",
        "sourceDrill",
        "severity",
        "symptoms",
        "goldenSignals",
        "requiredArtifacts",
        "diagnosisGate",
        "rollbackTrigger",
        "postIncidentEvidence",
    ):
        require(bool(item.get(field)), f"{incident or '<missing>'}: {field} is required")
    require(item.get("sourceDrill") in required_drills, f"{incident}: sourceDrill must reference an operator drill")
    require(item.get("severity") in {"sev1", "sev2", "sev3"}, f"{incident}: severity must be sev1, sev2, or sev3")
    require(len(item.get("symptoms") or []) >= 3, f"{incident}: symptoms must include at least three signals")
    for signal in item.get("goldenSignals") or []:
        require(signal in required_signals, f"{incident}: unknown golden signal {signal!r}")
    for evidence in item.get("requiredArtifacts") or []:
        require((root / evidence).exists(), f"{incident}: required artifact is missing: {evidence}")
    gate = item.get("diagnosisGate", "")
    require(gate.startswith("make "), f"{incident}: diagnosisGate must be a make target")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        require(target in target_names, f"{incident}: diagnosisGate target {target!r} missing")
    for field in ("rollbackTrigger", "postIncidentEvidence"):
        require(len(str(item.get(field) or "").split()) >= 8, f"{incident}: {field} must be actionable")
for item in drills:
    if not isinstance(item, dict):
        missing.append(f"operator drill entry must be an object: {item!r}")
        continue
    drill = item.get("id", "")
    for field in (
        "id",
        "symptom",
        "goldenSignals",
        "evidence",
        "checkCommands",
        "expectedObservation",
        "operatorAction",
        "rollbackOrEscalation",
    ):
        require(bool(item.get(field)), f"{drill or '<missing>'}: {field} is required")
    if drill:
        require(drill in docs, f"runtime-slo.md must document operator drill {drill!r}")
    for signal in item.get("goldenSignals") or []:
        require(signal in required_signals, f"{drill}: unknown golden signal {signal!r}")
    for evidence in item.get("evidence") or []:
        path = root / evidence
        require(path.exists(), f"{drill}: evidence path is missing: {evidence}")
    for command in item.get("checkCommands") or []:
        command_text = str(command)
        require(
            command_text in docs or command_text in troubleshooting or command_text in makefile,
            f"{drill}: check command {command_text!r} must be documented",
        )
    for field in ("expectedObservation", "operatorAction", "rollbackOrEscalation"):
        value = str(item.get(field) or "")
        require(
            len(value.split()) >= 6,
            f"{drill}: {field} must describe an actionable operator path",
        )

for needle in [
    "operator-runbook-drills.json",
    "gofly.operator_runbook_drills.v1",
    "health probe failures",
    "metrics regressions",
    "trace correlation breaks",
    "control-plane drift",
    "rollback decisions",
    "make runtime-slo-check",
]:
    require(needle in troubleshooting, f"docs/operations/troubleshooting.md missing {needle!r}")

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
    "deploy/k8s/servicemonitor.yaml": [
        "kind: ServiceMonitor",
        "port: metrics",
    ],
    "deploy/helm/gofly/templates/servicemonitor.yaml": [
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
        "operator-runbook-drills.json",
        "gofly.operator_runbook_drills.v1",
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

print(f"runtime SLO governance ok: {len(signals)} golden signals, {len(drills)} operator drills")
PY
