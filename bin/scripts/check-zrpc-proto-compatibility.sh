#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/gofly-zrpc-proto-XXXXXX")"
trap 'rm -rf "$tmp_root"' EXIT
mkdir -p "$tmp_root/gocache" "$tmp_root/gotmp" "$tmp_root/out"

run_go_test() {
	pkg="$1"
	pattern="$2"
	printf 'zrpc-proto-compatibility: %s %s\n' "$pkg" "$pattern"
	(
		cd "$root"
		GOCACHE="${GOCACHE:-$tmp_root/gocache}" GOTMPDIR="${GOTMPDIR:-$tmp_root/gotmp}" "$go_cmd" test -count=1 -shuffle=on "$pkg" -run "$pattern"
	)
}

run_go_test ./cmd/gofly/internal/generator 'TestZRPCProtoCompatibilityMatrix|TestGenerateRPCFromProtoMultipleAndStreamVariants'

python3 - "$root" <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
manifest_path = root / "docs/reference/zrpc-proto-compatibility.json"
missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def read(path):
    if not path.is_file():
        missing.append(f"{path} is missing")
        return ""
    return path.read_text(encoding="utf-8")


manifest = json.loads(read(manifest_path)) if manifest_path.is_file() else {}
makefile = read(root / "Makefile")
targets = set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.M))
require(manifest.get("schema") == "gofly.zrpc_proto_compatibility.v1", "schema mismatch")
require(manifest.get("acceptanceGate") == "make zrpc-proto-compatibility-check", "acceptanceGate mismatch")
require("zrpc-proto-compatibility-check" in targets, "Makefile must expose zrpc-proto-compatibility-check")
require("check-zrpc-proto-compatibility.sh" in makefile, "Makefile must call check-zrpc-proto-compatibility.sh")
for source in manifest.get("sourceOfTruth") or []:
    require((root / source).exists(), f"source path is missing: {source}")

rows = {row.get("id"): row for row in manifest.get("matrix") or []}
expected = {
    "external-proto-imports": "degraded",
    "multiple-services": "supported",
    "streaming-rpc": "supported",
    "google-well-known-types": "degraded",
    "client-wrapper": "supported",
}
require(set(rows) == set(expected), f"matrix ids mismatch: {sorted(rows)!r}")
for row_id, status in expected.items():
    row = rows.get(row_id) or {}
    require(row.get("status") == status, f"{row_id}: status must be {status}")
    require(row.get("gate") == "make zrpc-proto-compatibility-check", f"{row_id}: gate mismatch")
    require(row.get("evidence"), f"{row_id}: evidence is required")
    require(len(str(row.get("reason") or "").split()) >= 8, f"{row_id}: reason must be actionable")
    require(len(str(row.get("rollbackOrEscalation") or "").split()) >= 8, f"{row_id}: rollbackOrEscalation must be actionable")

shop = read(root / "testdata/zrpc-proto-matrix/shop.proto")
for marker in (
    'import "common.proto"',
    'import "google/protobuf/timestamp.proto"',
    "service OrderService",
    "service OrderEventService",
    "returns (stream WatchOrdersResponse)",
    "rpc UploadEvents(stream UploadOrderEvent)",
    "rpc Chat(stream ChatMessage) returns (stream ChatMessage)",
):
    require(marker in shop, f"shop.proto missing {marker!r}")

rules = manifest.get("releaseRules") or {}
for field in ("supportedRegression", "degradedClaim", "unsupportedPromotion"):
    require(len(str(rules.get(field) or "").split()) >= 8, f"releaseRules.{field} must be actionable")

if missing:
    print("zrpc proto compatibility check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    raise SystemExit(1)
print("zrpc proto compatibility OK")
PY
