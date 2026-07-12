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
    "mtlsClientOptions := append(tlsCfg.RPC.Mux.ClientOptions(), rpc.WithExperimentalMuxConnectionManager(mtlsManager))",
    'mtlsClient.DiagnosisHandler().ServeHTTP(refillRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?flowControlEvent=fragment-window-refill&eventFamily=flow-control&event=fragment-window-refill", nil))',
    "refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.Refills < 1",
    "refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.LastFlowControlEvent != \"fragment_window_refill\"",
    "refillDiagnosis.Diagnosis.Mux.Events[0].Event != \"fragment_window_refill\"",
    "mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.MutualTLS",
    'mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.NegotiatedProtocol != "gofly-mux/generated-mtls-test"',
    "mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.OpenedStreams != 1",
    'mtlsTraceAttrs["rpc.mux.candidate.negotiated_protocol"].AsString() != "gofly-mux/generated-mtls-test"',
    '"\\"negotiated_protocol\\":\\"gofly-mux/generated-mtls-test\\""',
    '"\\"mutual_tls\\":true"',
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
