#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
readiness_path = root / "docs" / "reference" / "command-split-readiness.json"
dependency_map_path = root / "docs" / "reference" / "command-family-dependency-map.json"
layout_path = root / "docs" / "reference" / "project-layout-governance.json"
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


readiness = read_json(readiness_path)
dependency_map = read_json(dependency_map_path)
layout = read_json(layout_path)
makefile = makefile_path.read_text(encoding="utf-8") if makefile_path.is_file() else ""
targets = make_target_names(makefile)
docs_check = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")

require(readiness.get("schema") == "gofly.command_split_readiness.v1", "command split readiness schema mismatch")
require(readiness.get("status") == "candidate-preflight", "command split readiness status must be candidate-preflight")
require(readiness.get("package") == "cmd/gofly/internal/command", "command split readiness package mismatch")
require(
    readiness.get("acceptanceGate") == "make command-split-readiness-check",
    "command split readiness acceptanceGate mismatch",
)
require("command-split-readiness-check" in targets, "Makefile must expose command-split-readiness-check")
require("command-split-readiness-check" in docs_check, "docs-check must depend on command-split-readiness-check")
require(
    "Do not split command families" in str(readiness.get("policy") or ""),
    "command split readiness policy must forbid immediate physical splits",
)

release_fix = readiness.get("releaseBlockerFix") or {}
require(release_fix.get("status") == "fixed", "releaseBlockerFix.status must be fixed")
for key in ("rootCause", "fix"):
    require(len(str(release_fix.get(key) or "").split()) >= 3, f"releaseBlockerFix.{key} must be descriptive")
require(
    release_fix.get("regressionTest") == "TestGenerateModelFromDDLGORMStyleDoesNotPolluteGoflyRootModule",
    "releaseBlockerFix.regressionTest mismatch",
)
proof_gates = release_fix.get("proofGates") or []
require(len(proof_gates) >= 3, "releaseBlockerFix.proofGates must include generator, release, and command package tests")
for gate in proof_gates:
    require(gate_is_known(str(gate), targets), f"releaseBlockerFix proof gate is not known: {gate}")

require(
    dependency_map.get("schema") == "gofly.command_family_dependency_map.v1",
    "command family dependency map schema mismatch",
)
require(
    dependency_map.get("acceptanceGate") == "make command-family-dependency-map-check",
    "command family dependency map acceptanceGate mismatch",
)
candidate_ids = [entry.get("id") for entry in readiness.get("candidateFamilies") or [] if isinstance(entry, dict)]
map_candidate_ids = [entry.get("id") for entry in dependency_map.get("nextCandidates") or [] if isinstance(entry, dict)]
require(candidate_ids == map_candidate_ids, "candidateFamilies must match command dependency map nextCandidates order")
require(set(readiness.get("deferredFamilies") or []) == set(dependency_map.get("deferredFamilies") or []), "deferredFamilies mismatch")

family_by_id = {
    family.get("id"): family
    for family in dependency_map.get("families") or []
    if isinstance(family, dict)
}
for family in readiness.get("candidateFamilies") or []:
    if not isinstance(family, dict):
        missing.append(f"candidate family must be object: {family!r}")
        continue
    family_id = family.get("id", "<missing>")
    mapped = family_by_id.get(family_id) or {}
    require(mapped.get("splitRecommendation") == "candidate", f"candidate {family_id}: dependency map must recommend candidate")
    require(
        family.get("status")
        in {
            "candidate-after-golden",
            "candidate-after-json-golden",
            "candidate-after-dry-run",
            "candidate-after-adapter-dry-run",
            "ready-for-single-family-split",
            "deferred-until-help-split-validation",
        },
        f"candidate {family_id}: status mismatch",
    )
    require(len(str(family.get("reason") or "").split()) >= 12, f"candidate {family_id}: reason must be descriptive")
    require(len(family.get("requiredPreSplitActions") or []) >= 3, f"candidate {family_id}: requiredPreSplitActions must be actionable")
    for gate in family.get("requiredGates") or []:
        require(gate_is_known(str(gate), targets), f"candidate {family_id}: unknown required gate {gate}")
    require("Restore" in str(family.get("rollbackRequirement") or ""), f"candidate {family_id}: rollbackRequirement must describe restore path")

blocked_ids = {entry.get("id") for entry in readiness.get("blockedFamilies") or [] if isinstance(entry, dict)}
require(blocked_ids == set(dependency_map.get("blockedFamilies") or []), "blocked family objects must match dependency map")
for family in readiness.get("blockedFamilies") or []:
    if not isinstance(family, dict):
        missing.append(f"blocked family must be object: {family!r}")
        continue
    family_id = family.get("id", "<missing>")
    require(family_by_id.get(family_id, {}).get("splitRecommendation") == "blocked", f"blocked {family_id}: dependency map must recommend blocked")
    require(len(str(family.get("reason") or "").split()) >= 10, f"blocked {family_id}: reason must be descriptive")

clean_signals = readiness.get("requiredCleanRootSignals") or []
require(len(clean_signals) >= 4, "requiredCleanRootSignals must cover go.mod, go.sum, release, and command tests")
joined_clean = " ".join(clean_signals)
for token in ("go.mod", "go.sum", "release check", "shuffle test"):
    require(token in joined_clean, f"requiredCleanRootSignals must mention {token}")

required_gates = readiness.get("requiredGates") or []
expected_required = {
    "make command-family-dependency-map-check",
    "make command-split-readiness-check",
    "make command-shared-reduction-plan-check",
    "make command-output-json-adapter-dry-run-check",
    "make command-help-doctor-split-preflight-check",
    "make project-layout-governance-check",
    "git diff --check",
}
require(expected_required.issubset(set(required_gates)), "requiredGates missing blocking governance gates")
for gate in required_gates:
    require(gate_is_known(str(gate), targets), f"required gate is not known: {gate}")

blocked_by_id = {
    item.get("id"): item
    for item in readiness.get("blockedFamilies") or []
    if isinstance(item, dict)
}
shared_blocked = blocked_by_id.get("shared") or {}
require(shared_blocked.get("status") == "blocked-during-help-single-family-split", "shared blocked status mismatch")
require("make command-shared-reduction-plan-check" in set(shared_blocked.get("requiredGates") or []), "shared blocked gates must include shared reduction plan check")
require("make command-output-json-adapter-dry-run-check" in set(shared_blocked.get("requiredGates") or []), "shared blocked gates must include output/json adapter dry-run check")
require("make command-help-doctor-split-preflight-check" in set(shared_blocked.get("requiredGates") or []), "shared blocked gates must include help/doctor split preflight check")

next_step = readiness.get("nextStep") or {}
require(next_step.get("id") == "P22-12-command-help-single-family-split", "nextStep id mismatch")
next_action = str(next_step.get("action") or "")
require("help" in next_action and "Do not move doctor" in next_action, "nextStep action must allow only help split")

reference_files = []
for family in (layout.get("referenceFileBoundaries") or {}).get("families") or []:
    if isinstance(family, dict):
        reference_files.extend(family.get("files") or [])
require("command-split-readiness.json" in reference_files, "referenceFileBoundaries must index command-split-readiness.json")
require("command-help-split-dry-run.json" in reference_files, "referenceFileBoundaries must index command-help-split-dry-run.json")
require("command-doctor-split-dry-run.json" in reference_files, "referenceFileBoundaries must index command-doctor-split-dry-run.json")
require("command-shared-reduction-plan.json" in reference_files, "referenceFileBoundaries must index command-shared-reduction-plan.json")
require("command-output-json-adapter-dry-run.json" in reference_files, "referenceFileBoundaries must index command-output-json-adapter-dry-run.json")
require("command-help-doctor-split-preflight.json" in reference_files, "referenceFileBoundaries must index command-help-doctor-split-preflight.json")

if missing:
    print("command split readiness check failed:")
    for item in missing:
        print(f"- {item}")
    sys.exit(1)

print("command split readiness OK")
PY
