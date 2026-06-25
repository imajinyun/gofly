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

require(manifest.get("schema") == "gofly.framework_gap_matrix.v1", "framework gap matrix schema mismatch")
require(next_wave.get("schema") == "gofly.framework_gap_next_wave.v1", "next-wave framework gap schema mismatch")
require(adoption_wave.get("schema") == "gofly.framework_gap_adoption_wave.v1", "adoption-wave framework gap schema mismatch")
require(adoption_risk.get("schema") == "gofly.adoption_risk_register.v1", "adoption risk register schema mismatch")
require(long_term.get("schema") == "gofly.framework_gap_long_term_adoption.v1", "long-term adoption framework gap schema mismatch")
require(adopter_proof.get("schema") == "gofly.framework_gap_adopter_proof.v1", "adopter proof framework gap schema mismatch")

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

require(adoption_risk.get("sourceOfTruth") == "docs/reference/framework-gap-adoption-wave.json", "adoption risk register sourceOfTruth mismatch")
require(adoption_risk.get("acceptanceGate") == "make framework-gap-check", "adoption risk register acceptanceGate mismatch")
risk_classes = set(adoption_risk.get("riskClasses") or [])
required_risk_classes = {"production-ready", "candidate", "report-only", "rollback-required"}
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
    "adoption-risk-register.json",
    "gofly.adoption_risk_register.v1",
    "gofly.framework_gap_long_term_adoption.v1",
    "gofly.framework_gap_adopter_proof.v1",
    "Next-Wave TODO Order",
    "Adoption-Wave TODO Order",
    "Long-Term Adoption TODO Order",
    "Adopter Proof TODO Order",
    "Adoption Risk Register",
    "production-ready",
    "candidate",
    "report-only",
    "rollback-required",
    "GOFLY-P4-2-RPC-LATENCY-RATCHET",
    "GOFLY-P5-1-EXAMPLES-HEALTH-INDEX",
    "GOFLY-P6-1-SUPPORT-LIFECYCLE",
    "GOFLY-P7-1-EVIDENCE-TRACEABILITY",
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
