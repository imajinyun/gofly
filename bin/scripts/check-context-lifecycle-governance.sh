#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "context-lifecycle-governance.json"
missing = []

expected_surfaces = {
    "app-runtime-lifecycle",
    "gateway-shadow-and-health-lifecycle",
    "rpc-resolver-and-client-lifecycle",
    "distributed-lock-watchdog",
    "cli-control-plane-watch",
}
required_policy = {
    "nilContextBoundary",
    "shutdownBoundary",
    "goroutineBoundary",
    "timeoutBoundary",
    "leakDetection",
}
required_release_gates = {
    "make context-lifecycle-governance-check",
    "targeted go test and go vet for touched packages",
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
    return gate.startswith("go test ") or gate.startswith("targeted ")


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/context-lifecycle-governance.json is missing")

makefile = read_text(root / "Makefile")
targets = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")

require(manifest.get("schema") == "gofly.context_lifecycle_governance.v1", "schema must be gofly.context_lifecycle_governance.v1")
require(manifest.get("aiflowTask") == "GOFLY-GOV-10R3-02", "aiflowTask must be GOFLY-GOV-10R3-02")
require(manifest.get("acceptanceGate") == "make context-lifecycle-governance-check", "acceptanceGate must be make context-lifecycle-governance-check")
require("context-lifecycle-governance-check" in targets, "Makefile must expose context-lifecycle-governance-check")
require("check-context-lifecycle-governance.sh" in makefile, "Makefile must call check-context-lifecycle-governance.sh")
require("context-lifecycle-governance-check" in docs_check_line, "docs-check must include context-lifecycle-governance-check")

policy = manifest.get("policy") or {}
require(set(policy) == required_policy, f"policy drifted: missing={sorted(required_policy - set(policy))} extra={sorted(set(policy) - required_policy)}")
for key, value in policy.items():
    require(len(str(value).split()) >= 6, f"policy {key} must be actionable")

surfaces = manifest.get("surfaces") or []
surface_map = {
    item.get("id"): item
    for item in surfaces
    if isinstance(item, dict) and item.get("id")
}
require(set(surface_map) == expected_surfaces, f"surfaces drifted: missing={sorted(expected_surfaces - set(surface_map))} extra={sorted(set(surface_map) - expected_surfaces)}")

for surface_id, item in surface_map.items():
    require(item.get("status") == "implemented", f"{surface_id}: status must be implemented")
    paths = item.get("paths") or []
    tests = item.get("tests") or []
    evidence = item.get("evidence") or []
    gate = str(item.get("gate") or "")
    require(paths, f"{surface_id}: paths are required")
    require(tests, f"{surface_id}: tests are required")
    require(len(evidence) >= 4, f"{surface_id}: at least four evidence anchors are required")
    require(gate_is_known(gate, targets), f"{surface_id}: gate is not known: {gate!r}")
    for rel in paths:
        require((root / rel).exists(), f"{surface_id}: path is missing: {rel}")
    for rel in tests:
        require((root / rel).exists(), f"{surface_id}: test path is missing: {rel}")
    searchable = "\n".join(
        read_text(root / rel)
        for rel in paths + tests
        if (root / rel).exists()
    )
    for anchor in evidence:
        require(str(anchor) in searchable, f"{surface_id}: evidence anchor {anchor!r} is not present in source or tests")

release_gates = set(manifest.get("releaseGates") or [])
require(release_gates == required_release_gates, f"releaseGates drifted: missing={sorted(required_release_gates - release_gates)} extra={sorted(release_gates - required_release_gates)}")
for gate in release_gates:
    require(gate_is_known(gate, targets), f"release gate is not known: {gate!r}")

blockers = manifest.get("knownExternalBlockers") or []
for blocker in blockers:
    require(blocker.get("id"), "knownExternalBlockers entries require id")
    require(blocker.get("scope"), f"{blocker.get('id', '<missing>')}: scope is required")
    require(blocker.get("symptom"), f"{blocker.get('id', '<missing>')}: symptom is required")
    require(blocker.get("impact"), f"{blocker.get('id', '<missing>')}: impact is required")
    require(blocker.get("goflyImpact"), f"{blocker.get('id', '<missing>')}: goflyImpact is required")

if missing:
    print("context lifecycle governance check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("context lifecycle governance OK")
PY
