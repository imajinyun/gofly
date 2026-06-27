#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "governance-boundary-inventory.json"
missing = []

expected_tasks = [f"GOFLY-GOV-10R-{idx:02d}" for idx in range(1, 11)]
expected_surfaces = {
    "cli",
    "generator",
    "runtime",
    "rest-rpc-contracts",
    "plugin-template-security",
    "cloud-native-production",
    "release-governance",
}
expected_ignored = {
    "docs/superpowers/",
    ".aiflow/",
    ".harness/",
    ".tmp-test/",
    ".trae/",
    "coverage.out",
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


def gate_is_known(gate, targets):
    if gate.startswith("make "):
        parts = gate.removeprefix("make ").split()
        return bool(parts) and parts[0] in targets
    return gate.startswith("go ") or gate.startswith("targeted ")


def gitignore_covers(path, patterns):
    if path in patterns:
        return True
    if path == "coverage.out" and ("*.out" in patterns or "coverage.*" in patterns):
        return True
    return False


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/governance-boundary-inventory.json is missing")

makefile = read_text(root / "Makefile")
gitignore = read_text(root / ".gitignore")
governance_script = read_text(root / "bin" / "scripts" / "governance-10-rounds.sh")
targets = make_target_names(makefile)

require(manifest.get("schema") == "gofly.governance_boundary_inventory.v1", "schema must be gofly.governance_boundary_inventory.v1")
require("governance-boundary-inventory-check" in targets, "Makefile must expose governance-boundary-inventory-check")
require("api-contract-check" in targets, "Makefile must expose api-contract-check")
require("check-governance-boundary-inventory.sh" in governance_script, "governance-10-rounds.sh must run the boundary inventory in Round 01")

api_contract_line = next((line for line in makefile.splitlines() if line.startswith("api-contract-check:")), "")
require("openapi-validation-check" in api_contract_line, "api-contract-check must depend on openapi-validation-check")
require("rpc-boundary-check" in api_contract_line, "api-contract-check must depend on rpc-boundary-check")

tasks = manifest.get("aiflowTasks") or []
actual_tasks = [item.get("id") for item in tasks if isinstance(item, dict)]
require(actual_tasks == expected_tasks, f"aiflowTasks must be ordered {expected_tasks!r}; got {actual_tasks!r}")
for expected_round, item in enumerate(tasks, start=1):
    if not isinstance(item, dict):
        missing.append(f"aiflowTasks entry must be an object: {item!r}")
        continue
    task_id = item.get("id", "<missing>")
    require(item.get("round") == expected_round, f"{task_id}: round must be {expected_round}")
    for field in ("id", "title", "gate"):
        require(bool(item.get(field)), f"{task_id}: {field} is required")
    require(gate_is_known(item.get("gate", ""), targets), f"{task_id}: gate is not known: {item.get('gate')!r}")

surfaces = manifest.get("surfaces") or []
actual_surfaces = {item.get("id") for item in surfaces if isinstance(item, dict)}
require(actual_surfaces == expected_surfaces, f"surfaces drifted: missing={sorted(expected_surfaces - actual_surfaces)} extra={sorted(actual_surfaces - expected_surfaces)}")
for item in surfaces:
    if not isinstance(item, dict):
        missing.append(f"surfaces entry must be an object: {item!r}")
        continue
    surface_id = item.get("id", "<missing>")
    paths = item.get("paths") or []
    require(paths, f"{surface_id}: paths are required")
    for path in paths:
        require((root / path).exists(), f"{surface_id}: path is missing: {path}")
    gate = item.get("gate", "")
    require(gate_is_known(gate, targets), f"{surface_id}: gate is not known: {gate!r}")

ignored = set(manifest.get("ignoredRuntimePaths") or [])
require(ignored == expected_ignored, f"ignoredRuntimePaths drifted: missing={sorted(expected_ignored - ignored)} extra={sorted(ignored - expected_ignored)}")
gitignore_patterns = {line.strip() for line in gitignore.splitlines() if line.strip() and not line.lstrip().startswith("#")}
for path in sorted(expected_ignored):
    require(gitignore_covers(path, gitignore_patterns), f".gitignore must contain or cover {path}")

baseline_gates = manifest.get("baselineGates") or []
for gate in baseline_gates:
    require(gate_is_known(gate, targets), f"baseline gate is not known: {gate!r}")

timeout_policy = manifest.get("timeoutPolicy") or {}
require(timeout_policy.get("aiflowDefaultCommandTimeout") == "2m", "timeoutPolicy must record the aiflow 2m command timeout")
require("governance-boundary-inventory-check" in timeout_policy.get("fallback", ""), "timeoutPolicy fallback must mention governance-boundary-inventory-check")

if missing:
    print("governance boundary inventory check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("governance boundary inventory OK")
PY
