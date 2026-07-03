#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import ast
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "project-layout-governance.json"
map_path = root / "docs" / "reference" / "command-family-dependency-map.json"
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
    if gate.startswith("make "):
        return gate.removeprefix("make ").split()[0] in targets
    if gate.startswith("GOCACHE="):
        return " go test " in gate or " go vet " in gate or " make " in gate
    if gate.startswith("go test ") or gate.startswith("go vet "):
        return True
    return False


def import_bucket(import_path):
    if import_path.startswith("github.com/imajinyun/gofly/cmd/gofly/internal/generator"):
        return "internalGenerator"
    if import_path.startswith("github.com/imajinyun/gofly/cmd/gofly/internal/spinner"):
        return "internalSpinner"
    if import_path.startswith("github.com/imajinyun/gofly/core/"):
        return "coreRuntime"
    if import_path.startswith("github.com/imajinyun/gofly/rpc"):
        return "runtimeRPC"
    if import_path.startswith("github.com/imajinyun/gofly/"):
        return "otherGofly"
    return "stdlibOrThirdParty"


def file_imports(path):
    try:
        module = ast.parse(path.read_text(encoding="utf-8"))
    except SyntaxError:
        # Go is not Python. Fall back to a small import-block parser instead of
        # shelling out so the gate has no tool dependency beyond python3.
        pass
    imports = []
    lines = path.read_text(encoding="utf-8").splitlines()
    seen_package = False
    index = 0
    while index < len(lines):
        line = lines[index].strip()
        if not seen_package:
            if line.startswith("package "):
                seen_package = True
            index += 1
            continue
        if not line or line.startswith("//"):
            index += 1
            continue
        if line.startswith("import ("):
            index += 1
            while index < len(lines):
                inner = lines[index].strip()
                if inner.startswith(")"):
                    index += 1
                    break
                match = re.search(r'"([^"]+)"', inner)
                if match:
                    imports.append(match.group(1))
                index += 1
            continue
        match = re.match(r'import\s+(?:[\w.]+\s+)?"([^"]+)"', line)
        if match:
            imports.append(match.group(1))
            index += 1
            continue
        break
    return imports


manifest = read_json(manifest_path)
dependency_map = read_json(map_path)
makefile = makefile_path.read_text(encoding="utf-8") if makefile_path.is_file() else ""
targets = make_target_names(makefile)

require(
    dependency_map.get("schema") == "gofly.command_family_dependency_map.v1",
    "command family dependency map schema mismatch",
)
require(dependency_map.get("status") == "blocking-contract", "command family dependency map status must be blocking-contract")
require(dependency_map.get("package") == "cmd/gofly/internal/command", "command family dependency map package mismatch")
require(
    dependency_map.get("acceptanceGate") == "make command-family-dependency-map-check",
    "command family dependency map acceptanceGate mismatch",
)
require("one family at a time" in str(dependency_map.get("policy") or ""), "command family dependency map policy must forbid broad splits")
require("command-family-dependency-map-check" in targets, "Makefile must expose command-family-dependency-map-check")
docs_check = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
require("command-family-dependency-map-check" in docs_check, "docs-check must depend on command-family-dependency-map-check")

split_policy = dependency_map.get("splitPolicy") or {}
require(split_policy.get("status") == "planned-only", "splitPolicy.status must be planned-only")
require(split_policy.get("selectedFamily") in ("", None), "splitPolicy.selectedFamily must stay empty until a family is selected")
require(
    set(split_policy.get("allowedRecommendations") or []) == {"candidate", "defer", "blocked"},
    "splitPolicy.allowedRecommendations mismatch",
)
require(len(split_policy.get("candidateRequirements") or []) >= 5, "splitPolicy.candidateRequirements must be descriptive")
expected_split_gates = {
    "make command-family-dependency-map-check",
    "make command-help-doctor-split-preflight-check",
    "make project-layout-governance-check",
    "make cli-command-surface-check",
    "make cli-json-contract-goldens-check",
    "go test -shuffle=on ./cmd/gofly/internal/command/...",
    "go vet ./cmd/gofly/internal/command/...",
    "git diff --check",
}
require(set(split_policy.get("requiredSplitGates") or []) == expected_split_gates, "splitPolicy.requiredSplitGates mismatch")
for gate in split_policy.get("requiredSplitGates") or []:
    require(gate_is_known(str(gate), targets), f"splitPolicy required gate is not known: {gate}")

command_files = sorted(path.name for path in command_dir.glob("*.go"))
family_entries = manifest.get("commandFileFamilies") or []
expected_family_files = {}
for family in family_entries:
    if not isinstance(family, dict):
        continue
    prefix = family.get("prefix", "")
    if prefix:
        family_id = prefix.rstrip("_") if prefix.endswith("_") else prefix
        expected_family_files[family_id] = sorted(path.name for path in command_dir.glob(f"{prefix}*.go"))
    else:
        expected_family_files["shared"] = sorted(family.get("files") or [])

map_families = dependency_map.get("families") or []
seen_ids = []
all_map_files = []
recommendations = {"candidate": [], "defer": [], "blocked": []}
for family in map_families:
    if not isinstance(family, dict):
        missing.append(f"command family dependency map entry must be object: {family!r}")
        continue
    family_id = family.get("id", "<missing>")
    seen_ids.append(family_id)
    expected_files = expected_family_files.get(family_id)
    require(expected_files is not None, f"command family dependency map {family_id}: family is not declared in project layout manifest")
    files = sorted(family.get("files") or [])
    all_map_files.extend(files)
    if expected_files is not None:
        require(files == expected_files, f"command family dependency map {family_id}: files mismatch")
    require(family.get("fileCount") == len(files), f"command family dependency map {family_id}: fileCount mismatch")
    require(len(str(family.get("domain") or "").split()) >= 3, f"command family dependency map {family_id}: domain must be descriptive")
    actual_buckets = {}
    for filename in files:
        path = command_dir / filename
        require(path.is_file(), f"command family dependency map {family_id}: missing command file {filename}")
        for import_path in file_imports(path):
            actual_buckets.setdefault(import_bucket(import_path), set()).add(import_path)
    normalized_actual = {key: sorted(values) for key, values in sorted(actual_buckets.items())}
    require(
        family.get("dependencyBuckets") == normalized_actual,
        f"command family dependency map {family_id}: dependencyBuckets mismatch",
    )
    recommendation = family.get("splitRecommendation")
    require(recommendation in recommendations, f"command family dependency map {family_id}: unsupported splitRecommendation")
    recommendations.setdefault(recommendation, []).append(family_id)
    require(family.get("splitRisk") in {"low", "medium", "high"}, f"command family dependency map {family_id}: splitRisk mismatch")
    require(len(family.get("blockers") or []) >= 2, f"command family dependency map {family_id}: at least 2 blockers required")
    require(
        all(len(str(item).split()) >= 8 for item in family.get("blockers") or []),
        f"command family dependency map {family_id}: blockers must be descriptive",
    )
    require(
        len(family.get("requiredPreSplitActions") or []) >= 3,
        f"command family dependency map {family_id}: requiredPreSplitActions must be actionable",
    )
    for gate in family.get("requiredGates") or []:
        require(gate_is_known(str(gate), targets), f"command family dependency map {family_id}: unknown required gate {gate}")
    require(
        "Restore" in str(family.get("rollbackRequirement") or ""),
        f"command family dependency map {family_id}: rollbackRequirement must describe restore path",
    )
    if recommendation == "candidate":
        require(
            not any(bucket in family.get("dependencyBuckets", {}) for bucket in ("coreRuntime", "runtimeRPC", "internalSpinner", "otherGofly")),
            f"command family dependency map {family_id}: candidate must not depend on runtime, spinner, or other gofly buckets",
        )
        require(family.get("splitRisk") == "low", f"command family dependency map {family_id}: candidate splitRisk must be low")
    if recommendation == "blocked":
        require(family.get("splitRisk") == "high", f"command family dependency map {family_id}: blocked splitRisk must be high")

require(sorted(seen_ids) == sorted(expected_family_files), "command family dependency map must account for every command family exactly once")
require(len(seen_ids) == len(set(seen_ids)), "command family dependency map must not contain duplicate family ids")
require(sorted(all_map_files) == command_files, "command family dependency map must account for every command file exactly once")
require(len(all_map_files) == len(set(all_map_files)), "command family dependency map must not contain duplicate command files")
require(set(dependency_map.get("blockedFamilies") or []) == set(recommendations["blocked"]), "blockedFamilies must match family recommendations")
require(set(dependency_map.get("deferredFamilies") or []) == set(recommendations["defer"]), "deferredFamilies must match family recommendations")
require(
    {entry.get("id") for entry in dependency_map.get("nextCandidates") or [] if isinstance(entry, dict)}
    == set(recommendations["candidate"]),
    "nextCandidates must match candidate family recommendations",
)
for entry in dependency_map.get("nextCandidates") or []:
    require(len(str(entry.get("reason") or "").split()) >= 12, f"next candidate {entry.get('id')}: reason must be descriptive")

if missing:
    print("command family dependency map check failed:")
    for item in missing:
        print(f"- {item}")
    sys.exit(1)

print("command family dependency map OK")
PY
