#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import subprocess
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
adoption_wave_path = root / "docs" / "reference" / "framework-gap-adoption-wave.json"
if adoption_wave_path.is_file():
    adoption_wave = json.loads(adoption_wave_path.read_text(encoding="utf-8"))
else:
    adoption_wave = {}
    missing.append("docs/reference/framework-gap-adoption-wave.json is missing")
adoption_risk_path = root / "docs" / "reference" / "adoption-risk-register.json"
if adoption_risk_path.is_file():
    adoption_risk = json.loads(adoption_risk_path.read_text(encoding="utf-8"))
else:
    adoption_risk = {}
    missing.append("docs/reference/adoption-risk-register.json is missing")
required_risk_classes = {"production-ready", "candidate", "report-only", "rollback-required"}
long_term_path = root / "docs" / "reference" / "framework-gap-long-term-adoption.json"
if long_term_path.is_file():
    long_term = json.loads(long_term_path.read_text(encoding="utf-8"))
else:
    long_term = {}
    missing.append("docs/reference/framework-gap-long-term-adoption.json is missing")
adopter_proof_path = root / "docs" / "reference" / "framework-gap-adopter-proof.json"
if adopter_proof_path.is_file():
    adopter_proof = json.loads(adopter_proof_path.read_text(encoding="utf-8"))
else:
    adopter_proof = {}
    missing.append("docs/reference/framework-gap-adopter-proof.json is missing")
post_r5_path = root / "docs" / "reference" / "framework-gap-post-r5-roadmap.json"
if post_r5_path.is_file():
    post_r5 = json.loads(post_r5_path.read_text(encoding="utf-8"))
else:
    post_r5 = {}
    missing.append("docs/reference/framework-gap-post-r5-roadmap.json is missing")
post_r6_path = root / "docs" / "reference" / "framework-gap-post-r6-roadmap.json"
if post_r6_path.is_file():
    post_r6 = json.loads(post_r6_path.read_text(encoding="utf-8"))
else:
    post_r6 = {}
    missing.append("docs/reference/framework-gap-post-r6-roadmap.json is missing")
post_r7_path = root / "docs" / "reference" / "framework-gap-post-r7-roadmap.json"
if post_r7_path.is_file():
    post_r7 = json.loads(post_r7_path.read_text(encoding="utf-8"))
else:
    post_r7 = {}
    missing.append("docs/reference/framework-gap-post-r7-roadmap.json is missing")
post_r8_path = root / "docs" / "reference" / "framework-gap-post-r8-roadmap.json"
if post_r8_path.is_file():
    post_r8 = json.loads(post_r8_path.read_text(encoding="utf-8"))
else:
    post_r8 = {}
    missing.append("docs/reference/framework-gap-post-r8-roadmap.json is missing")

require(manifest.get("schema") == "gofly.framework_gap_matrix.v1", "framework gap matrix schema mismatch")
require(next_wave.get("schema") == "gofly.framework_gap_next_wave.v1", "next-wave framework gap schema mismatch")
require(adoption_wave.get("schema") == "gofly.framework_gap_adoption_wave.v1", "adoption-wave framework gap schema mismatch")
require(adoption_risk.get("schema") == "gofly.adoption_risk_register.v1", "adoption risk register schema mismatch")
require(long_term.get("schema") == "gofly.framework_gap_long_term_adoption.v1", "long-term adoption framework gap schema mismatch")
require(adopter_proof.get("schema") == "gofly.framework_gap_adopter_proof.v1", "adopter proof framework gap schema mismatch")
require(post_r5.get("schema") == "gofly.framework_gap_post_r5_roadmap.v1", "post-R5 framework gap schema mismatch")
require(post_r6.get("schema") == "gofly.framework_gap_post_r6_roadmap.v1", "post-R6 framework gap schema mismatch")
require(post_r7.get("schema") == "gofly.framework_gap_post_r7_roadmap.v1", "post-R7 framework gap schema mismatch")
require(post_r8.get("schema") == "gofly.framework_gap_post_r8_roadmap.v1", "post-R8 framework gap schema mismatch")

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

adoption_completed = adoption_wave.get("completedBaseline") or []
adoption_completed_tasks = {item.get("task") for item in adoption_completed if isinstance(item, dict)}
for task in (
    "GOFLY-P4-2-RPC-LATENCY-RATCHET",
    "GOFLY-P4-3-GENERATED-MIGRATION-FIDELITY",
    "GOFLY-P4-4-CLOUD-NATIVE-POLICY-CONFORMANCE",
    "GOFLY-P4-5-DX-SUPPORT-BUNDLE",
    "GOFLY-P4-6-GOVERNANCE-DASHBOARD-PRODUCTIZATION",
):
    require(task in adoption_completed_tasks, f"adoption-wave completedBaseline missing {task}")
for item in adoption_completed:
    if not isinstance(item, dict):
        missing.append(f"adoption-wave completedBaseline entry must be an object: {item!r}")
        continue
    for evidence in item.get("evidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("bench/"):
            require((root / evidence).exists(), f"adoption-wave completed evidence path missing: {evidence}")

adoption_dimensions = adoption_wave.get("dimensions") or []
required_adoption_dimensions = {
    "examples-health-index",
    "release-evidence-consumption",
    "operator-runbook-drills",
    "template-profile-trust",
    "adoption-risk-register",
}
actual_adoption_dimensions = {item.get("id") for item in adoption_dimensions if isinstance(item, dict)}
require(
    actual_adoption_dimensions == required_adoption_dimensions,
    f"adoption-wave dimensions = {sorted(actual_adoption_dimensions)!r}, want {sorted(required_adoption_dimensions)!r}",
)
adoption_tasks = set()
adoption_frameworks = set()
for item in adoption_dimensions:
    if not isinstance(item, dict):
        missing.append(f"adoption-wave dimension entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    for field in ("id", "referenceFrameworks", "currentEvidence", "gap", "priority", "todo", "aiflowTask", "acceptanceGate"):
        if not item.get(field):
            missing.append(f"adoption-wave dimension {item_id}: {field} is required")
    adoption_tasks.add(item.get("aiflowTask", ""))
    adoption_frameworks.update(item.get("referenceFrameworks") or [])
    gate = item.get("acceptanceGate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"adoption-wave dimension {item_id}: gate target {target!r} missing")
    for evidence in item.get("currentEvidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("examples/") or evidence.startswith("bench/") or evidence.startswith("bin/"):
            require((root / evidence).exists(), f"adoption-wave dimension {item_id}: evidence path missing: {evidence}")

adoption_recommended = adoption_wave.get("recommendedOrder") or []
require(adoption_recommended and adoption_recommended[0] == "GOFLY-P5-0-ADOPTION-WAVE-ROADMAP", "adoption-wave recommendedOrder must start with GOFLY-P5-0-ADOPTION-WAVE-ROADMAP")
for task in (
    "GOFLY-P5-1-EXAMPLES-HEALTH-INDEX",
    "GOFLY-P5-2-RELEASE-EVIDENCE-CONSUMPTION",
    "GOFLY-P5-3-OPERATOR-RUNBOOK-DRILLS",
    "GOFLY-P5-4-TEMPLATE-PROFILE-TRUST",
    "GOFLY-P5-5-ADOPTION-RISK-REGISTER",
):
    require(task in adoption_recommended, f"adoption-wave recommendedOrder missing {task}")
    require(task in adoption_tasks, f"adoption-wave dimensions missing task {task}")
require({"Gin", "Echo", "Fiber", "Hertz", "go-zero", "Kratos", "Kitex", "gRPC-Go", "Beego"} <= adoption_frameworks, "adoption-wave matrix must cover major reference frameworks")
adoption_scope = adoption_wave.get("scope") or {}
adoption_excluded = set(adoption_scope.get("excluded") or [])
for out_of_scope in ("GitHub stars", "download counts", "community size", "brand awareness"):
    require(out_of_scope in adoption_excluded, f"adoption-wave scope.excluded missing {out_of_scope!r}")

long_term_completed = long_term.get("completedBaseline") or []
long_term_completed_tasks = {item.get("task") for item in long_term_completed if isinstance(item, dict)}
for task in (
    "GOFLY-P5-1-EXAMPLES-HEALTH-INDEX",
    "GOFLY-P5-2-RELEASE-EVIDENCE-CONSUMPTION",
    "GOFLY-P5-3-OPERATOR-RUNBOOK-DRILLS",
    "GOFLY-P5-4-TEMPLATE-PROFILE-TRUST",
    "GOFLY-P5-5-ADOPTION-RISK-REGISTER",
):
    require(task in long_term_completed_tasks, f"long-term adoption completedBaseline missing {task}")
for item in long_term_completed:
    if not isinstance(item, dict):
        missing.append(f"long-term adoption completedBaseline entry must be an object: {item!r}")
        continue
    for evidence in item.get("evidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("bench/"):
            require((root / evidence).exists(), f"long-term adoption completed evidence path missing: {evidence}")

long_term_dimensions = long_term.get("dimensions") or []
required_long_term_dimensions = {
    "support-lifecycle",
    "integration-matrix",
    "dependency-ownership",
    "required-check-drift",
    "production-readiness-scorecard",
}
actual_long_term_dimensions = {item.get("id") for item in long_term_dimensions if isinstance(item, dict)}
require(
    actual_long_term_dimensions == required_long_term_dimensions,
    f"long-term adoption dimensions = {sorted(actual_long_term_dimensions)!r}, want {sorted(required_long_term_dimensions)!r}",
)
long_term_tasks = set()
long_term_frameworks = set()
for item in long_term_dimensions:
    if not isinstance(item, dict):
        missing.append(f"long-term adoption dimension entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    for field in ("id", "referenceFrameworks", "currentEvidence", "gap", "priority", "todo", "aiflowTask", "acceptanceGate"):
        if not item.get(field):
            missing.append(f"long-term adoption dimension {item_id}: {field} is required")
    long_term_tasks.add(item.get("aiflowTask", ""))
    long_term_frameworks.update(item.get("referenceFrameworks") or [])
    gate = item.get("acceptanceGate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"long-term adoption dimension {item_id}: gate target {target!r} missing")
    for evidence in item.get("currentEvidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("examples/") or evidence.startswith("bench/") or evidence.startswith("bin/"):
            require((root / evidence).exists(), f"long-term adoption dimension {item_id}: evidence path missing: {evidence}")

long_term_recommended = long_term.get("recommendedOrder") or []
require(long_term_recommended and long_term_recommended[0] == "GOFLY-P6-0-LONG-TERM-ADOPTION-ROADMAP", "long-term adoption recommendedOrder must start with GOFLY-P6-0-LONG-TERM-ADOPTION-ROADMAP")
for task in (
    "GOFLY-P6-1-SUPPORT-LIFECYCLE",
    "GOFLY-P6-2-INTEGRATION-MATRIX",
    "GOFLY-P6-3-DEPENDENCY-OWNERSHIP-PLAYBOOK",
    "GOFLY-P6-4-REQUIRED-CHECK-DRIFT",
    "GOFLY-P6-5-PRODUCTION-READINESS-SCORECARD",
):
    require(task in long_term_recommended, f"long-term adoption recommendedOrder missing {task}")
    require(task in long_term_tasks, f"long-term adoption dimensions missing task {task}")
require({"Gin", "Echo", "Fiber", "Hertz", "go-zero", "Kratos", "Kitex", "gRPC-Go", "Beego"} <= long_term_frameworks, "long-term adoption matrix must cover major reference frameworks")
long_term_scope = long_term.get("scope") or {}
long_term_excluded = set(long_term_scope.get("excluded") or [])
for out_of_scope in ("GitHub stars", "download counts", "community size", "brand awareness"):
    require(out_of_scope in long_term_excluded, f"long-term adoption scope.excluded missing {out_of_scope!r}")

adopter_proof_completed = adopter_proof.get("completedBaseline") or []
adopter_proof_completed_tasks = {item.get("task") for item in adopter_proof_completed if isinstance(item, dict)}
for task in (
    "GOFLY-P6-1-SUPPORT-LIFECYCLE",
    "GOFLY-P6-2-INTEGRATION-MATRIX",
    "GOFLY-P6-3-DEPENDENCY-OWNERSHIP-PLAYBOOK",
    "GOFLY-P6-4-REQUIRED-CHECK-DRIFT",
    "GOFLY-P6-5-PRODUCTION-READINESS-SCORECARD",
):
    require(task in adopter_proof_completed_tasks, f"adopter proof completedBaseline missing {task}")
for item in adopter_proof_completed:
    if not isinstance(item, dict):
        missing.append(f"adopter proof completedBaseline entry must be an object: {item!r}")
        continue
    for evidence in item.get("evidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("bench/"):
            require((root / evidence).exists(), f"adopter proof completed evidence path missing: {evidence}")

adopter_proof_dimensions = adopter_proof.get("dimensions") or []
required_adopter_proof_dimensions = {
    "evidence-traceability",
    "upgrade-rehearsal",
    "incident-drill-evidence",
    "capability-claim-provenance",
}
actual_adopter_proof_dimensions = {item.get("id") for item in adopter_proof_dimensions if isinstance(item, dict)}
require(
    actual_adopter_proof_dimensions == required_adopter_proof_dimensions,
    f"adopter proof dimensions = {sorted(actual_adopter_proof_dimensions)!r}, want {sorted(required_adopter_proof_dimensions)!r}",
)
adopter_proof_tasks = set()
adopter_proof_frameworks = set()
for item in adopter_proof_dimensions:
    if not isinstance(item, dict):
        missing.append(f"adopter proof dimension entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    for field in ("id", "referenceFrameworks", "currentEvidence", "gap", "priority", "todo", "aiflowTask", "acceptanceGate"):
        if not item.get(field):
            missing.append(f"adopter proof dimension {item_id}: {field} is required")
    adopter_proof_tasks.add(item.get("aiflowTask", ""))
    adopter_proof_frameworks.update(item.get("referenceFrameworks") or [])
    gate = item.get("acceptanceGate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"adopter proof dimension {item_id}: gate target {target!r} missing")
    for evidence in item.get("currentEvidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("examples/") or evidence.startswith("bench/") or evidence.startswith("bin/"):
            require((root / evidence).exists(), f"adopter proof dimension {item_id}: evidence path missing: {evidence}")

adopter_proof_recommended = adopter_proof.get("recommendedOrder") or []
require(adopter_proof_recommended and adopter_proof_recommended[0] == "GOFLY-P7-0-ADOPTER-PROOF-ROADMAP", "adopter proof recommendedOrder must start with GOFLY-P7-0-ADOPTER-PROOF-ROADMAP")
for task in (
    "GOFLY-P7-1-EVIDENCE-TRACEABILITY",
    "GOFLY-P7-2-UPGRADE-REHEARSAL",
    "GOFLY-P7-3-INCIDENT-DRILL-EVIDENCE",
    "GOFLY-P7-4-CAPABILITY-CLAIM-PROVENANCE",
):
    require(task in adopter_proof_recommended, f"adopter proof recommendedOrder missing {task}")
    require(task in adopter_proof_tasks, f"adopter proof dimensions missing task {task}")
require({"Gin", "Echo", "Fiber", "Hertz", "go-zero", "Kratos", "Kitex", "gRPC-Go", "Beego"} <= adopter_proof_frameworks, "adopter proof matrix must cover major reference frameworks")
adopter_proof_scope = adopter_proof.get("scope") or {}
adopter_proof_excluded = set(adopter_proof_scope.get("excluded") or [])
for out_of_scope in ("GitHub stars", "download counts", "community size", "brand awareness"):
    require(out_of_scope in adopter_proof_excluded, f"adopter proof scope.excluded missing {out_of_scope!r}")
post_r5_completed = post_r5.get("completedBaseline") or []
post_r5_completed_tasks = {item.get("task") for item in post_r5_completed if isinstance(item, dict)}
for task in (
    "GOFLY-P7-1-EVIDENCE-TRACEABILITY",
    "GOFLY-P7-2-UPGRADE-REHEARSAL",
    "GOFLY-P7-3-INCIDENT-DRILL-EVIDENCE",
    "GOFLY-P7-4-CAPABILITY-CLAIM-PROVENANCE",
):
    require(task in post_r5_completed_tasks, f"post-R5 completedBaseline missing {task}")
for item in post_r5_completed:
    if not isinstance(item, dict):
        missing.append(f"post-R5 completedBaseline entry must be an object: {item!r}")
        continue
    for evidence in item.get("evidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("bench/"):
            require((root / evidence).exists(), f"post-R5 completed evidence path missing: {evidence}")

claim_boundary = post_r5.get("claimBoundary") or {}
for field in ("transportParityPolicy", "performanceClaimPolicy", "communityScopePolicy"):
    require(len(str(claim_boundary.get(field) or "").split()) >= 10, f"post-R5 claimBoundary.{field} must be actionable")
require("transport parity" in str(claim_boundary.get("transportParityPolicy") or ""), "post-R5 transportParityPolicy must name transport parity")
require("report-only" in str(claim_boundary.get("performanceClaimPolicy") or ""), "post-R5 performanceClaimPolicy must preserve report-only latency boundaries")
require("community" in str(claim_boundary.get("communityScopePolicy") or ""), "post-R5 communityScopePolicy must document community exclusion")

post_r5_dimensions = post_r5.get("dimensions") or []
expected_post_r5_tasks = [f"GOFLY-GOV-10R6-{idx:02d}" for idx in range(1, 11)]
required_post_r5_dimensions = {
    "post-r5-roadmap",
    "http-migration-dx-parity",
    "generated-scaffold-long-term-compatibility",
    "rpc-transport-boundary-evidence",
    "data-integration-ownership-matrix",
    "production-reference-topology-proof",
    "release-supply-chain-adoption-contract",
    "performance-credibility-promotion",
    "adopter-decision-claim-provenance",
    "r6-convergence-evidence",
}
actual_post_r5_dimensions = {item.get("id") for item in post_r5_dimensions if isinstance(item, dict)}
require(
    actual_post_r5_dimensions == required_post_r5_dimensions,
    f"post-R5 dimensions = {sorted(actual_post_r5_dimensions)!r}, want {sorted(required_post_r5_dimensions)!r}",
)
post_r5_tasks = []
post_r5_frameworks = set()
post_r5_priorities = set()
for expected_round, item in enumerate(post_r5_dimensions, start=1):
    if not isinstance(item, dict):
        missing.append(f"post-R5 dimension entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    task_id = item.get("aiflowTask", "")
    post_r5_tasks.append(task_id)
    post_r5_frameworks.update(item.get("referenceFrameworks") or [])
    post_r5_priorities.add(item.get("priority", ""))
    require(item.get("round") == expected_round, f"post-R5 dimension {item_id}: round must be {expected_round}")
    for field in ("id", "round", "referenceFrameworks", "currentEvidence", "gap", "priority", "todo", "aiflowTask", "acceptanceGate"):
        require(item.get(field) not in (None, "", []), f"post-R5 dimension {item_id}: {field} is required")
    require(task_id == expected_post_r5_tasks[expected_round - 1], f"post-R5 dimension {item_id}: task must be {expected_post_r5_tasks[expected_round - 1]}")
    gate = item.get("acceptanceGate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"post-R5 dimension {item_id}: gate target {target!r} missing")
    else:
        missing.append(f"post-R5 dimension {item_id}: acceptanceGate must be a make target")
    for evidence in item.get("currentEvidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("examples/") or evidence.startswith("bench/") or evidence.startswith("bin/"):
            require((root / evidence).exists(), f"post-R5 dimension {item_id}: evidence path missing: {evidence}")
        elif evidence.startswith("make "):
            target = evidence.removeprefix("make ").split()[0]
            makefile = read_text(root / "Makefile")
            require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"post-R5 dimension {item_id}: evidence target {target!r} missing")
require(post_r5_tasks == expected_post_r5_tasks, f"post-R5 tasks must be ordered {expected_post_r5_tasks!r}; got {post_r5_tasks!r}")
require({"P0", "P1", "P2"} <= post_r5_priorities, "post-R5 roadmap must contain P0, P1, and P2 priorities")
require({"Gin", "Echo", "Fiber", "Hertz", "go-zero", "Kratos", "Kitex", "gRPC-Go", "Beego"} <= post_r5_frameworks, "post-R5 roadmap must cover major reference frameworks")
post_r5_recommended = post_r5.get("recommendedOrder") or []
require(post_r5_recommended == expected_post_r5_tasks, "post-R5 recommendedOrder must exactly match the R6 aiflow task order")
post_r5_scope = post_r5.get("scope") or {}
post_r5_excluded = set(post_r5_scope.get("excluded") or [])
for out_of_scope in ("GitHub stars", "download counts", "community size", "brand awareness"):
    require(out_of_scope in post_r5_excluded, f"post-R5 scope.excluded missing {out_of_scope!r}")

post_r6_completed = post_r6.get("completedBaseline") or []
post_r6_completed_tasks = {item.get("task") for item in post_r6_completed if isinstance(item, dict)}
for task in (
    "GOFLY-GOV-10R6-01",
    "GOFLY-GOV-10R6-06",
    "GOFLY-GOV-10R6-09",
    "GOFLY-GOV-10R6-10",
):
    require(task in post_r6_completed_tasks, f"post-R6 completedBaseline missing {task}")
for item in post_r6_completed:
    if not isinstance(item, dict):
        missing.append(f"post-R6 completedBaseline entry must be an object: {item!r}")
        continue
    for evidence in item.get("evidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("bench/") or evidence.startswith("examples/"):
            require((root / evidence).exists(), f"post-R6 completed evidence path missing: {evidence}")

post_r6_claim_boundary = post_r6.get("claimBoundary") or {}
for field in ("nonCommunityPolicy", "transportParityPolicy", "migrationProofPolicy", "performanceClaimPolicy"):
    require(len(str(post_r6_claim_boundary.get(field) or "").split()) >= 10, f"post-R6 claimBoundary.{field} must be actionable")
require("community" in str(post_r6_claim_boundary.get("nonCommunityPolicy") or ""), "post-R6 nonCommunityPolicy must document community exclusion")
require("transport parity" in str(post_r6_claim_boundary.get("transportParityPolicy") or ""), "post-R6 transportParityPolicy must name transport parity")
require("runnable example" in str(post_r6_claim_boundary.get("migrationProofPolicy") or ""), "post-R6 migrationProofPolicy must require runnable examples")
require("report-only" in str(post_r6_claim_boundary.get("performanceClaimPolicy") or ""), "post-R6 performanceClaimPolicy must preserve report-only latency boundaries")

post_r6_dimensions = post_r6.get("dimensions") or []
expected_post_r6_tasks = [f"GOFLY-GOV-10R7-{idx:02d}" for idx in range(1, 11)]
required_post_r6_dimensions = {
    "post-r6-roadmap",
    "runnable-migration-depth",
    "rpc-tier1-promotion-evidence",
    "generated-importer-compatibility",
    "db-cache-discovery-proof",
    "plugin-template-supply-chain",
    "release-ci-supply-chain-enforcement",
    "performance-trend-promotion",
    "support-bundle-remediation-loop",
    "r7-convergence-evidence",
}
actual_post_r6_dimensions = {item.get("id") for item in post_r6_dimensions if isinstance(item, dict)}
require(
    actual_post_r6_dimensions == required_post_r6_dimensions,
    f"post-R6 dimensions = {sorted(actual_post_r6_dimensions)!r}, want {sorted(required_post_r6_dimensions)!r}",
)
post_r6_tasks = []
post_r6_frameworks = set()
post_r6_priorities = set()
for expected_round, item in enumerate(post_r6_dimensions, start=1):
    if not isinstance(item, dict):
        missing.append(f"post-R6 dimension entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    task_id = item.get("aiflowTask", "")
    post_r6_tasks.append(task_id)
    post_r6_frameworks.update(item.get("referenceFrameworks") or [])
    post_r6_priorities.add(item.get("priority", ""))
    require(item.get("round") == expected_round, f"post-R6 dimension {item_id}: round must be {expected_round}")
    for field in ("id", "round", "referenceFrameworks", "currentEvidence", "gap", "priority", "todo", "aiflowTask", "acceptanceGate"):
        require(item.get(field) not in (None, "", []), f"post-R6 dimension {item_id}: {field} is required")
    require(task_id == expected_post_r6_tasks[expected_round - 1], f"post-R6 dimension {item_id}: task must be {expected_post_r6_tasks[expected_round - 1]}")
    gate = item.get("acceptanceGate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"post-R6 dimension {item_id}: gate target {target!r} missing")
    else:
        missing.append(f"post-R6 dimension {item_id}: acceptanceGate must be a make target")
    for evidence in item.get("currentEvidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("examples/") or evidence.startswith("bench/") or evidence.startswith("bin/"):
            require((root / evidence).exists(), f"post-R6 dimension {item_id}: evidence path missing: {evidence}")
        elif evidence.startswith("make "):
            target = evidence.removeprefix("make ").split()[0]
            makefile = read_text(root / "Makefile")
            require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"post-R6 dimension {item_id}: evidence target {target!r} missing")
require(post_r6_tasks == expected_post_r6_tasks, f"post-R6 tasks must be ordered {expected_post_r6_tasks!r}; got {post_r6_tasks!r}")
require({"P0", "P1", "P2"} <= post_r6_priorities, "post-R6 roadmap must contain P0, P1, and P2 priorities")
require({"Gin", "Echo", "Fiber", "Hertz", "go-zero", "Kratos", "Kitex", "gRPC-Go", "Beego"} <= post_r6_frameworks, "post-R6 roadmap must cover major reference frameworks")
post_r6_recommended = post_r6.get("recommendedOrder") or []
require(post_r6_recommended == expected_post_r6_tasks, "post-R6 recommendedOrder must exactly match the R7 aiflow task order")
post_r6_scope = post_r6.get("scope") or {}
post_r6_excluded = set(post_r6_scope.get("excluded") or [])
for out_of_scope in ("GitHub stars", "download counts", "community size", "brand awareness"):
    require(out_of_scope in post_r6_excluded, f"post-R6 scope.excluded missing {out_of_scope!r}")

post_r7_completed = post_r7.get("completedBaseline") or []
post_r7_completed_tasks = {item.get("task") for item in post_r7_completed if isinstance(item, dict)}
for task in (
    "GOFLY-GOV-10R7-01",
    "GOFLY-GOV-10R7-03",
    "GOFLY-GOV-10R7-09",
    "GOFLY-GOV-10R7-10",
):
    require(task in post_r7_completed_tasks, f"post-R7 completedBaseline missing {task}")
for item in post_r7_completed:
    if not isinstance(item, dict):
        missing.append(f"post-R7 completedBaseline entry must be an object: {item!r}")
        continue
    for evidence in item.get("evidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("bench/") or evidence.startswith("examples/"):
            require((root / evidence).exists(), f"post-R7 completed evidence path missing: {evidence}")

post_r7_claim_boundary = post_r7.get("claimBoundary") or {}
for field in ("nonCommunityPolicy", "transportParityPolicy", "migrationProofPolicy", "performanceClaimPolicy"):
    require(len(str(post_r7_claim_boundary.get(field) or "").split()) >= 10, f"post-R7 claimBoundary.{field} must be actionable")
require("community" in str(post_r7_claim_boundary.get("nonCommunityPolicy") or ""), "post-R7 nonCommunityPolicy must document community exclusion")
require("transport parity" in str(post_r7_claim_boundary.get("transportParityPolicy") or ""), "post-R7 transportParityPolicy must name transport parity")
require("runnable examples" in str(post_r7_claim_boundary.get("migrationProofPolicy") or ""), "post-R7 migrationProofPolicy must require runnable examples")
require("report-only" in str(post_r7_claim_boundary.get("performanceClaimPolicy") or ""), "post-R7 performanceClaimPolicy must preserve report-only latency boundaries")

post_r7_dimensions = post_r7.get("dimensions") or []
expected_post_r7_tasks = [f"GOFLY-GOV-10R8-{idx:02d}" for idx in range(1, 11)]
required_post_r7_dimensions = {
    "post-r7-roadmap",
    "http-migration-compatibility-proof",
    "generated-scaffold-profile-matrix",
    "rpc-streaming-boundary-evidence",
    "adapter-matrix-runnable-proof",
    "production-reference-topology-drill",
    "plugin-template-publish-protocol",
    "release-evidence-tag-ci-closure",
    "performance-budget-ratchet-depth",
    "r8-convergence-evidence",
}
actual_post_r7_dimensions = {item.get("id") for item in post_r7_dimensions if isinstance(item, dict)}
require(
    actual_post_r7_dimensions == required_post_r7_dimensions,
    f"post-R7 dimensions = {sorted(actual_post_r7_dimensions)!r}, want {sorted(required_post_r7_dimensions)!r}",
)
post_r7_tasks = []
post_r7_frameworks = set()
post_r7_priorities = set()
for expected_round, item in enumerate(post_r7_dimensions, start=1):
    if not isinstance(item, dict):
        missing.append(f"post-R7 dimension entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    task_id = item.get("aiflowTask", "")
    post_r7_tasks.append(task_id)
    post_r7_frameworks.update(item.get("referenceFrameworks") or [])
    post_r7_priorities.add(item.get("priority", ""))
    require(item.get("round") == expected_round, f"post-R7 dimension {item_id}: round must be {expected_round}")
    for field in ("id", "round", "referenceFrameworks", "currentEvidence", "gap", "priority", "todo", "aiflowTask", "acceptanceGate"):
        require(item.get(field) not in (None, "", []), f"post-R7 dimension {item_id}: {field} is required")
    require(task_id == expected_post_r7_tasks[expected_round - 1], f"post-R7 dimension {item_id}: task must be {expected_post_r7_tasks[expected_round - 1]}")
    gate = item.get("acceptanceGate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"post-R7 dimension {item_id}: gate target {target!r} missing")
    else:
        missing.append(f"post-R7 dimension {item_id}: acceptanceGate must be a make target")
    for evidence in item.get("currentEvidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("examples/") or evidence.startswith("bench/") or evidence.startswith("bin/"):
            require((root / evidence).exists(), f"post-R7 dimension {item_id}: evidence path missing: {evidence}")
        elif evidence.startswith("make "):
            target = evidence.removeprefix("make ").split()[0]
            makefile = read_text(root / "Makefile")
            require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"post-R7 dimension {item_id}: evidence target {target!r} missing")
require(post_r7_tasks == expected_post_r7_tasks, f"post-R7 tasks must be ordered {expected_post_r7_tasks!r}; got {post_r7_tasks!r}")
require({"P0", "P1", "P2"} <= post_r7_priorities, "post-R7 roadmap must contain P0, P1, and P2 priorities")
require({"Gin", "Echo", "Fiber", "Hertz", "go-zero", "Kratos", "Kitex", "gRPC-Go", "Beego"} <= post_r7_frameworks, "post-R7 roadmap must cover major reference frameworks")
post_r7_recommended = post_r7.get("recommendedOrder") or []
require(post_r7_recommended == expected_post_r7_tasks, "post-R7 recommendedOrder must exactly match the R8 aiflow task order")
post_r7_scope = post_r7.get("scope") or {}
post_r7_excluded = set(post_r7_scope.get("excluded") or [])
for out_of_scope in ("GitHub stars", "download counts", "community size", "brand awareness"):
    require(out_of_scope in post_r7_excluded, f"post-R7 scope.excluded missing {out_of_scope!r}")

post_r8_completed = post_r8.get("completedBaseline") or []
post_r8_completed_tasks = {item.get("task") for item in post_r8_completed if isinstance(item, dict)}
for task in (
    "GOFLY-GOV-10R8-01",
    "GOFLY-GOV-10R8-04",
    "GOFLY-GOV-10R8-08",
    "GOFLY-GOV-10R8-09",
    "GOFLY-GOV-10R8-10",
):
    require(task in post_r8_completed_tasks, f"post-R8 completedBaseline missing {task}")
for item in post_r8_completed:
    if not isinstance(item, dict):
        missing.append(f"post-R8 completedBaseline entry must be an object: {item!r}")
        continue
    for evidence in item.get("evidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("bench/") or evidence.startswith("examples/"):
            require((root / evidence).exists(), f"post-R8 completed evidence path missing: {evidence}")

post_r8_claim_boundary = post_r8.get("claimBoundary") or {}
for field in ("nonCommunityPolicy", "rpcPromotionPolicy", "fixtureTrustPolicy", "liveProofPolicy", "publishProtocolPolicy"):
    require(len(str(post_r8_claim_boundary.get(field) or "").split()) >= 10, f"post-R8 claimBoundary.{field} must be actionable")
require("community" in str(post_r8_claim_boundary.get("nonCommunityPolicy") or ""), "post-R8 nonCommunityPolicy must document community exclusion")
require("latency remains report-only" in str(post_r8_claim_boundary.get("rpcPromotionPolicy") or ""), "post-R8 rpcPromotionPolicy must preserve report-only latency boundaries")
require("old, current, and future" in str(post_r8_claim_boundary.get("fixtureTrustPolicy") or ""), "post-R8 fixtureTrustPolicy must require versioned fixtures")
require("fallback evidence" in str(post_r8_claim_boundary.get("liveProofPolicy") or ""), "post-R8 liveProofPolicy must document fallback evidence")
require("no-partial-write" in str(post_r8_claim_boundary.get("publishProtocolPolicy") or ""), "post-R8 publishProtocolPolicy must document no partial writes")

post_r8_dimensions = post_r8.get("dimensions") or []
expected_post_r8_tasks = [f"GOFLY-GOV-10P9-{idx:02d}" for idx in range(1, 11)]
required_post_r8_dimensions = {
    "rpc-tier1-release-train-closeout",
    "generated-project-historical-fixture-matrix",
    "reference-app-docker-backed-live-proof",
    "gateway-cache-benchmark-ownership",
    "hosted-release-ci-evidence-closure",
    "cloud-native-live-render-validation",
    "plugin-registry-publish-hardening",
    "adopter-migration-proof-expansion",
    "cli-doctor-remediation-loop",
    "p9-convergence-evidence",
}
actual_post_r8_dimensions = {item.get("id") for item in post_r8_dimensions if isinstance(item, dict)}
require(
    actual_post_r8_dimensions == required_post_r8_dimensions,
    f"post-R8 dimensions = {sorted(actual_post_r8_dimensions)!r}, want {sorted(required_post_r8_dimensions)!r}",
)
post_r8_tasks = []
post_r8_frameworks = set()
post_r8_priorities = set()
for expected_round, item in enumerate(post_r8_dimensions, start=1):
    if not isinstance(item, dict):
        missing.append(f"post-R8 dimension entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    task_id = item.get("aiflowTask", "")
    post_r8_tasks.append(task_id)
    post_r8_frameworks.update(item.get("referenceFrameworks") or [])
    post_r8_priorities.add(item.get("priority", ""))
    require(item.get("round") == expected_round, f"post-R8 dimension {item_id}: round must be {expected_round}")
    for field in ("id", "round", "referenceFrameworks", "currentEvidence", "gap", "priority", "todo", "aiflowTask", "acceptanceGate"):
        require(item.get(field) not in (None, "", []), f"post-R8 dimension {item_id}: {field} is required")
    require(task_id == expected_post_r8_tasks[expected_round - 1], f"post-R8 dimension {item_id}: task must be {expected_post_r8_tasks[expected_round - 1]}")
    gate = item.get("acceptanceGate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"post-R8 dimension {item_id}: gate target {target!r} missing")
    else:
        missing.append(f"post-R8 dimension {item_id}: acceptanceGate must be a make target")
    for evidence in item.get("currentEvidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("examples/") or evidence.startswith("bench/") or evidence.startswith("bin/") or evidence.startswith("charts/") or evidence.startswith("k8s/"):
            require((root / evidence).exists(), f"post-R8 dimension {item_id}: evidence path missing: {evidence}")
        elif evidence.startswith("make "):
            target = evidence.removeprefix("make ").split()[0]
            makefile = read_text(root / "Makefile")
            require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"post-R8 dimension {item_id}: evidence target {target!r} missing")
require(post_r8_tasks == expected_post_r8_tasks, f"post-R8 tasks must be ordered {expected_post_r8_tasks!r}; got {post_r8_tasks!r}")
require({"P0", "P1", "P2"} <= post_r8_priorities, "post-R8 roadmap must contain P0, P1, and P2 priorities")
require({"Gin", "Echo", "Fiber", "Hertz", "go-zero", "Kratos", "Kitex", "gRPC-Go"} <= post_r8_frameworks, "post-R8 roadmap must cover major reference frameworks")
post_r8_recommended = post_r8.get("recommendedOrder") or []
require(post_r8_recommended == expected_post_r8_tasks, "post-R8 recommendedOrder must exactly match the P9 aiflow task order")
post_r8_scope = post_r8.get("scope") or {}
post_r8_excluded = set(post_r8_scope.get("excluded") or [])
for out_of_scope in ("GitHub stars", "download counts", "community size", "brand awareness"):
    require(out_of_scope in post_r8_excluded, f"post-R8 scope.excluded missing {out_of_scope!r}")
claim_provenance = adopter_proof.get("capabilityClaimProvenance") or {}
require(claim_provenance.get("schema") == "gofly.capability_claim_provenance.v1", "capability claim provenance schema mismatch")
require(claim_provenance.get("sourceOfTruth") == "docs/reference/framework-gap-matrix.md", "capability claim provenance sourceOfTruth mismatch")
require(claim_provenance.get("acceptanceGate") == "make framework-gap-check", "capability claim provenance acceptanceGate mismatch")
require(
    len(str(claim_provenance.get("unsupportedClaimPolicy") or "").split()) >= 16,
    "capability claim provenance unsupportedClaimPolicy must be actionable",
)
claim_entries = claim_provenance.get("claims") or []
require(len(claim_entries) >= 7, "capability claim provenance must contain at least 7 claims")
required_claim_ids = {
    "http-dx-openapi-envelope",
    "generated-scaffold-upgrade",
    "rpc-boundary-tier1",
    "production-reference-proof",
    "release-trust-evidence",
    "plugin-publishing-protocol",
    "performance-credibility",
}
claim_ids = set()
claim_frameworks = set()
claim_types = set()
for item in claim_entries:
    if not isinstance(item, dict):
        missing.append(f"capability claim provenance entry must be an object: {item!r}")
        continue
    claim_id = item.get("id", "<missing>")
    if claim_id in claim_ids:
        missing.append(f"duplicate capability claim provenance id {claim_id!r}")
    claim_ids.add(claim_id)
    for field in (
        "id",
        "claim",
        "claimType",
        "referenceFrameworks",
        "sourceEvidence",
        "gate",
        "riskClass",
        "adopterAction",
        "unsupportedClaimHandling",
    ):
        require(bool(item.get(field)), f"capability claim {claim_id}: {field} is required")
    claim_types.add(item.get("claimType", ""))
    claim_frameworks.update(item.get("referenceFrameworks") or [])
    require(item.get("riskClass") in required_risk_classes, f"capability claim {claim_id}: unknown riskClass {item.get('riskClass')!r}")
    gate = item.get("gate", "")
    require(gate.startswith("make "), f"capability claim {claim_id}: gate must be a make target")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"capability claim {claim_id}: gate target {target!r} missing")
    for evidence in item.get("sourceEvidence") or []:
        if evidence.startswith("docs/") or evidence.startswith("examples/") or evidence.startswith("bench/"):
            require((root / evidence).exists(), f"capability claim {claim_id}: source evidence path missing: {evidence}")
        elif evidence.startswith("make "):
            target = evidence.removeprefix("make ").split()[0]
            makefile = read_text(root / "Makefile")
            require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"capability claim {claim_id}: source evidence target {target!r} missing")
        else:
            missing.append(f"capability claim {claim_id}: unsupported source evidence {evidence!r}")
    for field in ("adopterAction", "unsupportedClaimHandling"):
        require(len(str(item.get(field) or "").split()) >= 8, f"capability claim {claim_id}: {field} must be actionable")
require(required_claim_ids <= claim_ids, f"capability claim provenance missing claims: {sorted(required_claim_ids - claim_ids)!r}")
require({"Gin", "Echo", "Fiber", "Hertz", "go-zero", "Kratos", "Kitex", "gRPC-Go", "Beego"} <= claim_frameworks, "capability claim provenance must cover major reference frameworks")
for required_type in (
    "product-capability",
    "engineering-maturity",
    "contract-depth",
    "production-proof",
    "release-trust",
    "ecosystem-protocol",
    "performance-evidence",
):
    require(required_type in claim_types, f"capability claim provenance missing claimType {required_type!r}")
docs_scan = claim_provenance.get("docsScan") or {}
require(docs_scan.get("schema") == "gofly.docs_claim_provenance_scan.v1", "docs claim provenance scan schema mismatch")
ignored_paths = set(docs_scan.get("ignoredPaths") or [])
require("docs/superpowers/" in ignored_paths, "docs claim provenance scan must ignore docs/superpowers/")
gitignore = read_text(root / ".gitignore")
require("docs/superpowers/" in gitignore, ".gitignore must permanently ignore docs/superpowers/")
tracked_superpowers = subprocess.run(
    ["git", "ls-files", "docs/superpowers"],
    cwd=root,
    check=False,
    text=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
)
if tracked_superpowers.returncode == 0:
    tracked = [line for line in tracked_superpowers.stdout.splitlines() if line.strip()]
    require(not tracked, f"docs/superpowers must never be tracked: {tracked}")
else:
    missing.append(f"could not verify docs/superpowers tracked files: {tracked_superpowers.stderr.strip()}")
target_docs = docs_scan.get("targetDocs") or []
require(len(target_docs) >= 4, "docs claim provenance scan must cover at least 4 adopter-facing docs")
seen_target_docs = set()
for item in target_docs:
    if not isinstance(item, dict):
        missing.append(f"docs claim provenance target must be an object: {item!r}")
        continue
    path = item.get("path", "")
    seen_target_docs.add(path)
    require(path, "docs claim provenance target path is required")
    require(path != "docs/superpowers/" and not path.startswith("docs/superpowers/"), f"docs claim provenance target must not scan {path!r}")
    doc_text = read_text(root / path) if path else ""
    required_ids = item.get("requiredClaimIds") or []
    require(required_ids, f"docs claim provenance target {path}: requiredClaimIds is required")
    for claim_id in required_ids:
        require(claim_id in claim_ids, f"docs claim provenance target {path}: unknown claim id {claim_id!r}")
        require(f"claim-provenance: {claim_id}" in doc_text, f"docs claim provenance target {path}: missing marker for {claim_id}")
    require(
        len(str(item.get("unsupportedClaimHandling") or "").split()) >= 8,
        f"docs claim provenance target {path}: unsupportedClaimHandling must be actionable",
    )
for required_doc in (
    "README.md",
    "docs/index.md",
    "docs/explanation/adopter-decision-guide.md",
    "docs/reference/framework-gap-matrix.md",
):
    require(required_doc in seen_target_docs, f"docs claim provenance scan missing {required_doc}")

require(adoption_risk.get("sourceOfTruth") == "docs/reference/framework-gap-adoption-wave.json", "adoption risk register sourceOfTruth mismatch")
require(adoption_risk.get("acceptanceGate") == "make framework-gap-check", "adoption risk register acceptanceGate mismatch")
risk_classes = set(adoption_risk.get("riskClasses") or [])
require(risk_classes == required_risk_classes, f"adoption risk classes = {sorted(risk_classes)!r}, want {sorted(required_risk_classes)!r}")
risk_entries = adoption_risk.get("entries") or []
require(len(risk_entries) >= 12, "adoption risk register must contain at least 12 entries")
seen_risk_classes = set()
seen_entry_ids = set()
for item in risk_entries:
    if not isinstance(item, dict):
        missing.append(f"adoption risk entry must be an object: {item!r}")
        continue
    entry_id = item.get("id", "<missing>")
    if entry_id in seen_entry_ids:
        missing.append(f"duplicate adoption risk entry id {entry_id!r}")
    seen_entry_ids.add(entry_id)
    for field in (
        "id",
        "surface",
        "riskClass",
        "currentGuardrail",
        "recommendedAdopterAction",
        "promotionGate",
        "rollbackOrEscalation",
    ):
        if not item.get(field):
            missing.append(f"adoption risk {entry_id}: {field} is required")
    risk_class = item.get("riskClass", "")
    seen_risk_classes.add(risk_class)
    require(risk_class in required_risk_classes, f"adoption risk {entry_id}: unknown riskClass {risk_class!r}")
    for gate_field in ("currentGuardrail", "promotionGate"):
        gate = item.get(gate_field, "")
        if gate.startswith("make "):
            target = gate.removeprefix("make ").split()[0]
            makefile = read_text(root / "Makefile")
            require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"adoption risk {entry_id}: {gate_field} target {target!r} missing")
        elif gate.startswith("go test -C "):
            parts = gate.split()
            if len(parts) >= 4:
                require((root / parts[3]).exists(), f"adoption risk {entry_id}: {gate_field} path missing: {parts[3]}")
        else:
            missing.append(f"adoption risk {entry_id}: {gate_field} must be a make or go test -C gate")
    for field in ("recommendedAdopterAction", "rollbackOrEscalation"):
        require(len(str(item.get(field) or "").split()) >= 8, f"adoption risk {entry_id}: {field} must be actionable")
require(seen_risk_classes == required_risk_classes, f"adoption risk register missing classes: {sorted(required_risk_classes - seen_risk_classes)!r}")
for required_entry in (
    "rest-openapi-error-envelope",
    "generated-production-service",
    "release-evidence",
    "cloud-native-policy-assets",
    "rpc-tier1-promotion",
    "plugin-publishing",
    "template-profile-trust",
    "operator-runbook-drills",
    "rpc-latency-budget",
    "benchmark-comparison-claims",
    "release-gate-failure",
    "generated-diff-breaking-candidate",
):
    require(required_entry in seen_entry_ids, f"adoption risk register missing {required_entry}")

doc = read_text(root / "docs" / "reference" / "framework-gap-matrix.md")
for needle in (
    "gofly.framework_gap_matrix.v1",
    "make framework-gap-check",
    "framework-gap-next-wave.json",
    "framework-gap-adoption-wave.json",
    "framework-gap-long-term-adoption.json",
    "framework-gap-adopter-proof.json",
    "framework-gap-post-r5-roadmap.json",
    "framework-gap-post-r6-roadmap.json",
    "framework-gap-post-r7-roadmap.json",
    "framework-gap-post-r8-roadmap.json",
    "adoption-risk-register.json",
    "gofly.adoption_risk_register.v1",
    "gofly.framework_gap_long_term_adoption.v1",
    "gofly.framework_gap_adopter_proof.v1",
    "gofly.framework_gap_post_r5_roadmap.v1",
    "gofly.framework_gap_post_r6_roadmap.v1",
    "gofly.framework_gap_post_r7_roadmap.v1",
    "gofly.framework_gap_post_r8_roadmap.v1",
    "Next-Wave TODO Order",
    "Adoption-Wave TODO Order",
    "Long-Term Adoption TODO Order",
    "Adopter Proof TODO Order",
    "Post-R5 R6 TODO Order",
    "Post-R6 R7 TODO Order",
    "Post-R7 R8 TODO Order",
    "Post-R8 P9 TODO Order",
    "Adoption Risk Register",
    "production-ready",
    "candidate",
    "report-only",
    "rollback-required",
    "GOFLY-P4-2-RPC-LATENCY-RATCHET",
    "GOFLY-P5-1-EXAMPLES-HEALTH-INDEX",
    "GOFLY-P6-1-SUPPORT-LIFECYCLE",
    "GOFLY-P7-1-EVIDENCE-TRACEABILITY",
    "GOFLY-GOV-10R6-01",
    "GOFLY-GOV-10R7-01",
    "GOFLY-GOV-10R8-01",
    "GOFLY-GOV-10P9-01",
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
