#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
evidence_path = root / "docs" / "reference" / "command-doctor-split-dry-run.json"
readiness_path = root / "docs" / "reference" / "command-split-readiness.json"
dependency_map_path = root / "docs" / "reference" / "command-family-dependency-map.json"
layout_path = root / "docs" / "reference" / "project-layout-governance.json"
test_path = root / "cmd" / "gofly" / "internal" / "command" / "command_doctor_split_readiness_test.go"
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


evidence = read_json(evidence_path)
readiness = read_json(readiness_path)
dependency_map = read_json(dependency_map_path)
layout = read_json(layout_path)
makefile = makefile_path.read_text(encoding="utf-8") if makefile_path.is_file() else ""
targets = make_target_names(makefile)
docs_check = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
test_source = test_path.read_text(encoding="utf-8") if test_path.is_file() else ""

require(evidence.get("schema") == "gofly.command_doctor_split_dry_run.v1", "doctor split dry-run schema mismatch")
require(evidence.get("status") == "completed-physical-split", "doctor split dry-run status must be completed-physical-split")
require(evidence.get("family") == "doctor", "doctor split dry-run family mismatch")
require(evidence.get("package") == "cmd/gofly/internal/command/doctor", "doctor split dry-run package mismatch")
require(evidence.get("acceptanceGate") == "make command-doctor-split-dry-run-check", "doctor split dry-run acceptanceGate mismatch")
require(evidence.get("dryRunOnly") is False, "doctor split dry-run must no longer be dryRunOnly after P22-14")
require(evidence.get("noPhysicalMove") is False, "doctor split dry-run must allow completed physical move after P22-14")
require(evidence.get("doctorPackage") == "cmd/gofly/internal/command/doctor", "doctorPackage mismatch")
require(evidence.get("commandAdapter") == "doctor_adapter.go", "commandAdapter mismatch")
require("command-doctor-split-dry-run-check" in targets, "Makefile must expose command-doctor-split-dry-run-check")
require("command-doctor-split-dry-run-check" in docs_check, "docs-check must depend on command-doctor-split-dry-run-check")

expected_files = sorted(["doctor/doctor.go", "doctor/doctor_checks.go", "doctor/doctor_test.go"])
require(sorted(evidence.get("familyFiles") or []) == expected_files, "doctor family file list mismatch")
for filename in evidence.get("familyFiles") or []:
    require((command_dir / filename).is_file(), f"doctor family file is missing: {filename}")
require((command_dir / "doctor_adapter.go").is_file(), "doctor command adapter is missing")

required_tests = {
    "TestDoctorFamilyDryRunEvidence",
    "TestDoctorFamilyJSONGoldenContract",
    "TestDoctorFamilySupportBundleContract",
    "TestDoctorCommandJSON",
    "TestDoctorNextActionsContract",
    "TestBugCommandSupportBundleJSONContract",
}
require(set(evidence.get("goldenTests") or []) == required_tests, "doctor goldenTests mismatch")
for test_name in required_tests:
    require(f"func {test_name}" in test_source or test_name in test_source, f"missing executable test evidence for {test_name}")

required_fields = {
    "version",
    "go",
    "os",
    "arch",
    "checks",
    "summary",
    "nextActions",
    "checks.name",
    "checks.status",
    "checks.message",
    "checks.fix_hint",
    "checks.nextActions",
}
require(set(evidence.get("goldenFields") or []) == required_fields, "doctor goldenFields mismatch")
for field in required_fields:
    require(field in test_source, f"doctor golden test must reference field {field!r}")

support_fields = {
    "supportBundle.schema",
    "supportBundle.redaction",
    "supportBundle.commands",
    "supportBundle.description",
    "nextActions",
}
require(set(evidence.get("supportBundleFields") or []) == support_fields, "doctor supportBundleFields mismatch")
for field in ("gofly.support_bundle.v1", "Authorization", "Cookie", "gofly doctor --json", "gofly bug --json"):
    require(field in test_source, f"support bundle test must pin {field!r}")

related = {item.get("id"): item for item in evidence.get("relatedSharedContracts") or [] if isinstance(item, dict)}
for contract_id in ("support-bundle", "json-output"):
    require(contract_id in related, f"relatedSharedContracts missing {contract_id}")
    require(len(str(related.get(contract_id, {}).get("reason") or "").split()) >= 10, f"{contract_id} reason must be descriptive")

findings = {item.get("id"): item for item in evidence.get("dryRunFindings") or [] if isinstance(item, dict)}
for finding_id in ("json-output-adapter", "support-bundle-coupling", "diagnostic-next-actions"):
    require(finding_id in findings, f"dryRunFindings missing {finding_id}")
    require(len(str(findings.get(finding_id, {}).get("finding") or "").split()) >= 10, f"{finding_id} finding must be descriptive")

for gate in evidence.get("requiredGates") or []:
    require(gate_is_known(str(gate), targets), f"doctor split dry-run required gate is not known: {gate}")
require("make command-doctor-split-dry-run-check" in set(evidence.get("requiredGates") or []), "requiredGates must include command-doctor-split-dry-run-check")

completed_by_id = {
    item.get("id"): item
    for item in readiness.get("completedFamilies") or []
    if isinstance(item, dict)
}
doctor_completed = completed_by_id.get("doctor") or {}
require(doctor_completed.get("status") == "physical-split-completed", "command split readiness must mark doctor physical split completed")
require("make command-doctor-split-dry-run-check" in set(doctor_completed.get("requiredGates") or []), "doctor completed gates must include doctor split dry-run check")
require("make command-output-json-adapter-dry-run-check" in set(doctor_completed.get("requiredGates") or []), "doctor completed gates must include output/json adapter dry-run check")
require("make command-help-doctor-split-preflight-check" in set(doctor_completed.get("requiredGates") or []), "doctor completed gates must include help/doctor split preflight check")
require("cmd/gofly/internal/command/doctor" in str(doctor_completed.get("reason") or ""), "doctor completed reason must mention doctor subpackage")
require(readiness.get("nextStep", {}).get("id") == "P22-15-command-next-family-candidate-refresh", "command split readiness nextStep must refresh next candidate")

map_candidates = [item.get("id") for item in dependency_map.get("nextCandidates") or [] if isinstance(item, dict)]
require(map_candidates == [], "command dependency map must not keep doctor as a next candidate after P22-14")

reference_files = []
for family in (layout.get("referenceFileBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        reference_files.extend(family.get("files") or [])
require("command-doctor-split-dry-run.json" in reference_files, "referenceFileBoundaries must index command-doctor-split-dry-run.json")

admission = evidence.get("physicalSplitAdmission") or {}
require(admission.get("status") == "completed-doctor-single-family-split", "physicalSplitAdmission status mismatch")
require("Restore" in str(admission.get("rollbackRequirement") or ""), "physicalSplitAdmission rollbackRequirement must describe restore path")

if missing:
    print("command doctor split dry-run check failed:")
    for item in missing:
        print(f"- {item}")
    sys.exit(1)

print("command doctor split dry-run OK")
PY
