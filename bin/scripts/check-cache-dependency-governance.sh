#!/usr/bin/env sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
go_cmd="${GO:-go}"
testflags="${TESTFLAGS:--shuffle=on}"
tmp="$(mktemp -d)"
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM

case " ${GOFLAGS:-} " in
*" -count=1 "*) ;;
*) export GOFLAGS="${GOFLAGS:+$GOFLAGS }-count=1" ;;
esac
export GOCACHE="${GOCACHE:-$tmp/gocache}"
export GOTMPDIR="${GOTMPDIR:-$tmp/gotmp}"
export GOPROXY="${GOPROXY:-direct}"
mkdir -p "$GOCACHE" "$GOTMPDIR"

python3 - "$root" <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
manifest_path = root / "docs" / "reference" / "cache-dependency-governance.json"
missing = []


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
    return gate.startswith("go test ") or gate.startswith("GOVERNANCE_ISOLATE_GOMODCACHE=true make ")


try:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
except FileNotFoundError:
    manifest = {}
    missing.append("docs/reference/cache-dependency-governance.json is missing")
except json.JSONDecodeError as exc:
    manifest = {}
    missing.append(f"docs/reference/cache-dependency-governance.json is invalid JSON: {exc}")

makefile = read_text(root / "Makefile")
script = read_text(root / "bin" / "scripts" / "check-cache-dependency-governance.sh")
governance_script = read_text(root / "bin" / "scripts" / "governance-10-rounds.sh")
dependency_script = read_text(root / "bin" / "scripts" / "check-dependency-upgrade-evidence.sh")
root_policy_script = read_text(root / "bin" / "scripts" / "check-root-dependency-policy.sh")
tidy_script = read_text(root / "bin" / "scripts" / "check-mod-tidy.sh")
dependency_manifest = read_text(root / "docs" / "reference" / "dependency-upgrade-evidence.json")
cache_tests = read_text(root / "cache" / "cache_test.go") + "\n" + read_text(root / "cache" / "tiered_test.go")
plugin_tests = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "plugin_test.go")
targets = make_target_names(makefile)

require(manifest.get("schema") == "gofly.cache_dependency_governance.v1", "cache dependency governance schema mismatch")
require(manifest.get("aiflowTask") == "GOFLY-GOV-10R3-06", "cache dependency governance aiflowTask mismatch")
require(manifest.get("acceptanceGate") == "make cache-dependency-governance-check", "cache dependency governance acceptanceGate mismatch")

aggregate_gates = set(manifest.get("aggregateGates") or [])
require("make docs-check" in aggregate_gates, "aggregateGates must include make docs-check")
require("make dependency-upgrade-evidence-check" in aggregate_gates, "aggregateGates must include make dependency-upgrade-evidence-check")

docs_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
require("cache-dependency-governance-check" in targets, "Makefile must expose cache-dependency-governance-check")
require("check-cache-dependency-governance.sh" in makefile, "Makefile must call check-cache-dependency-governance.sh")
require("cache-dependency-governance-check" in docs_line, "docs-check must depend on cache-dependency-governance-check")

for rel in manifest.get("sourceCode") or []:
    require((root / rel).exists(), f"cache dependency governance source is missing: {rel}")

policy = manifest.get("policy") or {}
for key in (
    "runtimeCacheBypassUsesExplicitEnvironment",
    "goBuildCacheUsesTemporaryDirectory",
    "goTempUsesTemporaryDirectory",
    "goModCacheIsolationIsExplicitOptIn",
    "goProxyDefaultsToDirectForGovernance",
    "remotePluginDownloadsAvoidUserCache",
    "rootDependenciesMustBeImportedByRootModule",
    "generatedOnlyDependenciesStayOutOfRootModule",
    "tidyCheckRestoresGoModAndGoSum",
):
    require(policy.get(key) is True, f"policy.{key} must be true")

surfaces = manifest.get("surfaces") or []
surface_ids = {item.get("id") for item in surfaces if isinstance(item, dict)}
required_surface_ids = {
    "runtime-cache-bypass",
    "go-build-cache-isolation",
    "gomodcache-isolation",
    "remote-plugin-cache-safety",
    "root-dependency-policy",
    "module-tidy-recovery",
    "dependency-upgrade-evidence",
}
require(surface_ids == required_surface_ids, f"cache dependency surfaces mismatch: {sorted(surface_ids)!r}")

searchable_by_surface = {
    "runtime-cache-bypass": script + "\n" + governance_script + "\n" + cache_tests,
    "go-build-cache-isolation": script + "\n" + governance_script,
    "gomodcache-isolation": script + "\n" + governance_script,
    "remote-plugin-cache-safety": script + "\n" + governance_script + "\n" + plugin_tests,
    "root-dependency-policy": script + "\n" + root_policy_script,
    "module-tidy-recovery": script + "\n" + tidy_script,
    "dependency-upgrade-evidence": script + "\n" + dependency_script + "\n" + dependency_manifest,
}

for item in surfaces:
    if not isinstance(item, dict):
        missing.append(f"surface entry must be an object: {item!r}")
        continue
    surface = item.get("id", "<missing>")
    for field in ("risk", "gate", "tests", "evidence"):
        require(item.get(field), f"surface {surface}: {field} is required")
    require(gate_is_known(str(item.get("gate") or ""), targets), f"surface {surface}: gate is not known: {item.get('gate')!r}")
    corpus = searchable_by_surface.get(surface, "")
    for test_name in item.get("tests") or []:
        is_command_anchor = gate_is_known(str(test_name), targets)
        require(test_name in corpus or (root / test_name).exists() or is_command_anchor, f"surface {surface}: test or script anchor {test_name!r} is missing")
    for evidence in item.get("evidence") or []:
        require(str(evidence) in corpus, f"surface {surface}: evidence anchor {evidence!r} is missing")

for needle in (
    "GOFLY_CACHE_DISABLED=true",
    "export GOCACHE",
    "export GOTMPDIR",
    "export GOPROXY",
    "check-dependency-upgrade-evidence.sh",
    "check-root-dependency-policy.sh",
    "check-mod-tidy.sh",
    "Test(CacheDisabledBy|TieredCacheDisabledBy)",
    "TestPluginRunnerDownloadPlugin(DoesNotReuseLocalCache|IgnoresUserCache|UsesUniqueTempFile)",
):
    require(needle in script, f"check-cache-dependency-governance.sh missing {needle!r}")

for needle in (
    "GOFLY_GOVERNANCE_CACHE_DISABLED",
    "GOVERNANCE_ISOLATE_GOMODCACHE",
    "export GOMODCACHE",
    "GOPROXY",
):
    require(needle in governance_script, f"governance-10-rounds.sh missing {needle!r}")

for needle in (
    "generated-project-dependencies",
    "root-runtime-dependencies",
    "docker-backed-integration-dependencies",
    "gofly.dependency_upgrade_evidence.v1",
):
    require(needle in dependency_manifest, f"dependency-upgrade-evidence.json missing {needle!r}")

execution = manifest.get("aiflowExecution") or {}
require(execution.get("status") == "local-fallback", "aiflowExecution.status must be local-fallback")
require("fmt" in str(execution.get("blocker") or ""), "aiflowExecution.blocker must document current aiflow compile blocker")

if missing:
    print("cache dependency governance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("cache dependency governance manifest ok")
PY

sh "$root/bin/scripts/check-dependency-upgrade-evidence.sh"
sh "$root/bin/scripts/check-root-dependency-policy.sh"
sh "$root/bin/scripts/check-mod-tidy.sh"

GOFLY_CACHE_DISABLED=true "$go_cmd" test $testflags ./cache -run 'Test(CacheDisabledBy|TieredCacheDisabledBy)'
"$go_cmd" test $testflags ./cmd/gofly/internal/generator -run 'TestPluginRunnerDownloadPlugin(DoesNotReuseLocalCache|IgnoresUserCache|UsesUniqueTempFile)'

printf '%s\n' "cache and remote-dependency governance ok"
