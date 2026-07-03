#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
evidence_path = root / "docs" / "reference" / "command-release-family-preflight.json"
readiness_path = root / "docs" / "reference" / "command-split-readiness.json"
dependency_map_path = root / "docs" / "reference" / "command-family-dependency-map.json"
candidate_refresh_path = root / "docs" / "reference" / "command-next-family-candidate-refresh.json"
layout_path = root / "docs" / "reference" / "project-layout-governance.json"
cli_surface_path = root / "docs" / "reference" / "cli-command-surface.json"
json_goldens_path = root / "docs" / "reference" / "cli-json-contract-goldens.json"
command_dir = root / "cmd" / "gofly" / "internal" / "command"
test_path = command_dir / "command_release_family_preflight_test.go"
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
candidate_refresh = read_json(candidate_refresh_path)
layout = read_json(layout_path)
cli_surface = read_json(cli_surface_path)
json_goldens = read_json(json_goldens_path)
makefile = makefile_path.read_text(encoding="utf-8") if makefile_path.is_file() else ""
targets = make_target_names(makefile)
docs_check = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
test_source = test_path.read_text(encoding="utf-8") if test_path.is_file() else ""

release_files = [
    "release.go",
    "release_contract_checks.go",
    "release_helpers.go",
    "release_local_checks.go",
    "release_output.go",
    "release_test.go",
    "release_types.go",
]

require(evidence.get("schema") == "gofly.command_release_family_preflight.v1", "release preflight schema mismatch")
require(evidence.get("status") == "completed-release-family-preflight", "release preflight status mismatch")
require(evidence.get("package") == "cmd/gofly/internal/command", "release preflight package mismatch")
require(
    evidence.get("acceptanceGate") == "make command-release-family-preflight-check",
    "release preflight acceptanceGate mismatch",
)
require(evidence.get("preflightOnly") is True, "release preflight must be preflightOnly")
require(evidence.get("noPhysicalMove") is True, "release preflight must forbid physical movement")
require("does not move release files" in str(evidence.get("policy") or ""), "release preflight policy must forbid this-round file moves")
require("command-release-family-preflight-check" in targets, "Makefile must expose command-release-family-preflight-check")
require(
    "command-release-family-preflight-check" in docs_check,
    "docs-check must depend on command-release-family-preflight-check",
)

selected = evidence.get("selectedFamily") or {}
require(selected.get("id") == "release", "selectedFamily.id must be release")
require(selected.get("status") == "ready-for-release-single-family-split", "selectedFamily.status mismatch")
require(selected.get("currentPackage") == "cmd/gofly/internal/command", "selectedFamily currentPackage mismatch")
require(selected.get("futurePackage") == "cmd/gofly/internal/command/release", "selectedFamily futurePackage mismatch")
require(selected.get("files") == release_files, "selectedFamily release files mismatch")
for filename in release_files:
    require((command_dir / filename).is_file(), f"release preflight file must remain in command package: {filename}")
require(not (command_dir / "release").exists(), "release subpackage must not exist during P22-16 preflight")
require(len(selected.get("blockedMoves") or []) >= 5, "selectedFamily.blockedMoves must pin non-release boundaries")
for phrase in ("JSON error helpers", "global output or IO helpers", "non-release command family"):
    require(
        any(phrase in item for item in selected.get("blockedMoves") or []),
        f"selectedFamily.blockedMoves must mention {phrase}",
    )
require("Restore release files" in str(selected.get("rollbackRequirement") or ""), "selectedFamily rollbackRequirement mismatch")

contracts = evidence.get("contracts") or {}
registration = contracts.get("commandRegistration") or {}
require(registration.get("rootCommand") == "release", "commandRegistration.rootCommand mismatch")
require(registration.get("childCommands") == ["check"], "commandRegistration.childCommands mismatch")
require(registration.get("manifestEntry") == "release", "commandRegistration.manifestEntry mismatch")

help_routing = contracts.get("helpRouting") or {}
require(help_routing.get("topics") == ["release", "release check"], "helpRouting topics mismatch")
require(help_routing.get("usage") == "gofly release check [flags]", "helpRouting usage mismatch")
require(help_routing.get("stdoutOnly") is True, "helpRouting must be stdoutOnly")

json_envelope = contracts.get("jsonEnvelope") or {}
require(json_envelope.get("command") == "release.check", "jsonEnvelope.command mismatch")
require(json_envelope.get("errorCode") == "RELEASE_CHECK_FAILED", "jsonEnvelope.errorCode mismatch")
require(
    json_envelope.get("requiredEnvelopeFields") == ["ok", "command", "version", "data"],
    "jsonEnvelope requiredEnvelopeFields mismatch",
)
require(
    json_envelope.get("requiredDataFields") == ["summary", "recommended_semver", "checks", "blocking", "warnings"],
    "jsonEnvelope requiredDataFields mismatch",
)
require(json_envelope.get("noDuplicateGlobalJSON") is True, "jsonEnvelope must prevent duplicate global JSON")
require(
    contracts.get("releaseChecks") == ["api-breaking", "rpc-breaking", "go-api-compat", "changelog-version", "go-mod-tidy"],
    "releaseChecks mismatch",
)

local_boundary = contracts.get("localExecutionBoundary") or {}
require(local_boundary.get("status") == "file-separated-before-package-split", "localExecutionBoundary status mismatch")
require(local_boundary.get("runnerFiles") == ["release_helpers.go", "release_local_checks.go"], "localExecutionBoundary runnerFiles mismatch")
require(local_boundary.get("renderingFiles") == ["release_output.go"], "localExecutionBoundary renderingFiles mismatch")
require("separate from release output rendering" in str(local_boundary.get("policy") or ""), "localExecutionBoundary policy mismatch")

output_discipline = contracts.get("outputDiscipline") or {}
require(output_discipline.get("jsonStdoutOnly") is True, "outputDiscipline.jsonStdoutOnly mismatch")
require(output_discipline.get("errorsDoNotEmitDuplicateJSON") is True, "outputDiscipline duplicate JSON mismatch")
require(output_discipline.get("textOutputUsesCommandIO") is True, "outputDiscipline command IO mismatch")

admission = evidence.get("physicalSplitAdmission") or {}
require(admission.get("status") == "ready-for-single-family-split", "physicalSplitAdmission status mismatch")
require(admission.get("nextStep") == "P22-17-command-release-single-family-split", "physicalSplitAdmission nextStep mismatch")
require("Move only release family files" in str(admission.get("allowedAction") or ""), "physicalSplitAdmission allowedAction mismatch")
signals = admission.get("requiredSignals") or []
for phrase in (
    "project-layout-governance",
    "docs-check",
    "release subpackage does not exist",
    "RELEASE_CHECK_FAILED",
    "required-check drift",
):
    require(any(phrase in str(signal) for signal in signals), f"physicalSplitAdmission missing signal phrase {phrase}")
for gate in admission.get("requiredGates") or []:
    require(gate_is_known(str(gate), targets), f"physicalSplitAdmission gate is not known: {gate}")

required_tests = {
    "TestCommandReleaseFamilyPreflightEvidence",
    "TestCommandReleaseFamilyPreflightContracts",
}
require(set(evidence.get("goldenTests") or []) == required_tests, "goldenTests mismatch")
for test_name in required_tests:
    require(f"func {test_name}" in test_source, f"missing executable test {test_name}")
for token in ("ExecuteWithIO", "RELEASE_CHECK_FAILED", "release check", "command-release-family-preflight"):
    require(token in test_source, f"release preflight test source must reference {token}")

for gate in evidence.get("requiredGates") or []:
    require(gate_is_known(str(gate), targets), f"release preflight required gate is not known: {gate}")
for gate in (
    "make command-release-family-preflight-check",
    "make command-next-family-candidate-refresh-check",
    "make required-checks-drift-check",
    "make project-layout-governance-check",
):
    require(gate in set(evidence.get("requiredGates") or []), f"release preflight requiredGates must include {gate}")

require(readiness.get("status") == "release-family-preflight-completed", "readiness status must record release preflight completion")
readiness_candidates = [item for item in readiness.get("candidateFamilies") or [] if isinstance(item, dict)]
require([item.get("id") for item in readiness_candidates] == ["release"], "readiness candidateFamilies must contain release only")
readiness_release = readiness_candidates[0] if readiness_candidates else {}
require(readiness_release.get("status") == "ready-for-release-single-family-split", "readiness release status mismatch")
require("make command-release-family-preflight-check" in set(readiness_release.get("requiredGates") or []), "readiness release gates must include release preflight")
require(readiness.get("nextStep", {}).get("id") == "P22-17-command-release-single-family-split", "readiness nextStep mismatch")

family_by_id = {
    family.get("id"): family
    for family in dependency_map.get("families") or []
    if isinstance(family, dict)
}
release_family = family_by_id.get("release") or {}
require(release_family.get("splitRecommendation") == "candidate", "dependency map release must remain candidate")
require(release_family.get("splitRisk") == "low", "dependency map release splitRisk must remain low")
require(release_family.get("files") == release_files, "dependency map release files mismatch")
require("make command-release-family-preflight-check" in set(release_family.get("requiredGates") or []), "dependency map release gates must include release preflight")
next_candidates = dependency_map.get("nextCandidates") or []
require([item.get("id") for item in next_candidates if isinstance(item, dict)] == ["release"], "dependency map nextCandidates must contain release only")
if next_candidates:
    require(next_candidates[0].get("status") == "ready-for-release-single-family-split", "dependency map release next candidate status mismatch")
    require(next_candidates[0].get("requiredGate") == "make command-release-family-preflight-check", "dependency map release next candidate requiredGate mismatch")
    require(next_candidates[0].get("nextStep") == "P22-17-command-release-single-family-split", "dependency map release next candidate nextStep mismatch")

require(candidate_refresh.get("nextStep", {}).get("id") == "P22-16-command-release-family-preflight", "candidate refresh must still point to this preflight")

surface_commands = {
    item.get("name"): item
    for item in cli_surface.get("rootCommands") or []
    if isinstance(item, dict)
}
release_surface = surface_commands.get("release") or {}
require(release_surface.get("children") == ["check"], "cli command surface release children mismatch")
require(release_surface.get("jsonContract") == "gofly release check --json", "cli command surface release JSON contract mismatch")
require(release_surface.get("helpTopic") == "release", "cli command surface release help topic mismatch")

golden_by_id = {
    item.get("id"): item
    for item in json_goldens.get("cases") or []
    if isinstance(item, dict)
}
release_golden = golden_by_id.get("release-check-envelope") or {}
require(release_golden.get("command") == "gofly release check --json", "release JSON golden command mismatch")
require(release_golden.get("mode") == "envelope", "release JSON golden mode mismatch")
require(release_golden.get("requiredEnvelopeFields") == ["ok", "command", "version", "data"], "release JSON golden envelope mismatch")
require(release_golden.get("requiredDataFields") == ["summary", "recommended_semver", "checks", "blocking", "warnings"], "release JSON golden data fields mismatch")

reference_files = []
for family in (layout.get("referenceFileBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        reference_files.extend(family.get("files") or [])
require("command-release-family-preflight.json" in reference_files, "referenceFileBoundaries must index command-release-family-preflight.json")

script_files = []
for family in (layout.get("scriptFamilyBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        script_files.extend(family.get("files") or [])
require("check-command-release-family-preflight.sh" in script_files, "scriptFamilyBoundaries must index check-command-release-family-preflight.sh")

if missing:
    print("command release family preflight check failed:")
    for item in missing:
        print(f"- {item}")
    sys.exit(1)

print("command release family preflight OK")
PY
