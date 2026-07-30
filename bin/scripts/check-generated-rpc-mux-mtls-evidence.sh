#!/usr/bin/env sh
set -eu

json_path="${1:-}"
tmp_path=""

if [ -z "$json_path" ]; then
	tmp_path="$(mktemp)"
	json_path="$tmp_path"
	trap 'rm -f "$tmp_path"' EXIT INT TERM
	go run ./cmd/gofly release check --json --evidence generated-rpc-mux-retry-smoke > "$json_path"
fi

python3 - "$json_path" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
envelope = json.loads(path.read_text(encoding="utf-8"))
checks = envelope.get("data", {}).get("checks", [])
if len(checks) != 1 or checks[0].get("name") != "generated-rpc-mux-retry-smoke":
    raise SystemExit(f"unexpected generated RPC mux evidence checks: {checks!r}")

evidence = checks[0].get("evidence", {}).get("generated-rpc-mux-retry-smoke", {})
required = {
    "generatedMTLSSuccess": True,
    "negotiatedProtocol": True,
    "lifecycleDiagnosis": True,
    "candidateLargePayloadFragmentation": True,
    "candidateMessagePolicy": True,
    "candidateFramePolicyDiagnosis": True,
    "candidatePolicyRiskModeValidation": True,
    "fragmentBackpressure": True,
    "fragmentCreditWaitTimeout": True,
    "fragmentWindowUpdateDiagnosis": True,
    "fragmentWindowRefillPolicy": True,
    "fragmentWindowRefillRuntimeDiagnosis": True,
    "generatedRefillProfileAdminSmoke": True,
    "generatedConfigWarningContract": True,
    "generatedConfigWarningSnapshotKey": "generated.rpcMuxConfigWarnings",
    "fragmentMaxDeferredFailFast": True,
    "generatedPolicyRiskModeValidation": True,
    "successProtocol": "gofly-mux/generated-mtls-test",
}
missing = {
    key: {"got": evidence.get(key), "want": want}
    for key, want in required.items()
    if evidence.get(key) != want
}
if missing:
    raise SystemExit(f"generated RPC mux mTLS success evidence drifted: {missing!r}")

markers = evidence.get("generatedSuccessMarkers") or []
for marker in (
    'mtlsClient.MuxStream(mtlsTraceCtx, "greeter/Watch")',
    "mtlsClientOptions := append(tlsCfg.RPC.Mux.ClientOptions(),",
    "rpc.WithExperimentalMuxConnectionManager(mtlsManager),",
    'mtlsClient.DiagnosisHandler().ServeHTTP(refillRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?flowControlEvent=fragment-window-refill&eventFamily=flow-control&event=fragment-window-refill", nil))',
    "refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.Refills < 1",
    "refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.LastFlowControlEvent != \"fragment_window_refill\"",
    "refillDiagnosis.Diagnosis.Mux.Events[0].Event != \"fragment_window_refill\"",
    "mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.MutualTLS",
    'mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.NegotiatedProtocol != "gofly-mux/generated-mtls-test"',
    "mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.OpenedStreams != 1",
    'RPCMuxLogConfig{Enabled: true, Diagnosis: true, ExportEvents: true, EventFamily: "flow-control", Event: "fragment-window-refill"}',
    'RPCMuxOTelCompatibleLogConfig{Enabled: true, Sink: "slog", Profile: "generated-mtls-refill"}',
    'mtlsClient.ObserveMuxDiagnosis(mtlsRefillTraceCtx, refillDiagnosis)',
    "rpc.ValidateRPCMuxDiagnosisSinkSetConfig",
    "rpc.NewRPCMuxDiagnosisSinkSet",
    "rpc.WithMuxDiagnosisEventExporter(sinkSet",
    "rpc.WithServerMuxDiagnosisEventExporter(sinkSet",
    "ValidateRPCMuxConfigWithWarnings(cfg.RPC.Mux)",
    'addGeneratedControlPlaneConfig("rpcMuxConfigWarnings", rpcMuxConfigWarnings)',
    'snapshot.Configs["generated.rpcMuxConfigWarnings"]',
    "ReloadSinkSet(ctx context.Context, sinkSet *rpc.RPCMuxDiagnosisSinkSet)",
    "BreakerFailureThreshold",
    "BreakerCooldown",
    "RPCMuxDiagnosisExporterDeliveryConfig",
    "func (c RPCMuxConfig) ServerOptionsWithSinkSet(sinkSet *rpc.RPCMuxDiagnosisSinkSet) []rpc.ServerOption",
    "func TestRPCMuxConfigValidatesOTelCompatibleSink",
    'mtlsTraceAttrs["rpc.mux.candidate.negotiated_protocol"].AsString() != "gofly-mux/generated-mtls-test"',
    'mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.refills.count"].AsInt64() < 1',
    'mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.stream_window_refill_ratio"].AsFloat64() != 0.5',
    'mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.connection_window_refill_ratio"].AsFloat64() != 0.25',
    'mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.max_deferred_fragments"].AsInt64() != 2',
    'mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.last_flow_control_event"].AsString() != "fragment_window_refill"',
    'mtlsRefillTraceAttrs["rpc.mux.event.flow_control.count"].AsInt64() < 1',
    '"\\"negotiated_protocol\\":\\"gofly-mux/generated-mtls-test\\""',
    '"\\"mutual_tls\\":true"',
    '"\\"refill_profile_stream_window_refill_ratio\\":0.5"',
    '"\\"refill_profile_connection_window_refill_ratio\\":0.25"',
    '"\\"refill_profile_max_deferred_fragments\\":2"',
    '"\\"refill_profile_last_flow_control_event\\":\\"fragment_window_refill\\""',
    '"\\"msg\\":\\"rpc mux runtime event\\""',
    '"\\"event_family\\":\\"flow_control\\""',
    '"\\"event\\":\\"fragment_window_refill\\""',
    '"\\"connection_id\\":\\""',
    '"\\"pool_slot\\":1"',
    '"\\"msg\\":\\"rpc mux otel log event\\""',
    '"\\"otel_log_name\\":\\"rpc.mux.diagnosis_event\\""',
    '"\\"otel_log_profile\\":\\"generated-mtls-refill\\""',
    '"\\"rpc_mux_event_family\\":\\"flow_control\\""',
    '"\\"rpc_mux_event_name\\":\\"fragment_window_refill\\""',
    '"\\"rpc_mux_connection_id\\":\\""',
    '"\\"rpc_mux_pool_slot\\":1"',
):
    if marker not in markers:
        raise SystemExit(f"generated RPC mux mTLS success marker missing: {marker}")

runtime_proofs = set(evidence.get("runtimeProofs") or [])
for proof in (
    "TestExperimentalMuxCandidateAdapterFragmentsLargePayload",
    "TestExperimentalMuxCandidateAdapterRejectsOversizedMessagePolicy",
    "TestExperimentalMuxCandidateConfigValidateFragmentWindowPolicyRiskModes",
    "TestExperimentalMuxTransportFragmentBackpressureWaitsForWindowUpdate",
    "TestExperimentalMuxTransportFragmentCreditWaitTimeout",
    "TestExperimentalMuxTransportFragmentWindowRefillPolicy",
    "TestExperimentalMuxCandidateFragmentBackpressureMetrics",
    "TestExperimentalMuxCandidateConfigValidateFragmentWindowRefillPolicy",
):
    if proof not in runtime_proofs:
        raise SystemExit(f"generated RPC mux candidate payload proof missing: {proof}")

print("generated RPC mux mTLS success and candidate payload evidence ok")
PY
