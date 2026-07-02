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

r8_matrix = manifest.get("r8RunnableProofMatrix") or {}
require(
    r8_matrix.get("schema") == "gofly.db_cache_runnable_proof_matrix.v1",
    "r8RunnableProofMatrix.schema must be gofly.db_cache_runnable_proof_matrix.v1",
)
require(
    r8_matrix.get("aiflowTask") == "GOFLY-GOV-10R8-05",
    "r8RunnableProofMatrix.aiflowTask must be GOFLY-GOV-10R8-05",
)
require(
    r8_matrix.get("status") == "blocking-contract",
    "r8RunnableProofMatrix.status must be blocking-contract",
)
require(
    r8_matrix.get("acceptanceGate") == "make db-cache-productization-check",
    "r8RunnableProofMatrix.acceptanceGate must be make db-cache-productization-check",
)
r8_rows = {
    item.get("id"): item
    for item in r8_matrix.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
expected_r8_rows = {
    "sql-transaction-proof": {
        "adapterNeedles": {"SQLStore.Transact", "Cluster.Transact"},
        "gate": "go test -shuffle=on ./core/storage/...",
        "fallbackNeedles": {"Fallback", "sqlite-memory"},
    },
    "sql-outbox-proof": {
        "adapterNeedles": {"SQL outbox"},
        "gate": "go test -shuffle=on ./core/outbox/...",
        "fallbackNeedles": {"Fallback", "broker"},
    },
    "redis-model-cache-proof": {
        "adapterNeedles": {"Redis model cache"},
        "gate": "go test -shuffle=on ./cache ./core/kv/...",
        "fallbackNeedles": {"Fallback", "GOFLY_CACHE_DISABLED"},
    },
    "local-tiered-cache-proof": {
        "adapterNeedles": {"typed local", "tiered cache"},
        "gate": "go test -shuffle=on ./cache",
        "fallbackNeedles": {"Fallback", "GOFLY_CACHE_DISABLED"},
    },
    "cache-invalidation-proof": {
        "adapterNeedles": {"generated cache invalidation"},
        "gate": "go test -shuffle=on ./cmd/gofly/internal/generator -run TestGenerateModelFromDDL",
        "fallbackNeedles": {"Fallback", "direct repository writes"},
    },
}
require(
    set(r8_rows) == set(expected_r8_rows),
    f"r8RunnableProofMatrix rows drifted: missing={sorted(set(expected_r8_rows) - set(r8_rows))} extra={sorted(set(r8_rows) - set(expected_r8_rows))}",
)
for row_id, expected in expected_r8_rows.items():
    row = r8_rows.get(row_id) or {}
    require(row.get("status") == "runnable", f"{row_id}: status must be runnable")
    require(row.get("runnableGate") == expected["gate"], f"{row_id}: runnableGate mismatch")
    require(gate_is_known(str(row.get("runnableGate") or ""), targets), f"{row_id}: runnableGate is not known")
    for needle in expected["adapterNeedles"]:
        require(needle in str(row.get("adapter") or ""), f"{row_id}: adapter missing {needle!r}")
    for field in ("dependencyBoundary", "observabilityEvidence", "fallbackBehavior", "rollbackOrEscalation"):
        require(len(str(row.get(field) or "").split()) >= 10, f"{row_id}: {field} must be actionable")
    for needle in expected["fallbackNeedles"]:
        require(needle in str(row.get("fallbackBehavior") or ""), f"{row_id}: fallbackBehavior missing {needle!r}")
    require(
        "generated" in str(row.get("dependencyBoundary") or "").lower()
        or row_id in {"sql-outbox-proof", "local-tiered-cache-proof"},
        f"{row_id}: dependencyBoundary must name generated dependencies or root-owned runtime scope",
    )

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

p10_closeout = manifest.get("p10StorageCacheProductization") or {}
require(
    p10_closeout.get("schema") == "gofly.db_cache_p10_productization.v1",
    "p10StorageCacheProductization schema mismatch",
)
require(
    p10_closeout.get("aiflowTask") == "GOFLY-P10-3-STORAGE-CACHE-PRODUCTIZATION",
    "p10StorageCacheProductization aiflowTask mismatch",
)
require(p10_closeout.get("status") == "blocking-contract", "p10StorageCacheProductization status must be blocking-contract")
require(
    p10_closeout.get("acceptanceGate") == "make db-cache-productization-check",
    "p10StorageCacheProductization acceptanceGate mismatch",
)
require(
    len(str(p10_closeout.get("goZeroGapClosure") or "").split()) >= 18,
    "p10StorageCacheProductization goZeroGapClosure must be actionable",
)
p10_rows = {
    item.get("id"): item
    for item in p10_closeout.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
expected_p10_rows = {
    "read-write-strategy": {"SQLStore", "NewCluster", "Reader", "Writer", "FOR UPDATE"},
    "transaction-examples": {"SQLStore.Transact", "Cluster.Transact", "SQL outbox", "outbox relay"},
    "generated-model-cache-contracts": {"RedisCachedOrderRepo", "UpdateWithInvalidate", "GenerateModelFromDDL", "ensureModelGoModDependencies"},
    "redis-cache-observability": {"Cache stats", "WritePrometheus", "Redis miss semantics", "GOFLY_CACHE_DISABLED"},
    "local-smoke-reference-app": {"examples/cache-local", "examples/production-orders", "REFERENCE_APP_MODE=memory make reference-app-smoke", "gofly.cache_local.v1"},
}
require(set(p10_rows) == set(expected_p10_rows), f"p10StorageCacheProductization rows mismatch: {sorted(p10_rows)!r}")
for row_id, expected_evidence in expected_p10_rows.items():
    row = p10_rows.get(row_id) or {}
    for field in ("id", "surface", "evidence", "gate", "rollbackOrEscalation"):
        require(row.get(field), f"p10StorageCacheProductization {row_id}: {field} is required")
    evidence = set(row.get("evidence") or [])
    require(expected_evidence <= evidence, f"p10StorageCacheProductization {row_id}: evidence missing {sorted(expected_evidence - evidence)!r}")
    gate = str(row.get("gate") or "")
    require(gate_is_known(gate, targets), f"p10StorageCacheProductization {row_id}: gate is not known: {gate!r}")
    require(len(str(row.get("rollbackOrEscalation") or "").split()) >= 10, f"p10StorageCacheProductization {row_id}: rollbackOrEscalation must be actionable")
policy_text = str(p10_closeout.get("promotionPolicy") or "")
for needle in ("read/write routing", "transaction examples", "generated model cache contracts", "Redis/cache observability", "local smoke", "rollback notes"):
    require(needle in policy_text, f"p10StorageCacheProductization promotionPolicy missing {needle!r}")
runtime_policy = str(p10_closeout.get("runtimeArtifactPolicy") or "")
for needle in ("runtime evidence", "ignored temporary paths"):
    require(needle in runtime_policy, f"p10StorageCacheProductization runtimeArtifactPolicy missing {needle!r}")

p13_closeout = manifest.get("p13DBCacheProductization") or {}
require(
    p13_closeout.get("schema") == "gofly.db_cache_p13_productization.v1",
    "p13DBCacheProductization schema mismatch",
)
require(
    p13_closeout.get("aiflowTask") == "GOFLY-P13-05-DB-CACHE-PRODUCTIZATION",
    "p13DBCacheProductization aiflowTask mismatch",
)
require(p13_closeout.get("status") == "blocking-contract", "p13DBCacheProductization status must be blocking-contract")
require(
    p13_closeout.get("acceptanceGate") == "make db-cache-productization-check",
    "p13DBCacheProductization acceptanceGate mismatch",
)
for needle in ("SQL repository", "transaction", "pagination", "optimistic-lock", "read/write split", "Redis cache-aside", "cache observability", "generated DB/cache smoke", "cache hot-path candidate"):
    require(needle in str(p13_closeout.get("objective") or ""), f"p13DBCacheProductization objective missing {needle!r}")
expected_p13_capabilities = [
    "sql-repository-template",
    "transaction-sample",
    "pagination",
    "optimistic-lock",
    "read-write-split",
    "redis-cache-aside",
    "cache-observability",
    "generated-db-cache-smoke",
    "cache-hot-path-candidate",
]
require(
    p13_closeout.get("requiredCapabilities") == expected_p13_capabilities,
    "p13DBCacheProductization requiredCapabilities must match the P13 contract",
)
p13_rows = {
    item.get("id"): item
    for item in p13_closeout.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
require(set(p13_rows) == set(expected_p13_capabilities), f"p13DBCacheProductization rows mismatch: {sorted(p13_rows)!r}")
p13_expected = {
    "sql-repository-template": {
        "status": "implemented",
        "needles": {"type %sRepo struct", "store *storage.SQLStore", "FindWhere", "storage.SelectWhere"},
    },
    "transaction-sample": {
        "status": "implemented",
        "needles": {"OrderRepo) Transact", "SQLStore) Transact", "Cluster) Transact"},
    },
    "pagination": {
        "status": "implemented",
        "needles": {"writeSQLCursorPage", "ListAfter", "LimitOffset"},
    },
    "optimistic-lock": {
        "status": "implemented",
        "needles": {"UpdateWithVersion", "expectedVersion+1", "storage.ErrNotFound", "optimistic lock"},
    },
    "read-write-split": {
        "status": "implemented",
        "needles": {"NewCluster", "Reader", "Writer", "FOR UPDATE", "NewOrderRepoWithCluster", "cluster *storage.Cluster", "ForQuery(query)"},
    },
    "redis-cache-aside": {
        "status": "implemented",
        "needles": {"cache-aside", "NewRedisModel", "RedisCachedOrderRepo", "UpdateWithInvalidate", "redis.ErrNil"},
    },
    "cache-observability": {
        "status": "implemented",
        "needles": {"type Stats struct", "WritePrometheus", "cache.Stats", "gofly.cache_local.v1"},
    },
    "generated-db-cache-smoke": {
        "status": "implemented",
        "needles": {"TestGenerateModelFromDDLRedisCacheCompilesInTempModule", "writeGeneratedModule", "runGoCommand", '"test", "./..."'},
    },
    "cache-hot-path-candidate": {
        "status": "candidate",
        "needles": {"BenchmarkCacheHotPath", "BenchmarkCacheHotPathGetOrLoadHit", "cache-hot-path", "candidate evidence"},
    },
}
for row_id, expected in p13_expected.items():
    row = p13_rows.get(row_id) or {}
    require(row.get("status") == expected["status"], f"p13DBCacheProductization {row_id}: status must be {expected['status']}")
    for field in ("surface", "source", "tests", "evidence", "gate", "rollbackOrEscalation"):
        require(row.get(field), f"p13DBCacheProductization {row_id}: {field} is required")
    source = str(row.get("source") or "")
    require((root / source).exists(), f"p13DBCacheProductization {row_id}: source path is missing: {source!r}")
    tests = [str(path) for path in row.get("tests") or []]
    for rel in tests:
        require((root / rel).exists(), f"p13DBCacheProductization {row_id}: test/evidence path is missing: {rel}")
    gate = str(row.get("gate") or "")
    require(gate_is_known(gate, targets) or gate == "BENCH_PATTERN=BenchmarkCacheHotPath make bench-stat", f"p13DBCacheProductization {row_id}: gate is not known: {gate!r}")
    require(len(str(row.get("rollbackOrEscalation") or "").split()) >= 10, f"p13DBCacheProductization {row_id}: rollbackOrEscalation must be actionable")
    searchable_paths = {source, *tests}
    searchable = "\n".join(read_text(root / rel) for rel in sorted(searchable_paths) if (root / rel).exists())
    for anchor in row.get("evidence") or []:
        require(str(anchor) in searchable or str(anchor) in reference_topology, f"p13DBCacheProductization {row_id}: evidence anchor {anchor!r} is not present")
    for needle in expected["needles"]:
        require(needle in searchable or needle in str(row.get("evidence") or []) or needle in reference_topology, f"p13DBCacheProductization {row_id}: missing required needle {needle!r}")
    if row_id == "cache-hot-path-candidate":
        require("P13-06" in str(row.get("promotionBoundary") or ""), "p13 cache-hot-path candidate must defer full benchmark promotion to P13-06")
        require("five baseline samples" in str(row.get("promotionBoundary") or ""), "p13 cache-hot-path candidate must name baseline sample blocker")
    else:
        require(row.get("status") == "implemented", f"p13DBCacheProductization {row_id}: non-benchmark row must be implemented")
promotion_policy = str(p13_closeout.get("promotionPolicy") or "")
for needle in ("source", "tests", "runtime evidence", "generated evidence", "rollback notes", "cache hot-path performance remains candidate"):
    require(needle in promotion_policy, f"p13DBCacheProductization promotionPolicy missing {needle!r}")
root_policy = str(p13_closeout.get("rootModulePolicy") or "")
for needle in ("Generated-project-only", "generated project go.mod", "root go.mod"):
    require(needle in root_policy, f"p13DBCacheProductization rootModulePolicy missing {needle!r}")

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
    "p10StorageCacheProductization",
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
