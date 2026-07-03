#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
evidence_path = root / "docs" / "reference" / "command-next-family-candidate-refresh.json"
readiness_path = root / "docs" / "reference" / "command-split-readiness.json"
dependency_map_path = root / "docs" / "reference" / "command-family-dependency-map.json"
help_doctor_path = root / "docs" / "reference" / "command-help-doctor-split-preflight.json"
layout_path = root / "docs" / "reference" / "project-layout-governance.json"
command_dir = root / "cmd" / "gofly" / "internal" / "command"
test_path = command_dir / "command_next_family_candidate_refresh_test.go"
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
help_doctor = read_json(help_doctor_path)
layout = read_json(layout_path)
makefile = makefile_path.read_text(encoding="utf-8") if makefile_path.is_file() else ""
targets = make_target_names(makefile)
docs_check = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
test_source = test_path.read_text(encoding="utf-8") if test_path.is_file() else ""

require(evidence.get("schema") == "gofly.command_next_family_candidate_refresh.v1", "candidate refresh schema mismatch")
require(evidence.get("status") == "completed-candidate-refresh", "candidate refresh status mismatch")
require(evidence.get("package") == "cmd/gofly/internal/command", "candidate refresh package mismatch")
require(
    evidence.get("acceptanceGate") == "make command-next-family-candidate-refresh-check",
    "candidate refresh acceptanceGate mismatch",
)
require(evidence.get("planningOnly") is True, "candidate refresh must be planningOnly")
require(evidence.get("noPhysicalMove") is True, "candidate refresh must forbid physical movement")
require("command-next-family-candidate-refresh-check" in targets, "Makefile must expose command-next-family-candidate-refresh-check")
require(
    "command-next-family-candidate-refresh-check" in docs_check,
    "docs-check must depend on command-next-family-candidate-refresh-check",
)

candidate = evidence.get("selectedCandidate") or {}
require(candidate.get("id") == "release", "selectedCandidate.id must be release")
require(candidate.get("status") == "candidate-after-json-golden", "selectedCandidate.status mismatch")
require(candidate.get("package") == "cmd/gofly/internal/command", "selectedCandidate package mismatch")
release_files = [
    "release.go",
    "release_contract_checks.go",
    "release_helpers.go",
    "release_local_checks.go",
    "release_output.go",
    "release_test.go",
    "release_types.go",
]
require(candidate.get("files") == release_files, "selectedCandidate files mismatch")
for filename in release_files:
    require((command_dir / filename).is_file(), f"release candidate file is missing from command package: {filename}")
require(not (command_dir / "release").exists(), "release subpackage must not exist during candidate refresh")
require("bounded file family" in str(candidate.get("reason") or ""), "selectedCandidate reason must explain bounded family")
require("JSON golden coverage" in str(candidate.get("reason") or ""), "selectedCandidate reason must explain JSON golden coverage")
require(len(candidate.get("requiredPreSplitActions") or []) >= 4, "selectedCandidate actions must be actionable")
for gate in candidate.get("requiredGates") or []:
    require(gate_is_known(str(gate), targets), f"selectedCandidate required gate is not known: {gate}")
for gate in (
    "make command-next-family-candidate-refresh-check",
    "make cli-json-contract-goldens-check",
    "make required-checks-drift-check",
):
    require(gate in set(candidate.get("requiredGates") or []), f"selectedCandidate gates must include {gate}")
require("Restore release to deferred status" in str(candidate.get("rollbackRequirement") or ""), "selectedCandidate rollbackRequirement mismatch")

deferred_ids = {item.get("id") for item in evidence.get("deferredCandidates") or [] if isinstance(item, dict)}
require(deferred_ids == {"config", "plugin", "api", "rpc", "model", "new"}, "deferredCandidates mismatch")
blocked_ids = {item.get("id") for item in evidence.get("blockedFamilies") or [] if isinstance(item, dict)}
require(blocked_ids == {"ai", "shared"}, "blockedFamilies mismatch")

required_tests = {
    "TestCommandNextFamilyCandidateRefreshEvidence",
    "TestCommandNextFamilyCandidateRefreshContracts",
}
require(set(evidence.get("goldenTests") or []) == required_tests, "goldenTests mismatch")
for test_name in required_tests:
    require(f"func {test_name}" in test_source, f"missing executable test {test_name}")
for token in ("releaseCheckCommand", "cli-json-contract-goldens", "required-checks-drift", "noPhysicalMove"):
    require(token in test_source, f"candidate refresh test source must reference {token}")

for gate in evidence.get("requiredGates") or []:
    require(gate_is_known(str(gate), targets), f"candidate refresh required gate is not known: {gate}")
require(
    "make command-next-family-candidate-refresh-check" in set(evidence.get("requiredGates") or []),
    "requiredGates must include command-next-family-candidate-refresh-check",
)

require(readiness.get("status") == "next-family-candidate-refreshed", "readiness status must record candidate refresh")
readiness_candidates = [item for item in readiness.get("candidateFamilies") or [] if isinstance(item, dict)]
require([item.get("id") for item in readiness_candidates] == ["release"], "readiness candidateFamilies must contain release only")
readiness_release = readiness_candidates[0] if readiness_candidates else {}
require(readiness_release.get("status") == "candidate-after-json-golden", "readiness release status mismatch")
require("make command-next-family-candidate-refresh-check" in set(readiness_release.get("requiredGates") or []), "readiness release gates must include candidate refresh")
require("release" not in set(readiness.get("deferredFamilies") or []), "release must not remain deferred after candidate refresh")
require(readiness.get("nextStep", {}).get("id") == "P22-16-command-release-family-preflight", "readiness nextStep mismatch")

family_by_id = {
    family.get("id"): family
    for family in dependency_map.get("families") or []
    if isinstance(family, dict)
}
release_family = family_by_id.get("release") or {}
require(release_family.get("splitRecommendation") == "candidate", "dependency map release must be candidate")
require(release_family.get("splitRisk") == "low", "dependency map release splitRisk must be low")
require(not any(bucket in release_family.get("dependencyBuckets", {}) for bucket in ("coreRuntime", "runtimeRPC", "internalSpinner", "otherGofly")), "release candidate must not depend on runtime, spinner, or other gofly buckets")
require(release_family.get("files") == release_files, "dependency map release files mismatch")
require(
    [item.get("id") for item in dependency_map.get("nextCandidates") or [] if isinstance(item, dict)] == ["release"],
    "dependency map nextCandidates must contain release only",
)
require("release" not in set(dependency_map.get("deferredFamilies") or []), "dependency map deferredFamilies must not include release")
require(set(dependency_map.get("blockedFamilies") or []) == {"ai", "shared"}, "dependency map blockedFamilies mismatch")

require(help_doctor.get("selectedNextFamily") == "release", "help/doctor preflight must point to release after refresh")
require("release family preflight" in str(help_doctor.get("physicalSplitAdmission", {}).get("nextAllowedAction") or ""), "help/doctor nextAllowedAction must mention release preflight")

reference_files = []
for family in (layout.get("referenceFileBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        reference_files.extend(family.get("files") or [])
require("command-next-family-candidate-refresh.json" in reference_files, "referenceFileBoundaries must index command-next-family-candidate-refresh.json")

script_files = []
for family in (layout.get("scriptFamilyBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        script_files.extend(family.get("files") or [])
require("check-command-next-family-candidate-refresh.sh" in script_files, "scriptFamilyBoundaries must index check-command-next-family-candidate-refresh.sh")

if missing:
    print("command next family candidate refresh check failed:")
    for item in missing:
        print(f"- {item}")
    sys.exit(1)

print("command next family candidate refresh OK")
PY
