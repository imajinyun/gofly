#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import subprocess
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "cli-configuration-governance.json"
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
    missing.append("docs/reference/cli-configuration-governance.json is missing")
except json.JSONDecodeError as exc:
    manifest = {}
    missing.append(f"docs/reference/cli-configuration-governance.json is invalid JSON: {exc}")

require(manifest.get("schema") == "gofly.cli_configuration_governance.v1", "CLI configuration governance schema mismatch")
require(manifest.get("aiflowTask") == "GOFLY-GOV-10R3-03", "CLI configuration governance aiflowTask mismatch")
require(manifest.get("acceptanceGate") == "make cli-configuration-governance-check", "CLI configuration governance acceptanceGate mismatch")
require(manifest.get("surfaceGate") == "make cli-command-surface-check", "CLI configuration governance surfaceGate mismatch")
require(manifest.get("jsonGoldenGate") == "make cli-json-contract-goldens-check", "CLI configuration governance jsonGoldenGate mismatch")

makefile = read_text(root / "Makefile")
config_command = read_text(root / "cmd" / "gofly" / "internal" / "command" / "config_command.go")
root_command = read_text(root / "cmd" / "gofly" / "internal" / "command" / "root.go")
registry = read_text(root / "cmd" / "gofly" / "internal" / "command" / "registry.go")
idl_tests = read_text(root / "cmd" / "gofly" / "internal" / "command" / "idl_test.go")
ai_tests = read_text(root / "cmd" / "gofly" / "internal" / "command" / "ai_helpers_test.go")
golden_tests = read_text(root / "cmd" / "gofly" / "internal" / "command" / "cli_json_contract_golden_test.go")
cli_surface = read_text(root / "docs" / "reference" / "cli-command-surface.json")
cli_goldens = read_text(root / "docs" / "reference" / "cli-json-contract-goldens.json")

for rel in manifest.get("sourceCode") or []:
    require((root / rel).is_file(), f"CLI configuration source is missing: {rel}")

docs_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
contract_line = next((line for line in makefile.splitlines() if line.startswith("contract-docs-check:")), "")
require("cli-configuration-governance-check" in makefile, "Makefile must expose cli-configuration-governance-check")
require("cli-configuration-governance-check" in docs_line, "docs-check must depend on cli-configuration-governance-check")
require("cli-configuration-governance-check" in contract_line, "contract-docs-check must depend on cli-configuration-governance-check")

config_surface = next((item for item in manifest.get("configurationSurfaces") or [] if isinstance(item, dict) and item.get("id") == "config-command"), {})
require("config" in {item.get("name") for item in json.loads(cli_surface or "{}").get("rootCommands", []) if isinstance(item, dict)}, "cli-command-surface.json must include config root command")
for child in ("init", "show", "get", "set", "clean"):
    require(f'case "{child}"' in config_command, f"config command implementation missing {child!r}")
    require(child in (config_surface.get("commands") or []), f"CLI configuration manifest missing config command {child!r}")

require('Name: "config"' in registry, "root registry must include config command")
require('generator.DefaultConfigFile' in config_command, "config command must use generator.DefaultConfigFile")
require('registerDryRunPlanFlags' in config_command, "config command must support dry-run/plan flags")
require('errUsage' in config_command, "config command must classify usage errors with errUsage")
require('parseGlobalControls' in root_command or 'parseGlobalOutput' in idl_tests, "global output parsing evidence is missing")

policy = manifest.get("policy") or {}
for key in (
    "preserveExistingSemantics",
    "noImplicitJsonForConfigCommands",
    "usageErrorsExitCode2",
    "globalOutputJsonUsesSingleEnvelope",
    "configMutationsSupportDryRunPlan",
):
    require(policy.get(key) is True, f"policy.{key} must be true")

test_corpus = "\n".join([idl_tests, ai_tests, golden_tests])
for test_name in manifest.get("evidenceTests") or []:
    require(test_name in test_corpus, f"CLI configuration evidence test missing: {test_name}")

required_patterns = {
    "config-defaults": r"TestExecuteConfigInitPersistsDefaultEcosystemFeature",
    "config-show-get": r"TestExecuteConfigShowAndGet",
    "config-set-get": r"TestExecuteConfigSetAndGet",
    "config-clean": r"TestExecuteConfigClean",
    "config-usage-errors": r"TestExecuteConfigInvalidSubcommand|config command text dry-run and apply branches",
    "global-output-mode": r"TestParseGlobalOutput|TestGlobalOutputJSONErrorsToStdoutOnly|TestCLISTDIOExitContract",
    "json-goldens": r"TestCLIJSONContractGoldens|TestCLIJSONErrorEnvelopeGolden",
    "help-alias-alignment": r"TestCLICommandSurfaceManifestMatchesRegistries|TestCommandHelpSubcommandBoundaries",
}
coverage_ids = {item.get("id") for item in manifest.get("coverage") or [] if isinstance(item, dict)}
for coverage_id, pattern in required_patterns.items():
    require(coverage_id in coverage_ids, f"CLI configuration coverage missing {coverage_id!r}")
    require(re.search(pattern, test_corpus), f"CLI configuration tests missing pattern for {coverage_id}: {pattern}")

known_drifts = {item.get("id") for item in manifest.get("knownDrift") or [] if isinstance(item, dict)}
require("config-json-output" in known_drifts, "knownDrift must document config-json-output boundary")
require("gofly.cli_json_contract_goldens.v1" in cli_goldens, "CLI JSON golden manifest must remain available")
surfaces = {item.get("id"): item for item in manifest.get("configurationSurfaces") or [] if isinstance(item, dict)}
help_surface = surfaces.get("help-alias-contract") or {}
require(bool(help_surface), "configurationSurfaces must include help-alias-contract")
require(help_surface.get("source") == "cmd/gofly/internal/command/help_metadata.go", "help-alias-contract source mismatch")
require(help_surface.get("surfaceManifest") == "docs/reference/cli-command-surface.json", "help-alias-contract surfaceManifest mismatch")
require("topLevelHelpAliases" in read_text(root / "cmd" / "gofly" / "internal" / "command" / "help_metadata.go"), "help alias metadata missing topLevelHelpAliases")
require("nestedHelpAliases" in read_text(root / "cmd" / "gofly" / "internal" / "command" / "help_metadata.go"), "help alias metadata missing nestedHelpAliases")
require("TestCLICommandSurfaceManifestMatchesRegistries" in test_corpus, "help alias evidence missing TestCLICommandSurfaceManifestMatchesRegistries")
require("TestCommandHelpSubcommandBoundaries" in test_corpus, "help alias evidence missing TestCommandHelpSubcommandBoundaries")
aiflow_execution = manifest.get("aiflowExecution") or {}
require(aiflow_execution.get("status") == "aiflow-driven", "aiflowExecution.status must be aiflow-driven")
require("GOFLY-GOV-10R3-03" in aiflow_execution.get("driver", ""), "aiflowExecution.driver must reference GOFLY-GOV-10R3-03")
require("make cli-command-surface-check" in aiflow_execution.get("completionPolicy", ""), "aiflowExecution.completionPolicy must require cli-command-surface-check")
require("make cli-configuration-governance-check" in aiflow_execution.get("completionPolicy", ""), "aiflowExecution.completionPolicy must require cli-configuration-governance-check")

test_cmd = [
    "go",
    "test",
    "-count=1",
    "./cmd/gofly/internal/command",
    "-run",
    "TestCLICommandSurfaceManifestMatchesRegistries|TestCommandHelpSubcommandBoundaries|TestExecuteConfigShowAndGet|TestExecuteConfigSetAndGet|TestExecuteConfigInvalidSubcommand|TestExecuteConfigClean|TestExecuteConfigInitPersistsDefaultEcosystemFeature|TestExecuteConfigSetFeaturesValidatesAndAllowsEmptyList|TestParseGlobalOutput|TestGlobalOutputJSONErrorsToStdoutOnly|TestCLISTDIOExitContract|TestCommandConfigFeaturePluginCoverageBuffer|TestCLIJSONContractGoldens|TestCLIJSONErrorEnvelopeGolden",
]
test = subprocess.run(test_cmd, cwd=root, check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
if test.returncode != 0:
    missing.append("targeted CLI configuration tests failed:\n" + test.stdout)

if missing:
    print("CLI configuration governance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("CLI configuration governance ok")
PY
