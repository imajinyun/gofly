#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "goctl-generator-compatibility.json"
missing = []

expected_capabilities = {
    "gozero-compatible-profile": "implemented",
    "goctl-compatible-flags": "implemented",
    "api-tooling-compatibility": "implemented",
    "generated-version-fixtures": "implemented",
    "upgrade-diff-contract": "implemented",
    "route-layout-boundary": "implemented",
    "multi-language-client-generation": "implemented",
}
required_boundaries = {
    "noNewJSONEnvelopeFlags",
    "apiRouteAndDiffFormatValidationUnchanged",
    "positionalPluginAndMiddlewareNamesRemainNames",
    "generatedProjectDependenciesStayInGeneratedGoMod",
    "runtimeArtifactsRemainVolatile",
}
required_release_gates = {
    "make goctl-generator-compat-check",
    "make generated-version-compat-check",
    "make generated-upgrade-dry-run-check",
    "make test-generated-matrix",
}


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


def make_target_names(makefile):
    return set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.M))


def gate_is_known(gate, targets):
    if gate.startswith("make "):
        parts = gate.removeprefix("make ").split()
        return bool(parts) and parts[0] in targets
    return gate.startswith("go test ")


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/goctl-generator-compatibility.json is missing")

makefile = read_text(root / "Makefile")
upgrade_doc = read_text(root / "docs" / "reference" / "generated-upgrade-dry-run.md")
docs_index = read_text(root / "docs" / "index.md")
framework_long_term = read_text(root / "docs" / "reference" / "framework-gap-long-term-adoption.json")
targets = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")

require(manifest.get("schema") == "gofly.goctl_generator_compatibility.v1", "schema must be gofly.goctl_generator_compatibility.v1")
require(manifest.get("acceptanceGate") == "make goctl-generator-compat-check", "acceptanceGate must be make goctl-generator-compat-check")
require("goctl-generator-compat-check" in targets, "Makefile must expose goctl-generator-compat-check")
require("goctl-generator-compat-check" in docs_check_line, "docs-check must depend on goctl-generator-compat-check")
require("check-goctl-generator-compat.sh" in makefile, "Makefile must call check-goctl-generator-compat.sh")

boundaries = manifest.get("compatibilityBoundaries") or {}
require(set(boundaries) == required_boundaries, f"compatibilityBoundaries drifted: missing={sorted(required_boundaries - set(boundaries))} extra={sorted(set(boundaries) - required_boundaries)}")
for key, value in boundaries.items():
    require(value is True, f"compatibility boundary {key} must be true")

status_policy = manifest.get("statusPolicy") or {}
for status in {"implemented", "planned"}:
    require(status in status_policy, f"statusPolicy must document {status}")

capabilities = manifest.get("capabilities") or []
capability_map = {
    item.get("id"): item
    for item in capabilities
    if isinstance(item, dict) and item.get("id")
}
require(set(capability_map) == set(expected_capabilities), f"capabilities drifted: missing={sorted(set(expected_capabilities) - set(capability_map))} extra={sorted(set(capability_map) - set(expected_capabilities))}")

for capability_id, expected_status in expected_capabilities.items():
    item = capability_map.get(capability_id) or {}
    require(item.get("status") == expected_status, f"{capability_id}: status must be {expected_status}")
    gate = str(item.get("gate") or "")
    require(gate_is_known(gate, targets), f"{capability_id}: gate is not known: {gate!r}")
    rollback = str(item.get("rollbackNote") or "")
    require(len(rollback.split()) >= 8, f"{capability_id}: rollbackNote must be actionable")
    if expected_status == "implemented":
        implementations = item.get("implementation") or []
        tests = item.get("tests") or []
        evidence = item.get("evidence") or []
        require(implementations, f"{capability_id}: implementation paths are required")
        require(tests, f"{capability_id}: test paths are required")
        require(len(evidence) >= 3, f"{capability_id}: evidence must include at least three anchors")
        for rel in implementations:
            require((root / rel).exists(), f"{capability_id}: implementation path is missing: {rel}")
        for rel in tests:
            require((root / rel).exists(), f"{capability_id}: test path is missing: {rel}")
        searchable = "\n".join(read_text(root / rel) for rel in implementations + tests if (root / rel).exists())
        for anchor in evidence:
            require(str(anchor) in searchable or str(anchor) in upgrade_doc, f"{capability_id}: evidence anchor {anchor!r} is not present in implementation, tests, or upgrade docs")
        if capability_id == "multi-language-client-generation":
            for language in ("typescript", "javascript", "dart", "java", "kotlin"):
                require(language in evidence, f"{capability_id}: evidence must include {language!r}")
                require(language in searchable, f"{capability_id}: implementation or tests must mention {language!r}")
    else:
        require(not item.get("implementation"), f"{capability_id}: planned capability must not claim implementation paths")
        require(not item.get("tests"), f"{capability_id}: planned capability must not claim test paths")
        require(len(item.get("promotionCriteria") or []) >= 3, f"{capability_id}: promotionCriteria must include at least three items")

release_gates = set(manifest.get("releaseGates") or [])
require(release_gates == required_release_gates, f"releaseGates drifted: missing={sorted(required_release_gates - release_gates)} extra={sorted(release_gates - required_release_gates)}")
for gate in release_gates:
    require(gate_is_known(str(gate), targets), f"release gate is not known: {gate!r}")

for needle in [
    "Goctl-compatible generator matrix",
    "docs/reference/goctl-generator-compatibility.json",
    "make goctl-generator-compat-check",
    "gozero-compatible",
    "api format",
    "api import",
    "api route",
    "api diff",
    "deterministic-repeat-generation",
]:
    require(needle in upgrade_doc, f"generated-upgrade-dry-run.md missing {needle!r}")

require(
    "[Goctl generator compatibility](reference/goctl-generator-compatibility.json)" in docs_index,
    "docs/index.md must link goctl generator compatibility evidence",
)
require(
    "docs/reference/goctl-generator-compatibility.json" in framework_long_term,
    "long-term framework adoption evidence must link goctl generator compatibility matrix",
)

if missing:
    print("goctl generator compatibility check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("goctl generator compatibility OK")
PY
