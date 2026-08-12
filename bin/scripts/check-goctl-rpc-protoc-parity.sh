#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "goctl-rpc-protoc-parity.json"
missing = []

required_surfaces = {
    "rpc-protoc-include-path": "implemented",
    "rpc-protoc-go-options": "implemented",
    "rpc-protoc-multiple": "implemented-for-gofly-plugin",
    "rpc-protoc-client": "implemented-for-gofly-plugin",
    "rpc-protoc-name-from-filename": "implemented-for-gofly-plugin",
    "rpc-protoc-external-plugin": "implemented-explicit-opt-in",
}
required_flags = {
    "proto_path",
    "I",
    "go_opt",
    "go-grpc_opt",
    "multiple",
    "client",
    "name-from-filename",
    "plugin",
}
required_diff_categories = {
    "same-contract",
    "compatible-flag",
    "implemented-for-gofly-plugin",
    "implemented-explicit-opt-in",
    "missing-capability",
    "generation-error",
}
required_release_gates = {
    "make goctl-rpc-protoc-parity-check",
    "make goctl-generator-compat-check",
    "make zrpc-proto-compatibility-check",
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
zrpc_text = read_text(root / "docs" / "reference" / "zrpc-proto-compatibility.json")
from_gozero_text = read_text(root / "docs" / "reference" / "from-go-zero-migration.md")
rpc_protoc_command = read_text(root / "cmd" / "gofly" / "internal" / "command" / "rpc_protoc_command.go")
command_args_go = read_text(root / "cmd" / "gofly" / "internal" / "command" / "command_args.go")
protoc_go = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "protoc.go")
protoc_plugin_go = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "protoc_plugin.go")
command_tests = read_text(root / "cmd" / "gofly" / "internal" / "command" / "idl_test.go")
generator_tests = read_text(root / "cmd" / "gofly" / "internal" / "generator" / "idl_test.go")

targets = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
generator_check_line = next((line for line in makefile.splitlines() if line.startswith("goctl-generator-compat-check:")), "")

require(manifest.get("schema") == "gofly.goctl_rpc_protoc_parity.v1", "schema must be gofly.goctl_rpc_protoc_parity.v1")
require(manifest.get("acceptanceGate") == "make goctl-rpc-protoc-parity-check", "acceptanceGate mismatch")
require("goctl-rpc-protoc-parity-check" in targets, "Makefile must expose goctl-rpc-protoc-parity-check")
require("check-goctl-rpc-protoc-parity.sh" in makefile, "Makefile must call check-goctl-rpc-protoc-parity.sh")
require("goctl-rpc-protoc-parity-check" in docs_check_line, "docs-check must depend on goctl-rpc-protoc-parity-check")
require("goctl-rpc-protoc-parity-check" in generator_check_line, "goctl-generator-compat-check must depend on goctl-rpc-protoc-parity-check")

scope = manifest.get("scope") or {}
require(scope.get("expandsCLICommandSurface") is False, "scope.expandsCLICommandSurface must be false")
require("migration-critical" in str(scope.get("positioning") or ""), "scope.positioning must preserve migration-critical stance")
require("external plugin execution" in str(scope.get("positioning") or ""), "scope.positioning must mention external plugin execution boundary")
require("goctl rpc protoc --multiple" in str(scope.get("referenceFramework") or ""), "scope.referenceFramework must mention goctl rpc protoc --multiple")

for source in manifest.get("sourceOfTruth") or []:
    require((root / source).exists(), f"sourceOfTruth path missing: {source}")

policy = manifest.get("compatibilityPolicy") or {}
for key in ("standardProtoc", "goflyPlugin", "standardModeWrapperFlags", "externalPlugin", "timeout"):
    require(len(str(policy.get(key) or "").split()) >= 8, f"compatibilityPolicy.{key} must be actionable")
require("must not alter standard protoc argv" in str(policy.get("standardModeWrapperFlags") or ""), "standardModeWrapperFlags policy must preserve standard argv boundary")
require("--allow-external-plugin" in str(policy.get("externalPlugin") or ""), "externalPlugin policy must require explicit opt-in")
for rejected in ("flag-like values", "URL schemes", "whitespace", "control characters", "shell metacharacters"):
    require(rejected in str(policy.get("externalPlugin") or ""), f"externalPlugin policy must reject {rejected}")

surfaces = manifest.get("rpcSurfaces") or []
surface_map = {item.get("id"): item for item in surfaces}
require(set(surface_map) == set(required_surfaces), f"rpcSurfaces drifted: missing={sorted(set(required_surfaces) - set(surface_map))} extra={sorted(set(surface_map) - set(required_surfaces))}")

all_flags = set()
test_haystack = command_tests + "\n" + generator_tests
for surface_id, status in required_surfaces.items():
    item = surface_map.get(surface_id) or {}
    require(item.get("status") == status, f"{surface_id}: status must be {status}")
    require(str(item.get("goctlSurface") or "").startswith("goctl rpc protoc "), f"{surface_id}: goctlSurface must start with goctl rpc protoc")
    require(str(item.get("goflySurface") or "").startswith("gofly rpc protoc "), f"{surface_id}: goflySurface must start with gofly rpc protoc")
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
    "goctl rpc protoc --multiple",
    "goctl rpc protoc --proto_path",
    "goctl rpc new --name-from-filename",
):
    require(needle in surface_text, f"goctl surface drift contract missing {needle!r}")

for needle in (
    "goctl-rpc-protoc-parity-check",
    "goctl-rpc-protoc-parity.json",
    "goctl-rpc-protoc-parity",
):
    require(needle in generator_text or needle in from_gozero_text or needle in makefile or needle in json.dumps(manifest), f"RPC protoc parity evidence missing {needle!r}")

for needle in (
    "csvListFlag",
    'fs.Var(&protoPath, "proto_path"',
    'fs.Var(&protoPath, "proto-path"',
    'fs.Var(&protoPath, "I"',
    'fs.Bool("multiple"',
    'fs.Bool("m"',
    'fs.Bool("client"',
    'fs.Bool("c"',
    'fs.Bool("name-from-filename"',
    'fs.String("plugin"',
    'fs.Bool("allow-external-plugin"',
    "validateExternalProtocPlugins",
    "external protoc plugins require --allow-external-plugin",
    "externalProtocPluginsForOptions",
):
    require(needle in rpc_protoc_command, f"rpc protoc command missing {needle!r}")

for needle in (
    "type csvListFlag struct",
    "append(f.values, splitCSV(value)...",
    "strings.Join(f.values",
):
    require(needle in command_args_go, f"command args missing {needle!r}")

for needle in (
    "exec.CommandContext(ctx, bin, args...)",
    "#nosec G204",
    "WaitDelay",
    "context.WithTimeout",
    'args = append(args, "-I", path)',
    '"--go_out="+goOut',
    '"--go-grpc_out="+goGRPCOut',
    '"--gofly_opt="+opt',
	"ExternalPlugins []string",
	'"--plugin="+plugin',
    "hasProtocOptionOverride",
):
    require(needle in protoc_go, f"protoc generator missing {needle!r}")

for needle in (
    "NameFromFilename bool",
    "NoClient         bool",
    "Multiple         bool",
    "name_from_filename",
    "no_client",
    "multiple",
    "if opts.NameFromFilename",
    "if !opts.NoClient",
    "if !opts.Multiple",
	"externalProtocPluginsForOptions",
):
	require(needle in protoc_plugin_go or needle in rpc_protoc_command, f"protoc plugin missing {needle!r}")

for needle in (
    "external-proto-imports",
    "multiple-services",
    "client-wrapper",
    "supported",
):
    require(needle in zrpc_text, f"zrpc compatibility matrix missing {needle!r}")

for needle in (
    "TestExecuteRPCProtocAcceptsGoctlPositionalAndSrcAlias",
    "TestExecuteRPCProtocAcceptsGoctlReservedFlags",
    "TestExecuteRPCProtocGoflyPluginArgs",
    "TestExecuteRPCProtocGoflyPluginNoClientMultipleArgs",
    "TestExecuteRPCProtocAllowsExternalPluginOptIn",
    "TestExecuteRPCProtocRejectsUnsafeExternalPluginOptIn",
    "unsafe external protoc plugin",
    '"--gofly_out="',
    '"--gofly_opt=multiple=true"',
    '"--gofly_opt=no_client=true"',
    '"--gofly_opt=name_from_filename=true"',
    '"--plugin=protoc-gen-api"',
    '"--allow-external-plugin"',
    "TestProtocArgs",
    "TestProtocArgsUserPathOptionsOverrideDefaults",
    "TestProtocArgsWithGoflyPlugin",
    "TestExecuteRPCGenGoflyNoClientAndMultiple",
):
    require(needle in test_haystack, f"required RPC protoc parity test missing {needle}")

criteria = manifest.get("nextPromotionCriteria") or []
require(len(criteria) >= 3, "nextPromotionCriteria must include at least three items")
require(any("--allow-external-plugin" in item for item in criteria), "promotion criteria must mention explicit external plugin opt-in")
require(any("flag-like values" in item and "shell metacharacters" in item for item in criteria), "promotion criteria must mention unsafe plugin value rejection")
require(any("--gofly_out" in item and "--gofly_opt" in item for item in criteria), "promotion criteria must preserve standard protoc no-gofly argv boundary")
require(any("full goctl parity" in item for item in criteria), "promotion criteria must guard full goctl parity claims")

if missing:
    print("goctl RPC protoc parity check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("goctl RPC protoc parity OK")
PY
