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

required_fixture_files = {
    "testdata/goctl-replay/orderservice/orders.api",
    "testdata/goctl-replay/orderservice/etc/orders-api.yaml",
    "testdata/goctl-replay/orderservice/model/orders.sql",
    "testdata/goctl-replay/orderservice/replay.json",
    "testdata/goctl-replay/orderservice/rollback.md",
}
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
required_artifacts = {
    "cmd/orders-api/main.go",
    "internal/handler/routes.go",
    "internal/logic/pinglogic.go",
    "internal/svc/servicecontext.go",
    "internal/types/types.go",
    "internal/api/v1/types.go",
    "internal/api/v1/orders_api/routes.go",
    "internal/api/v1/orders_api/service.go",
    "model/entity/order_gen.go",
    "model/repo/order.go",
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
):
    require(source in source_of_truth, f"sourceOfTruth missing {source!r}")
    require((root / source).exists(), f"sourceOfTruth path is missing: {source}")

scope = manifest.get("scope") or {}
require(scope.get("expandsCLICommandSurface") is False, "scope.expandsCLICommandSurface must be false")
require(scope.get("fixtureClass") == "realistic-gozero-goctl-project", "scope.fixtureClass mismatch")
require("goctl" in str(scope.get("referenceFramework") or "").lower(), "scope.referenceFramework must mention goctl")

fixture_files = set(manifest.get("fixtureFiles") or [])
require(fixture_files == required_fixture_files, f"fixtureFiles drifted: missing={sorted(required_fixture_files - fixture_files)} extra={sorted(fixture_files - required_fixture_files)}")
for rel in sorted(required_fixture_files):
    require((root / rel).is_file(), f"fixture file is missing: {rel}")

replay = manifest.get("replay") or {}
require(replay.get("id") == "orderservice-goctl-replay", "replay.id mismatch")
require(replay.get("profile") == "gozero-compatible", "replay.profile must be gozero-compatible")
require(replay.get("module") == "example.com/orderservice", "replay.module mismatch")
require(replay.get("style") == "minimal", "replay.style mismatch")
require(replay.get("cache") is True, "replay.cache must be true")
expected_artifacts = set(replay.get("expectedArtifacts") or [])
require(expected_artifacts == required_artifacts, f"expectedArtifacts drifted: missing={sorted(required_artifacts - expected_artifacts)} extra={sorted(expected_artifacts - required_artifacts)}")

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
require(status.get("modelCacheTemplateDepth") == "candidate-next-task", "status.modelCacheTemplateDepth must point to next task")

for needle in (
    "goctl-real-project-replay-check",
    "orderservice-goctl-replay",
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
