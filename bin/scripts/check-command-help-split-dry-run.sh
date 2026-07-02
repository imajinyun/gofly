#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
evidence_path = root / "docs" / "reference" / "command-help-split-dry-run.json"
readiness_path = root / "docs" / "reference" / "command-split-readiness.json"
dependency_map_path = root / "docs" / "reference" / "command-family-dependency-map.json"
layout_path = root / "docs" / "reference" / "project-layout-governance.json"
test_path = root / "cmd" / "gofly" / "internal" / "command" / "command_help_split_readiness_test.go"
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

require(evidence.get("schema") == "gofly.command_help_split_dry_run.v1", "help split dry-run schema mismatch")
require(evidence.get("status") == "completed-preflight", "help split dry-run status must be completed-preflight")
require(evidence.get("family") == "help", "help split dry-run family mismatch")
require(evidence.get("package") == "cmd/gofly/internal/command", "help split dry-run package mismatch")
require(evidence.get("acceptanceGate") == "make command-help-split-dry-run-check", "help split dry-run acceptanceGate mismatch")
require(evidence.get("dryRunOnly") is True, "help split dry-run must be marked dryRunOnly")
require(evidence.get("noPhysicalMove") is True, "help split dry-run must be marked noPhysicalMove")
require("command-help-split-dry-run-check" in targets, "Makefile must expose command-help-split-dry-run-check")
require("command-help-split-dry-run-check" in docs_check, "docs-check must depend on command-help-split-dry-run-check")

expected_files = sorted(path.name for path in command_dir.glob("help*.go"))
require(sorted(evidence.get("familyFiles") or []) == expected_files, "help family file list must match cmd/gofly/internal/command/help*.go")
for filename in evidence.get("familyFiles") or []:
    require((command_dir / filename).is_file(), f"help family file is missing: {filename}")

required_tests = {
    "TestHelpFamilyGoldenOutput",
    "TestHelpFamilyDryRunEvidence",
    "TestCommandHelpForTopicBoundaries",
    "TestExecuteColoredHelp",
    "TestExecuteNestedColoredHelp",
}
require(set(evidence.get("goldenTests") or []) == required_tests, "help goldenTests mismatch")
for test_name in required_tests:
    require(f"func {test_name}" in test_source or test_name in test_source, f"missing executable test evidence for {test_name}")
require("NO_COLOR" in test_source and "GOFLY_NO_COLOR" in test_source, "help golden tests must pin no-color output")
require("Usage:" in test_source and "Available Commands:" in test_source, "help golden tests must pin rendered sections")

required_topics = {"doctor", "api", "rpc gen", "plugin run"}
require(set(evidence.get("goldenTopics") or []) == required_topics, "help goldenTopics mismatch")
for topic in required_topics:
    require(topic in test_source, f"help golden test must cover topic {topic!r}")

findings = {item.get("id"): item for item in evidence.get("dryRunFindings") or [] if isinstance(item, dict)}
for finding_id in ("shared-output-adapter", "topic-alias-contract", "rendering-contract"):
    require(finding_id in findings, f"dryRunFindings missing {finding_id}")
    require(len(str(findings.get(finding_id, {}).get("finding") or "").split()) >= 10, f"{finding_id} finding must be descriptive")

for gate in evidence.get("requiredGates") or []:
    require(gate_is_known(str(gate), targets), f"help split dry-run required gate is not known: {gate}")
require("make command-help-split-dry-run-check" in set(evidence.get("requiredGates") or []), "requiredGates must include command-help-split-dry-run-check")

candidate_by_id = {
    item.get("id"): item
    for item in readiness.get("candidateFamilies") or []
    if isinstance(item, dict)
}
help_candidate = candidate_by_id.get("help") or {}
require(help_candidate.get("status") == "candidate-after-dry-run", "command split readiness must mark help candidate after dry-run")
require("make command-help-split-dry-run-check" in set(help_candidate.get("requiredGates") or []), "help candidate gates must include help split dry-run check")
require("output adapter" in " ".join(help_candidate.get("requiredPreSplitActions") or []), "help candidate must keep output adapter as pre-split action")
require(readiness.get("nextStep", {}).get("id") == "P22-08-doctor-split-dry-run", "command split readiness nextStep must move to doctor dry-run")

map_candidates = [item.get("id") for item in dependency_map.get("nextCandidates") or [] if isinstance(item, dict)]
require(map_candidates[:2] == ["help", "doctor"], "command dependency map must keep help and doctor as next candidates")

reference_files = []
for family in (layout.get("referenceFileBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        reference_files.extend(family.get("files") or [])
require("command-help-split-dry-run.json" in reference_files, "referenceFileBoundaries must index command-help-split-dry-run.json")

admission = evidence.get("physicalSplitAdmission") or {}
require(admission.get("status") == "candidate-after-dry-run", "physicalSplitAdmission status mismatch")
require("Restore" in str(admission.get("rollbackRequirement") or ""), "physicalSplitAdmission rollbackRequirement must describe restore path")

if missing:
    print("command help split dry-run check failed:")
    for item in missing:
        print(f"- {item}")
    sys.exit(1)

print("command help split dry-run OK")
PY
