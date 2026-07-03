#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
evidence_path = root / "docs" / "reference" / "command-output-json-adapter-dry-run.json"
readiness_path = root / "docs" / "reference" / "command-split-readiness.json"
shared_plan_path = root / "docs" / "reference" / "command-shared-reduction-plan.json"
dependency_map_path = root / "docs" / "reference" / "command-family-dependency-map.json"
layout_path = root / "docs" / "reference" / "project-layout-governance.json"
test_path = root / "cmd" / "gofly" / "internal" / "command" / "command_output_json_adapter_dry_run_test.go"
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
shared_plan = read_json(shared_plan_path)
dependency_map = read_json(dependency_map_path)
layout = read_json(layout_path)
makefile = makefile_path.read_text(encoding="utf-8") if makefile_path.is_file() else ""
targets = make_target_names(makefile)
docs_check = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
test_source = test_path.read_text(encoding="utf-8") if test_path.is_file() else ""

require(evidence.get("schema") == "gofly.command_output_json_adapter_dry_run.v1", "output/json adapter dry-run schema mismatch")
require(evidence.get("status") == "completed-preflight", "output/json adapter dry-run status must be completed-preflight")
require(evidence.get("family") == "shared", "output/json adapter dry-run family mismatch")
require(evidence.get("package") == "cmd/gofly/internal/command", "output/json adapter dry-run package mismatch")
require(evidence.get("acceptanceGate") == "make command-output-json-adapter-dry-run-check", "output/json adapter dry-run acceptanceGate mismatch")
require(evidence.get("dryRunOnly") is True, "output/json adapter dry-run must be dryRunOnly")
require(evidence.get("noPhysicalMove") is True, "output/json adapter dry-run must be noPhysicalMove")
require("command-output-json-adapter-dry-run-check" in targets, "Makefile must expose command-output-json-adapter-dry-run-check")
require("command-output-json-adapter-dry-run-check" in docs_check, "docs-check must depend on command-output-json-adapter-dry-run-check")

required_sources = {
    "io.go",
    "output_flags.go",
    "json_error.go",
    "json_error_writer.go",
    "json_error_classify.go",
    "doctor_adapter.go",
    "bug.go",
}
require(set(evidence.get("sourceFiles") or []) == required_sources, "sourceFiles mismatch")
for filename in required_sources:
    require((command_dir / filename).is_file(), f"source file missing: {filename}")
    require(filename in test_source, f"test must reference source file {filename}")

required_contracts = {
    "withCommandIO restores output mode and stdout/stderr writers",
    "quiet mode suppresses stdout helpers without suppressing stderr errors",
    "verbose mode writes diagnostic output to stderr",
    "printJSONEnvelope keeps ok command version and data fields",
    "printJSONError keeps error code retryable remediation and nextActions fields",
    "WriteErrorJSON keeps the legacy error envelope",
    "doctor --json stays stdout-only with stable nextActions",
    "bug --json supportBundle stays stdout-only with redaction guidance",
}
require(set(evidence.get("adapterContracts") or []) == required_contracts, "adapterContracts mismatch")
for contract in required_contracts:
    first_token = contract.split()[0]
    require(first_token in test_source, f"test source must pin contract token {first_token!r}")

required_tests = {
    "TestCommandOutputJSONAdapterDryRunEvidence",
    "TestCommandOutputAdapterContract",
    "TestCommandJSONAdapterContract",
    "TestCommandOutputJSONAdapterDoctorAndBugContracts",
}
require(set(evidence.get("goldenTests") or []) == required_tests, "goldenTests mismatch")
for test_name in required_tests:
    require(f"func {test_name}" in test_source, f"missing executable test evidence for {test_name}")

for gate in evidence.get("requiredGates") or []:
    require(gate_is_known(str(gate), targets), f"output/json adapter required gate is not known: {gate}")
require("make command-output-json-adapter-dry-run-check" in set(evidence.get("requiredGates") or []), "requiredGates must include output/json adapter dry-run check")

domain_by_id = {
    item.get("id"): item
    for item in shared_plan.get("reductionDomains") or []
    if isinstance(item, dict)
}
for domain_id in ("output-io", "json-envelope"):
    domain = domain_by_id.get(domain_id) or {}
    require(domain.get("physicalMoveAllowed") is False, f"{domain_id} must still forbid physical movement")
    require("go test -shuffle=on ./cmd/gofly/internal/command/..." in set(domain.get("requiredGates") or []), f"{domain_id} gates must include command tests")

completed_by_id = {
    item.get("id"): item
    for item in readiness.get("completedFamilies") or []
    if isinstance(item, dict)
}
help_completed = completed_by_id.get("help") or {}
require(help_completed.get("status") == "physical-split-completed", "help completed status mismatch after P22-12")
require("make command-output-json-adapter-dry-run-check" in set(help_completed.get("requiredGates") or []), "help completed gates must include output/json adapter dry-run check")
require("make command-help-doctor-split-preflight-check" in set(help_completed.get("requiredGates") or []), "help completed gates must include help/doctor split preflight check")
doctor_completed = completed_by_id.get("doctor") or {}
require(doctor_completed.get("status") == "physical-split-completed", "doctor completed status mismatch after doctor split")
require("make command-output-json-adapter-dry-run-check" in set(doctor_completed.get("requiredGates") or []), "doctor completed gates must include output/json adapter dry-run check")
require("make command-help-doctor-split-preflight-check" in set(doctor_completed.get("requiredGates") or []), "doctor completed gates must include help/doctor split preflight check")

blocked = {
    item.get("id"): item
    for item in readiness.get("blockedFamilies") or []
    if isinstance(item, dict)
}
shared_blocked = blocked.get("shared") or {}
require(shared_blocked.get("status") == "blocked-after-help-doctor-single-family-splits", "shared blocked status mismatch")
require("make command-output-json-adapter-dry-run-check" in set(shared_blocked.get("requiredGates") or []), "shared blocked gates must include output/json adapter dry-run check")
require("make command-help-doctor-split-preflight-check" in set(shared_blocked.get("requiredGates") or []), "shared blocked gates must include help/doctor split preflight check")

next_step = readiness.get("nextStep") or {}
require(next_step.get("id") == "P22-15-command-next-family-candidate-refresh", "readiness nextStep must refresh next candidate")
require("Refresh the next command family candidate" in str(next_step.get("action") or ""), "readiness nextStep action must refresh next candidate")

family_by_id = {
    family.get("id"): family
    for family in dependency_map.get("families") or []
    if isinstance(family, dict)
}
shared_family = family_by_id.get("shared") or {}
require("make command-output-json-adapter-dry-run-check" in set(shared_family.get("requiredGates") or []), "dependency map shared gates must include output/json adapter dry-run check")

reference_files = []
for family in (layout.get("referenceFileBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        reference_files.extend(family.get("files") or [])
require("command-output-json-adapter-dry-run.json" in reference_files, "referenceFileBoundaries must index command-output-json-adapter-dry-run.json")
require("command-help-doctor-split-preflight.json" in reference_files, "referenceFileBoundaries must index command-help-doctor-split-preflight.json")

script_files = []
for family in (layout.get("scriptFamilyBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        script_files.extend(family.get("files") or [])
require("check-command-output-json-adapter-dry-run.sh" in script_files, "scriptFamilyBoundaries must index check-command-output-json-adapter-dry-run.sh")

admission = evidence.get("physicalSplitAdmission") or {}
require(admission.get("status") == "candidate-for-help-doctor-preflight", "physicalSplitAdmission status mismatch")
require("Restore" in str(admission.get("rollbackRequirement") or ""), "physicalSplitAdmission rollbackRequirement must describe restore path")

if missing:
    print("command output/json adapter dry-run check failed:")
    for item in missing:
        print(f"- {item}")
    sys.exit(1)

print("command output/json adapter dry-run OK")
PY
