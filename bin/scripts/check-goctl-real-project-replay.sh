#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "goctl-real-project-replay.json"
missing = []

required_gates = {
    "make goctl-real-project-replay-check",
    "make goctl-generator-compat-check",
    "make generated-version-compat-check",
}
required_diff_categories = {
    "deterministic-repeat-generation",
    "compatible-addition",
    "generated-cache-template",
    "breaking-candidate",
}
required_matrix_capabilities = {
    "api-import",
    "multi-service-group",
    "multi-middleware",
    "complex-model",
    "soft-delete",
    "optimistic-lock",
    "composite-unique-key",
    "cache-template",
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
    missing.append("docs/reference/goctl-real-project-replay.json is missing")

makefile = read_text(root / "Makefile")
goctl_manifest_text = read_text(root / "docs" / "reference" / "goctl-generator-compatibility.json")
upgrade_manifest_text = read_text(root / "docs" / "reference" / "generated-upgrade-dry-run.json")
test_text = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "goctl_replay_test.go")
targets = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")

require(manifest.get("schema") == "gofly.goctl_real_project_replay.v1", "schema must be gofly.goctl_real_project_replay.v1")
require(manifest.get("acceptanceGate") == "make goctl-real-project-replay-check", "acceptanceGate must be make goctl-real-project-replay-check")
require("goctl-real-project-replay-check" in targets, "Makefile must expose goctl-real-project-replay-check")
require("goctl-real-project-replay-check" in docs_check_line, "docs-check must depend on goctl-real-project-replay-check")
require("check-goctl-real-project-replay.sh" in makefile, "Makefile must call check-goctl-real-project-replay.sh")

source_of_truth = set(manifest.get("sourceOfTruth") or [])
for source in (
    "docs/reference/goctl-generator-compatibility.json",
    "docs/reference/generated-upgrade-dry-run.json",
    "testdata/goctl-replay/orderservice/replay.json",
    "testdata/goctl-replay/inventoryservice/replay.json",
):
    require(source in source_of_truth, f"sourceOfTruth missing {source!r}")
    require((root / source).exists(), f"sourceOfTruth path is missing: {source}")

scope = manifest.get("scope") or {}
require(scope.get("expandsCLICommandSurface") is False, "scope.expandsCLICommandSurface must be false")
require(scope.get("fixtureClass") == "realistic-gozero-goctl-project-matrix", "scope.fixtureClass mismatch")
require("goctl" in str(scope.get("referenceFramework") or "").lower(), "scope.referenceFramework must mention goctl")

matrix = manifest.get("matrix") or {}
fixtures = matrix.get("fixtures") or []
require(matrix.get("minimumFixtures") == 2, "matrix.minimumFixtures must be 2")
require(len(fixtures) >= matrix.get("minimumFixtures", 0), "matrix must contain at least minimumFixtures entries")
require(set(matrix.get("requiredCapabilities") or []) == required_matrix_capabilities, "matrix.requiredCapabilities drifted")

fixture_ids = set()
matrix_capabilities = set()
for entry in fixtures:
    fixture_id = entry.get("id")
    fixture_ids.add(fixture_id)
    require(fixture_id, "matrix fixture id is required")
    require(entry.get("profile") == "gozero-compatible", f"{fixture_id}: profile must be gozero-compatible")
    require(entry.get("style") == "minimal", f"{fixture_id}: style mismatch")
    require(entry.get("cache") is True, f"{fixture_id}: cache must be true")
    manifest_rel = entry.get("manifest")
    require(manifest_rel in source_of_truth, f"{fixture_id}: manifest missing from sourceOfTruth")
    manifest_file = root / manifest_rel if manifest_rel else root / "__missing__"
    require(manifest_file.is_file(), f"{fixture_id}: manifest path is missing: {manifest_rel}")
    fixture_manifest = json.loads(manifest_file.read_text(encoding="utf-8")) if manifest_file.is_file() else {}
    for field in ("id", "module", "profile", "style", "cache", "expectedArtifacts"):
        require(fixture_manifest.get(field) == entry.get(field), f"{fixture_id}: matrix field {field} drifted from replay.json")
    for rel in entry.get("files") or []:
        require((root / rel).is_file(), f"{fixture_id}: fixture file is missing: {rel}")
    expected_artifacts = set(entry.get("expectedArtifacts") or [])
    require(expected_artifacts, f"{fixture_id}: expectedArtifacts are required")
    require(expected_artifacts == set(fixture_manifest.get("expectedArtifacts") or []), f"{fixture_id}: expectedArtifacts drifted from replay.json")
    matrix_capabilities.update(entry.get("capabilities") or [])
    matrix_capabilities.update(fixture_manifest.get("capabilities") or [])

require({"orderservice-goctl-replay", "inventoryservice-imported-multigroup-replay"}.issubset(fixture_ids), f"fixture ids drifted: {sorted(fixture_ids)!r}")
missing_capabilities = required_matrix_capabilities - matrix_capabilities
require(not missing_capabilities, f"matrix capabilities missing: {sorted(missing_capabilities)!r}")
require((root / "testdata/goctl-replay/inventoryservice/types/common.api").is_file(), "inventory imported common.api is missing")
inventory_api = read_text(root / "testdata/goctl-replay/inventoryservice/inventory.api")
for needle in (
    'import "types/common.api"',
    "service inventory-api",
    "service admin-api",
    "middlewares: auth,trace,audit",
    "middlewares: adminAuth,trace",
):
    require(needle in inventory_api, f"inventory API fixture missing {needle!r}")
inventory_sql = read_text(root / "testdata/goctl-replay/inventoryservice/model/inventory.sql")
for needle in ("UNIQUE KEY uk_inventory_tenant_sku_warehouse", "version bigint", "deleted_at timestamp", "KEY idx_inventory_status_updated"):
    require(needle in inventory_sql, f"inventory SQL fixture missing {needle!r}")

diff_contract = manifest.get("diffContract") or {}
categories = set(diff_contract.get("categories") or [])
require(categories == required_diff_categories, f"diff categories drifted: {sorted(categories)!r}")
for field in ("repeatGeneration", "diffReport", "rollbackNote"):
    require(diff_contract.get(field), f"diffContract.{field} is required")

smoke = manifest.get("smoke") or {}
for field in ("goTest", "repeatReplay", "rootDependencyPolicy"):
    require(smoke.get(field), f"smoke.{field} is required")
require("go test ./..." in str(smoke.get("goTest")), "smoke.goTest must run generated module tests")
require("go.mod" in str(smoke.get("rootDependencyPolicy")), "smoke.rootDependencyPolicy must mention go.mod")

release_gates = set(manifest.get("releaseGates") or [])
require(release_gates == required_gates, f"releaseGates drifted: missing={sorted(required_gates - release_gates)} extra={sorted(release_gates - required_gates)}")
for gate in release_gates:
    require(gate_is_known(gate, targets), f"release gate is not known: {gate}")

status = manifest.get("status") or {}
require(status.get("goctlCommandSurface") == "unchanged", "status.goctlCommandSurface must remain unchanged")
require(status.get("fixtureMatrixDepth") == "imported-multigroup-complex-model", "status.fixtureMatrixDepth mismatch")
require(status.get("modelCacheTemplateDepth") == "covered-by-replay-matrix", "status.modelCacheTemplateDepth mismatch")

for needle in (
    "goctl-real-project-replay-check",
    "orderservice-goctl-replay",
    "inventoryservice-imported-multigroup-replay",
    "api-import",
    "multi-service-group",
    "composite-unique-key",
    "deterministic-repeat-generation",
    "compatible-addition",
    "generated-cache-template",
    "breaking-candidate",
):
    require(needle in test_text or needle in json.dumps(manifest), f"replay evidence missing {needle!r}")

for needle in (
    "gozero-compatible-profile",
    "goctl-compatible-flags",
    "generated-version-fixtures",
    "upgrade-diff-contract",
):
    require(needle in goctl_manifest_text, f"goctl compatibility matrix missing {needle!r}")
require("diffReportContract" in upgrade_manifest_text, "generated upgrade dry-run manifest must expose diffReportContract")

if missing:
    print("goctl real-project replay check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("goctl real-project replay OK")
PY
