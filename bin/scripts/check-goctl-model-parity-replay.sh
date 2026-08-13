#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "goctl-model-parity-replay.json"
missing = []

required_surfaces = {
    "mysql-ddl-gozero-style",
    "mysql-datasource-options",
    "postgres-datasource-options",
    "mongo-type-cache-prefix",
}
required_options = {
    "cache",
    "strict",
    "ignore-columns",
    "prefix",
    "table-filter",
    "database",
    "schema",
    "datasource",
    "module",
}
required_diff_categories = {
    "same-contract",
    "model-layout-difference",
    "generated-cache-template",
    "missing-capability",
    "generation-error",
}
required_release_gates = {
    "make goctl-model-parity-replay-check",
    "make goctl-generator-compat-check",
    "make goctl-real-project-replay-check",
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
real_replay_text = read_text(root / "docs" / "reference" / "goctl-real-project-replay.json")
from_gozero_text = read_text(root / "docs" / "reference" / "from-go-zero-migration.md")
command_tests = read_text(root / "cmd" / "gofly" / "internal" / "command" / "idl_test.go")
generator_tests = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "idl_test.go")
model_bench_tests = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "model_bench_test.go")
model_gen_flags = read_text(root / "cmd" / "gofly" / "internal" / "command" / "model_gen_flags.go")
model_datasource_flags = read_text(root / "cmd" / "gofly" / "internal" / "command" / "model_datasource_command.go")
model_mongo_flags = read_text(root / "cmd" / "gofly" / "internal" / "command" / "model_mongo_command.go")
model_codegen = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "model_codegen.go")

targets = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
generator_check_line = next((line for line in makefile.splitlines() if line.startswith("goctl-generator-compat-check:")), "")

require(manifest.get("schema") == "gofly.goctl_model_parity_replay.v1", "schema must be gofly.goctl_model_parity_replay.v1")
require(manifest.get("acceptanceGate") == "make goctl-model-parity-replay-check", "acceptanceGate mismatch")
require("goctl-model-parity-replay-check" in targets, "Makefile must expose goctl-model-parity-replay-check")
require("check-goctl-model-parity-replay.sh" in makefile, "Makefile must call check-goctl-model-parity-replay.sh")
require("goctl-model-parity-replay-check" in docs_check_line, "docs-check must depend on goctl-model-parity-replay-check")
require("goctl-model-parity-replay-check" in generator_check_line, "goctl-generator-compat-check must depend on goctl-model-parity-replay-check")

scope = manifest.get("scope") or {}
require(scope.get("expandsCLICommandSurface") is False, "scope.expandsCLICommandSurface must be false")
require("migration-critical" in str(scope.get("positioning") or ""), "scope.positioning must preserve migration-critical stance")
require("goctl model" in str(scope.get("referenceFramework") or ""), "scope.referenceFramework must mention goctl model")

for source in manifest.get("sourceOfTruth") or []:
    require((root / source).exists(), f"sourceOfTruth path missing: {source}")

policy = manifest.get("compatibilityPolicy") or {}
for key in ("layout", "oracleDiff", "rootModuleHygiene", "offlineDatasourceFixtures"):
    require(len(str(policy.get(key) or "").split()) >= 8, f"compatibilityPolicy.{key} must be actionable")
require("model-layout-difference" in policy.get("oracleDiff", ""), "oracleDiff must mention model-layout-difference")
require("root module" in policy.get("rootModuleHygiene", "").lower(), "rootModuleHygiene must mention root module")
require("offline-datasource-fixtures" in policy.get("offlineDatasourceFixtures", ""), "offlineDatasourceFixtures policy must mention offline-datasource-fixtures")

surfaces = manifest.get("modelSurfaces") or []
surface_ids = {item.get("id") for item in surfaces}
require(surface_ids == required_surfaces, f"modelSurfaces drifted: missing={sorted(required_surfaces - surface_ids)} extra={sorted(surface_ids - required_surfaces)}")

all_covered_options = set()
test_haystack = command_tests + "\n" + generator_tests + "\n" + model_bench_tests
for item in surfaces:
    surface_id = item.get("id")
    require(item.get("status") == "implemented", f"{surface_id}: status must be implemented")
    require(str(item.get("goctlSurface") or "").startswith("goctl model "), f"{surface_id}: goctlSurface must start with goctl model")
    require(str(item.get("goflySurface") or "").startswith("gofly model "), f"{surface_id}: goflySurface must start with gofly model")
    covered = set(item.get("coveredOptions") or [])
    require(covered, f"{surface_id}: coveredOptions are required")
    all_covered_options.update(covered)
    evidence = item.get("evidence") or []
    require(len(evidence) >= 2, f"{surface_id}: at least two evidence anchors are required")
    for anchor in evidence:
        anchor_text = str(anchor)
        require(anchor_text in test_haystack or (root / anchor_text).is_file(), f"{surface_id}: evidence anchor missing from tests or fixtures: {anchor}")

required = set(manifest.get("requiredOptions") or [])
require(required == required_options, f"requiredOptions drifted: missing={sorted(required_options - required)} extra={sorted(required - required_options)}")
require(required_options <= all_covered_options, f"coveredOptions missing required options: {sorted(required_options - all_covered_options)}")

diff_categories = set(manifest.get("diffCategories") or [])
require(diff_categories == required_diff_categories, f"diffCategories drifted: missing={sorted(required_diff_categories - diff_categories)} extra={sorted(diff_categories - required_diff_categories)}")

release_gates = set(manifest.get("releaseGates") or [])
require(release_gates == required_release_gates, f"releaseGates drifted: missing={sorted(required_release_gates - release_gates)} extra={sorted(release_gates - required_release_gates)}")
for gate in release_gates:
    require(gate_is_known(gate, targets), f"release gate is not known: {gate}")

offline_fixtures = manifest.get("offlineDatasourceFixtures") or []
fixture_ids = {item.get("id") for item in offline_fixtures}
require(
    fixture_ids == {"mysql-multi-table-datasource-replay", "postgres-multi-schema-datasource-replay"},
    f"offlineDatasourceFixtures drifted: {sorted(fixture_ids)!r}",
)
for item in offline_fixtures:
    fixture_path = root / str(item.get("path") or "")
    require(fixture_path.is_file(), f"{item.get('id')}: fixture path missing: {item.get('path')}")
    fixture = json.loads(fixture_path.read_text(encoding="utf-8")) if fixture_path.is_file() else {}
    require(fixture.get("schema") == "gofly.goctl_datasource_replay_fixture.v1", f"{item.get('id')}: fixture schema mismatch")
    require(fixture.get("id") == item.get("id"), f"{item.get('id')}: fixture id mismatch")
    require(fixture.get("driver") == item.get("driver"), f"{item.get('id')}: fixture driver mismatch")
    require(fixture.get("strict") is True, f"{item.get('id')}: strict must be true")
    require(fixture.get("cache") is True, f"{item.get('id')}: cache must be true")
    require(fixture.get("expectedArtifacts"), f"{item.get('id')}: expectedArtifacts are required")
    require(fixture.get("assertions"), f"{item.get('id')}: assertions are required")
    required_caps = set(item.get("capabilities") or [])
    actual_caps = set(fixture.get("capabilities") or [])
    require(required_caps <= actual_caps, f"{item.get('id')}: capabilities missing {sorted(required_caps - actual_caps)}")

for needle in (
    "mysql ddl",
    "mysql datasource",
    "postgres datasource",
    "mongo type/cache/easy flags",
    "ignore-columns",
    "cache prefix",
):
    require(needle in surface_text, f"goctl surface drift contract missing {needle!r}")

for needle in (
    "goctl-model-parity-replay-check",
    "goctl-model-parity-replay.json",
    "model-parity-replay",
):
    require(needle in generator_text or needle in real_replay_text or needle in from_gozero_text or needle in makefile, f"model parity evidence missing {needle!r}")

for needle in (
    'fs.Bool("strict"',
    'fs.String("ignore-columns"',
    'fs.String("prefix"',
    'fs.Bool("cache"',
    'fs.String("table"',
    'fs.String("database"',
    'fs.String("module"',
):
    require(needle in model_gen_flags or needle in model_datasource_flags, f"model flags missing {needle!r}")

for needle in (
    'fs.String("schema"',
    'fs.String("datasource"',
    'fs.String("dsn"',
    'fs.String("url"',
):
    require(needle in model_datasource_flags, f"model datasource flags missing {needle!r}")

for needle in (
    'fs.String("type"',
    'fs.Bool("cache"',
    'fs.String("prefix"',
    'fs.Bool("easy"',
    'fs.String("style"',
):
    require(needle in model_mongo_flags, f"model mongo flags missing {needle!r}")

for needle in (
    "isGoZeroModelStyle",
    "writeGoZeroModelFacadeFiles",
    "prepareModelTables",
    "validateKnownModelColumnTypes",
):
    require(needle in model_codegen, f"model codegen missing {needle!r}")

for needle in (
    "TestGenerateModelFromDDLGoctlOptions",
    "TestGenerateModelFromDDLMultiTableGoctlOptionsCacheReplay",
    "TestGenerateModelFromDDLGoZeroStyleWritesGoctlFacade",
    "TestGenerateModelFromDatasourceMultiTableReplayCompiles",
    "TestGenerateModelFromPostgresDatasourceMultiSchemaReplayCompiles",
    "TestGenerateModelFromReplaySchemaIRCompiles",
    "readGoctlDatasourceReplayFixture",
    "assertGoctlDatasourceReplayFixture",
    "assertRootModuleFilesUnchanged",
    "TestExecuteModelGoctlCompatibleInputAliases",
    "TestExecuteModelAcceptsGoctlShortAndSingleDashFlags",
    "TestExecuteModelMongoCacheAndPrefix",
):
    require(needle in test_haystack, f"required model parity test missing {needle}")

criteria = manifest.get("nextPromotionCriteria") or []
require(len(criteria) >= 3, "nextPromotionCriteria must include at least three items")
require(any("datasource-backed fixture replay" in item for item in criteria), "promotion criteria must mention datasource-backed fixture replay")
require(any("full goctl parity" in item for item in criteria), "promotion criteria must guard full goctl parity claims")

if missing:
    print("goctl model parity replay check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("goctl model parity replay OK")
PY
