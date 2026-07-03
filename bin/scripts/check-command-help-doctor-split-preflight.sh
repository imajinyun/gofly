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
require(evidence.get("status") == "help-and-doctor-physical-split-completed", "help/doctor split evidence status must be help-and-doctor-physical-split-completed")
require(evidence.get("package") == "cmd/gofly/internal/command", "help/doctor split preflight package mismatch")
require(
    evidence.get("acceptanceGate") == "make command-help-doctor-split-preflight-check",
    "help/doctor split preflight acceptanceGate mismatch",
)
require(evidence.get("dryRunOnly") is False, "help/doctor split evidence must no longer be dryRunOnly after P22-12")
require(evidence.get("noPhysicalMove") is False, "help/doctor split evidence must allow the completed help physical move")
require(evidence.get("helpPhysicalSplitDone") is True, "help/doctor split evidence must record helpPhysicalSplitDone=true")
require(evidence.get("helpPackage") == "cmd/gofly/internal/command/help", "help/doctor split evidence helpPackage mismatch")
require(evidence.get("commandAdapter") == "help_adapter.go", "help/doctor split evidence commandAdapter mismatch")
require(evidence.get("doctorPhysicalSplitDone") is True, "doctorPhysicalSplitDone must be true after P22-14")
require(evidence.get("doctorPackage") == "cmd/gofly/internal/command/doctor", "doctorPackage mismatch")
require(evidence.get("doctorCommandAdapter") == "doctor_adapter.go", "doctorCommandAdapter mismatch")
require(evidence.get("selectedNextFamily") == "doctor", "selectedNextFamily must be doctor")
require(evidence.get("deferredNextFamily") == "", "deferredNextFamily must be empty after P22-14")
require(evidence.get("doctorPreflightRefreshed") is True, "doctorPreflightRefreshed must be true after P22-13")
require("command-help-doctor-split-preflight-check" in targets, "Makefile must expose command-help-doctor-split-preflight-check")
require("command-help-doctor-split-preflight-check" in docs_check, "docs-check must depend on command-help-doctor-split-preflight-check")

expected_help_files = sorted(path.name for path in (command_dir / "help").glob("*.go"))
expected_doctor_files = ["doctor/doctor.go", "doctor/doctor_checks.go", "doctor/doctor_test.go"]
require(sorted(evidence.get("helpFiles") or []) == expected_help_files, "helpFiles must match current help*.go files")
require(sorted(evidence.get("doctorFiles") or []) == expected_doctor_files, "doctorFiles mismatch")
for filename in evidence.get("helpFiles") or []:
    require((command_dir / "help" / filename).is_file(), f"help split file is missing from help subpackage: {filename}")
for filename in evidence.get("doctorFiles") or []:
    require((command_dir / filename).is_file(), f"doctor file is missing from command package: {filename}")
require((command_dir / "help_adapter.go").is_file(), "help command adapter is missing")
require((command_dir / "doctor_adapter.go").is_file(), "doctor command adapter is missing")
require(not (command_dir / "help.go").exists(), "root help.go must be moved into help subpackage after P22-12")
require(not (command_dir / "doctor.go").exists(), "root doctor.go must be moved into doctor subpackage after P22-14")

contracts = set(evidence.get("preflightContracts") or [])
expected_contracts = {
    "help remains reachable through root help dispatch and command-specific help routing",
    "help output stays stdout-only through the command output adapter",
    "doctor remains reachable through root command dispatch",
    "doctor --json stays stdout-only with stable nextActions fields",
    "bug --json supportBundle remains available for doctor remediation guidance",
    "only help and doctor files moved into dedicated subpackages; shared files remain in the command package",
}
require(contracts == expected_contracts, "preflightContracts mismatch")
for token in ("ExecuteWithIO", "printCommandHelp", "doctorCommand", "bugCommand", "commandUsage", "supportBundle"):
    require(token in test_source, f"preflight test source must reference {token}")

required_tests = {
    "TestCommandHelpDoctorSplitPreflightEvidence",
    "TestCommandHelpDoctorSplitPreflightContracts",
    "TestCommandHelpDoctorSplitPhysicalBoundary",
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

completed_by_id = {
    item.get("id"): item
    for item in readiness.get("completedFamilies") or []
    if isinstance(item, dict)
}
help_candidate = completed_by_id.get("help") or {}
doctor_completed = completed_by_id.get("doctor") or {}
require(help_candidate.get("status") == "physical-split-completed", "help family must record physical split completion")
require(doctor_completed.get("status") == "physical-split-completed", "doctor family must record physical split completion")
require(
    "make command-help-doctor-split-preflight-check" in set(help_candidate.get("requiredGates") or []),
    "help candidate gates must include help/doctor split preflight check",
)
require(
    "make command-help-doctor-split-preflight-check" in set(doctor_completed.get("requiredGates") or []),
    "doctor completed gates must include help/doctor split preflight check",
)

blocked_by_id = {
    item.get("id"): item
    for item in readiness.get("blockedFamilies") or []
    if isinstance(item, dict)
}
shared_blocked = blocked_by_id.get("shared") or {}
require(shared_blocked.get("status") == "blocked-after-help-doctor-single-family-splits", "shared blocked status mismatch")
require(
    "make command-help-doctor-split-preflight-check" in set(shared_blocked.get("requiredGates") or []),
    "shared blocked gates must include help/doctor split preflight check",
)

next_step = readiness.get("nextStep") or {}
require(next_step.get("id") == "P22-15-command-next-family-candidate-refresh", "readiness nextStep must refresh next candidate")
next_action = str(next_step.get("action") or "")
require("Refresh the next command family candidate" in next_action, "nextStep action must refresh next candidate")

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
require(admission.get("status") == "completed-help-and-doctor-single-family-splits", "physicalSplitAdmission status mismatch")
require("Refresh the next command family candidate" in str(admission.get("nextAllowedAction") or ""), "nextAllowedAction must refresh next candidate")
require("Do not move shared helpers" in str(admission.get("blockedAction") or ""), "blockedAction must block shared movement")
require("Restore help or doctor files" in str(admission.get("rollbackRequirement") or ""), "rollbackRequirement must describe help/doctor restore path")

if missing:
    print("command help/doctor split preflight check failed:")
    for item in missing:
        print(f"- {item}")
    sys.exit(1)

print("command help/doctor split preflight OK")
PY
