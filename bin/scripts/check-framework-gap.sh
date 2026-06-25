#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "framework-gap-matrix.json"
missing = []


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/framework-gap-matrix.json is missing")
next_wave_path = root / "docs" / "reference" / "framework-gap-next-wave.json"
if next_wave_path.is_file():
    next_wave = json.loads(next_wave_path.read_text(encoding="utf-8"))
else:
    next_wave = {}
    missing.append("docs/reference/framework-gap-next-wave.json is missing")

require(manifest.get("schema") == "gofly.framework_gap_matrix.v1", "framework gap matrix schema mismatch")
require(next_wave.get("schema") == "gofly.framework_gap_next_wave.v1", "next-wave framework gap schema mismatch")

sources = manifest.get("sources") or []
for source in sources:
    require((root / source).is_file(), f"source path is missing: {source}")

dimensions = manifest.get("dimensions") or []
required_dimensions = {
    "http-dx",
    "microservice-scaffold",
    "rpc-tier1",
    "production-proof",
    "release-trust",
    "ecosystem-plugins",
    "performance-credibility",
    "adopter-dx",
}
actual_dimensions = {item.get("id") for item in dimensions if isinstance(item, dict)}
require(actual_dimensions == required_dimensions, f"dimensions = {sorted(actual_dimensions)!r}, want {sorted(required_dimensions)!r}")

required_frameworks = {"Gin", "Echo", "Fiber", "Hertz", "go-zero", "Kratos", "Kitex", "gRPC-Go", "Beego"}
seen_frameworks = set()
priorities = set()
for item in dimensions:
    if not isinstance(item, dict):
        missing.append(f"dimension entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    for field in ("id", "referenceFrameworks", "currentEvidence", "gap", "priority", "todo", "aiflowTask", "acceptanceGate"):
        if not item.get(field):
            missing.append(f"dimension {item_id}: {field} is required")
    seen_frameworks.update(item.get("referenceFrameworks") or [])
    priorities.add(item.get("priority", ""))
    gate = item.get("acceptanceGate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"dimension {item_id}: gate target {target!r} missing")
    for evidence in item.get("currentEvidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("examples/") or evidence.startswith("bench/"):
            require((root / evidence).exists(), f"dimension {item_id}: evidence path missing: {evidence}")

require(required_frameworks <= seen_frameworks, f"missing compared frameworks: {sorted(required_frameworks - seen_frameworks)!r}")
require({"P0", "P1"} <= priorities, "matrix must contain P0 and P1 priorities")

recommended = manifest.get("recommendedOrder") or []
require(recommended and recommended[0] == "GOFLY-P3-1-FRAMEWORK-GAP-MATRIX", "recommendedOrder must start with GOFLY-P3-1-FRAMEWORK-GAP-MATRIX")
for task in ("GOFLY-P3-2-RPC-TIER1-EVIDENCE", "GOFLY-P3-3-OPENAPI-INVALID-SMOKE", "GOFLY-P3-4-MIDDLEWARE-ECOSYSTEM-MATRIX", "GOFLY-P3-5-REFERENCE-APP-TOPOLOGY"):
    require(task in recommended, f"recommendedOrder missing {task}")

completed = next_wave.get("completedBaseline") or []
completed_tasks = {item.get("task") for item in completed if isinstance(item, dict)}
for task in (
    "GOFLY-P3-FOLLOWUP-RELEASE-READINESS-SCORE",
    "GOFLY-P3-FOLLOWUP-PLUGIN-PUBLISHING-UX",
    "GOFLY-P3-FOLLOWUP-BENCH-BUDGET-RATCHET",
):
    require(task in completed_tasks, f"next-wave completedBaseline missing {task}")
for item in completed:
    if not isinstance(item, dict):
        missing.append(f"next-wave completedBaseline entry must be an object: {item!r}")
        continue
    for evidence in item.get("evidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("bench/"):
            require((root / evidence).exists(), f"next-wave completed evidence path missing: {evidence}")

next_dimensions = next_wave.get("dimensions") or []
required_next_dimensions = {
    "rpc-latency-depth",
    "generated-migration-fidelity",
    "cloud-native-policy-conformance",
    "dx-support-bundle",
    "governance-dashboard-productization",
}
actual_next_dimensions = {item.get("id") for item in next_dimensions if isinstance(item, dict)}
require(
    actual_next_dimensions == required_next_dimensions,
    f"next-wave dimensions = {sorted(actual_next_dimensions)!r}, want {sorted(required_next_dimensions)!r}",
)
next_tasks = set()
next_frameworks = set()
for item in next_dimensions:
    if not isinstance(item, dict):
        missing.append(f"next-wave dimension entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    for field in ("id", "referenceFrameworks", "currentEvidence", "gap", "priority", "todo", "aiflowTask", "acceptanceGate"):
        if not item.get(field):
            missing.append(f"next-wave dimension {item_id}: {field} is required")
    next_tasks.add(item.get("aiflowTask", ""))
    next_frameworks.update(item.get("referenceFrameworks") or [])
    gate = item.get("acceptanceGate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"next-wave dimension {item_id}: gate target {target!r} missing")
    for evidence in item.get("currentEvidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("examples/") or evidence.startswith("bench/") or evidence.startswith("charts/") or evidence.startswith("k8s/"):
            require((root / evidence).exists(), f"next-wave dimension {item_id}: evidence path missing: {evidence}")

next_recommended = next_wave.get("recommendedOrder") or []
require(next_recommended and next_recommended[0] == "GOFLY-P4-1-NEXT-WAVE-GAP-ROADMAP", "next-wave recommendedOrder must start with GOFLY-P4-1-NEXT-WAVE-GAP-ROADMAP")
for task in (
    "GOFLY-P4-2-RPC-LATENCY-RATCHET",
    "GOFLY-P4-3-GENERATED-MIGRATION-FIDELITY",
    "GOFLY-P4-4-CLOUD-NATIVE-POLICY-CONFORMANCE",
    "GOFLY-P4-5-DX-SUPPORT-BUNDLE",
    "GOFLY-P4-6-GOVERNANCE-DASHBOARD-PRODUCTIZATION",
):
    require(task in next_recommended, f"next-wave recommendedOrder missing {task}")
    require(task in next_tasks, f"next-wave dimensions missing task {task}")
require({"Gin", "go-zero", "Kratos", "Kitex", "gRPC-Go", "Beego"} <= next_frameworks, "next-wave matrix must cover major reference frameworks")
scope = next_wave.get("scope") or {}
excluded = set(scope.get("excluded") or [])
for out_of_scope in ("GitHub stars", "download counts", "community size", "brand awareness"):
    require(out_of_scope in excluded, f"next-wave scope.excluded missing {out_of_scope!r}")

doc = read_text(root / "docs" / "reference" / "framework-gap-matrix.md")
for needle in (
    "gofly.framework_gap_matrix.v1",
    "make framework-gap-check",
    "framework-gap-next-wave.json",
    "Next-Wave TODO Order",
    "GOFLY-P4-2-RPC-LATENCY-RATCHET",
    "Gin",
    "go-zero",
    "Kratos",
    "Kitex",
    "Executable TODO Order",
    "Out Of Scope",
):
    require(needle in doc, f"docs/reference/framework-gap-matrix.md missing {needle!r}")

makefile = read_text(root / "Makefile")
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
require("framework-gap-check" in makefile, "Makefile must expose framework-gap-check")
require("framework-gap-check" in docs_check_line, "docs-check must depend on framework-gap-check")
require("check-framework-gap.sh" in makefile, "Makefile must call check-framework-gap.sh")

docs_index = read_text(root / "docs" / "index.md")
readme = read_text(root / "README.md")
require("reference/framework-gap-matrix.md" in docs_index, "docs/index.md must link framework gap matrix")
require("docs/reference/framework-gap-matrix.md" in readme, "README.md must link framework gap matrix")

if missing:
    print("framework gap check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("framework gap governance ok")
PY
