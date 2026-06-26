#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "cli-json-contract-goldens.json"
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
    missing.append("docs/reference/cli-json-contract-goldens.json is missing")
except json.JSONDecodeError as exc:
    manifest = {}
    missing.append(f"docs/reference/cli-json-contract-goldens.json is invalid JSON: {exc}")

require(manifest.get("schema") == "gofly.cli_json_contract_goldens.v1", "CLI JSON golden schema mismatch")
require(manifest.get("acceptanceGate") == "make cli-json-contract-goldens-check", "CLI JSON golden acceptanceGate mismatch")
require(manifest.get("testPackage") == "./cmd/gofly/internal/command", "CLI JSON golden testPackage mismatch")
require("TestCLIJSONContractGoldens" in str(manifest.get("testPattern") or ""), "CLI JSON golden testPattern missing main golden test")
require("TestCLIJSONErrorEnvelopeGolden" in str(manifest.get("testPattern") or ""), "CLI JSON golden testPattern missing error envelope test")

for rel in manifest.get("sourceContracts") or []:
    require((root / rel).is_file(), f"source contract is missing: {rel}")

policy = manifest.get("stdoutPolicy") or {}
for key in (
    "jsonCommandsMustWriteOnlyJSONToStdout",
    "successfulJSONCommandsMustNotWriteDiagnosticsToStderr",
    "globalJSONErrorsMustWriteOneEnvelopeToStdout",
    "globalJSONFlagErrorsMustUseUsageErrorCode",
):
    require(policy.get(key) is True, f"stdoutPolicy.{key} must be true")

cases = manifest.get("cases") or []
require(len(cases) >= 8, "CLI JSON golden manifest must cover at least 8 cases")
case_ids = {item.get("id") for item in cases if isinstance(item, dict)}
for case_id in (
    "version-envelope",
    "doctor-raw",
    "release-check-envelope",
    "new-service-plan-envelope",
    "api-gen-envelope",
    "rpc-gen-envelope",
    "model-gen-envelope",
    "global-error-envelope",
    "global-flag-error-envelope",
):
    require(case_id in case_ids, f"CLI JSON golden case missing {case_id!r}")

cli_json = read_text(root / "docs" / "reference" / "cli-json-contracts.md")
surface = read_text(root / "docs" / "reference" / "cli-command-surface.json")
makefile = read_text(root / "Makefile")
test_file = read_text(root / "cmd" / "gofly" / "internal" / "command" / "cli_json_contract_golden_test.go")

require("cli-json-contract-goldens.json" in cli_json, "cli-json-contracts.md must link cli-json-contract-goldens.json")
require("cli-json-contract-goldens-check" in makefile, "Makefile must expose cli-json-contract-goldens-check")
contract_deps = next((line for line in makefile.splitlines() if line.startswith("contract-docs-check:")), "")
require("cli-json-contract-goldens-check" in contract_deps, "contract-docs-check must depend on cli-json-contract-goldens-check")
require("cli-json-contract-goldens-check" in surface, "cli-command-surface.json must reference cli-json-contract-goldens-check")
for test_name in ("TestCLIJSONContractGoldens", "TestCLIJSONErrorEnvelopeGolden"):
    require(test_name in test_file, f"cli_json_contract_golden_test.go missing {test_name}")

flag_error_case = next((item for item in cases if isinstance(item, dict) and item.get("id") == "global-flag-error-envelope"), {})
require(flag_error_case.get("requiredErrorCode") == "USAGE_ERROR", "global-flag-error-envelope must require USAGE_ERROR")
require("version --bad" in str(flag_error_case.get("command") or ""), "global-flag-error-envelope command must cover flag diagnostics")
require("TestRunMainFlagDiagnosticsContract" in (root / "cmd" / "gofly" / "main_test.go").read_text(encoding="utf-8"), "main_test.go missing TestRunMainFlagDiagnosticsContract")

for item in cases:
    if not isinstance(item, dict):
        missing.append(f"case entry must be an object: {item!r}")
        continue
    command = str(item.get("command") or "")
    require(command.startswith("gofly "), f"case {item.get('id') or '<missing>'}: command must start with gofly")
    require(bool(item.get("mode")), f"case {item.get('id') or '<missing>'}: mode is required")
    for field in item.get("requiredDataFields") or []:
        require(isinstance(field, str) and field, f"case {item.get('id') or '<missing>'}: requiredDataFields must contain non-empty strings")

if missing:
    print("CLI JSON contract golden check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("CLI JSON contract golden manifest ok")
PY

GOFLAGS="${GOFLAGS:-}" go test -count=1 ./cmd/gofly/internal/command -run 'TestCLIJSONContractGoldens|TestCLIJSONErrorEnvelopeGolden'
