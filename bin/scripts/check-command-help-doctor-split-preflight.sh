#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
evidence_path = root / "docs" / "reference" / "command-help-doctor-split-preflight.json"
readiness_path = root / "docs" / "reference" / "command-split-readiness.json"
dependency_map_path = root / "docs" / "reference" / "command-family-dependency-map.json"
layout_path = root / "docs" / "reference" / "project-layout-governance.json"
test_path = root / "cmd" / "gofly" / "internal" / "command" / "command_help_doctor_split_preflight_test.go"
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
    if " go test " in f" {gate} " or " go vet " in f" {gate} ":
        return True
    if gate.startswith("GOCACHE="):
        return " go test " in gate or " go vet " in gate or " make " in gate
    if gate.startswith("make "):
        return gate.removeprefix("make ").split()[0] in targets
    return gate.startswith("go test ") or gate.startswith("go vet ")


evidence = read_json(evidence_path)
readiness = read_json(readiness_path)
dependency_map = read_json(dependency_map_path)
layout = read_json(layout_path)
makefile = makefile_path.read_text(encoding="utf-8") if makefile_path.is_file() else ""
targets = make_target_names(makefile)
docs_check = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
test_source = test_path.read_text(encoding="utf-8") if test_path.is_file() else ""

require(evidence.get("schema") == "gofly.command_help_doctor_split_preflight.v1", "help/doctor split preflight schema mismatch")
require(evidence.get("status") == "completed-preflight", "help/doctor split preflight status must be completed-preflight")
require(evidence.get("package") == "cmd/gofly/internal/command", "help/doctor split preflight package mismatch")
require(
    evidence.get("acceptanceGate") == "make command-help-doctor-split-preflight-check",
    "help/doctor split preflight acceptanceGate mismatch",
)
require(evidence.get("dryRunOnly") is True, "help/doctor split preflight must be dryRunOnly")
require(evidence.get("noPhysicalMove") is True, "help/doctor split preflight must forbid physical movement")
require(evidence.get("selectedNextFamily") == "help", "selectedNextFamily must be help")
require(evidence.get("deferredNextFamily") == "doctor", "deferredNextFamily must be doctor")
require("command-help-doctor-split-preflight-check" in targets, "Makefile must expose command-help-doctor-split-preflight-check")
require("command-help-doctor-split-preflight-check" in docs_check, "docs-check must depend on command-help-doctor-split-preflight-check")

expected_help_files = sorted(path.name for path in command_dir.glob("help*.go"))
expected_doctor_files = ["doctor.go", "doctor_checks.go", "doctor_test.go"]
require(sorted(evidence.get("helpFiles") or []) == expected_help_files, "helpFiles must match current help*.go files")
require(sorted(evidence.get("doctorFiles") or []) == expected_doctor_files, "doctorFiles mismatch")
for filename in (evidence.get("helpFiles") or []) + (evidence.get("doctorFiles") or []):
    require((command_dir / filename).is_file(), f"preflight file is missing from command package: {filename}")

contracts = set(evidence.get("preflightContracts") or [])
expected_contracts = {
    "help remains reachable through root help dispatch and command-specific help routing",
    "help output stays stdout-only through the command output adapter",
    "doctor remains reachable through root command dispatch",
    "doctor --json stays stdout-only with stable nextActions fields",
    "bug --json supportBundle remains available for doctor remediation guidance",
    "no command files move during this preflight",
}
require(contracts == expected_contracts, "preflightContracts mismatch")
for token in ("ExecuteWithIO", "printCommandHelp", "doctorCommand", "bugCommand", "commandUsage", "supportBundle"):
    require(token in test_source, f"preflight test source must reference {token}")

required_tests = {
    "TestCommandHelpDoctorSplitPreflightEvidence",
    "TestCommandHelpDoctorSplitPreflightContracts",
    "TestCommandHelpDoctorSplitPreflightNoPhysicalMove",
}
require(set(evidence.get("goldenTests") or []) == required_tests, "goldenTests mismatch")
for test_name in required_tests:
    require(f"func {test_name}" in test_source, f"missing executable preflight test {test_name}")

for gate in evidence.get("requiredGates") or []:
    require(gate_is_known(str(gate), targets), f"help/doctor split preflight required gate is not known: {gate}")
require(
    "make command-help-doctor-split-preflight-check" in set(evidence.get("requiredGates") or []),
    "requiredGates must include command-help-doctor-split-preflight-check",
)

candidate_by_id = {
    item.get("id"): item
    for item in readiness.get("candidateFamilies") or []
    if isinstance(item, dict)
}
help_candidate = candidate_by_id.get("help") or {}
doctor_candidate = candidate_by_id.get("doctor") or {}
require(help_candidate.get("status") == "ready-for-single-family-split", "help candidate must be ready for single-family split")
require(doctor_candidate.get("status") == "deferred-until-help-split-validation", "doctor candidate must be deferred until help split validation")
require(
    "make command-help-doctor-split-preflight-check" in set(help_candidate.get("requiredGates") or []),
    "help candidate gates must include help/doctor split preflight check",
)
require(
    "make command-help-doctor-split-preflight-check" in set(doctor_candidate.get("requiredGates") or []),
    "doctor candidate gates must include help/doctor split preflight check",
)

blocked_by_id = {
    item.get("id"): item
    for item in readiness.get("blockedFamilies") or []
    if isinstance(item, dict)
}
shared_blocked = blocked_by_id.get("shared") or {}
require(shared_blocked.get("status") == "blocked-during-help-single-family-split", "shared blocked status mismatch")
require(
    "make command-help-doctor-split-preflight-check" in set(shared_blocked.get("requiredGates") or []),
    "shared blocked gates must include help/doctor split preflight check",
)

next_step = readiness.get("nextStep") or {}
require(next_step.get("id") == "P22-12-command-help-single-family-split", "readiness nextStep must move to help single-family split")
next_action = str(next_step.get("action") or "")
require("Move only the help family" in next_action, "nextStep action must focus on help")
require("Do not move doctor" in next_action, "nextStep action must explicitly block doctor movement")

family_by_id = {
    family.get("id"): family
    for family in dependency_map.get("families") or []
    if isinstance(family, dict)
}
for family_id in ("help", "doctor", "shared"):
    family = family_by_id.get(family_id) or {}
    require(
        "make command-help-doctor-split-preflight-check" in set(family.get("requiredGates") or []),
        f"dependency map {family_id} gates must include help/doctor split preflight check",
    )

reference_files = []
for family in (layout.get("referenceFileBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        reference_files.extend(family.get("files") or [])
require("command-help-doctor-split-preflight.json" in reference_files, "referenceFileBoundaries must index command-help-doctor-split-preflight.json")

script_files = []
for family in (layout.get("scriptFamilyBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        script_files.extend(family.get("files") or [])
require("check-command-help-doctor-split-preflight.sh" in script_files, "scriptFamilyBoundaries must index check-command-help-doctor-split-preflight.sh")

admission = evidence.get("physicalSplitAdmission") or {}
require(admission.get("status") == "ready-for-help-single-family-split", "physicalSplitAdmission status mismatch")
require("Move only the help family" in str(admission.get("nextAllowedAction") or ""), "nextAllowedAction must limit movement to help")
require("Do not move doctor" in str(admission.get("blockedAction") or ""), "blockedAction must block doctor movement")
require("Restore all help files" in str(admission.get("rollbackRequirement") or ""), "rollbackRequirement must describe help restore path")

if missing:
    print("command help/doctor split preflight check failed:")
    for item in missing:
        print(f"- {item}")
    sys.exit(1)

print("command help/doctor split preflight OK")
PY
