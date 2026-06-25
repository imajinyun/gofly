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

require(manifest.get("schema") == "gofly.framework_gap_matrix.v1", "framework gap matrix schema mismatch")

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

doc = read_text(root / "docs" / "reference" / "framework-gap-matrix.md")
for needle in (
    "gofly.framework_gap_matrix.v1",
    "make framework-gap-check",
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
