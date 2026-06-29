#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import subprocess
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "cli-command-surface.json"
missing = []

if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/cli-command-surface.json is missing")


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


require(manifest.get("schema") == "gofly.cli_command_surface.v1", "cli command surface schema mismatch")
require(manifest.get("acceptanceGate") == "make cli-command-surface-check", "cli command surface acceptanceGate mismatch")
require("docs/superpowers/" in set(manifest.get("ignoredPaths") or []), "cli command surface must ignore docs/superpowers/")

gitignore = read_text(root / ".gitignore")
require("docs/superpowers/" in gitignore, ".gitignore must permanently ignore docs/superpowers/")
tracked = subprocess.run(["git", "ls-files", "docs/superpowers"], cwd=root, check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
if tracked.returncode == 0:
    tracked_paths = [line for line in tracked.stdout.splitlines() if line.strip()]
    require(not tracked_paths, f"docs/superpowers must never be tracked: {tracked_paths}")
else:
    missing.append(f"could not verify docs/superpowers tracked files: {tracked.stderr.strip()}")

for rel in manifest.get("sourceCode") or []:
    require((root / rel).is_file(), f"cli command surface source is missing: {rel}")

root_commands = manifest.get("rootCommands") or []
require(len(root_commands) >= 10, "cli command surface must cover core root commands")
root_names = {item.get("name") for item in root_commands if isinstance(item, dict)}
for name in ("version", "new", "gen", "api", "rpc", "model", "plugin", "release", "doctor", "ai"):
    require(name in root_names, f"cli command surface rootCommands missing {name!r}")

registry = read_text(root / "cmd" / "gofly" / "internal" / "command" / "registry.go")
idl_registry = read_text(root / "cmd" / "gofly" / "internal" / "command" / "idl_registry.go")
cli_json = read_text(root / "docs" / "reference" / "cli-json-contracts.md")
makefile = read_text(root / "Makefile")

for item in root_commands:
    if not isinstance(item, dict):
        missing.append(f"root command entry must be an object: {item!r}")
        continue
    name = item.get("name", "")
    for field in ("name", "helpTopic"):
        require(bool(item.get(field)), f"root command {name or '<missing>'}: {field} is required")
    require(f'Name: "{name}"' in registry, f"root command {name!r} missing from registry.go")
    for alias in item.get("aliases") or []:
        require(f'"{alias}"' in registry or f'"{alias}"' in idl_registry, f"alias {alias!r} for {name!r} missing from registries")
    if item.get("jsonContract"):
        for part in str(item["jsonContract"]).split(","):
            part = part.strip()
            if not part or "..." in part:
                continue
            require(part in cli_json, f"JSON contract {part!r} for {name!r} missing from cli-json-contracts.md")
    for child in item.get("children") or []:
        if name in {"api", "rpc", "model"}:
            require(f'Name: "{child}"' in idl_registry, f"{name} child {child!r} missing from idl_registry.go")

closed_governance = manifest.get("closedGovernance") or []
closed_ids = {item.get("id") for item in closed_governance if isinstance(item, dict)}
require("stdio-error-discipline" in closed_ids, "cli command surface closedGovernance missing 'stdio-error-discipline'")
stdio_closeout = next((item for item in closed_governance if isinstance(item, dict) and item.get("id") == "stdio-error-discipline"), {})
require(stdio_closeout.get("task") == "GOFLY-P9-3-CLI-STDIO-AND-ERROR-DISCIPLINE", "stdio-error-discipline closeout task mismatch")
for subtask in (
    "GOFLY-P9-3A-CLI-STDIO-EXIT-CONTRACT",
    "GOFLY-P9-3B-CLI-FLAG-DIAGNOSTICS",
    "GOFLY-P9-3C-CLI-GOVERNANCE-MANIFEST-CLOSEOUT",
):
    require(subtask in (stdio_closeout.get("subtasks") or []), f"stdio-error-discipline closeout missing subtask {subtask}")
for gate in ("make cli-command-surface-check", "make cli-json-contract-goldens-check"):
    require(gate in (stdio_closeout.get("gates") or []), f"stdio-error-discipline closeout missing gate {gate}")

main_test = read_text(root / "cmd" / "gofly" / "main_test.go")
command_tests = read_text(root / "cmd" / "gofly" / "internal" / "command" / "idl_test.go")
golden_tests = read_text(root / "cmd" / "gofly" / "internal" / "command" / "cli_json_contract_golden_test.go")
test_corpus = "\n".join([main_test, command_tests, golden_tests])
for test_name in stdio_closeout.get("evidence") or []:
    require(test_name in test_corpus, f"stdio-error-discipline evidence test missing: {test_name}")

known_drift = manifest.get("knownDrift") or []
drift_ids = {item.get("id") for item in known_drift if isinstance(item, dict)}
for drift in ("plugin-help-boundary", "rpc-doc-discovery", "json-contract-goldens"):
    require(drift in drift_ids, f"cli command surface knownDrift missing {drift!r}")
require("stdio-error-discipline" not in drift_ids, "stdio-error-discipline must be tracked in closedGovernance, not knownDrift")

recommended = manifest.get("recommendedOrder") or []
for task in (
    "GOFLY-P9-0-CLI-GOVERNANCE-ROADMAP",
    "GOFLY-P9-1-CLI-COMMAND-SURFACE-GATE",
    "GOFLY-P9-2-CLI-JSON-CONTRACT-GOLDENS",
    "GOFLY-P9-3-CLI-STDIO-AND-ERROR-DISCIPLINE",
):
    require(task in recommended, f"cli command surface recommendedOrder missing {task}")

require("cli-command-surface-check" in makefile, "Makefile must expose cli-command-surface-check")
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
require("cli-command-surface-check" in docs_check_line, "docs-check must depend on cli-command-surface-check")
contract_deps = next((line for line in makefile.splitlines() if line.startswith("contract-docs-check:")), "")
require("cli-command-surface-check" in contract_deps, "contract-docs-check must depend on cli-command-surface-check")
require("cli-command-surface.json" in cli_json, "cli-json-contracts.md must link cli-command-surface.json")

test_cmd = ["go", "test", "-count=1", "./cmd/gofly/internal/command", "-run", "TestCLICommandSurfaceManifestMatchesRegistries_BitsUT|TestCommandHelpSubcommandBoundaries_BitsUT"]
test = subprocess.run(test_cmd, cwd=root, check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
if test.returncode != 0:
    missing.append("targeted CLI command surface tests failed:\n" + test.stdout)

if missing:
    print("cli command surface check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("cli command surface governance ok")
PY
