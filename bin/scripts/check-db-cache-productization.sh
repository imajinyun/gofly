#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "db-cache-productization.json"
missing = []

expected_capabilities = {
    "sql-storage-core": "implemented",
    "transactional-outbox": "implemented",
    "typed-tiered-cache": "implemented",
    "redis-model-cache": "implemented",
    "model-generator-boundary": "implemented",
    "reference-app-db-cache": "implemented",
    "migration-runner": "planned",
    "production-redis-integration": "planned",
}
required_release_gates = {
    "go test -shuffle=on ./core/storage/...",
    "go test -shuffle=on ./core/outbox/...",
    "go test -shuffle=on ./cache ./core/kv/...",
    "go test -shuffle=on ./cmd/gofly/internal/generator -run TestGenerateModelFromDDL",
    "make reference-app-smoke",
    "make framework-gap-check",
    "make db-cache-productization-check",
}
expected_workflows = {
    "generated-transaction-workflow",
    "generated-pagination-workflow",
    "generated-optimistic-lock-workflow",
    "read-write-split-workflow",
    "redis-sql-observability-workflow",
    "generated-db-smoke-workflow",
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
    return gate.startswith("go test ")


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/db-cache-productization.json is missing")

makefile = read_text(root / "Makefile")
cache_guide = read_text(root / "docs" / "guides" / "cache.md")
model_guide = read_text(root / "docs" / "guides" / "model.md")
docs_index = read_text(root / "docs" / "index.md")
reference_topology = read_text(root / "docs" / "reference" / "reference-app-topology.json")
framework_long_term = read_text(root / "docs" / "reference" / "framework-gap-long-term-adoption.json")
targets = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")

require(manifest.get("schema") == "gofly.db_cache_productization.v1", "schema must be gofly.db_cache_productization.v1")
require(manifest.get("acceptanceGate") == "make db-cache-productization-check", "acceptanceGate must be make db-cache-productization-check")
require("db-cache-productization-check" in targets, "Makefile must expose db-cache-productization-check")
require("db-cache-productization-check" in docs_check_line, "docs-check must depend on db-cache-productization-check")
require("check-db-cache-productization.sh" in makefile, "Makefile must call check-db-cache-productization.sh")

gozero = manifest.get("goZeroAlignment") or {}
require(gozero.get("reference") == "docs/comparisons/go-zero.md", "goZeroAlignment.reference must be docs/comparisons/go-zero.md")
require(gozero.get("acceptanceGate") == "make framework-gap-check", "goZeroAlignment.acceptanceGate must be make framework-gap-check")
require(len(str(gozero.get("strategy") or "").split()) >= 10, "goZeroAlignment.strategy must explain sqlx/cache alignment")
require(len(str(gozero.get("rollbackOrEscalation") or "").split()) >= 10, "goZeroAlignment.rollbackOrEscalation must be actionable")
expected_gozero_evidence = {
    "SQL read/write strategy through SQLStore and NewCluster",
    "transaction examples through SQLStore.Transact and SQL outbox",
    "generated model cache contracts through RedisCachedOrderRepo and UpdateWithInvalidate",
    "Redis/cache observability through cache stats and WritePrometheus",
    "local smoke tests through examples/cache-local and reference-app-smoke",
}
actual_gozero_evidence = set(gozero.get("requiredEvidence") or [])
require(
    actual_gozero_evidence == expected_gozero_evidence,
    f"goZeroAlignment.requiredEvidence drifted: missing={sorted(expected_gozero_evidence - actual_gozero_evidence)} extra={sorted(actual_gozero_evidence - expected_gozero_evidence)}",
)
gozero_doc = read_text(root / "docs" / "comparisons" / "go-zero.md")
for needle in ("sqlx", "cache", "Rollback plan", "production orders"):
    require(needle in gozero_doc, f"go-zero comparison doc missing {needle!r}")

status_policy = manifest.get("statusPolicy") or {}
for status in {"implemented", "planned"}:
    require(status in status_policy, f"statusPolicy must document {status}")

capabilities = manifest.get("capabilities") or []
capability_map = {
    item.get("id"): item
    for item in capabilities
    if isinstance(item, dict) and item.get("id")
}
require(set(capability_map) == set(expected_capabilities), f"capabilities drifted: missing={sorted(set(expected_capabilities) - set(capability_map))} extra={sorted(set(capability_map) - set(expected_capabilities))}")

workflow_matrix = manifest.get("workflowMatrix") or []
workflow_map = {
    item.get("id"): item
    for item in workflow_matrix
    if isinstance(item, dict) and item.get("id")
}
require(
    set(workflow_map) == expected_workflows,
    f"workflowMatrix drifted: missing={sorted(expected_workflows - set(workflow_map))} extra={sorted(set(workflow_map) - expected_workflows)}",
)

for workflow_id in sorted(expected_workflows):
    item = workflow_map.get(workflow_id) or {}
    require(item.get("status") == "implemented", f"{workflow_id}: status must be implemented")
    source = str(item.get("source") or "")
    require(source and (root / source).exists(), f"{workflow_id}: source path is missing: {source!r}")
    require(gate_is_known(str(item.get("gate") or ""), targets), f"{workflow_id}: gate is not known: {item.get('gate')!r}")
    for field in ("runtimeEvidence", "generatedEvidence", "tests"):
        values = item.get(field) or []
        require(len(values) >= 2, f"{workflow_id}: {field} must include at least two anchors")
    observability = str(item.get("observability") or "")
    require(len(observability.split()) >= 8, f"{workflow_id}: observability must explain the evidence path")
    rollback = str(item.get("rollbackNote") or "")
    require(len(rollback.split()) >= 8, f"{workflow_id}: rollbackNote must be actionable")
    searchable_paths = {source}
    searchable_paths.update(str(path) for path in item.get("tests") or [])
    searchable = "\n".join(read_text(root / rel) for rel in sorted(searchable_paths) if (root / rel).exists())
    for anchor in item.get("runtimeEvidence") or []:
        require(str(anchor) in searchable or str(anchor) in reference_topology, f"{workflow_id}: runtimeEvidence anchor {anchor!r} is not present")
    for anchor in item.get("generatedEvidence") or []:
        require(str(anchor) in searchable or str(anchor) in reference_topology, f"{workflow_id}: generatedEvidence anchor {anchor!r} is not present")
    for rel in item.get("tests") or []:
        require((root / rel).exists(), f"{workflow_id}: test path is missing: {rel}")

for capability_id, expected_status in expected_capabilities.items():
    item = capability_map.get(capability_id) or {}
    require(item.get("status") == expected_status, f"{capability_id}: status must be {expected_status}")
    require(bool(item.get("adopterValue")), f"{capability_id}: adopterValue is required")
    rollback = str(item.get("rollbackNote") or "")
    require(len(rollback.split()) >= 8, f"{capability_id}: rollbackNote must be actionable")
    gate = str(item.get("gate") or "")
    require(gate_is_known(gate, targets), f"{capability_id}: gate is not known: {gate!r}")
    if expected_status == "implemented":
        implementations = item.get("implementation") or []
        tests = item.get("tests") or []
        evidence = item.get("evidence") or []
        require(implementations, f"{capability_id}: implementation paths are required")
        require(tests, f"{capability_id}: test paths are required")
        require(len(evidence) >= 3, f"{capability_id}: evidence must include at least three anchors")
        for rel in implementations:
            require((root / rel).exists(), f"{capability_id}: implementation path is missing: {rel}")
        for rel in tests:
            require((root / rel).exists(), f"{capability_id}: test path is missing: {rel}")
        searchable = "\n".join(read_text(root / rel) for rel in implementations + tests if (root / rel).exists())
        for anchor in evidence:
            require(str(anchor) in searchable or str(anchor) in reference_topology, f"{capability_id}: evidence anchor {anchor!r} is not present in implementation, tests, or topology")
    else:
        require(not item.get("implementation"), f"{capability_id}: planned capability must not claim implementation paths")
        require(not item.get("tests"), f"{capability_id}: planned capability must not claim test paths")
        require(len(item.get("promotionCriteria") or []) >= 3, f"{capability_id}: promotionCriteria must include at least three items")

release_gates = set(manifest.get("releaseGates") or [])
require(release_gates == required_release_gates, f"releaseGates drifted: missing={sorted(required_release_gates - release_gates)} extra={sorted(release_gates - required_release_gates)}")
for gate in release_gates:
    require(gate_is_known(str(gate), targets), f"release gate is not known: {gate!r}")

for needle in [
    "DB and cache productization matrix",
    "docs/reference/db-cache-productization.json",
    "make db-cache-productization-check",
    "SQLStore",
    "NewCluster",
    "SQL outbox",
    "Redis-backed model cache",
    "planned",
]:
    require(needle in model_guide or needle in cache_guide, f"cache/model guides missing {needle!r}")

require(
    "[DB/cache productization](reference/db-cache-productization.json)" in docs_index,
    "docs/index.md must link DB/cache productization evidence",
)
require(
    "docs/reference/db-cache-productization.json" in framework_long_term,
    "long-term framework adoption evidence must link DB/cache productization matrix",
)

if missing:
    print("DB/cache productization check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("DB/cache productization OK")
PY
