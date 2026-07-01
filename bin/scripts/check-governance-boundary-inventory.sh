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
post_r8_path = root / "docs" / "reference" / "framework-gap-post-r8-roadmap.json"
missing = []

expected_active_batch = "GOFLY-P12"
expected_converged_batch = "GOFLY-P12"
expected_tasks = [
    "GOFLY-P12-1-RPC-BENCHMARK-BUDGET-PROMOTION",
    "GOFLY-P12-2-GENERATED-UPGRADE-REAL-BRANCH",
    "GOFLY-P12-3-HOSTED-CLOUD-NATIVE-LIVE-CI",
]
expected_converged_tasks = expected_tasks
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
expected_task_gates = [
    "make rpc-boundary-check",
    "make generated-upgrade-dry-run-check",
    "make cloud-native-render-check",
]
actual_task_gates = [item.get("gate") for item in tasks if isinstance(item, dict)]
require(actual_task_gates == expected_task_gates, f"P12 task gates mismatch: {actual_task_gates!r}")
require(
    "budget promotion" in tasks[0].get("title", "").lower(),
    "P12 round 01 title must document RPC budget promotion",
)
require(
    "blocking" in tasks[0].get("objective", "").lower(),
    "P12 round 01 objective must document blocking budget promotion criteria",
)
for item in tasks:
    task_id = item.get("id")
    require(item.get("commit") == expected_converged_commits.get(task_id), f"{task_id}: commit mismatch")
    require(
        item.get("verification") == expected_converged_verification.get(task_id),
        f"{task_id}: verification mismatch",
    )

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

if missing:
    print("governance boundary inventory check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("governance boundary inventory OK")
PY
