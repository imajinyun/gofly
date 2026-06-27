#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "api-contract-governance.json"
missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


makefile = read_text(root / "Makefile")
boundary = json.loads(read_text(root / "docs" / "reference" / "governance-boundary-inventory.json") or "{}")
required_checks = read_text(root / "docs" / "reference" / "ci-required-check-evidence.json")
cli_contracts = read_text(root / "docs" / "reference" / "cli-json-contracts.md")
openapi_script = read_text(root / "bin" / "scripts" / "check-openapi-validation-envelope.sh")
rpc_script = read_text(root / "bin" / "scripts" / "check-rpc-boundary.sh")
openapi_manifest = read_text(root / "docs" / "reference" / "openapi-invalid-request-smoke.json")
rpc_manifest = read_text(root / "docs" / "reference" / "rpc-tier1-evidence.json")

try:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
except FileNotFoundError:
    manifest = {}
    missing.append("docs/reference/api-contract-governance.json is missing")
except json.JSONDecodeError as exc:
    manifest = {}
    missing.append(f"docs/reference/api-contract-governance.json is invalid JSON: {exc}")

target = re.search(
    r"^api-contract-check:(?P<deps>[^#\n]*)(?:[^\n]*)\n(?P<body>.*?)(?=^\.PHONY:|\Z)",
    makefile,
    re.M | re.S,
)
require(target is not None, "Makefile target api-contract-check is missing")
deps = target.group("deps") if target else ""
body = target.group("body") if target else ""
for dep in ("openapi-validation-check", "rpc-boundary-check"):
    require(dep in deps, f"api-contract-check must depend on {dep}")
require(
    "check-api-contract-governance.sh" in body,
    "api-contract-check must run check-api-contract-governance.sh after child gates",
)
docs_deps = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
contract_deps = next((line for line in makefile.splitlines() if line.startswith("contract-docs-check:")), "")
require("api-contract-governance-check" in makefile, "Makefile must expose api-contract-governance-check")
require("api-contract-governance-check" in docs_deps, "docs-check must depend on api-contract-governance-check")
require("api-contract-governance-check" in contract_deps, "contract-docs-check must depend on api-contract-governance-check")

tasks = boundary.get("aiflowTasks") or []
round7 = next((item for item in tasks if item.get("round") == 7), {})
require(round7.get("gate") == "make api-contract-check", "Round 07 gate must be make api-contract-check")
surfaces = boundary.get("surfaces") or []
rest_rpc = next((item for item in surfaces if item.get("id") == "rest-rpc-contracts"), {})
require(rest_rpc.get("gate") == "make api-contract-check", "rest-rpc-contracts surface must use make api-contract-check")

for needle in (
    "docs/reference/openapi-invalid-request-smoke.json",
    "generated-service-invalid-request",
    "runtimeEnvelope",
    "rest.ErrorResponse",
    "coreerrors.CodeInvalidArgument",
):
    require(needle in openapi_script, f"OpenAPI contract gate missing {needle!r}")

for needle in (
    "docs/reference/rpc-tier1-evidence.json",
    "BenchmarkRPCUnary/gofly_rpc",
    "BenchmarkRPCBidiStreamGovernance",
    "resolver-updates",
    "kitex-coexistence-rollback",
    "grpc-compatibility",
):
    require(needle in rpc_script, f"RPC boundary gate missing {needle!r}")

require(manifest.get("schema") == "gofly.api_contract_governance.v1", "api contract governance schema mismatch")
require(manifest.get("aiflowTask") == "GOFLY-GOV-10R3-07", "api contract governance aiflowTask mismatch")
require(manifest.get("acceptanceGate") == "make api-contract-check", "api contract governance acceptanceGate mismatch")
require(set(manifest.get("childGates") or []) == {"make openapi-validation-check", "make rpc-boundary-check"}, "api contract governance childGates mismatch")
aggregate_gates = set(manifest.get("aggregateGates") or [])
for gate in ("make api-contract-governance-check", "make docs-check", "make contract-docs-check"):
    require(gate in aggregate_gates, f"api contract governance aggregateGates missing {gate}")

for rel in manifest.get("sourceCode") or []:
    require((root / rel).exists(), f"api contract governance source is missing: {rel}")

policy = manifest.get("policy") or {}
for key in (
    "openapiRuntimeBindingMustMatchSchema",
    "invalidRequestsReturnStableRestErrorResponse",
    "rpcTier1PromotionRequiresReleaseTrainEvidence",
    "rpcLatencyRemainsReportOnlyUntilBudgetPromotion",
    "gatewayAndDescriptorContractsUseAggregateGate",
    "releaseContractCheckMustStayReleaseBlocking",
):
    require(policy.get(key) is True, f"policy.{key} must be true")

surface_ids = {item.get("id") for item in manifest.get("surfaces") or [] if isinstance(item, dict)}
required_surfaces = {
    "rest-openapi-validation-envelope",
    "rpc-tier1-boundary",
    "gateway-descriptor-contracts",
    "release-contract-required-check",
}
require(surface_ids == required_surfaces, f"api contract surfaces mismatch: {sorted(surface_ids)!r}")

for item in manifest.get("surfaces") or []:
    if not isinstance(item, dict):
        missing.append(f"surface entry must be an object: {item!r}")
        continue
    surface = item.get("id", "<missing>")
    for field in ("risk", "gate", "evidenceRefs"):
        require(item.get(field), f"surface {surface}: {field} is required")
    gate = str(item.get("gate") or "")
    require(gate.startswith("make ") or gate.startswith("go test "), f"surface {surface}: gate must be runnable")
    for ref in item.get("evidenceRefs") or []:
        ref_path = ref.get("path", "")
        needles = ref.get("contains") or []
        require(ref_path, f"surface {surface}: ref path is required")
        require(needles, f"surface {surface}: ref contains list is required for {ref_path}")
        if not ref_path:
            continue
        path = root / ref_path
        if not path.exists():
            missing.append(f"surface {surface}: evidence path is missing: {ref_path}")
            continue
        text = path.read_text(encoding="utf-8") if path.is_file() else ""
        for needle in needles:
            require(needle in text, f"surface {surface}: {ref_path} missing {needle!r}")

for needle in ("gofly.openapi_invalid_request_smoke.v1", "rest.ErrorResponse", "generated-service-invalid-request"):
    require(needle in openapi_manifest, f"openapi invalid request smoke missing {needle!r}")
for needle in ("gofly.rpc_tier1_evidence.v1", "rpc-release-train-missing", "rpc-budget-report-only"):
    require(needle in rpc_manifest, f"rpc tier1 evidence missing {needle!r}")
for needle in ("contract-check", "contract / api+rpc (check + breaking)", "stable-surface and API/RPC contract checks must remain release-blocking"):
    require(needle in required_checks, f"ci required check evidence missing {needle!r}")
for needle in ("gofly rpc descriptor", "gofly rpc doc", "OpenAPI JSON generated from protobuf HTTP transcoding metadata"):
    require(needle in cli_contracts, f"CLI JSON contracts missing {needle!r}")

execution = manifest.get("aiflowExecution") or {}
require(execution.get("status") == "local-fallback", "aiflowExecution.status must be local-fallback")
require("fmt" in str(execution.get("blocker") or ""), "aiflowExecution.blocker must document current aiflow compile blocker")

if missing:
    print("api contract governance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("api contract governance ok")
PY
