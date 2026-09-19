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
benchmark = json.loads((root / "bench/grpc_adaptive_admission_evidence.json").read_text(encoding="utf-8"))
expected = {
    "safe-client-defaults",
    "default-client-observability",
    "discovery-health-lifecycle",
    "grpc-balancers",
    "adaptive-shedding-default-policy",
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
balancers = next(item for item in manifest["capabilities"] if item.get("id") == "grpc-balancers")
assert balancers.get("generatedDefault") == "gofly_p2c_ewma", balancers
assert set(balancers.get("configuredPolicies") or []) == {"round_robin", "gofly_p2c_ewma", "gofly_consistent_hash"}, balancers
assert {"TestConfiguredLoadBalancing", "configured load balancing"} <= set(balancers.get("evidence") or []), balancers
assert "real discovery-backed clients" in balancers.get("runtimeVerification", ""), balancers
scaffold = next(item for item in manifest["capabilities"] if item.get("id") == "runnable-gozero-compatible-scaffold")
assert "TestGovernanceRuleRestartRecovery" in (scaffold.get("productionEvidence") or []), scaffold
assert "read-only resolvers for native zRPC etcd keys" in scaffold.get("defaultWiring", ""), scaffold
assert "top-level Etcd block" in scaffold.get("defaultWiring", ""), scaffold
adaptive = next(item for item in manifest["capabilities"] if item.get("id") == "adaptive-shedding-default-policy")
assert adaptive.get("generatedDefault", {}).get("enabled") is True, adaptive
assert adaptive.get("generatedDefault", {}).get("cpuThresholdPermille") == 800, adaptive
assert {"AdaptiveLimitUnaryServerInterceptor", "AdaptiveLimitStreamServerInterceptor", "TestDefaultServerAdaptiveLimiterAcrossUnaryAndBidiStream", "TestGenerateRPCNewGoZeroCompatibleProducesRunnableGRPCProject", "TestDefaultServerAdaptiveLimiterSnapshot", "bench/grpc_adaptive_admission_evidence.json"} <= set(adaptive.get("evidence") or []), adaptive
assert {"passes", "drops", "inFlight", "cpuLoad"} <= set(adaptive.get("runtimeFields") or []), adaptive
gateway = next(item for item in manifest["capabilities"] if item.get("id") == "native-grpc-gateway")
assert {"CallClientStreamRaw", "OpenServerStreamRaw", "OpenBidirectionalStreamRaw", "client stream incrementally consumes NDJSON", "client stream upstream failure is not retried", "TestDecodeNDJSONStreamLimits", "server stream uses SSE", "HTTP cancellation cancels gRPC stream", "bidirectional stream interleaves messages and half closes", "bidirectional stream maps local and upstream errors", "bidirectional stream disconnect cancels upstream", "bidirectional stream half close still observes disconnect"} <= set(gateway.get("evidence") or []), gateway
assert "application/x-ndjson" in gateway.get("contract", ""), gateway
assert "text/event-stream" in gateway.get("contract", ""), gateway
assert "gofly.grpc.bidi.v1" in gateway.get("contract", ""), gateway
assert "non-upgraded HTTP call remains Unimplemented with HTTP 501" in gateway.get("limitations", ""), gateway
gateway_source = (root / "gateway" / "grpc_transcode.go").read_text(encoding="utf-8")
gateway_test = (root / "gateway" / "grpc_transcode_test.go").read_text(encoding="utf-8")
for marker in ("CallClientStreamRaw", "OpenServerStreamRaw", "OpenBidirectionalStreamRaw", "ClientStreams: true", "ServerStreams: true", "CloseSend()", "grpcTranscodeMetadata"):
    assert marker in gateway_source, marker
transcode_source = (root / "gateway" / "transcode.go").read_text(encoding="utf-8")
for marker in ("for _, stream := range desc.Streams", 'grpcClientStreamMediaType     = "application/x-ndjson"', 'grpcBidiWebSocketSubprotocol  = "gofly.grpc.bidi.v1"', "grpcClientStreamMaxFrameBytes", "grpcClientStreamMaxBodyBytes", "grpcClientStreamMaxMessages", "halfClosed"):
    assert marker in transcode_source, marker
for marker in ("client stream incrementally consumes NDJSON", "client stream upstream failure is not retried", "client stream propagates cancellation", "client stream protobuf mapping error is local", "frame too large", "server stream uses SSE", "HTTP cancellation cancels gRPC stream", "bidirectional stream rejected", "bidirectional stream interleaves messages and half closes", "bidirectional stream maps local and upstream errors", "bidirectional stream disconnect cancels upstream", "bidirectional stream half close still observes disconnect"):
    assert marker in gateway_test, marker
grpc_scaffold = (root / "cmd" / "gofly" / "internal" / "generator" / "grpc_scaffold_templates.go").read_text(encoding="utf-8")
grpc_codegen_test = (root / "cmd" / "gofly" / "internal" / "generator" / "grpc_codegen_test.go").read_text(encoding="utf-8")
for marker in ("NewConfiguredGreeter", "configured load balancing ", "WithHashKey", "unsupported configured load-balancing policy was accepted", "appdiscovery.NewZRPCResolver", "appdiscovery.NewZRPCRegistrar"):
    assert marker in grpc_scaffold, marker
for marker in ("TestConfiguredLoadBalancing", "NewConfiguredChat", "WithWaitForReady", "least_request"):
    assert marker in grpc_codegen_test, marker
assert "adaptive-shedding-default-policy" not in (manifest.get("deferred") or []), manifest.get("deferred")
assert benchmark.get("schema") == "gofly.benchmark_grpc_adaptive_admission_evidence.v1", benchmark
assert benchmark.get("status") == "report-only", benchmark
for row in (benchmark.get("results") or {}).values():
    assert len(row.get("nsPerOp") or []) >= 5, row
print("native gRPC golden path OK")
PY
