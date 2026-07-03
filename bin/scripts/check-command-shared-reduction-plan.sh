#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
plan_path = root / "docs" / "reference" / "command-shared-reduction-plan.json"
readiness_path = root / "docs" / "reference" / "command-split-readiness.json"
dependency_map_path = root / "docs" / "reference" / "command-family-dependency-map.json"
layout_path = root / "docs" / "reference" / "project-layout-governance.json"
test_path = root / "cmd" / "gofly" / "internal" / "command" / "command_shared_reduction_plan_test.go"
command_dir = root / "cmd" / "gofly" / "internal" / "command"
makefile_path = root / "Makefile"
missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def read_json(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return {}
    return json.loads(path.read_text(encoding="utf-8"))


def make_target_names(makefile):
    return set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.M))


def gate_is_known(gate, targets):
    if gate == "git diff --check":
        return True
    if gate.startswith("GOCACHE="):
        return " go test " in gate or " go vet " in gate or " make " in gate
    if gate.startswith("make "):
        return gate.removeprefix("make ").split()[0] in targets
    return gate.startswith("go test ") or gate.startswith("go vet ")


plan = read_json(plan_path)
readiness = read_json(readiness_path)
dependency_map = read_json(dependency_map_path)
layout = read_json(layout_path)
makefile = makefile_path.read_text(encoding="utf-8") if makefile_path.is_file() else ""
targets = make_target_names(makefile)
docs_check = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
test_source = test_path.read_text(encoding="utf-8") if test_path.is_file() else ""

require(plan.get("schema") == "gofly.command_shared_reduction_plan.v1", "shared reduction plan schema mismatch")
require(plan.get("status") == "completed-preflight", "shared reduction plan status must be completed-preflight")
require(plan.get("family") == "shared", "shared reduction plan family mismatch")
require(plan.get("package") == "cmd/gofly/internal/command", "shared reduction plan package mismatch")
require(plan.get("acceptanceGate") == "make command-shared-reduction-plan-check", "shared reduction plan acceptanceGate mismatch")
require(plan.get("planningOnly") is True, "shared reduction plan must be marked planningOnly")
require(plan.get("noPhysicalMove") is True, "shared reduction plan must be marked noPhysicalMove")
require("command-shared-reduction-plan-check" in targets, "Makefile must expose command-shared-reduction-plan-check")
require("command-shared-reduction-plan-check" in docs_check, "docs-check must depend on command-shared-reduction-plan-check")

required_domains = {
    "output-io",
    "json-envelope",
    "root-wiring",
    "path-flags",
    "template-source",
}
domains = {
    item.get("id"): item
    for item in plan.get("reductionDomains") or []
    if isinstance(item, dict)
}
require(set(domains) == required_domains, "reductionDomains must cover output/json/root/path/template domains exactly")
for domain_id, domain in domains.items():
    files = domain.get("files") or []
    require(len(files) >= 1, f"{domain_id}: files must not be empty")
    for filename in files:
        require((command_dir / filename).is_file(), f"{domain_id}: source file is missing: {filename}")
        require(filename in test_source, f"{domain_id}: test must reference source file {filename}")
    require(len(str(domain.get("currentCoupling") or "").split()) >= 8, f"{domain_id}: currentCoupling must be descriptive")
    require(len(str(domain.get("targetBoundary") or "").split()) >= 8, f"{domain_id}: targetBoundary must be descriptive")
    require(len(str(domain.get("firstAction") or "").split()) >= 8, f"{domain_id}: firstAction must be actionable")
    require(domain.get("physicalMoveAllowed") is False, f"{domain_id}: physicalMoveAllowed must stay false")
    for gate in domain.get("requiredGates") or []:
        require(gate_is_known(str(gate), targets), f"{domain_id}: unknown required gate {gate}")

order = plan.get("recommendedOrder") or []
require(order == ["output-io", "json-envelope", "root-wiring", "path-flags", "template-source"], "recommendedOrder mismatch")
for domain_id in order:
    require(domain_id in test_source, f"recommended order test must include {domain_id}")

required_tests = {
    "TestCommandSharedReductionPlanEvidence",
    "TestCommandSharedReductionPlanDomains",
}
require(set(plan.get("goldenTests") or []) == required_tests, "goldenTests mismatch")
for test_name in required_tests:
    require(f"func {test_name}" in test_source, f"missing executable test evidence for {test_name}")

for gate in plan.get("requiredGates") or []:
    require(gate_is_known(str(gate), targets), f"shared reduction required gate is not known: {gate}")
require("make command-shared-reduction-plan-check" in set(plan.get("requiredGates") or []), "requiredGates must include command-shared-reduction-plan-check")

candidate_by_id = {
    item.get("id"): item
    for item in readiness.get("candidateFamilies") or []
    if isinstance(item, dict)
}
completed_by_id = {
    item.get("id"): item
    for item in readiness.get("completedFamilies") or []
    if isinstance(item, dict)
}
help_completed = completed_by_id.get("help") or {}
require(help_completed.get("status") == "physical-split-completed", "help completed status mismatch after P22-12")
require("make command-help-doctor-split-preflight-check" in set(help_completed.get("requiredGates") or []), "help completed gates must include help/doctor split preflight")
doctor_candidate = candidate_by_id.get("doctor") or {}
require(doctor_candidate.get("status") == "deferred-until-help-split-validation", "doctor candidate status mismatch after help split")
joined = " ".join(doctor_candidate.get("requiredPreSplitActions") or [])
require("help" in joined, "doctor candidate actions must reference the help split validation")
require("make command-help-doctor-split-preflight-check" in set(doctor_candidate.get("requiredGates") or []), "doctor candidate gates must include help/doctor split preflight")

blocked = {
    item.get("id"): item
    for item in readiness.get("blockedFamilies") or []
    if isinstance(item, dict)
}
shared_blocked = blocked.get("shared") or {}
require(shared_blocked.get("status") == "blocked-during-help-single-family-split", "shared blocked status mismatch")
require("make command-shared-reduction-plan-check" in set(shared_blocked.get("requiredGates") or []), "shared blocked gates must include shared reduction plan check")
require("make command-output-json-adapter-dry-run-check" in set(shared_blocked.get("requiredGates") or []), "shared blocked gates must include output/json adapter dry-run check")
require("make command-help-doctor-split-preflight-check" in set(shared_blocked.get("requiredGates") or []), "shared blocked gates must include help/doctor split preflight check")

next_step = readiness.get("nextStep") or {}
require(next_step.get("id") == "P22-13-command-doctor-single-family-preflight-refresh", "readiness nextStep must refresh doctor split preflight")
require("without moving doctor" in str(next_step.get("action") or ""), "readiness nextStep action must not move doctor yet")

family_by_id = {
    family.get("id"): family
    for family in dependency_map.get("families") or []
    if isinstance(family, dict)
}
shared_family = family_by_id.get("shared") or {}
require(shared_family.get("splitRecommendation") == "blocked", "dependency map shared family must remain blocked")
require("make command-shared-reduction-plan-check" in set(shared_family.get("requiredGates") or []), "dependency map shared gates must include shared reduction plan check")
require("output/json adapter dry-run" in " ".join(shared_family.get("requiredPreSplitActions") or []), "dependency map shared actions must require output/json adapter dry-run")

reference_files = []
for family in (layout.get("referenceFileBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        reference_files.extend(family.get("files") or [])
require("command-shared-reduction-plan.json" in reference_files, "referenceFileBoundaries must index command-shared-reduction-plan.json")
require("command-output-json-adapter-dry-run.json" in reference_files, "referenceFileBoundaries must index command-output-json-adapter-dry-run.json")
require("command-help-doctor-split-preflight.json" in reference_files, "referenceFileBoundaries must index command-help-doctor-split-preflight.json")

script_files = []
for family in (layout.get("scriptFamilyBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        script_files.extend(family.get("files") or [])
require("check-command-shared-reduction-plan.sh" in script_files, "scriptFamilyBoundaries must index check-command-shared-reduction-plan.sh")

admission = plan.get("physicalSplitAdmission") or {}
require(admission.get("status") == "blocked-until-adapters", "physicalSplitAdmission status mismatch")
require("Restore" in str(admission.get("rollbackRequirement") or ""), "physicalSplitAdmission rollbackRequirement must describe restore path")

if missing:
    print("command shared reduction plan check failed:")
    for item in missing:
        print(f"- {item}")
    sys.exit(1)

print("command shared reduction plan OK")
PY
