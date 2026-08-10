#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "goctl-api-flag-parity.json"
missing = []

required_surfaces = {
    "api-go-test": "implemented",
    "api-go-type-group": "implemented",
    "api-format-stdin": "implemented",
    "api-format-declare": "implemented",
}
required_flags = {"test", "type-group", "stdin", "declare"}
required_diff_categories = {
    "same-contract",
    "compatible-flag",
    "missing-capability",
    "generation-error",
}
required_release_gates = {
    "make goctl-api-flag-parity-check",
    "make goctl-generator-compat-check",
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


manifest = json.loads(read_text(manifest_path)) if manifest_path.is_file() else {}
makefile = read_text(root / "Makefile")
surface_text = read_text(root / "docs" / "reference" / "goctl-surface-drift.json")
generator_text = read_text(root / "docs" / "reference" / "goctl-generator-compatibility.json")
from_gozero_text = read_text(root / "docs" / "reference" / "from-go-zero-migration.md")
api_gen_command = read_text(root / "cmd" / "gofly" / "internal" / "command" / "api_gen_command.go")
api_format_command = read_text(root / "cmd" / "gofly" / "internal" / "command" / "api_format_command.go")
api_codegen = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "api_codegen.go")
command_tests = read_text(root / "cmd" / "gofly" / "internal" / "command" / "idl_test.go")
generator_tests = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "idl_test.go")

targets = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
generator_check_line = next((line for line in makefile.splitlines() if line.startswith("goctl-generator-compat-check:")), "")

require(manifest.get("schema") == "gofly.goctl_api_flag_parity.v1", "schema must be gofly.goctl_api_flag_parity.v1")
require(manifest.get("acceptanceGate") == "make goctl-api-flag-parity-check", "acceptanceGate mismatch")
require("goctl-api-flag-parity-check" in targets, "Makefile must expose goctl-api-flag-parity-check")
require("check-goctl-api-flag-parity.sh" in makefile, "Makefile must call check-goctl-api-flag-parity.sh")
require("goctl-api-flag-parity-check" in docs_check_line, "docs-check must depend on goctl-api-flag-parity-check")
require("goctl-api-flag-parity-check" in generator_check_line, "goctl-generator-compat-check must depend on goctl-api-flag-parity-check")

scope = manifest.get("scope") or {}
require(scope.get("expandsCLICommandSurface") is False, "scope.expandsCLICommandSurface must be false")
require("migration-critical" in str(scope.get("positioning") or ""), "scope.positioning must preserve migration-critical stance")
require("goctl api go --test" in str(scope.get("referenceFramework") or ""), "scope.referenceFramework must mention goctl api go --test")
require("goctl api format --declare" in str(scope.get("referenceFramework") or ""), "scope.referenceFramework must mention goctl api format --declare")

for source in manifest.get("sourceOfTruth") or []:
    require((root / source).exists(), f"sourceOfTruth path missing: {source}")

policy = manifest.get("compatibilityPolicy") or {}
for key in ("apiGoTest", "apiGoTypeGroup", "apiFormatStdin", "apiFormatDeclare"):
    require(len(str(policy.get(key) or "").split()) >= 8, f"compatibilityPolicy.{key} must be actionable")
require("skips missing type declaration checks" in str(policy.get("apiFormatDeclare") or ""), "apiFormatDeclare policy must describe missing type declaration behavior")

surfaces = manifest.get("apiSurfaces") or []
surface_map = {item.get("id"): item for item in surfaces}
require(set(surface_map) == set(required_surfaces), f"apiSurfaces drifted: missing={sorted(set(required_surfaces) - set(surface_map))} extra={sorted(set(surface_map) - set(required_surfaces))}")

all_flags = set()
test_haystack = command_tests + "\n" + generator_tests
for surface_id, status in required_surfaces.items():
    item = surface_map.get(surface_id) or {}
    require(item.get("status") == status, f"{surface_id}: status must be {status}")
    require(str(item.get("goctlSurface") or "").startswith("goctl api "), f"{surface_id}: goctlSurface must start with goctl api")
    require(str(item.get("goflySurface") or "").startswith("gofly api "), f"{surface_id}: goflySurface must start with gofly api")
    require(len(str(item.get("behavior") or "").split()) >= 6, f"{surface_id}: behavior must be descriptive")
    covered = set(item.get("coveredFlags") or [])
    require(covered, f"{surface_id}: coveredFlags are required")
    all_flags.update(covered)
    evidence = item.get("evidence") or []
    require(evidence, f"{surface_id}: evidence anchors are required")
    for anchor in evidence:
        require(str(anchor) in test_haystack, f"{surface_id}: evidence anchor missing from tests: {anchor}")

required = set(manifest.get("requiredFlags") or [])
require(required == required_flags, f"requiredFlags drifted: missing={sorted(required_flags - required)} extra={sorted(required - required_flags)}")
require(required_flags <= all_flags, f"coveredFlags missing required flags: {sorted(required_flags - all_flags)}")

diff_categories = set(manifest.get("diffCategories") or [])
require(diff_categories == required_diff_categories, f"diffCategories drifted: missing={sorted(required_diff_categories - diff_categories)} extra={sorted(diff_categories - required_diff_categories)}")

release_gates = set(manifest.get("releaseGates") or [])
require(release_gates == required_release_gates, f"releaseGates drifted: missing={sorted(required_release_gates - release_gates)} extra={sorted(release_gates - required_release_gates)}")
for gate in release_gates:
    require(gate_is_known(gate, targets), f"release gate is not known: {gate}")

for needle in (
    "goctl api go --test",
    "goctl api format --stdin",
    "goctl api go --type-group",
):
    require(needle in surface_text, f"goctl surface drift contract missing {needle!r}")

for needle in (
    "goctl-api-flag-parity-check",
    "goctl-api-flag-parity.json",
    "goctl-api-flag-parity",
):
    require(needle in generator_text or needle in from_gozero_text or needle in makefile or needle in json.dumps(manifest), f"API flag parity evidence missing {needle!r}")

for needle in (
    'fs.Bool("test"',
    'fs.Bool("type-group"',
    "Test: *test",
    "TypeGroup: *typeGroup",
):
    require(needle in api_gen_command, f"api gen command missing {needle!r}")

for needle in (
    'fs.Bool("stdin"',
    'fs.Bool("declare"',
    "io.ReadAll(os.Stdin)",
    "generator.FormatAPIContent",
):
    require(needle in api_format_command, f"api format command missing {needle!r}")

for needle in (
    "if opts.TypeGroup",
    "if opts.Test",
    "types_\"+lowerSnake",
    "routes_test.go",
    "Declare bool",
    "FormatAPIContent",
    "validateAPITypeDeclarations",
):
    require(needle in api_codegen, f"api codegen missing {needle!r}")

for needle in (
    "TestExecuteAPIGenAcceptsGoctlTemplateFlags",
    "TestAPIFormatStdinAndControlPlaneVerificationCoverageBuffer",
    "TestFormatAPIFromFileDeclareSkipsMissingTypeDeclaration",
    "TestExecuteAPIFormatAndDoc",
    "TestGenerateRESTFromAPIWritesGatewayTypeGroupsAndRouteTests",
    "TestGenerateRESTFromAPITypeGroup",
):
    require(needle in test_haystack, f"required API flag parity test missing {needle}")

criteria = manifest.get("nextPromotionCriteria") or []
require(len(criteria) >= 3, "nextPromotionCriteria must include at least three items")
require(any("api-format-declare" in item for item in criteria), "promotion criteria must mention api-format-declare")
require(any("missing type declaration" in item for item in criteria), "promotion criteria must mention missing type declaration behavior")
require(any("full goctl parity" in item for item in criteria), "promotion criteria must guard full goctl parity claims")

if missing:
    print("goctl API flag parity check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("goctl API flag parity OK")
PY
