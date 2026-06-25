#!/usr/bin/env sh
set -eu

GO_CMD="${GO:-go}"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT INT TERM

(cd examples/resilience && "$GO_CMD" run . --json) >"$workdir/resilience-drill.json"

python3 - "$workdir/resilience-drill.json" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
report = json.loads(path.read_text(encoding="utf-8"))
missing = []

if report.get("schema") != "gofly.resilience_drill.v1":
    missing.append("schema must be gofly.resilience_drill.v1")
if report.get("scenario") != "limiter-retry-breaker-recovery":
    missing.append("scenario must be limiter-retry-breaker-recovery")

layers = set(report.get("layers") or [])
for layer in ("rate-limit", "retry", "circuit-breaker", "downstream"):
    if layer not in layers:
        missing.append(f"layers missing {layer!r}")

results = report.get("results") or {}
if results.get("downstreamCalls", 0) < 5:
    missing.append("downstreamCalls must prove retry attempts occurred")
if results.get("breakerOpen", 0) <= 0:
    missing.append("breakerOpen must be greater than zero")
if results.get("rejected", 0) <= 0:
    missing.append("rejected must be greater than zero")
if results.get("ok", 0) <= 0:
    missing.append("ok must be greater than zero")
if results.get("finalBreaker") != "closed":
    missing.append("finalBreaker must be closed")
if results.get("recovered") is not True:
    missing.append("recovered must be true")

gates = set(report.get("gates") or [])
for gate in ("make resilience-drill-check", "make examples-smoke"):
    if gate not in gates:
        missing.append(f"gates missing {gate!r}")

manifest_path = pathlib.Path("docs/reference/resilience-drill.json")
if not manifest_path.is_file():
    missing.append("docs/reference/resilience-drill.json is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

if manifest.get("schema") != "gofly.resilience_drill_evidence.v1":
    missing.append("manifest schema must be gofly.resilience_drill_evidence.v1")
if manifest.get("example") != "examples/resilience":
    missing.append("manifest example must be examples/resilience")
if manifest.get("reportSchema") != "gofly.resilience_drill.v1":
    missing.append("manifest reportSchema must be gofly.resilience_drill.v1")

commands = manifest.get("commands") or {}
if commands.get("drill") != "go run -C examples/resilience . --json":
    missing.append("manifest commands.drill must run the JSON drill")
if commands.get("gate") != "make resilience-drill-check":
    missing.append("manifest commands.gate must be make resilience-drill-check")

required = set(manifest.get("requiredEvidence") or [])
for item in ("rate-limit rejection", "retry attempts", "breaker open", "recovery to closed"):
    if item not in required:
        missing.append(f"manifest requiredEvidence missing {item!r}")

reference_evidence = manifest.get("referenceAppEvidence") or []
expected_components = {"saga compensation", "outbox retry", "topology fallback", "rollback note"}
components = {item.get("component") for item in reference_evidence if isinstance(item, dict)}
for component in expected_components:
    if component not in components:
        missing.append(f"manifest referenceAppEvidence missing {component!r}")
for item in reference_evidence:
    if not isinstance(item, dict):
        missing.append(f"referenceAppEvidence entry must be an object: {item!r}")
        continue
    path = pathlib.Path(item.get("path", ""))
    signal = item.get("signal", "")
    if not path.is_file():
        missing.append(f"reference evidence path is missing: {path}")
        continue
    if not signal:
        missing.append(f"reference evidence signal is required for {path}")
        continue
    if signal not in path.read_text(encoding="utf-8"):
        missing.append(f"{path}: missing reference evidence signal {signal!r}")

docs = {
    pathlib.Path("docs/reference/resilience-drill.md"): [
        "gofly.resilience_drill_evidence.v1",
        "gofly.resilience_drill.v1",
        "make resilience-drill-check",
        "go run -C examples/resilience . --json",
        "production-orders",
    ],
    pathlib.Path("docs/concepts/governance.md"): [
        "make resilience-drill-check",
        "gofly.resilience_drill.v1",
    ],
    pathlib.Path("examples/README.md"): [
        "go run -C examples/resilience . --json",
        "gofly.resilience_drill.v1",
    ],
    pathlib.Path("Makefile"): [
        "resilience-drill-check",
        "check-resilience-drill.sh",
    ],
}
for doc, needles in docs.items():
    if not doc.is_file():
        missing.append(f"{doc}: file is missing")
        continue
    text = doc.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{doc}: missing {needle!r}")

if missing:
    print("resilience drill check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    raise SystemExit(1)

print("resilience drill governance ok")
PY
