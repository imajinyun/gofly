#!/usr/bin/env sh
set -eu

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

export GOCACHE="$tmpdir/gocache"
export GOTMPDIR="$tmpdir/gotmp"
mkdir -p "$GOCACHE" "$GOTMPDIR"

doctor_json="$tmpdir/doctor.json"
bug_json="$tmpdir/bug.json"
release_json="$tmpdir/release.json"

go run ./cmd/gofly doctor --json > "$doctor_json"
go run ./cmd/gofly bug --json > "$bug_json"
API_BASE_REF=definitely-missing-release-base-ref go run ./cmd/gofly release check --json --strict > "$release_json"

python3 - "$doctor_json" "$bug_json" "$release_json" <<'PY'
import json
import pathlib
import sys

doctor = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
bug = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))
release = json.loads(pathlib.Path(sys.argv[3]).read_text(encoding="utf-8"))

missing = []

if not doctor.get("nextActions"):
    missing.append("doctor --json: missing non-empty nextActions")
for check in doctor.get("checks", []):
    if check.get("status") in {"warn", "fail"} and not (check.get("nextActions") or check.get("fix_hint")):
        missing.append(f"doctor --json: {check.get('name')} lacks nextActions/fix_hint")

support = bug.get("supportBundle") or {}
if support.get("schema") != "gofly.support_bundle.v1":
    missing.append("bug --json: supportBundle.schema is not gofly.support_bundle.v1")
if not support.get("commands"):
    missing.append("bug --json: supportBundle.commands is empty")
if not support.get("redaction"):
    missing.append("bug --json: supportBundle.redaction is empty")
if not bug.get("nextActions"):
    missing.append("bug --json: missing nextActions")

if release.get("command") != "release.check":
    missing.append("release check --json: command is not release.check")
data = release.get("data") or {}
if "summary" not in data or "checks" not in data:
    missing.append("release check --json: missing data.summary/checks")
error = release.get("error")
if error is not None and not error.get("remediation"):
    missing.append("release check --json: error lacks remediation")

manifest_path = pathlib.Path("docs/reference/dx-support-bundle.json")
if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/dx-support-bundle.json: file is missing")

if manifest.get("schema") != "gofly.dx_support_bundle.v1":
    missing.append("dx support bundle schema must be gofly.dx_support_bundle.v1")
if manifest.get("acceptanceGate") != "make dx-troubleshooting-check":
    missing.append("dx support bundle acceptanceGate must be make dx-troubleshooting-check")

surfaces = manifest.get("surfaces") or []
surface_by_command = {
    item.get("command"): item for item in surfaces if isinstance(item, dict)
}
required_surfaces = {
    "gofly doctor --json": {"version", "go", "os", "arch", "checks", "summary", "nextActions"},
    "gofly bug --json": {"tool", "version", "environment", "checks", "supportBundle", "nextActions"},
    "gofly release check --json --strict": {"ok", "command", "data.summary", "data.checks", "error.remediation"},
    "gofly ai new --json --apply --verify": {"data.verification", "data.verifyRan", "data.verifyPassed", "data.nextActions"},
}
for command, fields in required_surfaces.items():
    surface = surface_by_command.get(command)
    if not surface:
        missing.append(f"dx support bundle surfaces missing {command!r}")
        continue
    stable = set(surface.get("stableFields") or [])
    absent = fields - stable
    if absent:
        missing.append(f"dx support bundle {command}: stableFields missing {sorted(absent)!r}")
    if surface.get("nextActionRequired") is True and "nextActions" not in " ".join(stable):
        missing.append(f"dx support bundle {command}: nextActionRequired surface must declare nextActions")
    if surface.get("failureGuidance") in ("", None):
        missing.append(f"dx support bundle {command}: failureGuidance is required")

bug_surface = surface_by_command.get("gofly bug --json") or {}
if bug_surface.get("supportBundleSchema") != "gofly.support_bundle.v1":
    missing.append("dx support bundle bug surface must reference gofly.support_bundle.v1")
redaction = set(bug_surface.get("redactionPolicy") or [])
for term in ("Authorization", "Cookie", "Set-Cookie", "*TOKEN*", "*SECRET*", "*PASSWORD*"):
    if term not in redaction:
        missing.append(f"dx support bundle redactionPolicy missing {term!r}")

failure_report = manifest.get("generatedFailureReport") or {}
if failure_report.get("schema") != "gofly.generated_project_failure_report.v1":
    missing.append("generatedFailureReport.schema must be gofly.generated_project_failure_report.v1")
for field in ("command", "status", "output", "error", "nextActions"):
    if field not in set(failure_report.get("fields") or []):
        missing.append(f"generatedFailureReport.fields missing {field!r}")
if failure_report.get("boundedOutput") is not True:
    missing.append("generatedFailureReport.boundedOutput must be true")
if failure_report.get("outputLimitBytes") != 4096:
    missing.append("generatedFailureReport.outputLimitBytes must be 4096")
if set(failure_report.get("statusValues") or []) != {"passed", "failed", "skipped"}:
    missing.append("generatedFailureReport.statusValues must be passed/failed/skipped")
if failure_report.get("rerunGuidanceField") != "nextActions":
    missing.append("generatedFailureReport.rerunGuidanceField must be nextActions")
if failure_report.get("redactionRequired") is not True:
    missing.append("generatedFailureReport.redactionRequired must be true")
failure_redaction = set(failure_report.get("redactionTerms") or [])
for term in ("Authorization", "Cookie", "Set-Cookie", "GOFLY_LLM_*", "*TOKEN*", "*SECRET*", "*PASSWORD*"):
    if term not in failure_redaction:
        missing.append(f"generatedFailureReport.redactionTerms missing {term!r}")
evidence_producers = failure_report.get("evidenceProducers") or []
producer_by_command = {
    item.get("command"): item for item in evidence_producers if isinstance(item, dict)
}
producer = producer_by_command.get("gofly ai new --json --apply --verify")
if not producer:
    missing.append("generatedFailureReport.evidenceProducers missing gofly ai new --json --apply --verify")
else:
    fields = set(producer.get("fields") or [])
    for field in ("data.verification", "data.nextActions"):
        if field not in fields:
            missing.append(f"generatedFailureReport producer missing {field!r}")
if not failure_report.get("ciArtifactUsage"):
    missing.append("generatedFailureReport.ciArtifactUsage is required")

handoff = manifest.get("remediationHandoff") or {}
if handoff.get("schema") != "gofly.remediation_handoff.v1":
    missing.append("remediationHandoff.schema must be gofly.remediation_handoff.v1")
if handoff.get("aiflowTask") != "GOFLY-GOV-10P9-09":
    missing.append("remediationHandoff.aiflowTask must be GOFLY-GOV-10P9-09")
for previous in ("GOFLY-KG-07-AI-REMEDIATION-AIFLOW", "GOFLY-GOV-10R7-09"):
    if previous not in set(handoff.get("supersedes") or []):
        missing.append(f"remediationHandoff.supersedes missing {previous!r}")
if handoff.get("owner") != "human-or-current-agent":
    missing.append("remediationHandoff.owner must keep commit ownership outside aiflow")
commit_policy = str(handoff.get("commitPolicy") or "")
for needle in ("must not create commits", "current agent or human", "gates pass"):
    if needle not in commit_policy:
        missing.append(f"remediationHandoff.commitPolicy missing {needle!r}")
allowed_actions = set(handoff.get("allowedActions") or [])
for action in ("queue remediation tasks", "run diagnostic commands", "produce patch plans", "produce bounded failure reports"):
    if action not in allowed_actions:
        missing.append(f"remediationHandoff.allowedActions missing {action!r}")
forbidden_actions = set(handoff.get("forbiddenActions") or [])
for action in ("git commit", "git push", "modify docs/superpowers/", "stage runtime state"):
    if action not in forbidden_actions:
        missing.append(f"remediationHandoff.forbiddenActions missing {action!r}")
handoff_inputs = set(handoff.get("inputs") or [])
for item in ("gofly doctor --json", "gofly release check --json --strict", "gofly bug --json", "gofly.generated_project_failure_report.v1"):
    if item not in handoff_inputs:
        missing.append(f"remediationHandoff.inputs missing {item!r}")
handoff_gates = set(handoff.get("requiredGates") or [])
for gate in ("make dx-troubleshooting-check", "make governance-report-check"):
    if gate not in handoff_gates:
        missing.append(f"remediationHandoff.requiredGates missing {gate!r}")
if not any("TestCLIJSONContractGoldens" in gate for gate in handoff_gates):
    missing.append("remediationHandoff.requiredGates must include CLI JSON contract tests")
output_fields = set(handoff.get("outputFields") or [])
for field in ("taskId", "sourceCommand", "remediation", "nextActions", "gates", "commitPolicy"):
    if field not in output_fields:
        missing.append(f"remediationHandoff.outputFields missing {field!r}")
completion_policy = str(handoff.get("completionPolicy") or "")
for needle in ("aiflow task complete", "commit", "passing gates", "no runtime state staged"):
    if needle not in completion_policy:
        missing.append(f"remediationHandoff.completionPolicy missing {needle!r}")

for step in ("run gofly doctor --json", "run gofly release check --json --strict", "run gofly bug --json"):
    if step not in set(manifest.get("supportWorkflow") or []):
        missing.append(f"dx support bundle supportWorkflow missing {step!r}")

p9_closeout = manifest.get("p9RemediationCloseout") or {}
if p9_closeout.get("schema") != "gofly.p9_remediation_closeout.v1":
    missing.append("p9RemediationCloseout.schema must be gofly.p9_remediation_closeout.v1")
if p9_closeout.get("aiflowTask") != "GOFLY-GOV-10P9-09":
    missing.append("p9RemediationCloseout.aiflowTask must be GOFLY-GOV-10P9-09")
if p9_closeout.get("acceptanceGate") != "make dx-troubleshooting-check":
    missing.append("p9RemediationCloseout.acceptanceGate must be make dx-troubleshooting-check")
for command in ("gofly doctor --json", "gofly release check --json --strict", "gofly bug --json", "gofly ai new --json --apply --verify"):
    if command not in set(p9_closeout.get("sourceCommands") or []):
        missing.append(f"p9RemediationCloseout.sourceCommands missing {command!r}")
for source in ("nextActions", "error.remediation", "data.nextActions"):
    if source not in set(p9_closeout.get("requiredNextActionSources") or []):
        missing.append(f"p9RemediationCloseout.requiredNextActionSources missing {source!r}")
for field in ("taskId", "sourceCommand", "remediation", "nextActions", "gates", "commitPolicy"):
    if field not in set(p9_closeout.get("handoffOutputFields") or []):
        missing.append(f"p9RemediationCloseout.handoffOutputFields missing {field!r}")
for needle in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "docs/superpowers"):
    if needle not in str(p9_closeout.get("runtimeStatePolicy") or ""):
        missing.append(f"p9RemediationCloseout.runtimeStatePolicy missing {needle!r}")
for needle in ("GOFLY-GOV-10P9-09", "dx-troubleshooting-check", "CLI JSON contract", "no runtime state"):
    if needle not in str(p9_closeout.get("completionPolicy") or ""):
        missing.append(f"p9RemediationCloseout.completionPolicy missing {needle!r}")
for needle in ("doctor", "release", "generated-project", "support-bundle", "nextActions", "error.remediation"):
    if needle not in str(p9_closeout.get("nextActionPolicy") or ""):
        missing.append(f"p9RemediationCloseout.nextActionPolicy missing {needle!r}")

adoption_loop = manifest.get("troubleshootingAdoptionLoop") or {}
if adoption_loop.get("schema") != "gofly.troubleshooting_adoption_loop.v1":
    missing.append("troubleshootingAdoptionLoop.schema must be gofly.troubleshooting_adoption_loop.v1")
if adoption_loop.get("acceptanceGate") != "make dx-troubleshooting-check":
    missing.append("troubleshootingAdoptionLoop.acceptanceGate must be make dx-troubleshooting-check")
if adoption_loop.get("dashboardReportField") != "dxSupportBundle.troubleshootingAdoptionLoop":
    missing.append("troubleshootingAdoptionLoop.dashboardReportField mismatch")
if adoption_loop.get("aiflowHandoff") != "gofly.remediation_handoff.v1":
    missing.append("troubleshootingAdoptionLoop.aiflowHandoff must reference gofly.remediation_handoff.v1")
if adoption_loop.get("commitOwner") != "human-or-current-agent":
    missing.append("troubleshootingAdoptionLoop.commitOwner must keep commits outside aiflow")
runtime_policy = str(adoption_loop.get("runtimeStatePolicy") or "")
for needle in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "docs/superpowers"):
    if needle not in runtime_policy:
        missing.append(f"troubleshootingAdoptionLoop.runtimeStatePolicy missing {needle!r}")
steps = adoption_loop.get("steps") or []
steps_by_id = {
    item.get("id"): item for item in steps if isinstance(item, dict)
}
expected_steps = {
    "diagnose-environment": "gofly doctor --json",
    "check-release-gates": "gofly release check --json --strict",
    "collect-support-bundle": "gofly bug --json",
    "attach-generated-failure": "gofly ai new --json --apply --verify",
}
if set(steps_by_id) != set(expected_steps):
    missing.append(
        "troubleshootingAdoptionLoop steps mismatch: "
        f"missing={sorted(set(expected_steps) - set(steps_by_id))} "
        f"extra={sorted(set(steps_by_id) - set(expected_steps))}"
    )
surface_commands = set(surface_by_command)
for step_id, command in expected_steps.items():
    step = steps_by_id.get(step_id) or {}
    if step.get("sourceCommand") != command:
        missing.append(f"troubleshootingAdoptionLoop {step_id}: sourceCommand mismatch")
    if command not in surface_commands:
        missing.append(f"troubleshootingAdoptionLoop {step_id}: sourceCommand is not a declared DX surface")
    for field in ("evidenceArtifact", "nextActionSource", "handoffBoundary"):
        if not step.get(field):
            missing.append(f"troubleshootingAdoptionLoop {step_id}: {field} is required")
    if not step.get("requiredFields"):
        missing.append(f"troubleshootingAdoptionLoop {step_id}: requiredFields is required")
    if len(str(step.get("handoffBoundary") or "").split()) < 10:
        missing.append(f"troubleshootingAdoptionLoop {step_id}: handoffBoundary must be actionable")
if steps_by_id.get("collect-support-bundle", {}).get("redactionRequired") is not True:
    missing.append("troubleshootingAdoptionLoop collect-support-bundle must require redaction")
if steps_by_id.get("attach-generated-failure", {}).get("redactionRequired") is not True:
    missing.append("troubleshootingAdoptionLoop attach-generated-failure must require redaction")

remediation_loop = manifest.get("remediationLoopContract") or {}
if remediation_loop.get("schema") != "gofly.remediation_loop_contract.v1":
    missing.append("remediationLoopContract.schema must be gofly.remediation_loop_contract.v1")
if remediation_loop.get("acceptanceGate") != "make dx-troubleshooting-check":
    missing.append("remediationLoopContract.acceptanceGate must be make dx-troubleshooting-check")
if remediation_loop.get("dashboardReportField") != "dxSupportBundle.remediationLoopContract":
    missing.append("remediationLoopContract.dashboardReportField mismatch")
if len(str(remediation_loop.get("purpose") or "").split()) < 15:
    missing.append("remediationLoopContract.purpose must describe the support workflow")

source_contracts = remediation_loop.get("sourceContracts") or []
source_by_id = {
    item.get("id"): item for item in source_contracts if isinstance(item, dict)
}
expected_sources = {
    "doctor-json": "gofly doctor --json",
    "release-check-json": "gofly release check --json --strict",
    "support-bundle-json": "gofly bug --json",
    "generated-failure-report": "gofly ai new --json --apply --verify",
}
if set(source_by_id) != set(expected_sources):
    missing.append(
        "remediationLoopContract sourceContracts mismatch: "
        f"missing={sorted(set(expected_sources) - set(source_by_id))} "
        f"extra={sorted(set(source_by_id) - set(expected_sources))}"
    )
for source_id, command in expected_sources.items():
    item = source_by_id.get(source_id) or {}
    if item.get("sourceCommand") != command:
        missing.append(f"remediationLoopContract {source_id}: sourceCommand mismatch")
    if command not in surface_commands:
        missing.append(f"remediationLoopContract {source_id}: sourceCommand is not a declared DX surface")
    for field in ("evidenceArtifact", "requiredFields", "nextActionSource", "dashboardFields", "adopterAction"):
        if not item.get(field):
            missing.append(f"remediationLoopContract {source_id}: {field} is required")
    if len(str(item.get("adopterAction") or "").split()) < 10:
        missing.append(f"remediationLoopContract {source_id}: adopterAction must be actionable")
    for dashboard_field in item.get("dashboardFields") or []:
        if "." not in dashboard_field:
            missing.append(f"remediationLoopContract {source_id}: dashboard field must be namespaced")
if source_by_id.get("support-bundle-json", {}).get("redactionRequired") is not True:
    missing.append("remediationLoopContract support-bundle-json must require redaction")
if source_by_id.get("generated-failure-report", {}).get("redactionRequired") is not True:
    missing.append("remediationLoopContract generated-failure-report must require redaction")

migration_routes = remediation_loop.get("migrationRoutes") or []
route_by_id = {
    item.get("id"): item for item in migration_routes if isinstance(item, dict)
}
expected_routes = {
    "gin-to-gofly",
    "go-zero-to-gofly",
    "kratos-to-gofly",
    "kitex-with-gofly",
}
if set(route_by_id) != expected_routes:
    missing.append(
        "remediationLoopContract migrationRoutes mismatch: "
        f"missing={sorted(expected_routes - set(route_by_id))} "
        f"extra={sorted(set(route_by_id) - expected_routes)}"
    )
for route_id, item in route_by_id.items():
    if item.get("source") != "docs/reference/framework-gap-adopter-proof.json":
        missing.append(f"remediationLoopContract {route_id}: source mismatch")
    for field in ("example", "gate", "rollbackAction", "supportBundleAction"):
        if not item.get(field):
            missing.append(f"remediationLoopContract {route_id}: {field} is required")
    if "gofly bug --json" not in str(item.get("supportBundleAction") or ""):
        missing.append(f"remediationLoopContract {route_id}: supportBundleAction must mention gofly bug --json")
    if len(str(item.get("rollbackAction") or "").split()) < 8:
        missing.append(f"remediationLoopContract {route_id}: rollbackAction must be actionable")

dashboard_evidence = set(remediation_loop.get("dashboardEvidence") or [])
for field in (
    "dxSupportBundle.remediationHandoff.schema",
    "dxSupportBundle.remediationHandoff.aiflowTask",
    "dxSupportBundle.troubleshootingAdoptionLoop",
    "releaseAdoptionContract.supplyChainEnforcementCount",
    "generatedUpgradeDryRun.profileCount",
    "benchmark.adopterPerformanceContract",
):
    if field not in dashboard_evidence:
        missing.append(f"remediationLoopContract.dashboardEvidence missing {field!r}")
queue_policy = remediation_loop.get("aiflowQueuePolicy") or {}
if queue_policy.get("taskPrefix") != "GOFLY-GOV-10P9-09":
    missing.append("remediationLoopContract.aiflowQueuePolicy.taskPrefix mismatch")
if "GOFLY-GOV-10R7-09" not in set(queue_policy.get("supersedes") or []):
    missing.append("remediationLoopContract.aiflowQueuePolicy.supersedes missing GOFLY-GOV-10R7-09")
if queue_policy.get("completionGate") != "make dx-troubleshooting-check":
    missing.append("remediationLoopContract.aiflowQueuePolicy.completionGate mismatch")
if queue_policy.get("commitOwner") != "human-or-current-agent":
    missing.append("remediationLoopContract.aiflowQueuePolicy.commitOwner mismatch")
for action in ("queue remediation tasks", "run diagnostic commands", "produce patch plans", "produce bounded failure reports"):
    if action not in set(queue_policy.get("allowed") or []):
        missing.append(f"remediationLoopContract.aiflowQueuePolicy.allowed missing {action!r}")
for action in ("git commit", "git push", "modify docs/superpowers/", "stage runtime state"):
    if action not in set(queue_policy.get("forbidden") or []):
        missing.append(f"remediationLoopContract.aiflowQueuePolicy.forbidden missing {action!r}")
for needle in ("source command", "nextActionSource", "dashboard evidence", "gate", "rollback"):
    if needle not in str(remediation_loop.get("nextActionPolicy") or ""):
        missing.append(f"remediationLoopContract.nextActionPolicy missing {needle!r}")
for doc_path in manifest.get("docs") or []:
    if not pathlib.Path(doc_path).is_file():
        missing.append(f"dx support bundle docs path is missing: {doc_path}")

docs = {
    pathlib.Path("docs/reference/cli-json-contracts.md"): [
        "nextActions",
        "supportBundle",
        "gofly.support_bundle.v1",
        "gofly.dx_support_bundle.v1",
        "gofly.generated_project_failure_report.v1",
        "outputLimitBytes",
        "rerunGuidanceField",
        "gofly.remediation_handoff.v1",
        "gofly.troubleshooting_adoption_loop.v1",
        "gofly.remediation_loop_contract.v1",
        "commitPolicy",
    ],
    pathlib.Path("docs/operations/troubleshooting.md"): [
        "gofly doctor --json",
        "gofly bug --json",
        "gofly release check --json --strict",
        "support bundle",
        "generated project verification failure",
        "4096 bytes",
        "nextActions",
        "gofly.remediation_handoff.v1",
        "gofly.troubleshooting_adoption_loop.v1",
        "gofly.remediation_loop_contract.v1",
        "must not create commits",
    ],
}
for path, needles in docs.items():
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("dx troubleshooting check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("dx troubleshooting governance ok")
PY
