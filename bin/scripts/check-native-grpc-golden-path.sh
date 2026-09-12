#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/gofly-native-grpc-XXXXXX")"
trap 'rm -rf "$tmp_root"' EXIT
mkdir -p "$tmp_root/gocache" "$tmp_root/gotmp"

(
  cd "$root"
  GOCACHE="${GOCACHE:-$tmp_root/gocache}" GOTMPDIR="${GOTMPDIR:-$tmp_root/gotmp}" "$go_cmd" test -count=1 -shuffle=on -race ./rpc/grpc ./core/governance ./gateway
  GOCACHE="${GOCACHE:-$tmp_root/gocache}" GOTMPDIR="${GOTMPDIR:-$tmp_root/gotmp}" "$go_cmd" test -count=1 -shuffle=on ./cmd/gofly/internal/generator -run 'TestGenerateRPCNewGoZeroCompatibleProducesRunnableGRPCProject|TestGenerateGRPCBindingCodeSupportsStreaming|TestGenerateGRPCScaffold'
)

python3 - "$root" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
manifest = json.loads((root / "docs/reference/native-grpc-golden-path.json").read_text(encoding="utf-8"))
expected = {
    "safe-client-defaults",
    "default-client-observability",
    "discovery-health-lifecycle",
    "grpc-balancers",
    "runnable-gozero-compatible-scaffold",
    "keepalive-and-adaptive-shedding",
    "gozero-app-token-compatibility",
    "bounded-credential-proxy",
    "native-grpc-gateway",
}
actual = {item.get("id") for item in manifest.get("capabilities") or []}
assert manifest.get("schema") == "gofly.native_grpc_golden_path.v1"
assert manifest.get("acceptanceGate") == "make native-grpc-golden-path-check"
assert actual == expected, (sorted(expected - actual), sorted(actual - expected))
allowed_statuses = {"implemented", "implemented-opt-in"}
for item in manifest["capabilities"]:
    assert item.get("status") in allowed_statuses, item
    assert item.get("evidence"), item
print("native gRPC golden path OK")
PY
