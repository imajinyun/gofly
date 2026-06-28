#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import os
import pathlib
import subprocess
import sys
import tempfile

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "plugin-external-governance.json"
missing = []


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


try:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
except FileNotFoundError:
    manifest = {}
    missing.append("docs/reference/plugin-external-governance.json is missing")
except json.JSONDecodeError as exc:
    manifest = {}
    missing.append(f"docs/reference/plugin-external-governance.json is invalid JSON: {exc}")

require(manifest.get("schema") == "gofly.plugin_external_governance.v1", "plugin external governance schema mismatch")
require(manifest.get("aiflowTask") == "GOFLY-GOV-10R3-05", "plugin external governance aiflowTask mismatch")
require(manifest.get("acceptanceGate") == "make plugin-external-governance-check", "plugin external governance acceptanceGate mismatch")
require(manifest.get("aggregateGate") == "make plugin-conformance-check", "plugin external governance aggregateGate mismatch")

makefile = read_text(root / "Makefile")
plugin_go = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "plugin.go")
plugin_tests = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "plugin_test.go")
command_tests = read_text(root / "cmd" / "gofly" / "internal" / "command" / "idl_test.go")
conformance_doc = read_text(root / "docs" / "reference" / "plugin-conformance.md")
publishing_ux = read_text(root / "docs" / "reference" / "plugin-publishing-ux.json")
test_corpus = plugin_tests + "\n" + command_tests

require("plugin-external-governance-check" in makefile, "Makefile must expose plugin-external-governance-check")
plugin_line = next((line for line in makefile.splitlines() if line.startswith("plugin-conformance-check:")), "")
require("plugin-external-governance-check" in plugin_line, "plugin-conformance-check must depend on plugin-external-governance-check")

for rel in manifest.get("sourceCode") or []:
    require((root / rel).exists(), f"plugin external governance source is missing: {rel}")

policy = manifest.get("policy") or {}
for key in (
    "execCommandUsesSplitArgs",
    "remoteDownloadUsesFreshTempFiles",
    "remoteDownloadDoesNotReuseUserCache",
    "digestMismatchFailsClosed",
    "permissionEscapeFailsClosed",
    "pathEscapeAndSymlinkTraversalFailClosed",
    "partialWritesRejected",
    "outputSizeBounded",
):
    require(policy.get(key) is True, f"policy.{key} must be true")

surfaces = manifest.get("surfaces") or []
surface_ids = {item.get("id") for item in surfaces if isinstance(item, dict)}
required_surfaces = {
    "external-process",
    "remote-download",
    "registry-and-manifest",
    "filesystem-output",
    "permission-and-protocol",
    "cli-plugin-commands",
}
require(surface_ids == required_surfaces, f"plugin external surfaces mismatch: {sorted(surface_ids)!r}")

for item in surfaces:
    if not isinstance(item, dict):
        missing.append(f"surface entry must be an object: {item!r}")
        continue
    surface = item.get("id", "<missing>")
    for field in ("risk", "gate", "tests", "evidence"):
        require(item.get(field), f"surface {surface}: {field} is required")
    gate = str(item.get("gate") or "")
    require(gate.startswith("go test ") or gate.startswith("make "), f"surface {surface}: gate must be runnable")
    for test_name in item.get("tests") or []:
        require(test_name in test_corpus, f"surface {surface}: test {test_name} is missing")
    for evidence in item.get("evidence") or []:
        require((root / evidence).exists(), f"surface {surface}: evidence path is missing: {evidence}")

for needle in (
    "exec.CommandContext",
    "splitPluginArgs",
    "limitedPluginBuffer",
    "downloadPlugin",
    "validatePluginRegistryChecksum",
    "PluginPermissionWriteRelative",
    "PluginProtocolSchema",
):
    require(needle in plugin_go, f"plugin.go missing implementation anchor {needle!r}")

for needle in (
    "digest mismatch",
    "permission escape",
    "failure isolation",
    "old protocol",
    "current protocol",
    "future protocol",
):
    require(needle in conformance_doc, f"plugin conformance doc missing {needle!r}")
require("gofly.plugin_publishing_ux.v1" in publishing_ux, "plugin publishing UX manifest must remain available")

aiflow_execution = manifest.get("aiflowExecution") or {}
require(aiflow_execution.get("status") == "aiflow-driven", "aiflowExecution.status must be aiflow-driven")
require("GOFLY-GOV-10R3-05" in str(aiflow_execution.get("driver") or ""), "aiflowExecution.driver must reference GOFLY-GOV-10R3-05")
completion_policy = str(aiflow_execution.get("completionPolicy") or "")
require("make plugin-conformance-check" in completion_policy, "aiflowExecution.completionPolicy must require plugin-conformance-check")
require("commit" in completion_policy, "aiflowExecution.completionPolicy must document commit policy")

test_cmd = [
    "go",
    "test",
    "-count=1",
    "./cmd/gofly/internal/generator",
    "./cmd/gofly/internal/command",
    "-run",
    "TestPluginRunnerExternalExecutionBranches|TestPluginArgumentAndCacheHelpersBoundaries|TestLimitedPluginBufferRejectsOversizedOutput|TestPluginRunnerDownloadPluginDoesNotReuseLocalCache|TestPluginRunnerDownloadPluginIgnoresUserCache|TestPluginRunnerDownloadPluginUsesUniqueTempFile|TestResolveRemotePluginRejectsDigestMismatch|TestPluginResponseWriteFilesRejectsEscapingPaths|TestPluginResponseApplyRejectsPartialWritesWhenPatchFails|TestPluginResponseRejectsSymlinkParentTraversal|TestPluginResponseRejectsSymlinkLeaf|TestPluginManifestContractValidation|TestPluginProtocolCompatibilityMatrix|TestPluginProtocolSchemaContract|TestPluginRegistryIndexValidationAndFiltering|TestExecuteAPIPlugin|TestAPIPluginCommandLegacyPassesExtraArgsWithoutShell|TestExecutePluginInstallRunRemoteAndUninstall|TestExecutePluginRunJSONReportsWrittenFiles",
]
tmp = pathlib.Path(tempfile.mkdtemp(prefix="gofly-plugin-external-go-"))
env = os.environ.copy()
env.setdefault("GOCACHE", str(tmp / "gocache"))
env.setdefault("GOTMPDIR", str(tmp / "gotmp"))
pathlib.Path(env["GOCACHE"]).mkdir(parents=True, exist_ok=True)
pathlib.Path(env["GOTMPDIR"]).mkdir(parents=True, exist_ok=True)
test = subprocess.run(test_cmd, cwd=root, check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=env)
if test.returncode != 0:
    missing.append("targeted plugin external governance tests failed:\n" + test.stdout)

if missing:
    print("plugin external governance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("plugin external governance ok")
PY
