#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "rpc-tier1-evidence.json"
transport_boundary_path = root / "docs" / "reference" / "rpc-transport-boundary.json"
checks = {
    pathlib.Path("docs/reference/rpc-boundary.md"): [
        "gofly.rpc_boundary.v1",
        "docs/reference/rpc-tier1-evidence.json",
        "docs/reference/rpc-transport-boundary.json",
        "gofly.rpc_transport_boundary.v1",
        "gofly RPC",
        "rpc/grpc",
        "Kitex",
        "coexistence",
        "BenchmarkRPCUnary",
        "BenchmarkRPCStreamGovernance",
        "BenchmarkRPCServerStreamGovernance",
        "BenchmarkRPCClientStreamGovernance",
        "BenchmarkRPCBidiStreamGovernance",
        "Tier 1 promotion checklist",
        "gRPC compatibility matrix",
        "Unauthenticated",
        "SERVING",
        "resolver",
        "balancer",
        "rollback",
        "bench-evidence-check",
        "Deadline and error-code mapping",
        "deadline_exceeded",
        "invalid_argument",
        "Unauthenticated",
        "Retry and balancer contract",
        "health-aware",
        "service mesh",
        "go-zero coexistence",
        "generated service",
        "rollback routing",
        "Transport Boundary Contract",
        "transport-parity-forbidden",
        "Netpoll",
        "TTHeader",
        "Thrift",
    ],
    pathlib.Path("bench/matrix.md"): [
        "BenchmarkRPCUnary",
        "BenchmarkRPCStreamGovernance",
        "BenchmarkRPCServerStreamGovernance",
        "BenchmarkRPCClientStreamGovernance",
        "BenchmarkRPCBidiStreamGovernance",
        "resolver/balancer smoke",
        "Kitex boundary",
    ],
    pathlib.Path("bench/rpc_bench_test.go"): [
        "BenchmarkRPCUnary",
        "BenchmarkRPCStreamGovernance",
        "BenchmarkRPCServerStreamGovernance",
        "BenchmarkRPCClientStreamGovernance",
        "BenchmarkRPCBidiStreamGovernance",
        "server_stream",
        "client_stream",
        "bidi_stream",
        "stream governance overhead",
    ],
    pathlib.Path("docs/comparisons/kitex.md"): [
        "rollback",
        "BenchmarkRPCStreamGovernance",
        "BenchmarkRPCServerStreamGovernance",
        "BenchmarkRPCClientStreamGovernance",
        "BenchmarkRPCBidiStreamGovernance",
        "coexistence",
    ],
    pathlib.Path("docs/comparisons/go-zero.md"): [
        "timeout, retry, breaker, and rate-limit",
        "Rollback plan",
        "discovery",
    ],
    pathlib.Path("examples/rpc-idl-matrix/main_test.go"): [
        "server_stream",
        "client_stream",
        "bidi_stream",
        "round_robin",
        "health_aware",
    ],
}

missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/rpc-tier1-evidence.json: file is missing")
if transport_boundary_path.is_file():
    transport_boundary = json.loads(transport_boundary_path.read_text(encoding="utf-8"))
else:
    transport_boundary = {}
    missing.append("docs/reference/rpc-transport-boundary.json: file is missing")

require(manifest.get("schema") == "gofly.rpc_tier1_evidence.v1", "rpc tier1 evidence schema mismatch")
require(manifest.get("status") == "tier1-candidate", "rpc tier1 evidence status must be tier1-candidate")
require({"rpc", "rpc/grpc", "gateway", "app"} <= set(manifest.get("surfaces") or []), "rpc tier1 evidence surfaces must include rpc, rpc/grpc, gateway, and app")
require(transport_boundary.get("schema") == "gofly.rpc_transport_boundary.v1", "rpc transport boundary schema mismatch")
require(transport_boundary.get("status") == "blocking", "rpc transport boundary status must be blocking")
require(transport_boundary.get("blockingGate") == "make rpc-boundary-check", "rpc transport boundary blockingGate mismatch")

policy = manifest.get("promotionPolicy") or {}
require(policy.get("blockingGate") == "make rpc-boundary-check", "rpc tier1 evidence blocking gate must be make rpc-boundary-check")
require(policy.get("releaseGate") == "make bench-evidence-check", "rpc tier1 evidence release gate must be make bench-evidence-check")
require(policy.get("budgetRatchet") == "bench/budget-ratchet.json", "rpc tier1 evidence must reference bench/budget-ratchet.json")
require("report-only" in policy.get("latencyPolicy", ""), "rpc latency policy must remain report-only until promoted")

transport_scope = transport_boundary.get("scope") or {}
for included in (
    "RPC transport boundary",
    "gRPC compatibility",
    "Kitex coexistence",
    "stream governance evidence",
    "resolver and balancer behavior",
    "deadline and retry semantics",
    "benchmark promotion blockers",
    "rollback routing",
):
    require(included in set(transport_scope.get("included") or []), f"rpc transport boundary scope.included missing {included!r}")
for excluded in (
    "Kitex transport parity",
    "gRPC-Go transport parity",
    "Netpoll parity",
    "TTHeader parity",
    "Thrift protocol parity",
    "community size",
):
    require(excluded in set(transport_scope.get("excluded") or []), f"rpc transport boundary scope.excluded missing {excluded!r}")
require(
    set(transport_boundary.get("referenceFrameworks") or []) == {"Kitex", "gRPC-Go", "go-zero"},
    "rpc transport boundary referenceFrameworks mismatch",
)
claim_policy = transport_boundary.get("claimPolicy") or {}
require(claim_policy.get("transportParityClaim") == "forbidden", "rpc transport boundary must forbid transport parity claims")
for field in ("allowedClaim", "promotionPolicy", "rollbackPolicy"):
    require(
        len(str(claim_policy.get(field) or "").split()) >= 12,
        f"rpc transport boundary claimPolicy.{field} must be actionable",
    )
require("Tier 1" in str(claim_policy.get("promotionPolicy") or ""), "rpc transport boundary promotionPolicy must name Tier 1")
require("Kitex" in str(claim_policy.get("rollbackPolicy") or ""), "rpc transport boundary rollbackPolicy must mention Kitex")

transport_rows = transport_boundary.get("transportBoundaries") or []
required_transport_rows = {
    "kitex-hot-path-boundary",
    "grpc-ecosystem-boundary",
    "gozero-coexistence-boundary",
}
actual_transport_rows = {item.get("id") for item in transport_rows if isinstance(item, dict)}
require(actual_transport_rows == required_transport_rows, f"rpc transport boundary rows mismatch: {sorted(actual_transport_rows)!r}")
for item in transport_rows:
    if not isinstance(item, dict):
        missing.append(f"rpc transport boundary row must be an object: {item!r}")
        continue
    row_id = item.get("id", "<missing>")
    for field in ("id", "runtime", "keepWhen", "goflyRole", "requiredEvidence", "gate", "rollbackOrEscalation"):
        require(item.get(field) not in ("", None, []), f"rpc transport boundary {row_id}: {field} is required")
    for evidence in item.get("requiredEvidence") or []:
        require((root / evidence).exists(), f"rpc transport boundary {row_id}: evidence path missing: {evidence}")
    for field in ("keepWhen", "goflyRole", "rollbackOrEscalation"):
        require(len(str(item.get(field) or "").split()) >= 8, f"rpc transport boundary {row_id}: {field} must be actionable")

transport_blockers = transport_boundary.get("promotionBlockers") or []
required_transport_blockers = {
    "release-train-missing": "blocked",
    "budget-report-only": "report-only",
    "transport-parity-forbidden": "blocked",
}
actual_transport_blockers = {
    item.get("id"): item
    for item in transport_blockers
    if isinstance(item, dict) and item.get("id")
}
require(set(actual_transport_blockers) == set(required_transport_blockers), f"rpc transport boundary promotionBlockers mismatch: {sorted(actual_transport_blockers)!r}")
for blocker_id, status in required_transport_blockers.items():
    blocker = actual_transport_blockers.get(blocker_id) or {}
    require(blocker.get("currentStatus") == status, f"rpc transport boundary blocker {blocker_id}: currentStatus mismatch")
    for field in ("source", "requiredAction", "gate"):
        require(blocker.get(field), f"rpc transport boundary blocker {blocker_id}: {field} is required")
    require((root / blocker.get("source", "")).exists(), f"rpc transport boundary blocker {blocker_id}: source path missing")
    require(len(str(blocker.get("requiredAction") or "").split()) >= 10, f"rpc transport boundary blocker {blocker_id}: requiredAction must be actionable")

evidence_links = transport_boundary.get("evidenceLinks") or []
required_evidence_links = {
    "stream-governance": {"server-stream-governance", "client-stream-governance", "bidi-stream-governance"},
    "resolver-balancer": {"resolver-updates", "balancer-routing", "retry-balancer-contract"},
    "deadline-retry": {"deadline-error-code-mapping", "retry-balancer-contract", "grpc-compatibility"},
}
actual_evidence_links = {
    item.get("id"): set(item.get("evidenceIds") or [])
    for item in evidence_links
    if isinstance(item, dict) and item.get("id")
}
require(set(actual_evidence_links) == set(required_evidence_links), f"rpc transport boundary evidenceLinks mismatch: {sorted(actual_evidence_links)!r}")
for link_id, expected_ids in required_evidence_links.items():
    require(expected_ids <= actual_evidence_links.get(link_id, set()), f"rpc transport boundary link {link_id}: evidenceIds missing {sorted(expected_ids - actual_evidence_links.get(link_id, set()))!r}")
    link = next((item for item in evidence_links if isinstance(item, dict) and item.get("id") == link_id), {})
    require(link.get("gate"), f"rpc transport boundary link {link_id}: gate is required")
    require(len(str(link.get("rollbackOrEscalation") or "").split()) >= 10, f"rpc transport boundary link {link_id}: rollbackOrEscalation must be actionable")
readiness = manifest.get("promotionReadiness") or {}
require(readiness.get("currentTier") == "tier2", "rpc promotionReadiness.currentTier must be tier2 while candidate evidence is incomplete")
require(readiness.get("targetTier") == "tier1", "rpc promotionReadiness.targetTier must be tier1")
require(readiness.get("status") == "blocked", "rpc promotionReadiness.status must stay blocked until release train and budget evidence pass")
release_train = readiness.get("releaseTrainEvidence") or {}
require(release_train.get("requiredCompletedReleaseTrains") == 1, "rpc release train evidence must require one completed release train")
require(release_train.get("completedReleaseTrains") == 0, "rpc completed release trains must remain 0 until release evidence is attached")
require(release_train.get("status") == "pending", "rpc release train evidence must be pending before promotion")
release_refs = release_train.get("evidenceRefs") or []
require(release_refs, "rpc release train evidence must list release and required-check evidence refs")
for required_path in ("docs/releases/evidence-index.json", "docs/reference/ci-required-check-evidence.json"):
    require(any(ref.get("path") == required_path for ref in release_refs if isinstance(ref, dict)), f"rpc release train evidence missing {required_path}")
budget_evidence = readiness.get("budgetEvidence") or {}
require(budget_evidence.get("status") == "pending", "rpc budgetEvidence.status must be pending until blocking RPC budgets exist")
require(budget_evidence.get("ratchet") == "bench/budget-ratchet.json", "rpc budgetEvidence must reference bench/budget-ratchet.json")
require(budget_evidence.get("requiredGate") == "make bench-regression-check", "rpc budgetEvidence.requiredGate must be make bench-regression-check")
blockers = readiness.get("remainingBlockers") or []
blocker_ids = {item.get("id") for item in blockers if isinstance(item, dict)}
for blocker_id in ("rpc-release-train-missing", "rpc-budget-report-only"):
    require(blocker_id in blocker_ids, f"rpc promotionReadiness.remainingBlockers missing {blocker_id}")

adopter_contract = manifest.get("adopterContract") or {}
require(
    adopter_contract.get("schema") == "gofly.rpc_adopter_contract.v1",
    "rpc adopterContract schema must be gofly.rpc_adopter_contract.v1",
)
require(
    adopter_contract.get("source") == "docs/reference/rpc-tier1-evidence.json",
    "rpc adopterContract source mismatch",
)
require(
    adopter_contract.get("dashboardReportField") == "rpcAdoption.tier1Evidence",
    "rpc adopterContract dashboardReportField mismatch",
)
require(
    set(adopter_contract.get("acceptanceGates") or []) == {
        "make rpc-boundary-check",
        "make bench-evidence-check",
        "make bench-regression-check",
    },
    "rpc adopterContract acceptanceGates mismatch",
)
require(
    len(str(adopter_contract.get("policy") or "").split()) >= 20,
    "rpc adopterContract policy must be actionable",
)
adopter_tier_policy = adopter_contract.get("tierPolicy") or {}
require(adopter_tier_policy.get("currentTier") == "tier2", "rpc adopterContract tierPolicy.currentTier must be tier2")
require(adopter_tier_policy.get("targetTier") == "tier1", "rpc adopterContract tierPolicy.targetTier must be tier1")
require(adopter_tier_policy.get("promotionStatus") == "blocked", "rpc adopterContract tierPolicy.promotionStatus must be blocked")
for field in ("releaseTrainRequirement", "budgetRequirement"):
    require(
        len(str(adopter_tier_policy.get(field) or "").split()) >= 10,
        f"rpc adopterContract tierPolicy.{field} must be actionable",
    )

surface_classes = adopter_contract.get("surfaceClasses") or []
surface_by_id = {
    item.get("id"): item
    for item in surface_classes
    if isinstance(item, dict) and item.get("id")
}
required_surfaces = {
    "grpc-compatibility-ready": "tier1-ready-evidence",
    "governed-rpc-candidate": "candidate",
    "rpc-performance-budget-report-only": "report-only",
    "framework-coexistence-rollback": "rollback-required",
}
require(set(surface_by_id) == set(required_surfaces), f"rpc adopterContract surfaceClasses mismatch: {sorted(surface_by_id)!r}")
for surface_id, classification in required_surfaces.items():
    surface = surface_by_id.get(surface_id) or {}
    require(surface.get("classification") == classification, f"rpc adopterContract {surface_id}: classification mismatch")
    require(surface.get("surface"), f"rpc adopterContract {surface_id}: surface is required")
    evidence_ids = set(surface.get("evidenceIds") or [])
    require(evidence_ids, f"rpc adopterContract {surface_id}: evidenceIds must not be empty")
    for field in ("adopterDecision", "rollbackAction"):
        require(
            len(str(surface.get(field) or "").split()) >= 12,
            f"rpc adopterContract {surface_id}: {field} must be actionable",
        )
    for field in ("supportBundleAction", "failureReportEvidence"):
        require(
            len(str(surface.get(field) or "").split()) >= 12,
            f"rpc adopterContract {surface_id}: {field} must be actionable",
        )
    require(
        "gofly bug --json" in str(surface.get("supportBundleAction") or ""),
        f"rpc adopterContract {surface_id}: supportBundleAction must mention gofly bug --json",
    )

surface_evidence_requirements = {
    "grpc-compatibility-ready": {
        "grpc-compatibility",
        "deadline-error-code-mapping",
        "retry-balancer-contract",
    },
    "governed-rpc-candidate": {
        "unary-contract",
        "server-stream-governance",
        "client-stream-governance",
        "bidi-stream-governance",
        "resolver-updates",
        "balancer-routing",
    },
    "rpc-performance-budget-report-only": {
        "unary-contract",
        "server-stream-governance",
        "client-stream-governance",
        "bidi-stream-governance",
    },
    "framework-coexistence-rollback": {
        "kitex-coexistence-rollback",
        "gozero-coexistence-rollback",
    },
}
for surface_id, expected_ids in surface_evidence_requirements.items():
    actual_ids = set((surface_by_id.get(surface_id) or {}).get("evidenceIds") or [])
    require(
        expected_ids <= actual_ids,
        f"rpc adopterContract {surface_id}: evidenceIds missing {sorted(expected_ids - actual_ids)!r}",
    )

ratchet_path = root / "bench" / "budget-ratchet.json"
if ratchet_path.is_file():
    ratchet = json.loads(ratchet_path.read_text(encoding="utf-8"))
else:
    ratchet = {}
    missing.append("bench/budget-ratchet.json: file is missing")
require(ratchet.get("schema") == "gofly.benchmark_budget_ratchet.v1", "benchmark budget ratchet schema mismatch")
tracked_benchmarks = set(ratchet.get("trackedBenchmarks") or [])
latency_policy = ratchet.get("latencyPolicy") or {}
report_only = set(latency_policy.get("reportOnly") or [])
promoted_latency = {
    item.get("benchmark", "")
    for item in latency_policy.get("promoted") or []
    if isinstance(item, dict) and item.get("benchmark")
}
rpc_policy = ratchet.get("rpcPolicy") or {}
release_promotion = rpc_policy.get("releasePromotion") or {}
require(release_promotion.get("status") == "blocked", "budget ratchet rpcPolicy.releasePromotion.status must be blocked")
require(release_promotion.get("tier1Manifest") == "docs/reference/rpc-tier1-evidence.json", "budget ratchet rpcPolicy.releasePromotion must reference rpc tier1 manifest")
require(release_promotion.get("requiredCompletedReleaseTrains") == 1, "budget ratchet rpcPolicy.releasePromotion must require one release train")
require(release_promotion.get("completedReleaseTrains") == 0, "budget ratchet rpcPolicy.releasePromotion completed release trains must remain 0 before promotion")
require(release_promotion.get("requiredBlockingGate") == "make bench-regression-check", "budget ratchet rpcPolicy.releasePromotion gate must be make bench-regression-check")
require("Kitex" in release_promotion.get("rollbackGuidance", ""), "budget ratchet rpcPolicy.releasePromotion rollback guidance must mention Kitex")
candidate_benchmarks = {
    item.get("benchmark", "")
    for item in rpc_policy.get("candidates") or []
    if isinstance(item, dict) and item.get("benchmark")
}
for benchmark in (
    "BenchmarkRPCUnary/gofly_rpc",
    "BenchmarkRPCStreamGovernance",
    "BenchmarkRPCServerStreamGovernance",
    "BenchmarkRPCClientStreamGovernance",
    "BenchmarkRPCBidiStreamGovernance",
):
    require(benchmark not in tracked_benchmarks, f"RPC benchmark must stay out of trackedBenchmarks until promotion criteria pass: {benchmark}")
    require(benchmark in candidate_benchmarks, f"budget ratchet rpcPolicy.candidates missing {benchmark}")
    require(benchmark not in promoted_latency, f"RPC latency benchmark must not be promoted without trend evidence: {benchmark}")
require(rpc_policy.get("status") == "report-only", "budget ratchet rpcPolicy.status must be report-only")
for criterion in ("minimum 5 baseline samples", "minimum 3 current trend samples", "Kitex rollback note"):
    require(any(criterion in item for item in rpc_policy.get("promotionCriteria") or []), f"budget ratchet rpcPolicy.promotionCriteria missing {criterion!r}")

evidence = manifest.get("evidence") or []
required_evidence = {
    "unary-contract",
    "server-stream-governance",
    "client-stream-governance",
    "bidi-stream-governance",
    "resolver-updates",
    "balancer-routing",
    "kitex-coexistence-rollback",
    "grpc-compatibility",
    "deadline-error-code-mapping",
    "retry-balancer-contract",
    "gozero-coexistence-rollback",
}
actual_evidence = {item.get("id") for item in evidence if isinstance(item, dict)}
require(required_evidence <= actual_evidence, f"rpc tier1 evidence missing ids: {sorted(required_evidence - actual_evidence)!r}")

for item in evidence:
    if not isinstance(item, dict):
        missing.append(f"rpc tier1 evidence entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    for field in ("id", "capability", "gate", "evidenceRefs"):
        require(bool(item.get(field)), f"rpc tier1 evidence {item_id}: {field} is required")
    for field in ("decisionBoundary", "rollbackOrEscalation"):
        require(
            len(str(item.get(field) or "").split()) >= 8,
            f"rpc tier1 evidence {item_id}: {field} must be actionable",
        )
    require(item.get("requiredForTier1") is True, f"rpc tier1 evidence {item_id}: requiredForTier1 must be true")
    refs = item.get("evidenceRefs") or []
    require(refs, f"rpc tier1 evidence {item_id}: evidenceRefs must not be empty")
    for ref in refs:
        ref_path = ref.get("path", "")
        needles = ref.get("contains") or []
        require(bool(ref_path), f"rpc tier1 evidence {item_id}: ref path is required")
        require(bool(needles), f"rpc tier1 evidence {item_id}: ref contains list is required for {ref_path}")
        if not ref_path:
            continue
        path = root / ref_path
        if not path.is_file():
            missing.append(f"rpc tier1 evidence {item_id}: ref file is missing: {ref_path}")
            continue
        text = path.read_text(encoding="utf-8")
        for needle in needles:
            if needle not in text:
                missing.append(f"rpc tier1 evidence {item_id}: {ref_path} missing {needle!r}")

evidence_by_id = {
    item.get("id"): item for item in evidence
    if isinstance(item, dict) and item.get("id")
}

deadline_mapping = evidence_by_id.get("deadline-error-code-mapping") or {}
deadline_contracts = deadline_mapping.get("contractMappings") or []
deadline_stable_codes = {
    contract.get("stableCode")
    for contract in deadline_contracts
    if isinstance(contract, dict)
}
for code in ("deadline_exceeded", "invalid_argument", "Unauthenticated", "Unavailable"):
    require(code in deadline_stable_codes, f"deadline-error-code-mapping missing stable code {code}")
require(
    any(contract.get("httpStatus") == 504 for contract in deadline_contracts if isinstance(contract, dict)),
    "deadline-error-code-mapping must include HTTP 504 mapping for deadline expiry",
)

retry_mapping = evidence_by_id.get("retry-balancer-contract") or {}
retry_contracts = retry_mapping.get("contractMappings") or []
retry_text = json.dumps(retry_contracts, sort_keys=True)
for needle in ("unavailable", "codes.Unavailable", "round_robin", "weighted_round_robin", "p2c", "consistent_hash", "health_aware"):
    require(needle in retry_text, f"retry-balancer-contract missing {needle}")

gozero_mapping = evidence_by_id.get("gozero-coexistence-rollback") or {}
require(
    "generated-upgrade-dry-run-check" in gozero_mapping.get("gate", ""),
    "gozero-coexistence-rollback gate must include generated-upgrade-dry-run-check",
)
require(
    "go-zero" in gozero_mapping.get("rollbackOrEscalation", ""),
    "gozero-coexistence-rollback rollbackOrEscalation must mention go-zero",
)

for path, needles in checks.items():
    if not path.is_file():
        missing.append(f"{path}: file is missing")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("rpc boundary check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("rpc boundary governance ok")
PY
