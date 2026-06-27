#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "rpc-tier1-evidence.json"
checks = {
    pathlib.Path("docs/reference/rpc-boundary.md"): [
        "gofly.rpc_boundary.v1",
        "docs/reference/rpc-tier1-evidence.json",
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

require(manifest.get("schema") == "gofly.rpc_tier1_evidence.v1", "rpc tier1 evidence schema mismatch")
require(manifest.get("status") == "tier1-candidate", "rpc tier1 evidence status must be tier1-candidate")
require({"rpc", "rpc/grpc", "gateway", "app"} <= set(manifest.get("surfaces") or []), "rpc tier1 evidence surfaces must include rpc, rpc/grpc, gateway, and app")

policy = manifest.get("promotionPolicy") or {}
require(policy.get("blockingGate") == "make rpc-boundary-check", "rpc tier1 evidence blocking gate must be make rpc-boundary-check")
require(policy.get("releaseGate") == "make bench-evidence-check", "rpc tier1 evidence release gate must be make bench-evidence-check")
require(policy.get("budgetRatchet") == "bench/budget-ratchet.json", "rpc tier1 evidence must reference bench/budget-ratchet.json")
require("report-only" in policy.get("latencyPolicy", ""), "rpc latency policy must remain report-only until promoted")
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
