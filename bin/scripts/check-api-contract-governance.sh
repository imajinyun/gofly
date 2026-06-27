#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
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
openapi_script = read_text(root / "bin" / "scripts" / "check-openapi-validation-envelope.sh")
rpc_script = read_text(root / "bin" / "scripts" / "check-rpc-boundary.sh")

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

if missing:
    print("api contract governance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("api contract governance ok")
PY
