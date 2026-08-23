#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
missing = []

def read_text(path):
    full = root / path
    if not full.is_file():
        missing.append(f"{path}: file is missing")
        return ""
    return full.read_text(encoding="utf-8")

def require(condition, message):
    if not condition:
        missing.append(message)

template = read_text("cmd/gofly/internal/generator/templates.go")
service_test = read_text("cmd/gofly/internal/generator/service_test.go")
release = read_text("cmd/gofly/internal/command/release/release_local_checks.go")
release_test = read_text("cmd/gofly/internal/command/release/release_test.go")
evidence_script = read_text("bin/scripts/check-generated-rpc-mux-mtls-evidence.sh")
ai_manifest = read_text("cmd/gofly/internal/command/ai_control_plane_manifest.go")
bug = read_text("cmd/gofly/internal/command/bug.go")
makefile = read_text("Makefile")
contract_doc = read_text("docs/reference/control-plane-contracts.md")

support_path = root / "docs/reference/dx-support-bundle.json"
if support_path.is_file():
    support = json.loads(support_path.read_text(encoding="utf-8"))
else:
    support = {}
    missing.append("docs/reference/dx-support-bundle.json: file is missing")

required_tokens = [
    "generated.rpcMuxConfigWarningSchema",
    "generated.rpcMuxConfigWarnings",
    "generated.controlPlaneSchemaChecksums",
    "gofly.rpc_mux_config_warning.v1",
    "generated.rpcMuxOperatorAuditSchemas",
    "aiManifestSchema",
]

for token in required_tokens:
    for label, text in (
        ("templates.go", template),
        ("service_test.go", service_test),
        ("release_local_checks.go", release),
        ("check-generated-rpc-mux-mtls-evidence.sh", evidence_script),
        ("ai_control_plane_manifest.go", ai_manifest),
        ("control-plane-contracts.md", contract_doc),
    ):
        if token not in text:
            missing.append(f"{label}: missing {token!r}")

for token in ("generatedConfigWarningSchema", "generatedConfigWarningSchemaKey", "generatedConfigWarningSchemaChecksum"):
    if token not in release or token not in release_test or token not in evidence_script:
        missing.append(f"release evidence contract missing {token!r} in release code, test, or script")

for token in ("runGeneratedControlPlaneSmoke", "restoreRecommendedSmokeConfig", "assertControlPlaneMuxConfigWarningsCleared"):
    if token not in template or token not in service_test:
        missing.append(f"generated admin smoke helper marker missing {token!r}")

for token in ("curl -fsS http://127.0.0.1:9090/admin/control-plane", "generated.rpcMuxConfigWarningSchema", "generated.controlPlaneSchemaChecksums"):
    if token not in bug:
        missing.append(f"bug support bundle guidance missing {token!r}")

if support.get("schema") != "gofly.dx_support_bundle.v1":
    missing.append("dx-support-bundle.json: schema mismatch")
evidence = support.get("generatedControlPlaneEvidence") or {}
if evidence.get("schema") != "gofly.generated_control_plane_support.v1":
    missing.append("dx-support-bundle.json: generatedControlPlaneEvidence.schema mismatch")
if evidence.get("acceptanceGate") != "make generated-control-plane-contract-check":
    missing.append("dx-support-bundle.json: generatedControlPlaneEvidence.acceptanceGate mismatch")
for token in ("generated.rpcMuxConfigWarningSchema", "generated.rpcMuxConfigWarnings", "generated.controlPlaneSchemaChecksums"):
    if token not in set(evidence.get("requiredConfigs") or []):
        missing.append(f"dx-support-bundle.json: requiredConfigs missing {token!r}")
for token in ("generated.rpcMuxConfigWarningSchema", "generated.rpcMuxOperatorAuditSchemas", "aiManifestSchema"):
    if token not in set(evidence.get("schemaChecksumKeys") or []):
        missing.append(f"dx-support-bundle.json: schemaChecksumKeys missing {token!r}")
compat = evidence.get("compatibilityAssessment") or {}
if compat.get("currentWarningFieldType") != "[]string" or compat.get("targetWarningFieldType") != "[]object":
    missing.append("dx-support-bundle.json: warning compatibility assessment must document []string to []object migration")

for token in ("[]string", "[]object", "compatibility window", "JSON object"):
    if token not in contract_doc:
        missing.append(f"control-plane-contracts.md: compatibility assessment missing {token!r}")

target_match = re.search(r"^generated-control-plane-contract-check:", makefile, re.M)
if not target_match:
    missing.append("Makefile: generated-control-plane-contract-check target missing")
contract_line = next((line for line in makefile.splitlines() if line.startswith("contract-docs-check:")), "")
if "generated-control-plane-contract-check" not in contract_line:
    missing.append("Makefile: contract-docs-check must depend on generated-control-plane-contract-check")
if "check-generated-control-plane-contract.sh" not in makefile:
    missing.append("Makefile: generated-control-plane-contract-check must call check-generated-control-plane-contract.sh")

if missing:
    print("generated control-plane contract check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("generated control-plane contract ok")
PY
