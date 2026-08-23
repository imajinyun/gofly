#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "goctl-surface-drift.json"
missing = []

expected_categories = {
    "supported",
    "enhanced",
    "partial-parity",
    "goctl-only",
    "gofly-enhancement",
    "intentional-gap",
}
expected_gofly_root = {
    "ai",
    "api",
    "bug",
    "complete",
    "completion",
    "config",
    "docker",
    "env",
    "example",
    "feature",
    "gateway",
    "gen",
    "handler",
    "kube",
    "migrate",
    "model",
    "new",
    "plugin",
    "quickstart",
    "release",
    "rpc",
    "template",
    "upgrade",
    "version",
}
expected_goctl_root = {
    "api",
    "bug",
    "config",
    "docker",
    "env",
    "gateway",
    "kube",
    "migrate",
    "model",
    "quickstart",
    "rpc",
    "template",
    "upgrade",
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


def make_target_deps(makefile, target):
    for line in makefile.splitlines():
        if line.startswith(target + ":"):
            return set(line.split(":", 1)[1].split("##", 1)[0].split())
    return set()


def command_specs(text):
    return set(re.findall(r'commandSpec\{Name: "([^"]+)"', text))


def goctl_new_commands(text):
    return set(re.findall(r'NewCommand\("([^"]+)"', text))


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/goctl-surface-drift.json is missing")

makefile = read_text(root / "Makefile")
registry_text = read_text(root / "cmd" / "gofly" / "internal" / "command" / "registry.go")
idl_registry_text = read_text(root / "cmd" / "gofly" / "internal" / "command" / "idl_registry.go")
goctl_compat_text = read_text(root / "docs" / "reference" / "goctl-generator-compatibility.json")
long_term_text = read_text(root / "docs" / "reference" / "framework-gap-long-term-adoption.json")
from_gozero_text = read_text(root / "docs" / "reference" / "from-go-zero-migration.md")
targets = make_target_names(makefile)

require(manifest.get("schema") == "gofly.goctl_surface_drift.v1", "schema must be gofly.goctl_surface_drift.v1")
require(manifest.get("acceptanceGate") == "make goctl-surface-drift-check", "acceptanceGate mismatch")
require(manifest.get("mode") == "report-only", "surface drift must start in report-only mode")
require("goctl-surface-drift-check" in targets, "Makefile must expose goctl-surface-drift-check")
require("check-goctl-surface-drift.sh" in makefile, "Makefile must call check-goctl-surface-drift.sh")
require("goctl-surface-drift-check" in make_target_deps(makefile, "goctl-generator-compat-check"), "goctl-generator-compat-check must depend on goctl-surface-drift-check")
require("goctl-surface-drift-check" in make_target_deps(makefile, "docs-check"), "docs-check must depend on goctl-surface-drift-check")

external = manifest.get("externalReference") or {}
require(external.get("path") == "../gozero/tools/goctl", "externalReference.path mismatch")
require(external.get("optional") is True, "externalReference.optional must be true")
require("unavailable" in str(external.get("skipPolicy") or "").lower(), "externalReference.skipPolicy must explain unavailable sibling checkout")

policy = manifest.get("surfacePolicy") or {}
for field in ("positioning", "claimPolicy", "driftPolicy"):
    require(len(str(policy.get(field) or "").split()) >= 8, f"surfacePolicy.{field} must be actionable")
require("not a full goctl replacement" in str(policy.get("positioning") or ""), "positioning must preserve migration-path stance")

top = manifest.get("topLevel") or {}
gofly_top = set(top.get("gofly") or [])
goctl_top = set(top.get("goctl") or [])
require(gofly_top == expected_gofly_root, f"topLevel.gofly drifted: missing={sorted(expected_gofly_root - gofly_top)} extra={sorted(gofly_top - expected_gofly_root)}")
require(goctl_top == expected_goctl_root, f"topLevel.goctl drifted: missing={sorted(expected_goctl_root - goctl_top)} extra={sorted(goctl_top - expected_goctl_root)}")
actual_gofly_root = command_specs(registry_text)
require(expected_gofly_root <= actual_gofly_root, f"gofly root registry missing: {sorted(expected_gofly_root - actual_gofly_root)}")

families = manifest.get("families") or {}
for family in ("api", "rpc", "model", "governance"):
    require(family in families, f"families.{family} is required")
    entry = families.get(family) or {}
    require(entry.get("classification") in expected_categories, f"{family}: unknown classification {entry.get('classification')!r}")
    require(isinstance(entry.get("goctl"), list), f"{family}: goctl list is required")
    require(isinstance(entry.get("gofly"), list), f"{family}: gofly list is required")
    require(isinstance(entry.get("nextParityFocus"), list), f"{family}: nextParityFocus list is required")

api_entry = families.get("api") or {}
rpc_entry = families.get("rpc") or {}
model_entry = families.get("model") or {}
governance_entry = families.get("governance") or {}
require(api_entry.get("classification") == "enhanced", "api classification must be enhanced")
require(rpc_entry.get("classification") == "enhanced", "rpc classification must be enhanced")
require(model_entry.get("classification") == "partial-parity", "model classification must be partial-parity")
require(governance_entry.get("classification") == "gofly-enhancement", "governance classification must be gofly-enhancement")

for needle in ("goctl api go --test", "goctl api format --stdin", "goctl api go --type-group"):
    require(needle in api_entry.get("nextParityFocus", []), f"api nextParityFocus missing {needle!r}")
for needle in ("goctl rpc protoc --multiple", "goctl rpc protoc --proto_path", "goctl rpc new --name-from-filename"):
    require(needle in rpc_entry.get("nextParityFocus", []), f"rpc nextParityFocus missing {needle!r}")
for needle in ("byte-for-byte goctl model directory layout remains an intentional gap",):
    require(needle in model_entry.get("nextParityFocus", []), f"model nextParityFocus missing {needle!r}")

idl_specs = command_specs(idl_registry_text)
for command in api_entry.get("gofly", []):
    canonical = {
        "check": "check",
        "gen": "gen",
        "docs": "docs",
        "js": "js",
        "kotlin": "kotlin",
    }.get(command, command)
    if canonical in {"client"}:
        continue
    require(canonical in idl_specs, f"api gofly surface {command!r} is not registered")
for command in rpc_entry.get("gofly", []):
    require(command in idl_specs, f"rpc gofly surface {command!r} is not registered")
for command in model_entry.get("gofly", []):
    require(command in idl_specs, f"model gofly surface {command!r} is not registered")

categories = set(manifest.get("diffCategories") or [])
require(categories == expected_categories, f"diffCategories drifted: missing={sorted(expected_categories - categories)} extra={sorted(categories - expected_categories)}")

actions = manifest.get("nextActions") or []
action_ids = {item.get("id") for item in actions if isinstance(item, dict)}
require(action_ids == {"goctl-oracle-replay", "model-parity-replay", "cli-behavior-drift-report"}, f"nextActions drifted: {sorted(action_ids)!r}")
for action in actions:
    require(action.get("priority") in {"P0", "P1"}, f"{action.get('id')}: priority must be P0 or P1")
    require(action.get("status") in {"implemented", "planned"}, f"{action.get('id')}: status must be implemented or planned")
    require(str(action.get("gate") or "").startswith("make "), f"{action.get('id')}: gate must be a make target")
    gate = str(action.get("gate")).removeprefix("make ").split()[0]
    require(gate in targets, f"{action.get('id')}: unknown gate {gate!r}")
    require(len(str(action.get("description") or "").split()) >= 8, f"{action.get('id')}: description must be actionable")

release_gates = set(manifest.get("releaseGates") or [])
expected_release_gates = {
    "make goctl-surface-drift-check",
    "make goctl-generator-compat-check",
    "make goctl-real-project-replay-check",
}
require(release_gates == expected_release_gates, f"releaseGates drifted: missing={sorted(expected_release_gates - release_gates)} extra={sorted(release_gates - expected_release_gates)}")

for needle in (
    "goctl-surface-drift",
    "goctl surface",
    "goctl-compatible migration path",
    "goctl-real-project-replay-check",
):
    require(needle in goctl_compat_text or needle in long_term_text or needle in from_gozero_text or needle in json.dumps(manifest), f"documentation missing {needle!r}")

gozero_root = root.parent / "gozero" / "tools" / "goctl"
if gozero_root.is_dir():
    root_text = read_text(gozero_root / "cmd" / "root.go")
    api_text = read_text(gozero_root / "api" / "cmd.go")
    rpc_text = read_text(gozero_root / "rpc" / "cmd.go")
    model_text = read_text(gozero_root / "model" / "cmd.go")
    goctl_root_packages = {
        "template": "tpl",
    }
    for command in goctl_top:
        package = goctl_root_packages.get(command, command)
        require(f"{package}.Cmd" in root_text or f"{package}, " in root_text or f"{package})" in root_text, f"goctl root command {command!r} not found in sibling checkout")
    require(set(api_entry.get("goctl") or []) <= goctl_new_commands(api_text), f"goctl api surface missing from sibling checkout: {sorted(set(api_entry.get('goctl') or []) - goctl_new_commands(api_text))}")
    require(set(rpc_entry.get("goctl") or []) <= goctl_new_commands(rpc_text), f"goctl rpc surface missing from sibling checkout: {sorted(set(rpc_entry.get('goctl') or []) - goctl_new_commands(rpc_text))}")
    for needle in ("mysql", "ddl", "datasource", "pg", "mongo"):
        require(needle in model_text, f"goctl model sibling checkout missing {needle!r}")

if missing:
    print("goctl surface drift check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

if gozero_root.is_dir():
    print("goctl surface drift OK (sibling gozero scanned)")
else:
    print("goctl surface drift OK (sibling gozero unavailable; contract-only mode)")
PY
