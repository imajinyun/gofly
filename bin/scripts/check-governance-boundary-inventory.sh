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
missing = []

expected_active_batch = "GOFLY-GOV-10R6"
expected_converged_batch = "GOFLY-GOV-10R5"
expected_tasks = [f"{expected_active_batch}-{idx:02d}" for idx in range(1, 11)]
expected_converged_tasks = [f"{expected_converged_batch}-{idx:02d}" for idx in range(1, 11)]
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
        "status": "in-progress",
        "taskPrefix": "GOFLY-GOV-10R6-",
        "roundCount": 10,
    },
}
expected_converged_commits = {
    "GOFLY-GOV-10R5-01": "faaa720",
    "GOFLY-GOV-10R5-02": "f082543",
    "GOFLY-GOV-10R5-03": "8c2d7dd",
    "GOFLY-GOV-10R5-04": "5cd7dfc",
    "GOFLY-GOV-10R5-05": "22ea90f",
    "GOFLY-GOV-10R5-06": "3b8e940",
    "GOFLY-GOV-10R5-07": "f81aeeb",
    "GOFLY-GOV-10R5-08": "a054074",
    "GOFLY-GOV-10R5-09": "ffcc87f",
    "GOFLY-GOV-10R5-10": "self",
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

makefile = read_text(root / "Makefile")
gitignore = read_text(root / ".gitignore")
governance_script = read_text(root / "bin" / "scripts" / "governance-10-rounds.sh")
targets = make_target_names(makefile)

require(manifest.get("schema") == "gofly.governance_boundary_inventory.v1", "schema must be gofly.governance_boundary_inventory.v1")
require(p10_manifest.get("schema") == "gofly.governance_p10_roadmap.v1", "P10 roadmap schema mismatch")
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
require(
    tasks[0].get("gate") == "make framework-gap-check",
    "R6 round 01 must use framework-gap-check as the roadmap acceptance gate",
)
require(
    tasks[0].get("deliverable", "").lower().find("non-community") >= 0,
    "R6 round 01 deliverable must document the non-community comparison scope",
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

require(convergence_manifest.get("schema") == "gofly.governance_convergence_verification.v1", "convergence verification schema mismatch")
require(convergence_manifest.get("aiflowTask") == "GOFLY-GOV-10R5-10", "convergence verification aiflowTask mismatch")
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
require(len(round_commits) == 10, "convergence verification must track 10 round commits")
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
require(execution.get("blocker") == "none", "convergence verification aiflowExecution.blocker must be none")
require("commit and push" in str(execution.get("goflyImpact") or ""), "convergence verification aiflowExecution.goflyImpact must document aiflow commit policy")
previous_handoff = convergence_manifest.get("previousBatchHandoff") or {}
require("GOFLY-GOV-10R4-10" in set(previous_handoff.get("supersedes") or []), "previousBatchHandoff must supersede GOFLY-GOV-10R4-10")
require("R5 active batch" in str(previous_handoff.get("reason") or ""), "previousBatchHandoff.reason must document R5 active batch handoff")
previous_completion_policy = str(previous_handoff.get("completionPolicy") or "")
for needle in ("GOFLY-GOV-10R5-10", "make governance-boundary-inventory-check", "make governance-report-check", "current agent or human"):
    require(needle in previous_completion_policy, f"previousBatchHandoff.completionPolicy missing {needle!r}")

p10_rounds = p10_manifest.get("rounds") or []
require(len(p10_rounds) == 10, "P10 roadmap must contain 10 rounds")
actual_p10_ids = [
    item.get("id")
    for item in p10_rounds
    if isinstance(item, dict)
]
expected_p10_ids = [
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
require(actual_p10_ids == expected_p10_ids, f"P10 roadmap ids mismatch: {actual_p10_ids!r}")
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

if missing:
    print("governance boundary inventory check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("governance boundary inventory OK")
PY
