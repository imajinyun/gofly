#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "governance-boundary-inventory.json"
convergence_path = root / "docs" / "reference" / "governance-convergence-verification.json"
p10_path = root / "docs" / "reference" / "governance-p10-roadmap.json"
p11_path = root / "docs" / "reference" / "governance-p11-roadmap.json"
p13_path = root / "docs" / "reference" / "governance-p13-roadmap.json"
p14_path = root / "docs" / "reference" / "governance-p14-roadmap.json"
p15_path = root / "docs" / "reference" / "governance-p15-roadmap.json"
p16_path = root / "docs" / "reference" / "governance-p16-roadmap.json"
post_r8_path = root / "docs" / "reference" / "framework-gap-post-r8-roadmap.json"
missing = []

expected_active_batch = "GOFLY-P16"
expected_converged_batch = "GOFLY-P12"
expected_p13_tasks = [
    "GOFLY-P13-01-RPC-TIER1-PROMOTION-CLOSEOUT",
    "GOFLY-P13-02-GOCTL-GENERATOR-MATURITY",
    "GOFLY-P13-03-REST-BINDING-VALIDATION-ENVELOPE",
    "GOFLY-P13-04-GOZERO-RESILIENCE-DEFAULTS",
    "GOFLY-P13-05-DB-CACHE-PRODUCTIZATION",
    "GOFLY-P13-06-GATEWAY-CACHE-BENCH-EVIDENCE",
    "GOFLY-P13-07-DISCOVERY-FAILOVER-MATRIX",
    "GOFLY-P13-08-REFERENCE-APP-LIVE-PROOF",
    "GOFLY-P13-09-MIGRATION-CASE-UPGRADE",
    "GOFLY-P13-10-PLUGIN-TEMPLATE-PUBLISH-HARDENING",
    "GOFLY-P13-11-CLI-DOCTOR-TROUBLESHOOTING-LOOP",
    "GOFLY-P13-12-HOSTED-RELEASE-SUPPLY-CHAIN",
]
expected_p13_task_gates = [
    "make rpc-boundary-check",
    "make generated-version-compat-check",
    "make openapi-validation-check",
    "make resilience-drill-check",
    "make db-cache-productization-check",
    "make bench-regression-check",
    "make discovery-adapter-matrix-check",
    "make reference-app-smoke",
    "make adopter-decision-check",
    "make plugin-conformance-check",
    "make dx-troubleshooting-check",
    "make required-checks-drift-check",
]
expected_tasks = [
    "GOFLY-P16-01-P15-COMPLETION-HANDOFF",
    "GOFLY-P16-02-GATEWAY-CACHE-TREND-SAMPLE-ATTACHMENT",
    "GOFLY-P16-03-GATEWAY-CACHE-ALLOCATION-PROMOTION-REVIEW",
]
expected_task_gates = [
    "make governance-boundary-inventory-check",
    "make bench-regression-check",
    "make bench-regression-check",
]
expected_p15_tasks = [
    "GOFLY-P15-01-P14-COMPLETION-HANDOFF",
    "GOFLY-P15-02-RPC-RELEASE-TRAIN-ATTACHMENT",
    "GOFLY-P15-03-GATEWAY-CACHE-BUDGET-ATTACHMENT",
]
expected_p15_task_gates = [
    "make governance-boundary-inventory-check",
    "make rpc-boundary-check",
    "make bench-regression-check",
]
expected_p14_tasks = [
    "GOFLY-P14-01-P13-COMPLETION-HANDOFF",
    "GOFLY-P14-02-RPC-RELEASE-TRAIN-EVIDENCE",
    "GOFLY-P14-03-GENERATOR-ADOPTER-REPLAY-EVIDENCE",
]
expected_p14_task_gates = [
    "make governance-boundary-inventory-check",
    "make rpc-boundary-check",
    "make generated-version-compat-check",
]
expected_converged_tasks = [
    "GOFLY-P12-1-RPC-BENCHMARK-BUDGET-PROMOTION",
    "GOFLY-P12-2-GENERATED-UPGRADE-REAL-BRANCH",
    "GOFLY-P12-3-HOSTED-CLOUD-NATIVE-LIVE-CI",
]
expected_p11_tasks = [
    "GOFLY-P11-1-RPC-TIER1-PROMOTION-REVIEW",
    "GOFLY-P11-2-GENERATED-PROJECT-LIVE-UPGRADE",
    "GOFLY-P11-3-CLOUD-NATIVE-HOSTED-PROOF",
]
expected_p10_tasks = [
    "GOFLY-P10-1-RPC-TIER1-CLOSEOUT",
    "GOFLY-P10-2-GOCTL-GENERATOR-FIDELITY",
    "GOFLY-P10-3-STORAGE-CACHE-PRODUCTIZATION",
    "GOFLY-P10-4-REST-MIDDLEWARE-ECOSYSTEM-MATRIX",
    "GOFLY-P10-5-DISCOVERY-ADAPTER-MATRIX",
    "GOFLY-P10-6-AI_NATIVE_SUPPORT_BUNDLE",
    "GOFLY-P10-7-PERFORMANCE-BUDGET-RATCHET",
    "GOFLY-P10-8-CLOUD-NATIVE-ADOPTION-PROOF",
    "GOFLY-P10-9-RELEASE-DASHBOARD-CONSUMPTION",
    "GOFLY-P10-10-CONVERGENCE-REPORT",
]
expected_post_r8_tasks = [f"GOFLY-GOV-10P9-{idx:02d}" for idx in range(1, 11)]
expected_batches = {
    "GOFLY-GOV-10R": {
        "status": "completed-with-local-fallbacks",
        "taskPrefix": "GOFLY-GOV-10R-",
        "roundCount": 10,
    },
    "GOFLY-GOV-10R2": {
        "status": "completed-with-local-fallbacks",
        "taskPrefix": "GOFLY-GOV-10R2-",
        "roundCount": 10,
    },
    "GOFLY-GOV-10R3": {
        "status": "completed-with-local-fallbacks",
        "taskPrefix": "GOFLY-GOV-10R3-",
        "roundCount": 10,
    },
    "GOFLY-GOV-10R4": {
        "status": "completed",
        "taskPrefix": "GOFLY-GOV-10R4-",
        "roundCount": 10,
    },
    "GOFLY-GOV-10R5": {
        "status": "completed",
        "taskPrefix": "GOFLY-GOV-10R5-",
        "roundCount": 10,
    },
    "GOFLY-GOV-10R6": {
        "status": "completed",
        "taskPrefix": "GOFLY-GOV-10R6-",
        "roundCount": 10,
    },
    "GOFLY-GOV-10R7": {
        "status": "completed",
        "taskPrefix": "GOFLY-GOV-10R7-",
        "roundCount": 10,
    },
    "GOFLY-GOV-10R8": {
        "status": "completed",
        "taskPrefix": "GOFLY-GOV-10R8-",
        "roundCount": 10,
    },
    "GOFLY-GOV-10P9": {
        "status": "completed",
        "taskPrefix": "GOFLY-GOV-10P9-",
        "roundCount": 10,
    },
    "GOFLY-P10": {
        "status": "completed",
        "taskPrefix": "GOFLY-P10-",
        "roundCount": 10,
    },
    "GOFLY-P11": {
        "status": "completed",
        "taskPrefix": "GOFLY-P11-",
        "roundCount": 3,
    },
    "GOFLY-P12": {
        "status": "completed",
        "taskPrefix": "GOFLY-P12-",
        "roundCount": 3,
    },
    "GOFLY-P13": {
        "status": "completed",
        "taskPrefix": "GOFLY-P13-",
        "roundCount": 12,
    },
    "GOFLY-P14": {
        "status": "completed",
        "taskPrefix": "GOFLY-P14-",
        "roundCount": 3,
    },
    "GOFLY-P15": {
        "status": "completed",
        "taskPrefix": "GOFLY-P15-",
        "roundCount": 3,
    },
    "GOFLY-P16": {
        "status": "submitted",
        "taskPrefix": "GOFLY-P16-",
        "roundCount": 3,
    },
}
expected_converged_commits = {
    "GOFLY-P12-1-RPC-BENCHMARK-BUDGET-PROMOTION": "7d0b69a",
    "GOFLY-P12-2-GENERATED-UPGRADE-REAL-BRANCH": "d99c690",
    "GOFLY-P12-3-HOSTED-CLOUD-NATIVE-LIVE-CI": "155fd18",
}
expected_converged_verification = {
    "GOFLY-P12-1-RPC-BENCHMARK-BUDGET-PROMOTION": [
        "make rpc-boundary-check",
        "make bench-regression-check",
    ],
    "GOFLY-P12-2-GENERATED-UPGRADE-REAL-BRANCH": [
        "make generated-upgrade-dry-run-check",
    ],
    "GOFLY-P12-3-HOSTED-CLOUD-NATIVE-LIVE-CI": [
        "make cloud-native-render-check",
        "make ci-required-check-evidence-check",
        "make required-checks-drift-check",
    ],
}
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
if convergence_path.is_file():
    convergence_manifest = json.loads(convergence_path.read_text(encoding="utf-8"))
else:
    convergence_manifest = {}
    missing.append("docs/reference/governance-convergence-verification.json is missing")
if p10_path.is_file():
    p10_manifest = json.loads(p10_path.read_text(encoding="utf-8"))
else:
    p10_manifest = {}
    missing.append("docs/reference/governance-p10-roadmap.json is missing")
if p11_path.is_file():
    p11_manifest = json.loads(p11_path.read_text(encoding="utf-8"))
else:
    p11_manifest = {}
    missing.append("docs/reference/governance-p11-roadmap.json is missing")
if p13_path.is_file():
    p13_manifest = json.loads(p13_path.read_text(encoding="utf-8"))
else:
    p13_manifest = {}
    missing.append("docs/reference/governance-p13-roadmap.json is missing")
if p14_path.is_file():
    p14_manifest = json.loads(p14_path.read_text(encoding="utf-8"))
else:
    p14_manifest = {}
    missing.append("docs/reference/governance-p14-roadmap.json is missing")
if p15_path.is_file():
    p15_manifest = json.loads(p15_path.read_text(encoding="utf-8"))
else:
    p15_manifest = {}
    missing.append("docs/reference/governance-p15-roadmap.json is missing")
if p16_path.is_file():
    p16_manifest = json.loads(p16_path.read_text(encoding="utf-8"))
else:
    p16_manifest = {}
    missing.append("docs/reference/governance-p16-roadmap.json is missing")
if post_r8_path.is_file():
    post_r8 = json.loads(post_r8_path.read_text(encoding="utf-8"))
else:
    post_r8 = {}
    missing.append("docs/reference/framework-gap-post-r8-roadmap.json is missing")

makefile = read_text(root / "Makefile")
gitignore = read_text(root / ".gitignore")
governance_script = read_text(root / "bin" / "scripts" / "governance-10-rounds.sh")
targets = make_target_names(makefile)

require(manifest.get("schema") == "gofly.governance_boundary_inventory.v1", "schema must be gofly.governance_boundary_inventory.v1")
require(p10_manifest.get("schema") == "gofly.governance_p10_roadmap.v1", "P10 roadmap schema mismatch")
require(p11_manifest.get("schema") == "gofly.governance_p11_roadmap.v1", "P11 roadmap schema mismatch")
require(p13_manifest.get("schema") == "gofly.governance_p13_roadmap.v1", "P13 roadmap schema mismatch")
require(p14_manifest.get("schema") == "gofly.governance_p14_roadmap.v1", "P14 roadmap schema mismatch")
require(p15_manifest.get("schema") == "gofly.governance_p15_roadmap.v1", "P15 roadmap schema mismatch")
require(p16_manifest.get("schema") == "gofly.governance_p16_roadmap.v1", "P16 roadmap schema mismatch")
require(post_r8.get("schema") == "gofly.framework_gap_post_r8_roadmap.v1", "post-R8 framework gap schema mismatch")
require(manifest.get("activeAiflowBatch") == expected_active_batch, f"activeAiflowBatch must be {expected_active_batch}")
require("governance-boundary-inventory-check" in targets, "Makefile must expose governance-boundary-inventory-check")
require("api-contract-check" in targets, "Makefile must expose api-contract-check")
require("check-governance-boundary-inventory.sh" in governance_script, "governance-10-rounds.sh must run the boundary inventory in Round 01")

api_contract_line = next((line for line in makefile.splitlines() if line.startswith("api-contract-check:")), "")
require("openapi-validation-check" in api_contract_line, "api-contract-check must depend on openapi-validation-check")
require("rpc-boundary-check" in api_contract_line, "api-contract-check must depend on rpc-boundary-check")

batches = manifest.get("aiflowTaskBatches") or []
actual_batches = {
    item.get("id"): item
    for item in batches
    if isinstance(item, dict) and item.get("id")
}
require(set(actual_batches) == set(expected_batches), f"aiflowTaskBatches drifted: missing={sorted(set(expected_batches) - set(actual_batches))} extra={sorted(set(actual_batches) - set(expected_batches))}")
for batch_id, expected in expected_batches.items():
    item = actual_batches.get(batch_id) or {}
    for field, value in expected.items():
        require(item.get(field) == value, f"{batch_id}: {field} must be {value!r}")
    require(bool(item.get("evidence")), f"{batch_id}: evidence is required")

tasks = manifest.get("aiflowTasks") or []
actual_tasks = [item.get("id") for item in tasks if isinstance(item, dict)]
require(actual_tasks == expected_tasks, f"aiflowTasks must be ordered {expected_tasks!r}; got {actual_tasks!r}")
for expected_round, item in enumerate(tasks, start=1):
    if not isinstance(item, dict):
        missing.append(f"aiflowTasks entry must be an object: {item!r}")
        continue
    task_id = item.get("id", "<missing>")
    require(item.get("round") == expected_round, f"{task_id}: round must be {expected_round}")
    for field in ("id", "title", "objective", "deliverable", "gate", "commitPolicy"):
        require(bool(item.get(field)), f"{task_id}: {field} is required")
    require(task_id.startswith(expected_active_batch + "-"), f"{task_id}: id must use active batch prefix {expected_active_batch}-")
    require(
        "commit" in item.get("commitPolicy", "").lower(),
        f"{task_id}: commitPolicy must describe the per-task commit checkpoint",
    )
    require(gate_is_known(item.get("gate", ""), targets), f"{task_id}: gate is not known: {item.get('gate')!r}")
actual_task_gates = [item.get("gate") for item in tasks if isinstance(item, dict)]
require(actual_task_gates == expected_task_gates, f"P16 task gates mismatch: {actual_task_gates!r}")
require("p15 completion" in tasks[0].get("title", "").lower(), "P16 round 01 title must document P15 completion handoff")
require("p16" in tasks[0].get("objective", "").lower(), "P16 round 01 objective must document P16 active batch handoff")
for expected_round, item in enumerate(tasks, start=1):
    task_id = item.get("id")
    expected_status = "completed" if expected_round == 1 else "queued"
    require(item.get("status") == expected_status, f"{task_id}: active P16 task status must be {expected_status}")
    require(item.get("priority"), f"{task_id}: priority is required")
    if expected_status == "completed":
        require(item.get("commit") == "pending-current-commit", f"{task_id}: completed P16 task must record pending current commit")
        require(bool(item.get("verification")), f"{task_id}: completed P16 task must record verification")
    else:
        require("commit" not in item or item.get("commit") in ("", None), f"{task_id}: queued P16 task must not claim a completed commit")
        require("verification" not in item or item.get("verification") in ("", None), f"{task_id}: queued P16 task must not claim completed verification")

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

post_r8_dimensions = post_r8.get("dimensions") or []
expected_post_r8_ids = [
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
]
actual_post_r8_ids = [
    item.get("id")
    for item in post_r8_dimensions
    if isinstance(item, dict)
]
require(actual_post_r8_ids == expected_post_r8_ids, f"post-R8 roadmap dimensions mismatch: {actual_post_r8_ids!r}")
post_r8_order = post_r8.get("recommendedOrder") or []
require(post_r8_order == expected_post_r8_tasks, f"post-R8 recommendedOrder must remain historical P9 planning evidence: {post_r8_order!r}")
post_r8_scope = post_r8.get("scope") or {}
post_r8_excluded = set(post_r8_scope.get("excluded") or [])
for out_of_scope in ("GitHub stars", "download counts", "community size", "brand awareness"):
    require(out_of_scope in post_r8_excluded, f"post-R8 scope.excluded missing {out_of_scope!r}")
for expected_round, item in enumerate(post_r8_dimensions, start=1):
    if not isinstance(item, dict):
        missing.append(f"post-R8 dimension must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    require(item.get("round") == expected_round, f"{item_id}: round must be {expected_round}")
    for field in ("id", "priority", "referenceFrameworks", "currentEvidence", "gap", "todo", "aiflowTask", "acceptanceGate"):
        require(bool(item.get(field)), f"{item_id}: {field} is required")
    require(item.get("aiflowTask") == expected_post_r8_tasks[expected_round - 1], f"{item_id}: aiflowTask mismatch")
    require(gate_is_known(item.get("acceptanceGate", ""), targets), f"{item_id}: acceptanceGate is not known: {item.get('acceptanceGate')!r}")
    for evidence in item.get("currentEvidence") or []:
        if evidence.startswith(("docs/", "examples/", "bench/", "charts/", "k8s/")):
            require((root / evidence).exists(), f"{item_id}: evidence path missing: {evidence}")

require(convergence_manifest.get("schema") == "gofly.governance_convergence_verification.v1", "convergence verification schema mismatch")
require(convergence_manifest.get("aiflowTask") == "GOFLY-P12-CONVERGENCE-EVIDENCE", "convergence verification aiflowTask mismatch")
require(convergence_manifest.get("acceptanceGate") == "make governance-10-rounds", "convergence verification acceptanceGate mismatch")
require(convergence_manifest.get("activeBatch") == expected_converged_batch, "convergence verification activeBatch mismatch")
aggregate_gates = set(convergence_manifest.get("aggregateGates") or [])
for gate in (
    "make governance-boundary-inventory-check",
    "make governance-report-check",
    "make required-checks-drift-check",
    "make security",
    "make cache-dependency-governance-check",
):
    require(gate in aggregate_gates, f"convergence verification aggregateGates missing {gate}")

round_commits = convergence_manifest.get("roundCommits") or []
require(len(round_commits) == len(expected_converged_tasks), "convergence verification must track P12 round commits")
actual_round_commit_tasks = [
    item.get("task")
    for item in round_commits
    if isinstance(item, dict)
]
require(actual_round_commit_tasks == expected_converged_tasks, f"convergence verification roundCommits tasks mismatch: {actual_round_commit_tasks!r}")
for expected_round, item in enumerate(round_commits, start=1):
    if not isinstance(item, dict):
        missing.append(f"roundCommits entry must be an object: {item!r}")
        continue
    task_id = item.get("task", "<missing>")
    require(item.get("round") == expected_round, f"{task_id}: roundCommit round must be {expected_round}")
    require(item.get("commit"), f"{task_id}: roundCommit commit is required")
    gate = item.get("gate", "")
    require(gate_is_known(gate, targets), f"{task_id}: roundCommit gate is not known: {gate!r}")
    expected_commit = expected_converged_commits.get(task_id)
    require(item.get("commit") == expected_commit, f"{task_id}: roundCommit commit mismatch")
    require(
        item.get("verification") == expected_converged_verification.get(task_id),
        f"{task_id}: roundCommit verification mismatch",
    )
    if expected_commit == "self":
        require(item.get("selfReference") is True, f"{task_id}: selfReference must be true for the convergence commit")
    else:
        require(re.fullmatch(r"[0-9a-f]{7,40}", str(item.get("commit"))), f"{task_id}: commit must be a git short or full SHA")

final_gate_policy = convergence_manifest.get("finalGatePolicy") or {}
require(final_gate_policy.get("entrypoint") == "make governance-10-rounds", "finalGatePolicy.entrypoint mismatch")
require(final_gate_policy.get("script") == "bin/scripts/governance-10-rounds.sh", "finalGatePolicy.script mismatch")
require(final_gate_policy.get("skipReport") == "GOVERNANCE_SKIP_REPORT", "finalGatePolicy.skipReport mismatch")
require(set(final_gate_policy.get("runtimeIgnoredPaths") or []) == expected_ignored, "finalGatePolicy.runtimeIgnoredPaths mismatch")
for skip in ("GOVERNANCE_SKIP_RACE", "GOVERNANCE_SKIP_SECURITY", "GOVERNANCE_SKIP_GENERATED_CONTROL_PLANE_SMOKE"):
    require(skip in set(final_gate_policy.get("releaseSkipsRejected") or []), f"finalGatePolicy.releaseSkipsRejected missing {skip}")
    require(f"assert_not_release_skip {skip}" in governance_script, f"governance-10-rounds.sh must reject release skip {skip}")

execution = convergence_manifest.get("aiflowExecution") or {}
require(execution.get("status") == "completed", "convergence verification aiflowExecution.status must be completed")
require("aiflow CLI status" in str(execution.get("blocker") or ""), "convergence verification aiflowExecution.blocker must document the aiflow CLI status limitation")
require("commit and push" in str(execution.get("goflyImpact") or ""), "convergence verification aiflowExecution.goflyImpact must document aiflow commit policy")
previous_handoff = convergence_manifest.get("previousBatchHandoff") or {}
require("GOFLY-P11-CONVERGENCE-EVIDENCE" in set(previous_handoff.get("supersedes") or []), "previousBatchHandoff must supersede GOFLY-P11-CONVERGENCE-EVIDENCE")
require("P12 active batch" in str(previous_handoff.get("reason") or ""), "previousBatchHandoff.reason must document P12 active batch handoff")
previous_completion_policy = str(previous_handoff.get("completionPolicy") or "")
for needle in ("GOFLY-P12-CONVERGENCE-EVIDENCE", "make governance-boundary-inventory-check", "make governance-report-check", "current agent or human"):
    require(needle in previous_completion_policy, f"previousBatchHandoff.completionPolicy missing {needle!r}")

known_risks = {
    item.get("id"): item
    for item in convergence_manifest.get("knownRisks") or []
    if isinstance(item, dict) and item.get("id")
}
for risk_id in ("aiflow-adjacent-migration", "rpc-tier1-promotion-hold", "cloud-native-hosted-tooling", "runtime-state"):
    item = known_risks.get(risk_id) or {}
    require(bool(item), f"knownRisks missing {risk_id}")
    for field in ("riskClass", "evidence", "status", "followUp"):
        require(bool(item.get(field)), f"knownRisks {risk_id}: {field} is required")
    require(len(str(item.get("followUp") or "").split()) >= 10, f"knownRisks {risk_id}: followUp must be actionable")

recommendations = {
    item.get("id"): item
    for item in convergence_manifest.get("nextRoundRecommendations") or []
    if isinstance(item, dict) and item.get("id")
}
for rec_id in ("P13-rpc-tier1-blocking-promotion", "P13-docker-backed-reference-app-live-integration", "P13-release-supply-chain-hosted-artifacts"):
    item = recommendations.get(rec_id) or {}
    require(bool(item), f"nextRoundRecommendations missing {rec_id}")
    require(item.get("priority") in {"P0", "P1", "P2", "P3"}, f"nextRoundRecommendations {rec_id}: priority mismatch")
    require(len(str(item.get("action") or "").split()) >= 10, f"nextRoundRecommendations {rec_id}: action must be actionable")

p10_rounds = p10_manifest.get("rounds") or []
require(len(p10_rounds) == 10, "P10 roadmap must contain 10 rounds")
actual_p10_ids = [
    item.get("id")
    for item in p10_rounds
    if isinstance(item, dict)
]
require(actual_p10_ids == expected_p10_tasks, f"P10 roadmap ids mismatch: {actual_p10_ids!r}")
submission = p10_manifest.get("aiflowSubmission") or {}
require(submission.get("status") in {"submitted", "blocked"}, "P10 aiflowSubmission.status must be submitted or blocked")
if submission.get("status") == "blocked":
    require(bool(submission.get("blockedBy")), "P10 blocked submission must include blockedBy")
    require("aiflow" in str(submission.get("safetyPolicy") or ""), "P10 blocked submission must include safetyPolicy")
for expected_round, item in enumerate(p10_rounds, start=1):
    if not isinstance(item, dict):
        missing.append(f"P10 roadmap round must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    require(item.get("round") == expected_round, f"{item_id}: round must be {expected_round}")
    for field in ("id", "status", "title", "objective", "acceptanceGate", "commitPolicy"):
        require(bool(item.get(field)), f"{item_id}: {field} is required")
    require(gate_is_known(item.get("acceptanceGate", ""), targets), f"{item_id}: acceptanceGate is not known: {item.get('acceptanceGate')!r}")
    require("commit" in item.get("commitPolicy", "").lower(), f"{item_id}: commitPolicy must mention commit")

p11_tasks = p11_manifest.get("tasks") or []
expected_p11_gates = [
    "make rpc-boundary-check",
    "make generated-upgrade-dry-run-check",
    "make p1-growth-check",
]
expected_p11_commits = {
    "GOFLY-P11-1-RPC-TIER1-PROMOTION-REVIEW": "d68e130",
    "GOFLY-P11-2-GENERATED-PROJECT-LIVE-UPGRADE": "4cb635b",
    "GOFLY-P11-3-CLOUD-NATIVE-HOSTED-PROOF": "11fcce6",
}
actual_p11_tasks = [
    item.get("id")
    for item in p11_tasks
    if isinstance(item, dict)
]
require(actual_p11_tasks == expected_p11_tasks, f"P11 roadmap task ids mismatch: {actual_p11_tasks!r}")
submission = p11_manifest.get("aiflowSubmission") or {}
require(submission.get("status") == "completed", "P11 aiflowSubmission.status must be completed")
require(set(submission.get("completedTasks") or []) == set(expected_p11_tasks), "P11 completedTasks mismatch")
require("durable docs/reference evidence" in str(submission.get("completionPolicy") or ""), "P11 completionPolicy must document durable docs/reference evidence")
runtime_state_policy = str(submission.get("runtimeStatePolicy") or "")
for path in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "bench/regression-report.json", "docs/superpowers"):
    require(path in runtime_state_policy, f"P11 runtimeStatePolicy missing {path!r}")
for expected_round, item in enumerate(p11_tasks, start=1):
    if not isinstance(item, dict):
        missing.append(f"P11 roadmap task must be an object: {item!r}")
        continue
    task_id = item.get("id", "<missing>")
    require(item.get("round") == expected_round, f"{task_id}: round must be {expected_round}")
    for field in ("id", "status", "title", "objective", "deliverable", "acceptanceGate", "commitPolicy"):
        require(bool(item.get(field)), f"{task_id}: {field} is required")
    require(item.get("status") == "completed", f"{task_id}: status must be completed")
    require(item.get("acceptanceGate") == expected_p11_gates[expected_round - 1], f"{task_id}: acceptanceGate mismatch")
    require(gate_is_known(item.get("acceptanceGate", ""), targets), f"{task_id}: acceptanceGate is not known: {item.get('acceptanceGate')!r}")
    require("commit" in item.get("commitPolicy", "").lower(), f"{task_id}: commitPolicy must mention commit")
    require(item.get("commit") == expected_p11_commits[task_id], f"{task_id}: commit mismatch")
    require(bool(item.get("verification")), f"{task_id}: verification is required")

p13_tasks = p13_manifest.get("tasks") or []
actual_p13_tasks = [
    item.get("id")
    for item in p13_tasks
    if isinstance(item, dict)
]
require(actual_p13_tasks == expected_p13_tasks, f"P13 roadmap task ids mismatch: {actual_p13_tasks!r}")
p13_submission = p13_manifest.get("aiflowSubmission") or {}
require(p13_submission.get("status") == "completed", "P13 aiflowSubmission.status must be completed")
require(p13_submission.get("completedTasks") == expected_p13_tasks, "P13 completedTasks must match the completed queue order")
queue_summary = p13_submission.get("queueSummary") or {}
require(queue_summary.get("pendingRuns") == 0, "P13 queueSummary.pendingRuns must be 0")
require(queue_summary.get("failedRuns") == 0, "P13 queueSummary.failedRuns must be 0")
require(queue_summary.get("completedRuns") == 54, "P13 queueSummary.completedRuns must be 54")
require("aiflow submit" in str(p13_submission.get("submissionCommand") or ""), "P13 submissionCommand must document aiflow submit")
require("aiflow status" in str(p13_submission.get("queueStatusCommand") or ""), "P13 queueStatusCommand must document aiflow status")
p13_runtime_policy = str(p13_submission.get("runtimeStatePolicy") or "")
for path in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "bench/regression-report.json", "bin/gofly", "docs/superpowers"):
    require(path in p13_runtime_policy, f"P13 runtimeStatePolicy missing {path!r}")
require("committed by the current agent or human" in str(p13_submission.get("safetyPolicy") or ""), "P13 safetyPolicy must document commit ownership")
for expected_round, item in enumerate(p13_tasks, start=1):
    if not isinstance(item, dict):
        missing.append(f"P13 roadmap task must be an object: {item!r}")
        continue
    task_id = item.get("id", "<missing>")
    require(item.get("round") == expected_round, f"{task_id}: round must be {expected_round}")
    require(item.get("status") == "completed", f"{task_id}: status must be completed")
    require(item.get("priority") == 101 - expected_round, f"{task_id}: priority mismatch")
    for field in ("id", "title", "objective", "deliverable", "acceptanceGates", "commitPolicy", "commit", "verification"):
        require(bool(item.get(field)), f"{task_id}: {field} is required")
    gates = item.get("acceptanceGates") or []
    require(gates[0] == expected_p13_task_gates[expected_round - 1], f"{task_id}: first acceptance gate mismatch")
    for gate in gates:
        require(gate_is_known(gate, targets), f"{task_id}: acceptanceGate is not known: {gate!r}")
    require("commit" in item.get("commitPolicy", "").lower(), f"{task_id}: commitPolicy must mention commit")
    require(item.get("commit") not in ("", None), f"{task_id}: completed P13 task must claim a completed commit")
    require(bool(item.get("verification")), f"{task_id}: completed P13 task must claim completed verification")

p14_tasks = p14_manifest.get("tasks") or []
actual_p14_tasks = [
    item.get("id")
    for item in p14_tasks
    if isinstance(item, dict)
]
require(actual_p14_tasks == expected_p14_tasks, f"P14 roadmap task ids mismatch: {actual_p14_tasks!r}")
p14_submission = p14_manifest.get("aiflowSubmission") or {}
require(p14_submission.get("status") == "submitted", "P14 aiflowSubmission.status must be submitted")
require(p14_submission.get("completedTasks") == expected_p14_tasks, "P14 completedTasks must contain all three tasks")
require(p14_submission.get("pendingTasks") == [], "P14 pendingTasks must be empty after completion")
require("aiflow submit" in str(p14_submission.get("submissionCommand") or ""), "P14 submissionCommand must document aiflow submit")
require("aiflow status" in str(p14_submission.get("queueStatusCommand") or ""), "P14 queueStatusCommand must document aiflow status")
p14_handoff = p14_manifest.get("previousBatchHandoff") or {}
require(p14_handoff.get("completedBatch") == "GOFLY-P13", "P14 previousBatchHandoff.completedBatch must be GOFLY-P13")
require(p14_handoff.get("completedTaskCount") == 12, "P14 previousBatchHandoff.completedTaskCount must be 12")
p14_runtime_policy = str(p14_submission.get("runtimeStatePolicy") or "")
for path in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "bench/current.txt", "bench/regression-report.json", "bench/summary.md", "bin/gofly", "docs/superpowers"):
    require(path in p14_runtime_policy, f"P14 runtimeStatePolicy missing {path!r}")
require("committed by the current agent or human" in str(p14_submission.get("safetyPolicy") or ""), "P14 safetyPolicy must document commit ownership")
for expected_round, item in enumerate(p14_tasks, start=1):
    if not isinstance(item, dict):
        missing.append(f"P14 roadmap task must be an object: {item!r}")
        continue
    task_id = item.get("id", "<missing>")
    require(item.get("round") == expected_round, f"{task_id}: round must be {expected_round}")
    require(item.get("status") == "completed", f"{task_id}: status must be completed")
    require(item.get("priority") == 101 - expected_round, f"{task_id}: priority mismatch")
    for field in ("id", "title", "objective", "deliverable", "acceptanceGates", "commitPolicy"):
        require(bool(item.get(field)), f"{task_id}: {field} is required")
    gates = item.get("acceptanceGates") or []
    require(gates[0] == expected_p14_task_gates[expected_round - 1], f"{task_id}: first acceptance gate mismatch")
    for gate in gates:
        require(gate_is_known(gate, targets), f"{task_id}: acceptanceGate is not known: {gate!r}")
    require("commit" in item.get("commitPolicy", "").lower(), f"{task_id}: commitPolicy must mention commit")
    require(item.get("commit") == "pending-current-commit", f"{task_id}: completed task must record pending current commit")
    require(bool(item.get("verification")), f"{task_id}: completed task must record verification")

p15_tasks = p15_manifest.get("tasks") or []
actual_p15_tasks = [
    item.get("id")
    for item in p15_tasks
    if isinstance(item, dict)
]
require(actual_p15_tasks == expected_p15_tasks, f"P15 roadmap task ids mismatch: {actual_p15_tasks!r}")
p15_submission = p15_manifest.get("aiflowSubmission") or {}
require(p15_submission.get("status") == "submitted", "P15 aiflowSubmission.status must be submitted")
require(p15_submission.get("completedTasks") == expected_p15_tasks, "P15 completedTasks must contain all three tasks")
require(p15_submission.get("pendingTasks") == [], "P15 pendingTasks must be empty after completion")
submission_commands = p15_submission.get("submissionCommands") or []
require(len(submission_commands) == 3, "P15 submissionCommands must document all three aiflow submit calls")
for task_id in expected_p15_tasks:
    require(
        any(task_id in command and "aiflow submit" in command for command in submission_commands),
        f"P15 submissionCommands missing {task_id}",
    )
require("aiflow status" in str(p15_submission.get("queueStatusCommand") or ""), "P15 queueStatusCommand must document aiflow status")
p15_handoff = p15_manifest.get("previousBatchHandoff") or {}
require(p15_handoff.get("completedBatch") == "GOFLY-P14", "P15 previousBatchHandoff.completedBatch must be GOFLY-P14")
require(p15_handoff.get("completedTaskCount") == 3, "P15 previousBatchHandoff.completedTaskCount must be 3")
p15_runtime_policy = str(p15_submission.get("runtimeStatePolicy") or "")
for path in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "bench/current.txt", "bench/regression-report.json", "bench/summary.md", "bin/gofly", "docs/superpowers"):
    require(path in p15_runtime_policy, f"P15 runtimeStatePolicy missing {path!r}")
require("committed by the current agent or human" in str(p15_submission.get("safetyPolicy") or ""), "P15 safetyPolicy must document commit ownership")
for expected_round, item in enumerate(p15_tasks, start=1):
    if not isinstance(item, dict):
        missing.append(f"P15 roadmap task must be an object: {item!r}")
        continue
    task_id = item.get("id", "<missing>")
    require(item.get("round") == expected_round, f"{task_id}: round must be {expected_round}")
    expected_status = "completed"
    require(item.get("status") == expected_status, f"{task_id}: status must be {expected_status}")
    require(item.get("priority") == 101 - expected_round, f"{task_id}: priority mismatch")
    for field in ("id", "title", "objective", "deliverable", "acceptanceGates", "commitPolicy"):
        require(bool(item.get(field)), f"{task_id}: {field} is required")
    gates = item.get("acceptanceGates") or []
    require(gates[0] == expected_p15_task_gates[expected_round - 1], f"{task_id}: first acceptance gate mismatch")
    for gate in gates:
        require(gate_is_known(gate, targets), f"{task_id}: acceptanceGate is not known: {gate!r}")
    require("commit" in item.get("commitPolicy", "").lower(), f"{task_id}: commitPolicy must mention commit")
    require(item.get("commit") == "pending-current-commit", f"{task_id}: completed task must record pending current commit")
    require(bool(item.get("verification")), f"{task_id}: completed task must record verification")

p16_tasks = p16_manifest.get("tasks") or []
actual_p16_tasks = [
    item.get("id")
    for item in p16_tasks
    if isinstance(item, dict)
]
require(actual_p16_tasks == expected_tasks, f"P16 roadmap task ids mismatch: {actual_p16_tasks!r}")
p16_submission = p16_manifest.get("aiflowSubmission") or {}
require(p16_submission.get("status") == "submitted", "P16 aiflowSubmission.status must be submitted")
require(p16_submission.get("completedTasks") == expected_tasks[:1], "P16 completedTasks must contain the handoff task")
require(p16_submission.get("pendingTasks") == expected_tasks[1:], "P16 pendingTasks must match the remaining queue order")
p16_submission_commands = p16_submission.get("submissionCommands") or []
require(len(p16_submission_commands) == 3, "P16 submissionCommands must document all three aiflow submit calls")
for task_id in expected_tasks:
    require(
        any(task_id in command and "aiflow submit" in command for command in p16_submission_commands),
        f"P16 submissionCommands missing {task_id}",
    )
require("aiflow status" in str(p16_submission.get("queueStatusCommand") or ""), "P16 queueStatusCommand must document aiflow status")
p16_handoff = p16_manifest.get("previousBatchHandoff") or {}
require(p16_handoff.get("completedBatch") == "GOFLY-P15", "P16 previousBatchHandoff.completedBatch must be GOFLY-P15")
require(p16_handoff.get("completedTaskCount") == 3, "P16 previousBatchHandoff.completedTaskCount must be 3")
p16_runtime_policy = str(p16_submission.get("runtimeStatePolicy") or "")
for path in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "bench/current.txt", "bench/regression-report.json", "bench/summary.md", "bin/gofly", "docs/superpowers"):
    require(path in p16_runtime_policy, f"P16 runtimeStatePolicy missing {path!r}")
require("committed by the current agent or human" in str(p16_submission.get("safetyPolicy") or ""), "P16 safetyPolicy must document commit ownership")
for expected_round, item in enumerate(p16_tasks, start=1):
    if not isinstance(item, dict):
        missing.append(f"P16 roadmap task must be an object: {item!r}")
        continue
    task_id = item.get("id", "<missing>")
    require(item.get("round") == expected_round, f"{task_id}: round must be {expected_round}")
    expected_status = "completed" if expected_round == 1 else "queued"
    require(item.get("status") == expected_status, f"{task_id}: status must be {expected_status}")
    require(item.get("priority") == 101 - expected_round, f"{task_id}: priority mismatch")
    for field in ("id", "title", "objective", "deliverable", "acceptanceGates", "commitPolicy"):
        require(bool(item.get(field)), f"{task_id}: {field} is required")
    gates = item.get("acceptanceGates") or []
    require(gates[0] == expected_task_gates[expected_round - 1], f"{task_id}: first acceptance gate mismatch")
    for gate in gates:
        require(gate_is_known(gate, targets), f"{task_id}: acceptanceGate is not known: {gate!r}")
    require("commit" in item.get("commitPolicy", "").lower(), f"{task_id}: commitPolicy must mention commit")
    if expected_status == "completed":
        require(item.get("commit") == "pending-current-commit", f"{task_id}: completed task must record pending current commit")
        require(bool(item.get("verification")), f"{task_id}: completed task must record verification")
    else:
        require("commit" not in item or item.get("commit") in ("", None), f"{task_id}: queued task must not claim a completed commit")
        require("verification" not in item or item.get("verification") in ("", None), f"{task_id}: queued task must not claim completed verification")

if missing:
    print("governance boundary inventory check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("governance boundary inventory OK")
PY
