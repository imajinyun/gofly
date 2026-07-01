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

r8_stream_matrix = manifest.get("r8StreamingBoundaryMatrix") or {}
require(
    r8_stream_matrix.get("schema") == "gofly.rpc_streaming_boundary_matrix.v1",
    "rpc tier1 evidence r8StreamingBoundaryMatrix schema mismatch",
)
require(
    r8_stream_matrix.get("aiflowTask") == "GOFLY-GOV-10R8-04",
    "rpc tier1 evidence r8StreamingBoundaryMatrix aiflowTask mismatch",
)
require(
    r8_stream_matrix.get("status") == "blocking-contract",
    "rpc tier1 evidence r8StreamingBoundaryMatrix status must be blocking-contract",
)
require(
    r8_stream_matrix.get("acceptanceGate") == "make rpc-boundary-check",
    "rpc tier1 evidence r8StreamingBoundaryMatrix acceptanceGate mismatch",
)
require(
    r8_stream_matrix.get("transportParityClaim") == "forbidden",
    "rpc tier1 evidence r8StreamingBoundaryMatrix must forbid transport parity claims",
)
require(
    "Kitex" in str(r8_stream_matrix.get("summary") or "") and "gRPC-Go" in str(r8_stream_matrix.get("summary") or ""),
    "rpc tier1 evidence r8StreamingBoundaryMatrix summary must mention Kitex and gRPC-Go boundaries",
)
r8_rows = {
    item.get("id"): item
    for item in r8_stream_matrix.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
required_r8_rows = {
    "unary-boundary": {
        "classification": "candidate",
        "evidenceIds": {"unary-contract", "deadline-error-code-mapping", "retry-balancer-contract"},
        "needles": {"BenchmarkRPCUnary", "deadline_exceeded", "invalid_argument", "unavailable"},
    },
    "server-stream-boundary": {
        "classification": "candidate",
        "evidenceIds": {"server-stream-governance", "grpc-compatibility"},
        "needles": {"BenchmarkRPCServerStreamGovernance", "server_stream", "Unauthenticated", "traceparent"},
    },
    "client-stream-boundary": {
        "classification": "candidate",
        "evidenceIds": {"client-stream-governance", "grpc-compatibility"},
        "needles": {"BenchmarkRPCClientStreamGovernance", "client_stream", "Unauthenticated", "traceparent"},
    },
    "bidi-stream-boundary": {
        "classification": "candidate",
        "evidenceIds": {"bidi-stream-governance", "grpc-compatibility"},
        "needles": {"BenchmarkRPCBidiStreamGovernance", "bidi_stream", "Unauthenticated", "traceparent"},
    },
    "resolver-balancer-boundary": {
        "classification": "candidate",
        "evidenceIds": {"resolver-updates", "balancer-routing", "retry-balancer-contract"},
        "needles": {"round_robin", "weighted_round_robin", "p2c", "consistent_hash", "health_aware"},
    },
    "deadline-retry-auth-tracing-boundary": {
        "classification": "tier1-ready-evidence",
        "evidenceIds": {"deadline-error-code-mapping", "retry-balancer-contract", "grpc-compatibility"},
        "needles": {"deadline_exceeded", "codes.Unavailable", "Unauthenticated", "SERVING", "traceparent"},
    },
    "kitex-gozero-rollback-boundary": {
        "classification": "rollback-required",
        "evidenceIds": {"kitex-coexistence-rollback", "gozero-coexistence-rollback"},
        "needles": {"Kitex", "go-zero", "generated service", "rollback routing"},
    },
}
require(set(r8_rows) == set(required_r8_rows), f"rpc tier1 evidence r8StreamingBoundaryMatrix rows mismatch: {sorted(r8_rows)!r}")
for row_id, expected in required_r8_rows.items():
    row = r8_rows.get(row_id) or {}
    require(row.get("classification") == expected["classification"], f"rpc tier1 evidence R8 row {row_id}: classification mismatch")
    require(expected["evidenceIds"] <= set(row.get("evidenceIds") or []), f"rpc tier1 evidence R8 row {row_id}: evidenceIds missing {sorted(expected['evidenceIds'] - set(row.get('evidenceIds') or []))!r}")
    require(row.get("gate"), f"rpc tier1 evidence R8 row {row_id}: gate is required")
    require(row.get("surface"), f"rpc tier1 evidence R8 row {row_id}: surface is required")
    for field in ("runtimeCompatibility", "rollbackTrigger"):
        require(
            len(str(row.get(field) or "").split()) >= 8,
            f"rpc tier1 evidence R8 row {row_id}: {field} must be actionable",
        )
    report_evidence = set(row.get("reportEvidence") or [])
    require(expected["needles"] <= report_evidence, f"rpc tier1 evidence R8 row {row_id}: reportEvidence missing {sorted(expected['needles'] - report_evidence)!r}")
    require(
        any(name in row.get("rollbackTrigger", "") for name in ("Kitex", "gRPC-Go", "go-zero", "service mesh", "RPC stack")),
        f"rpc tier1 evidence R8 row {row_id}: rollbackTrigger must name the fallback runtime or routing stack",
    )

p9_closeout = manifest.get("p9ReleaseTrainCloseout") or {}
require(
    p9_closeout.get("schema") == "gofly.rpc_tier1_release_train_closeout.v1",
    "rpc tier1 evidence p9ReleaseTrainCloseout schema mismatch",
)
require(
    p9_closeout.get("aiflowTask") == "GOFLY-GOV-10P9-01",
    "rpc tier1 evidence p9ReleaseTrainCloseout aiflowTask mismatch",
)
require(
    p9_closeout.get("status") == "blocked-with-release-train-evidence",
    "rpc tier1 evidence p9ReleaseTrainCloseout status mismatch",
)
require(
    set(p9_closeout.get("acceptanceGates") or []) == {"make rpc-boundary-check", "make bench-regression-check"},
    "rpc tier1 evidence p9ReleaseTrainCloseout acceptanceGates mismatch",
)
p9_decision = p9_closeout.get("promotionDecision") or {}
require(p9_decision.get("currentTier") == "tier2", "P9 RPC promotionDecision.currentTier must be tier2")
require(p9_decision.get("targetTier") == "tier1", "P9 RPC promotionDecision.targetTier must be tier1")
require(p9_decision.get("decision") == "do-not-promote", "P9 RPC promotionDecision.decision must remain do-not-promote")
for field in ("reason", "latencyPolicy", "nextEligibleDecision"):
    require(
        len(str(p9_decision.get(field) or "").split()) >= 12,
        f"P9 RPC promotionDecision.{field} must be actionable",
    )
require("report-only" in str(p9_decision.get("latencyPolicy") or ""), "P9 RPC latencyPolicy must preserve report-only latency")
require("Kitex" in str(p9_decision.get("latencyPolicy") or ""), "P9 RPC latencyPolicy must mention Kitex boundary")

p9_requirements = {
    item.get("id"): item
    for item in p9_closeout.get("releaseTrainRequirements") or []
    if isinstance(item, dict) and item.get("id")
}
required_p9_requirements = {
    "release-evidence-chain": "pending",
    "rpc-boundary-contract": "satisfied-for-candidate",
    "rpc-benchmark-budget": "report-only",
}
require(set(p9_requirements) == set(required_p9_requirements), f"P9 RPC releaseTrainRequirements mismatch: {sorted(p9_requirements)!r}")
for req_id, status in required_p9_requirements.items():
    req = p9_requirements.get(req_id) or {}
    require(req.get("status") == status, f"P9 RPC requirement {req_id}: status mismatch")
    require(req.get("gate"), f"P9 RPC requirement {req_id}: gate is required")
    require(len(str(req.get("promotionUse") or "").split()) >= 10, f"P9 RPC requirement {req_id}: promotionUse must be actionable")
    for evidence_path in req.get("requiredEvidence") or []:
        require((root / evidence_path).exists(), f"P9 RPC requirement {req_id}: evidence path missing: {evidence_path}")

p9_surfaces = {
    item.get("id"): item
    for item in p9_closeout.get("surfaceCloseout") or []
    if isinstance(item, dict) and item.get("id")
}
required_p9_surfaces = {
    "unary": "candidate",
    "server-stream": "candidate",
    "client-stream": "candidate",
    "bidi-stream": "candidate",
    "resolver-balancer": "candidate",
    "grpc-compatibility": "tier1-ready-evidence",
    "kitex-gozero-coexistence": "rollback-required",
}
require(set(p9_surfaces) == set(required_p9_surfaces), f"P9 RPC surfaceCloseout mismatch: {sorted(p9_surfaces)!r}")
for surface_id, classification in required_p9_surfaces.items():
    surface = p9_surfaces.get(surface_id) or {}
    require(surface.get("classification") == classification, f"P9 RPC surface {surface_id}: classification mismatch")
    require(surface.get("releaseTrainStatus"), f"P9 RPC surface {surface_id}: releaseTrainStatus is required")
    require(set(surface.get("evidenceIds") or []), f"P9 RPC surface {surface_id}: evidenceIds are required")
    for field in ("blockingGap", "rollbackAction"):
        require(
            len(str(surface.get(field) or "").split()) >= 10,
            f"P9 RPC surface {surface_id}: {field} must be actionable",
        )
    require(
        any(runtime in str(surface.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "go-zero", "RPC stack", "service mesh")),
        f"P9 RPC surface {surface_id}: rollbackAction must name fallback runtime or routing stack",
    )

p10_closeout = manifest.get("p10Tier1Closeout") or {}
require(
    p10_closeout.get("schema") == "gofly.rpc_tier1_p10_closeout.v1",
    "rpc tier1 evidence p10Tier1Closeout schema mismatch",
)
require(
    p10_closeout.get("aiflowTask") == "GOFLY-P10-1-RPC-TIER1-CLOSEOUT",
    "rpc tier1 evidence p10Tier1Closeout aiflowTask mismatch",
)
require(
    p10_closeout.get("status") == "tier1-not-promoted",
    "rpc tier1 evidence p10Tier1Closeout status must be tier1-not-promoted",
)
require(
    p10_closeout.get("acceptanceGate") == "make rpc-boundary-check",
    "rpc tier1 evidence p10Tier1Closeout acceptanceGate mismatch",
)
p10_decision = p10_closeout.get("decision") or {}
require(p10_decision.get("currentTier") == "tier2", "P10 RPC decision.currentTier must be tier2")
require(p10_decision.get("targetTier") == "tier1", "P10 RPC decision.targetTier must be tier1")
require(p10_decision.get("result") == "hold", "P10 RPC decision.result must be hold")
for field in ("reason", "releaseNotePolicy"):
    require(len(str(p10_decision.get(field) or "").split()) >= 12, f"P10 RPC decision.{field} must be actionable")
require("Kitex" in str(p10_decision.get("releaseNotePolicy") or ""), "P10 RPC releaseNotePolicy must mention Kitex boundary")
require("gRPC-Go" in str(p10_decision.get("releaseNotePolicy") or ""), "P10 RPC releaseNotePolicy must mention gRPC-Go boundary")
require(
    p10_decision.get("nextReviewGate") == "make rpc-boundary-check && make bench-regression-check",
    "P10 RPC nextReviewGate mismatch",
)
p10_rows = {
    item.get("id"): item
    for item in p10_closeout.get("closeoutRows") or []
    if isinstance(item, dict) and item.get("id")
}
required_p10_rows = {
    "grpc-compatibility": "ready-evidence",
    "streaming-behavior": "candidate",
    "deadline-error-code-mapping": "ready-evidence",
    "retry-balancer-contract": "candidate",
    "kitex-gozero-coexistence": "rollback-required",
}
require(set(p10_rows) == set(required_p10_rows), f"P10 RPC closeoutRows mismatch: {sorted(p10_rows)!r}")
for row_id, status in required_p10_rows.items():
    row = p10_rows.get(row_id) or {}
    require(row.get("status") == status, f"P10 RPC closeout row {row_id}: status mismatch")
    require(row.get("requiredGate"), f"P10 RPC closeout row {row_id}: requiredGate is required")
    require(set(row.get("evidenceIds") or []), f"P10 RPC closeout row {row_id}: evidenceIds are required")
    for field in ("promotionGap", "rollbackAction"):
        require(len(str(row.get(field) or "").split()) >= 10, f"P10 RPC closeout row {row_id}: {field} must be actionable")
    require(
        any(runtime in str(row.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "go-zero", "service mesh", "client-side routing")),
        f"P10 RPC closeout row {row_id}: rollbackAction must name fallback runtime or routing stack",
    )
p10_prereqs = {
    item.get("id"): item
    for item in p10_closeout.get("promotionPrerequisites") or []
    if isinstance(item, dict) and item.get("id")
}
required_p10_prereqs = {
    "release-train-attached": "pending",
    "rpc-budget-promoted": "report-only",
    "transport-parity-forbidden": "blocking",
}
require(set(p10_prereqs) == set(required_p10_prereqs), f"P10 RPC promotionPrerequisites mismatch: {sorted(p10_prereqs)!r}")
for prereq_id, status in required_p10_prereqs.items():
    prereq = p10_prereqs.get(prereq_id) or {}
    require(prereq.get("status") == status, f"P10 RPC prerequisite {prereq_id}: status mismatch")
    require(prereq.get("gate"), f"P10 RPC prerequisite {prereq_id}: gate is required")
    for evidence_path in prereq.get("requiredEvidence") or []:
        require((root / evidence_path).exists(), f"P10 RPC prerequisite {prereq_id}: evidence path missing: {evidence_path}")

p11_review = manifest.get("p11PromotionReview") or {}
require(
    p11_review.get("schema") == "gofly.rpc_tier1_p11_promotion_review.v1",
    "rpc tier1 evidence p11PromotionReview schema mismatch",
)
require(
    p11_review.get("aiflowTask") == "GOFLY-P11-1-RPC-TIER1-PROMOTION-REVIEW",
    "rpc tier1 evidence p11PromotionReview aiflowTask mismatch",
)
require(
    p11_review.get("status") == "review-complete-promotion-held",
    "rpc tier1 evidence p11PromotionReview status must be review-complete-promotion-held",
)
require(
    set(p11_review.get("acceptanceGates") or []) == {"make rpc-boundary-check", "make bench-regression-check"},
    "rpc tier1 evidence p11PromotionReview acceptanceGates mismatch",
)
p11_decision = p11_review.get("decision") or {}
require(p11_decision.get("currentTier") == "tier2", "P11 RPC decision.currentTier must be tier2")
require(p11_decision.get("targetTier") == "tier1", "P11 RPC decision.targetTier must be tier1")
require(p11_decision.get("result") == "hold", "P11 RPC decision.result must be hold")
require(
    p11_decision.get("nextReviewGate") == "make rpc-boundary-check && make bench-regression-check",
    "P11 RPC nextReviewGate mismatch",
)
for field in ("reason", "releaseNotePolicy"):
    require(len(str(p11_decision.get(field) or "").split()) >= 14, f"P11 RPC decision.{field} must be actionable")
for forbidden_claim in ("Kitex", "gRPC-Go", "Netpoll", "TTHeader", "Thrift"):
    require(
        forbidden_claim in str(p11_decision.get("releaseNotePolicy") or ""),
        f"P11 RPC releaseNotePolicy must mention {forbidden_claim} boundary",
    )
p11_rows = {
    item.get("id"): item
    for item in p11_review.get("reviewRows") or []
    if isinstance(item, dict) and item.get("id")
}
required_p11_rows = {
    "unary": "candidate",
    "server-stream": "candidate",
    "client-stream": "candidate",
    "bidi-stream": "candidate",
    "resolver-balancer": "candidate",
    "grpc-compatibility": "tier1-ready-evidence",
    "kitex-gozero-coexistence": "rollback-required",
}
require(set(p11_rows) == set(required_p11_rows), f"P11 RPC reviewRows mismatch: {sorted(p11_rows)!r}")
for row_id, classification in required_p11_rows.items():
    row = p11_rows.get(row_id) or {}
    require(row.get("classification") == classification, f"P11 RPC review row {row_id}: classification mismatch")
    require(set(row.get("requiredEvidenceIds") or []), f"P11 RPC review row {row_id}: requiredEvidenceIds are required")
    for field in ("promotionFinding", "requiredAction", "rollbackAction"):
        require(len(str(row.get(field) or "").split()) >= 10, f"P11 RPC review row {row_id}: {field} must be actionable")
    require(
        any(runtime in str(row.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "go-zero", "RPC stack", "service mesh", "client balancer")),
        f"P11 RPC review row {row_id}: rollbackAction must name fallback runtime or routing stack",
    )
p11_blockers = {
    item.get("id"): item
    for item in p11_review.get("promotionBlockers") or []
    if isinstance(item, dict) and item.get("id")
}
required_p11_blockers = {
    "release-train-attached": "pending",
    "rpc-budget-promoted": "report-only",
    "transport-parity-forbidden": "blocking",
}
require(set(p11_blockers) == set(required_p11_blockers), f"P11 RPC promotionBlockers mismatch: {sorted(p11_blockers)!r}")
for blocker_id, status in required_p11_blockers.items():
    blocker = p11_blockers.get(blocker_id) or {}
    require(blocker.get("status") == status, f"P11 RPC blocker {blocker_id}: status mismatch")
    require(blocker.get("requiredGate"), f"P11 RPC blocker {blocker_id}: requiredGate is required")
    require(len(str(blocker.get("clearanceCondition") or "").split()) >= 10, f"P11 RPC blocker {blocker_id}: clearanceCondition must be actionable")
    for evidence_path in blocker.get("requiredEvidence") or []:
        require((root / evidence_path).exists(), f"P11 RPC blocker {blocker_id}: evidence path missing: {evidence_path}")

p12_budget = manifest.get("p12BenchmarkBudgetPromotion") or {}
require(
    p12_budget.get("schema") == "gofly.rpc_tier1_p12_benchmark_budget_promotion.v1",
    "rpc tier1 evidence p12BenchmarkBudgetPromotion schema mismatch",
)
require(
    p12_budget.get("aiflowTask") == "GOFLY-P12-1-RPC-BENCHMARK-BUDGET-PROMOTION",
    "rpc tier1 evidence p12BenchmarkBudgetPromotion aiflowTask mismatch",
)
require(
    p12_budget.get("status") == "promotion-held-report-only",
    "rpc tier1 evidence p12BenchmarkBudgetPromotion status must be promotion-held-report-only",
)
require(
    set(p12_budget.get("acceptanceGates") or []) == {"make rpc-boundary-check", "make bench-regression-check"},
    "rpc tier1 evidence p12BenchmarkBudgetPromotion acceptanceGates mismatch",
)
p12_ref = p12_budget.get("budgetDecisionRef") or {}
require(p12_ref.get("path") == "bench/budget-ratchet.json", "P12 RPC budgetDecisionRef path mismatch")
require(p12_ref.get("field") == "p12RpcBudgetPromotionDecision", "P12 RPC budgetDecisionRef field mismatch")
require(p12_ref.get("status") == "promotion-held-report-only", "P12 RPC budgetDecisionRef status mismatch")
p12_decision = p12_budget.get("decision") or {}
require(p12_decision.get("result") == "hold", "P12 RPC budget decision.result must be hold")
require(
    p12_decision.get("nextReviewGate") == "make bench-regression-check && make rpc-boundary-check",
    "P12 RPC budget nextReviewGate mismatch",
)
for field in ("reason", "releaseNotePolicy"):
    require(len(str(p12_decision.get(field) or "").split()) >= 16, f"P12 RPC budget decision.{field} must be actionable")
for forbidden_claim in ("Tier 1 replacement", "blocking RPC latency", "Kitex", "gRPC-Go"):
    require(
        forbidden_claim in str(p12_decision.get("releaseNotePolicy") or ""),
        f"P12 RPC budget releaseNotePolicy must mention {forbidden_claim!r}",
    )
p12_path = [
    item.get("step")
    for item in p12_budget.get("promotionPath") or []
    if isinstance(item, dict) and item.get("step")
]
require(
    p12_path == ["select-one-rpc-surface", "attach-benchmark-trend", "attach-release-train"],
    f"P12 RPC budget promotionPath mismatch: {p12_path!r}",
)
for item in p12_budget.get("promotionPath") or []:
    if not isinstance(item, dict):
        missing.append(f"P12 RPC budget promotionPath item must be an object: {item!r}")
        continue
    step = item.get("step", "<missing>")
    require(item.get("gate"), f"P12 RPC budget promotionPath {step}: gate is required")
    for field in ("requiredEvidence", "rollbackAction"):
        require(len(str(item.get(field) or "").split()) >= 10, f"P12 RPC budget promotionPath {step}: {field} must be actionable")
p12_surfaces = {
    item.get("benchmark"): item
    for item in p12_budget.get("candidateSurfaces") or []
    if isinstance(item, dict) and item.get("benchmark")
}
expected_p12_surfaces = {
    "BenchmarkRPCUnary/gofly_rpc",
    "BenchmarkRPCStreamGovernance",
    "BenchmarkRPCServerStreamGovernance",
    "BenchmarkRPCClientStreamGovernance",
    "BenchmarkRPCBidiStreamGovernance",
}
require(set(p12_surfaces) == expected_p12_surfaces, f"P12 RPC budget candidateSurfaces mismatch: {sorted(p12_surfaces)!r}")
for benchmark, item in p12_surfaces.items():
    require(item.get("currentMode") == "report-only", f"{benchmark}: P12 currentMode must be report-only")
    require(item.get("promotionStatus") == "blocked", f"{benchmark}: P12 promotionStatus must be blocked")
    require(
        any(runtime in str(item.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "RPC stack")),
        f"{benchmark}: P12 rollbackAction must name fallback runtime or previous RPC stack",
    )

p13_closeout = manifest.get("p13Tier1ReleaseTrainCloseout") or {}
require(
    p13_closeout.get("schema") == "gofly.rpc_tier1_p13_release_train_closeout.v1",
    "rpc tier1 evidence p13Tier1ReleaseTrainCloseout schema mismatch",
)
require(
    p13_closeout.get("aiflowTask") == "GOFLY-P13-01-RPC-TIER1-PROMOTION-CLOSEOUT",
    "rpc tier1 evidence p13Tier1ReleaseTrainCloseout aiflowTask mismatch",
)
require(
    p13_closeout.get("status") == "blocked-with-stable-promotion-contract",
    "rpc tier1 evidence p13Tier1ReleaseTrainCloseout status mismatch",
)
require(
    set(p13_closeout.get("acceptanceGates") or []) == {"make rpc-boundary-check", "make bench-regression-check"},
    "rpc tier1 evidence p13Tier1ReleaseTrainCloseout acceptanceGates mismatch",
)
p13_ref = p13_closeout.get("budgetDecisionRef") or {}
require(p13_ref.get("path") == "bench/budget-ratchet.json", "P13 RPC budgetDecisionRef path mismatch")
require(p13_ref.get("field") == "p13RpcTier1ReleaseTrainCloseout", "P13 RPC budgetDecisionRef field mismatch")
require(p13_ref.get("status") == "no-surface-promoted", "P13 RPC budgetDecisionRef status mismatch")
p13_release_train = p13_closeout.get("releaseTrainEvidence") or {}
require(p13_release_train.get("requiredCompletedReleaseTrains") == 1, "P13 RPC releaseTrainEvidence requiredCompletedReleaseTrains mismatch")
require(p13_release_train.get("completedReleaseTrains") == 0, "P13 RPC releaseTrainEvidence completedReleaseTrains must remain 0")
require(p13_release_train.get("status") == "pending", "P13 RPC releaseTrainEvidence status must be pending")
for ref_path in p13_release_train.get("requiredRefs") or []:
    require((root / ref_path).exists(), f"P13 RPC releaseTrainEvidence required ref missing: {ref_path}")
require(len(str(p13_release_train.get("clearanceCondition") or "").split()) >= 16, "P13 RPC releaseTrainEvidence clearanceCondition must be actionable")
p13_decision = p13_closeout.get("promotionDecision") or {}
require(p13_decision.get("result") == "hold", "P13 RPC promotionDecision.result must be hold")
require(p13_decision.get("selectedSurface") == "none", "P13 RPC promotionDecision.selectedSurface must be none")
require(p13_decision.get("allocationBlockingSurface") == "none", "P13 RPC promotionDecision.allocationBlockingSurface must be none")
require(p13_decision.get("latencyMode") == "report-only", "P13 RPC promotionDecision.latencyMode must be report-only")
require(
    p13_decision.get("nextReviewGate") == "make rpc-boundary-check && make bench-regression-check",
    "P13 RPC promotionDecision nextReviewGate mismatch",
)
for field in ("reason", "releaseNotePolicy"):
    require(len(str(p13_decision.get(field) or "").split()) >= 16, f"P13 RPC promotionDecision.{field} must be actionable")
for forbidden_claim in ("Kitex", "gRPC-Go", "blocking RPC latency", "drop-in replacement"):
    require(
        forbidden_claim in str(p13_decision.get("releaseNotePolicy") or ""),
        f"P13 RPC releaseNotePolicy must mention {forbidden_claim!r}",
    )
criteria = set(p13_closeout.get("stableSurfaceCriteria") or [])
for criterion in (
    "exactly one RPC surface may be selected for allocation-blocking promotion at a time",
    "selected RPC surface must have minimum 5 baseline samples",
    "selected RPC surface must have minimum 3 current trend samples",
    "selected RPC surface must pass bench-regression-check with no allocation regression",
):
    require(criterion in criteria, f"P13 RPC stableSurfaceCriteria missing {criterion!r}")
p13_ratchet_path = root / "bench" / "budget-ratchet.json"
if p13_ratchet_path.is_file():
    p13_ratchet = json.loads(p13_ratchet_path.read_text(encoding="utf-8"))
else:
    p13_ratchet = {}
    missing.append("bench/budget-ratchet.json: file is missing")
p13_tracked_benchmarks = set(p13_ratchet.get("trackedBenchmarks") or [])
p13_matrix = {
    item.get("surface"): item
    for item in p13_closeout.get("surfaceMatrix") or []
    if isinstance(item, dict) and item.get("surface")
}
required_p13_surface_status = {
    "rpc-unary": "candidate",
    "rpc-server-stream": "candidate",
    "rpc-client-stream": "candidate",
    "rpc-bidi-stream": "candidate",
    "resolver-balancer": "candidate",
    "deadline-retry-auth-tracing": "tier1-ready-evidence",
}
require(set(p13_matrix) == set(required_p13_surface_status), f"P13 RPC surfaceMatrix mismatch: {sorted(p13_matrix)!r}")
for surface, classification in required_p13_surface_status.items():
    row = p13_matrix.get(surface) or {}
    require(row.get("classification") == classification, f"P13 RPC {surface}: classification mismatch")
    require(row.get("promotionStatus"), f"P13 RPC {surface}: promotionStatus is required")
    if surface.startswith("rpc-"):
        require(row.get("allocationMode") == "report-only", f"P13 RPC {surface}: allocationMode must be report-only")
        require(row.get("latencyMode") == "report-only", f"P13 RPC {surface}: latencyMode must be report-only")
        require(row.get("benchmark") not in p13_tracked_benchmarks, f"P13 RPC {surface}: benchmark must stay out of trackedBenchmarks")
    require(len(row.get("blockers") or []) >= 3, f"P13 RPC {surface}: blockers must include at least three reasons")
    require(
        any(runtime in str(row.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "RPC stack", "service mesh", "client balancer")),
        f"P13 RPC {surface}: rollbackAction must name fallback runtime or routing stack",
    )
for forbidden in ("Kitex transport parity", "gRPC-Go ecosystem parity", "blocking RPC latency", "drop-in RPC replacement", "Tier 1 promoted RPC surface"):
    require(forbidden in set(p13_closeout.get("forbiddenClaims") or []), f"P13 RPC forbiddenClaims missing {forbidden!r}")

p14_review = manifest.get("p14ReleaseTrainEvidenceReview") or {}
require(
    p14_review.get("schema") == "gofly.rpc_tier1_p14_release_train_review.v1",
    "rpc tier1 evidence p14ReleaseTrainEvidenceReview schema mismatch",
)
require(
    p14_review.get("aiflowTask") == "GOFLY-P14-02-RPC-RELEASE-TRAIN-EVIDENCE",
    "rpc tier1 evidence p14ReleaseTrainEvidenceReview aiflowTask mismatch",
)
require(
    p14_review.get("status") == "blocked-no-surface-promoted",
    "rpc tier1 evidence p14ReleaseTrainEvidenceReview status mismatch",
)
require(
    set(p14_review.get("acceptanceGates") or []) == {"make rpc-boundary-check", "make bench-regression-check"},
    "rpc tier1 evidence p14ReleaseTrainEvidenceReview acceptanceGates mismatch",
)
p14_previous = p14_review.get("previousCloseoutRef") or {}
require(p14_previous.get("field") == "p13Tier1ReleaseTrainCloseout", "P14 RPC previousCloseoutRef field mismatch")
require(p14_previous.get("status") == "blocked-with-stable-promotion-contract", "P14 RPC previousCloseoutRef status mismatch")
p14_budget_ref = p14_review.get("budgetDecisionRef") or {}
require(p14_budget_ref.get("path") == "bench/budget-ratchet.json", "P14 RPC budgetDecisionRef path mismatch")
require(p14_budget_ref.get("field") == "p14RpcReleaseTrainEvidenceReview", "P14 RPC budgetDecisionRef field mismatch")
require(p14_budget_ref.get("status") == "hold-no-tracked-rpc-benchmark", "P14 RPC budgetDecisionRef status mismatch")
p14_release_train = p14_review.get("releaseTrainEvidence") or {}
require(p14_release_train.get("requiredCompletedReleaseTrains") == 1, "P14 RPC releaseTrainEvidence requiredCompletedReleaseTrains mismatch")
require(p14_release_train.get("completedReleaseTrains") == 0, "P14 RPC releaseTrainEvidence completedReleaseTrains must remain 0")
require(p14_release_train.get("status") == "not-attached", "P14 RPC releaseTrainEvidence status must be not-attached")
require(len(p14_release_train.get("blockingEvidence") or []) >= 5, "P14 RPC releaseTrainEvidence blockingEvidence must include at least five rows")
require(len(str(p14_release_train.get("clearanceCondition") or "").split()) >= 18, "P14 RPC releaseTrainEvidence clearanceCondition must be actionable")
p14_decision = p14_review.get("promotionDecision") or {}
require(p14_decision.get("result") == "hold", "P14 RPC promotionDecision.result must be hold")
require(p14_decision.get("selectedSurface") == "none", "P14 RPC promotionDecision.selectedSurface must be none")
require(p14_decision.get("allocationBlockingSurface") == "none", "P14 RPC promotionDecision.allocationBlockingSurface must be none")
require(p14_decision.get("latencyMode") == "report-only", "P14 RPC promotionDecision.latencyMode must be report-only")
require(
    p14_decision.get("nextReviewGate") == "make rpc-boundary-check && make bench-regression-check",
    "P14 RPC promotionDecision nextReviewGate mismatch",
)
for field in ("reason", "releaseNotePolicy"):
    require(len(str(p14_decision.get(field) or "").split()) >= 18, f"P14 RPC promotionDecision.{field} must be actionable")
for forbidden_claim in ("Kitex", "gRPC-Go", "blocking RPC latency", "drop-in RPC replacement", "Tier 1 promoted RPC"):
    require(
        forbidden_claim in str(p14_decision.get("releaseNotePolicy") or ""),
        f"P14 RPC releaseNotePolicy must mention {forbidden_claim!r}",
    )
p14_rows = {
    item.get("surface"): item
    for item in p14_review.get("reviewRows") or []
    if isinstance(item, dict) and item.get("surface")
}
required_p14_surfaces = {
    "rpc-unary",
    "rpc-server-stream",
    "rpc-client-stream",
    "rpc-bidi-stream",
}
require(set(p14_rows) == required_p14_surfaces, f"P14 RPC reviewRows mismatch: {sorted(p14_rows)!r}")
for surface, row in p14_rows.items():
    require(row.get("releaseTrainStatus") == "blocked", f"P14 RPC {surface}: releaseTrainStatus must be blocked")
    require(row.get("allocationMode") == "report-only", f"P14 RPC {surface}: allocationMode must be report-only")
    require(row.get("latencyMode") == "report-only", f"P14 RPC {surface}: latencyMode must be report-only")
    require(row.get("benchmark") not in p13_tracked_benchmarks, f"P14 RPC {surface}: benchmark must stay out of trackedBenchmarks")
    require(len(row.get("blockingEvidenceMissing") or []) >= 3, f"P14 RPC {surface}: blockingEvidenceMissing must include at least three rows")
    require(
        any(runtime in str(row.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "RPC stack")),
        f"P14 RPC {surface}: rollbackAction must name fallback runtime or previous RPC stack",
    )
for forbidden in ("Kitex transport parity", "gRPC-Go ecosystem parity", "blocking RPC latency", "drop-in RPC replacement", "Tier 1 promoted RPC surface"):
    require(forbidden in set(p14_review.get("forbiddenClaims") or []), f"P14 RPC forbiddenClaims missing {forbidden!r}")

p15_attachment = manifest.get("p15ReleaseTrainAttachmentReview") or {}
require(
    p15_attachment.get("schema") == "gofly.rpc_tier1_p15_release_train_attachment.v1",
    "rpc tier1 evidence p15ReleaseTrainAttachmentReview schema mismatch",
)
require(
    p15_attachment.get("aiflowTask") == "GOFLY-P15-02-RPC-RELEASE-TRAIN-ATTACHMENT",
    "rpc tier1 evidence p15ReleaseTrainAttachmentReview aiflowTask mismatch",
)
require(
    p15_attachment.get("status") == "attachment-held-no-release-train",
    "rpc tier1 evidence p15ReleaseTrainAttachmentReview status mismatch",
)
require(
    set(p15_attachment.get("acceptanceGates") or []) == {"make rpc-boundary-check", "make bench-regression-check"},
    "rpc tier1 evidence p15ReleaseTrainAttachmentReview acceptanceGates mismatch",
)
p15_previous = p15_attachment.get("previousReviewRef") or {}
require(p15_previous.get("field") == "p14ReleaseTrainEvidenceReview", "P15 RPC previousReviewRef field mismatch")
require(p15_previous.get("status") == "blocked-no-surface-promoted", "P15 RPC previousReviewRef status mismatch")
p15_budget_ref = p15_attachment.get("budgetDecisionRef") or {}
require(p15_budget_ref.get("path") == "bench/budget-ratchet.json", "P15 RPC budgetDecisionRef path mismatch")
require(p15_budget_ref.get("field") == "p15RpcReleaseTrainAttachment", "P15 RPC budgetDecisionRef field mismatch")
require(p15_budget_ref.get("status") == "hold-no-rpc-budget-attachment", "P15 RPC budgetDecisionRef status mismatch")
p15_decision = p15_attachment.get("attachmentDecision") or {}
require(p15_decision.get("result") == "hold", "P15 RPC attachmentDecision.result must be hold")
require(p15_decision.get("selectedSurface") == "none", "P15 RPC attachmentDecision.selectedSurface must be none")
require(p15_decision.get("allocationBlockingSurface") == "none", "P15 RPC attachmentDecision.allocationBlockingSurface must be none")
require(p15_decision.get("latencyMode") == "report-only", "P15 RPC attachmentDecision.latencyMode must be report-only")
require(p15_decision.get("releaseTrainAttachmentStatus") == "not-attached", "P15 RPC releaseTrainAttachmentStatus must be not-attached")
require(
    p15_decision.get("nextReviewGate") == "make rpc-boundary-check && make bench-regression-check",
    "P15 RPC attachmentDecision nextReviewGate mismatch",
)
for field in ("reason", "releaseNotePolicy"):
    require(len(str(p15_decision.get(field) or "").split()) >= 20, f"P15 RPC attachmentDecision.{field} must be actionable")
for forbidden_claim in ("Kitex", "gRPC-Go", "blocking RPC latency", "drop-in replacement", "Tier 1 promoted"):
    require(
        forbidden_claim in str(p15_decision.get("releaseNotePolicy") or ""),
        f"P15 RPC releaseNotePolicy must mention {forbidden_claim!r}",
    )
p15_diff = p15_attachment.get("blockerDiffFromP14") or {}
require(len(p15_diff.get("resolvedEvidence") or []) >= 3, "P15 RPC blockerDiffFromP14.resolvedEvidence must include at least three rows")
require(len(p15_diff.get("remainingBlockers") or []) >= 5, "P15 RPC blockerDiffFromP14.remainingBlockers must include at least five rows")
require(len(str(p15_diff.get("narrowingPolicy") or "").split()) >= 14, "P15 RPC blockerDiffFromP14.narrowingPolicy must be actionable")
p15_rows = {
    item.get("surface"): item
    for item in p15_attachment.get("surfaceAttachments") or []
    if isinstance(item, dict) and item.get("surface")
}
required_p15_surfaces = {
    "rpc-unary": "candidate",
    "rpc-server-stream": "candidate",
    "rpc-client-stream": "candidate",
    "rpc-bidi-stream": "candidate",
    "resolver-balancer": "candidate",
    "grpc-compatibility": "tier1-ready-evidence",
}
require(set(p15_rows) == set(required_p15_surfaces), f"P15 RPC surfaceAttachments mismatch: {sorted(p15_rows)!r}")
for surface, classification in required_p15_surfaces.items():
    row = p15_rows.get(surface) or {}
    require(row.get("classification") == classification, f"P15 RPC {surface}: classification mismatch")
    require(row.get("releaseTrainStatus") in {"not-attached", "candidate-ready", "ready-evidence-release-blocked"}, f"P15 RPC {surface}: releaseTrainStatus mismatch")
    require(row.get("allocationMode") in {"report-only", "not-applicable"}, f"P15 RPC {surface}: allocationMode mismatch")
    require(row.get("latencyMode") in {"report-only", "not-applicable"}, f"P15 RPC {surface}: latencyMode mismatch")
    require(row.get("budgetAttachment") in {"blocked", "not-applicable"}, f"P15 RPC {surface}: budgetAttachment mismatch")
    require(len(row.get("blockingEvidenceMissing") or []) >= 3, f"P15 RPC {surface}: blockingEvidenceMissing must include at least three rows")
    if surface.startswith("rpc-"):
        require(row.get("releaseTrainStatus") == "not-attached", f"P15 RPC {surface}: releaseTrainStatus must be not-attached")
        require(row.get("allocationMode") == "report-only", f"P15 RPC {surface}: allocationMode must be report-only")
        require(row.get("latencyMode") == "report-only", f"P15 RPC {surface}: latencyMode must be report-only")
        require(row.get("budgetAttachment") == "blocked", f"P15 RPC {surface}: budgetAttachment must be blocked")
        require(row.get("benchmark") not in p13_tracked_benchmarks, f"P15 RPC {surface}: benchmark must stay out of trackedBenchmarks")
    require(
        any(runtime in str(row.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "RPC stack", "service mesh", "client balancer")),
        f"P15 RPC {surface}: rollbackAction must name fallback runtime or routing stack",
    )
p15_rules = set(p15_attachment.get("attachmentRules") or [])
for rule in (
    "P15 must not add RPC rows to trackedBenchmarks until release-train evidence is attached",
    "P15 may attach at most one allocation-blocking RPC surface per release train",
    "attached RPC rows require minimum 5 baseline samples",
    "attached RPC rows require minimum 3 current trend samples",
    "attached RPC rows require no allocation regression under bench-regression-check",
):
    require(rule in p15_rules, f"P15 RPC attachmentRules missing {rule!r}")
for forbidden in ("trackedBenchmarks RPC entry", "blocking RPC latency claim", "Kitex transport parity claim", "gRPC-Go ecosystem parity claim", "drop-in RPC replacement claim", "Tier 1 promoted RPC surface"):
    require(forbidden in set(p15_attachment.get("forbiddenClaims") or []), f"P15 RPC forbiddenClaims missing {forbidden!r}")

r8_transport_matrix = transport_boundary.get("r8TransportEvidenceMatrix") or {}
require(
    r8_transport_matrix.get("schema") == "gofly.rpc_transport_r8_evidence_matrix.v1",
    "rpc transport boundary r8TransportEvidenceMatrix schema mismatch",
)
require(
    r8_transport_matrix.get("aiflowTask") == "GOFLY-GOV-10R8-04",
    "rpc transport boundary r8TransportEvidenceMatrix aiflowTask mismatch",
)
require(
    r8_transport_matrix.get("status") == "blocking-contract",
    "rpc transport boundary r8TransportEvidenceMatrix status must be blocking-contract",
)
require(
    r8_transport_matrix.get("source") == "docs/reference/rpc-tier1-evidence.json",
    "rpc transport boundary r8TransportEvidenceMatrix source mismatch",
)
require(
    r8_transport_matrix.get("acceptanceGate") == "make rpc-boundary-check",
    "rpc transport boundary r8TransportEvidenceMatrix acceptanceGate mismatch",
)
r8_transport_rows = {
    item.get("id"): item
    for item in r8_transport_matrix.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
required_r8_transport_rows = {
    "streaming-modes": {
        "claimClass": "candidate",
        "supportedEvidence": {"server-stream-governance", "client-stream-governance", "bidi-stream-governance"},
        "forbiddenNeedle": "streaming transport parity",
    },
    "resolver-balancer": {
        "claimClass": "candidate",
        "supportedEvidence": {"resolver-updates", "balancer-routing", "retry-balancer-contract"},
        "forbiddenNeedle": "routing parity",
    },
    "deadline-retry-auth-tracing": {
        "claimClass": "tier1-ready-evidence",
        "supportedEvidence": {"deadline-error-code-mapping", "retry-balancer-contract", "grpc-compatibility"},
        "forbiddenNeedle": "gRPC-Go ecosystem parity",
    },
    "benchmark-budget": {
        "claimClass": "report-only",
        "supportedEvidence": {"unary-contract", "server-stream-governance", "client-stream-governance", "bidi-stream-governance"},
        "forbiddenNeedle": "blocking RPC latency",
    },
    "framework-rollback": {
        "claimClass": "rollback-required",
        "supportedEvidence": {"kitex-coexistence-rollback", "gozero-coexistence-rollback"},
        "forbiddenNeedle": "in-place migration",
    },
}
require(set(r8_transport_rows) == set(required_r8_transport_rows), f"rpc transport boundary r8TransportEvidenceMatrix rows mismatch: {sorted(r8_transport_rows)!r}")
for row_id, expected in required_r8_transport_rows.items():
    row = r8_transport_rows.get(row_id) or {}
    require(row.get("claimClass") == expected["claimClass"], f"rpc transport boundary R8 row {row_id}: claimClass mismatch")
    require(expected["supportedEvidence"] <= set(row.get("supportedEvidence") or []), f"rpc transport boundary R8 row {row_id}: supportedEvidence missing {sorted(expected['supportedEvidence'] - set(row.get('supportedEvidence') or []))!r}")
    require(expected["forbiddenNeedle"] in str(row.get("forbiddenClaim") or ""), f"rpc transport boundary R8 row {row_id}: forbiddenClaim must mention {expected['forbiddenNeedle']!r}")
    require(row.get("requiredGate"), f"rpc transport boundary R8 row {row_id}: requiredGate is required")
    require(
        len(str(row.get("rollbackPolicy") or "").split()) >= 10,
        f"rpc transport boundary R8 row {row_id}: rollbackPolicy must be actionable",
    )

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

p9_transport_closeout = transport_boundary.get("p9ReleaseTrainTransportCloseout") or {}
require(
    p9_transport_closeout.get("schema") == "gofly.rpc_transport_p9_release_train_closeout.v1",
    "rpc transport boundary p9ReleaseTrainTransportCloseout schema mismatch",
)
require(
    p9_transport_closeout.get("aiflowTask") == "GOFLY-GOV-10P9-01",
    "rpc transport boundary p9ReleaseTrainTransportCloseout aiflowTask mismatch",
)
require(
    p9_transport_closeout.get("status") == "blocked-with-explicit-promotion-path",
    "rpc transport boundary p9ReleaseTrainTransportCloseout status mismatch",
)
require(
    set(p9_transport_closeout.get("acceptanceGates") or []) == {"make rpc-boundary-check", "make bench-regression-check"},
    "rpc transport boundary p9ReleaseTrainTransportCloseout acceptanceGates mismatch",
)
p9_promotion_steps = {
    item.get("id"): item
    for item in p9_transport_closeout.get("promotionPath") or []
    if isinstance(item, dict) and item.get("id")
}
required_p9_steps = {
    "candidate-contracts-pass": "satisfied-for-candidate",
    "release-train-attached": "pending",
    "rpc-budget-promoted": "report-only",
}
require(set(p9_promotion_steps) == set(required_p9_steps), f"rpc transport boundary P9 promotionPath mismatch: {sorted(p9_promotion_steps)!r}")
for step_id, status in required_p9_steps.items():
    step = p9_promotion_steps.get(step_id) or {}
    require(step.get("status") == status, f"rpc transport boundary P9 promotion step {step_id}: status mismatch")
    require(isinstance(step.get("step"), int) and step.get("step") > 0, f"rpc transport boundary P9 promotion step {step_id}: step number is required")
    require(step.get("requiredGate"), f"rpc transport boundary P9 promotion step {step_id}: requiredGate is required")
    require(len(str(step.get("evidence") or "").split()) >= 10, f"rpc transport boundary P9 promotion step {step_id}: evidence must be actionable")

p9_fallbacks = {
    item.get("id"): item
    for item in p9_transport_closeout.get("fallbackMatrix") or []
    if isinstance(item, dict) and item.get("id")
}
required_p9_fallbacks = {
    "kitex-hot-path": "Kitex",
    "grpc-go-ecosystem": "gRPC-Go",
    "gozero-coexistence": "go-zero",
}
require(set(p9_fallbacks) == set(required_p9_fallbacks), f"rpc transport boundary P9 fallbackMatrix mismatch: {sorted(p9_fallbacks)!r}")
for fallback_id, runtime in required_p9_fallbacks.items():
    fallback = p9_fallbacks.get(fallback_id) or {}
    require(fallback.get("fallbackRuntime") == runtime, f"rpc transport boundary P9 fallback {fallback_id}: fallbackRuntime mismatch")
    for field in ("trigger", "rollbackAction"):
        require(
            len(str(fallback.get(field) or "").split()) >= 10,
            f"rpc transport boundary P9 fallback {fallback_id}: {field} must be actionable",
        )
    require(runtime in str(fallback.get("rollbackAction") or ""), f"rpc transport boundary P9 fallback {fallback_id}: rollbackAction must mention {runtime}")
for forbidden in (
    "Kitex transport parity",
    "gRPC-Go transport parity",
    "Netpoll parity",
    "TTHeader parity",
    "Thrift protocol parity",
    "blocking RPC latency budget before promotion",
):
    require(forbidden in set(p9_transport_closeout.get("forbiddenClaims") or []), f"rpc transport boundary P9 forbiddenClaims missing {forbidden!r}")
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
