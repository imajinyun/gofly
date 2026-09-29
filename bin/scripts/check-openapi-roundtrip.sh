#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/gofly-openapi-roundtrip-XXXXXX")"
trap 'rm -rf "$tmp"' EXIT INT TERM

export GOCACHE="${GOCACHE:-$tmp/gocache}"
export GOTMPDIR="${GOTMPDIR:-$tmp/gotmp}"
mkdir -p "$GOCACHE" "$GOTMPDIR"
report="${OPENAPI_ROUNDTRIP_REPORT:-$tmp/openapi-roundtrip-report.json}"

cd "$root"
go test -count=1 -shuffle=on ./cmd/gofly/internal/generator -run '^TestOpenAPIRoundTripContract$'

python3 - "$root" "$report" <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
report_path = pathlib.Path(sys.argv[2])
missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def read(path):
    if not path.is_file():
        missing.append(f"missing file: {path.relative_to(root)}")
        return ""
    return path.read_text(encoding="utf-8")


manifest_path = root / "docs/reference/openapi-roundtrip.json"
fixture_path = root / "testdata/openapi-roundtrip/expectations.json"
manifest = json.loads(read(manifest_path) or "{}")
fixture = json.loads(read(fixture_path) or "{}")
makefile = read(root / "Makefile")
test_source = read(root / "cmd/gofly/internal/generator/openapi_roundtrip_test.go")
import_source = read(root / "cmd/gofly/internal/generator/api_codegen.go")

require(manifest.get("schema") == "gofly.openapi_roundtrip.v1", "round-trip manifest schema mismatch")
require(manifest.get("status") == "blocking", "round-trip manifest status must be blocking")
require(manifest.get("acceptanceGate") == "make openapi-roundtrip-check", "round-trip acceptance gate mismatch")
require(fixture.get("schema") == "gofly.openapi_roundtrip_fixture.v1", "round-trip fixture schema mismatch")
require(set((fixture.get("classification") or {}).keys()) == {"preserved", "normalized-compatible", "unsupported", "breaking"}, "fixture classification categories drifted")
require(not (fixture.get("classification") or {}).get("breaking"), "fixture must not claim unresolved breaking differences")
for name in ("roundtrip.json", "roundtrip.yaml", "remote-ref.json", "escape-ref.yaml"):
    require((root / "testdata/openapi-roundtrip" / name).is_file(), f"round-trip fixture {name} is missing")

target = re.search(r"^openapi-roundtrip-check:(?P<deps>[^#\n]*)", makefile, re.M)
require(target is not None, "Makefile must expose openapi-roundtrip-check")
api_contract = next((line for line in makefile.splitlines() if line.startswith("api-contract-check:")), "")
require("openapi-roundtrip-check" in api_contract, "api-contract-check must include openapi-roundtrip-check")
for needle in ("TestOpenAPIRoundTripContract", "remote-ref.json", "writeOpenAPIRoundTripSmoke"):
    require(needle in test_source, f"round-trip test missing {needle!r}")
for needle in ("validateOpenAPIReferenceBoundary", "only local OpenAPI references are supported", "openAPIRequestMessageWithParameterTags"):
    require(needle in import_source, f"OpenAPI importer missing {needle!r}")

status = "pass" if not missing else "fail"
report = {
    "schema": "gofly.openapi_roundtrip_report.v1",
    "acceptanceGate": "make openapi-roundtrip-check",
    "status": status,
    "pipeline": manifest.get("pipeline") or [],
    "referencePolicy": "pass" if not missing else "fail",
    "generatedModuleCompile": "pass" if not missing else "fail",
    "runtimeSmoke": "pass" if not missing else "fail",
    "missing": missing,
}
report_path.parent.mkdir(parents=True, exist_ok=True)
report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
if missing:
    for item in missing:
        print(f"openapi round-trip check failed: {item}", file=sys.stderr)
    raise SystemExit(1)
print("openapi round-trip check passed")
PY

printf 'openapi round-trip report: %s\n' "$report"
