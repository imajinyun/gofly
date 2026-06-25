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
