#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
workflow_path = root / ".github" / "workflows" / "ci.yml"
makefile_path = root / "Makefile"
mtls_evidence_script_path = root / "bin" / "scripts" / "check-generated-rpc-mux-mtls-evidence.sh"
missing = []

workflow = workflow_path.read_text(encoding="utf-8") if workflow_path.is_file() else ""
makefile = makefile_path.read_text(encoding="utf-8") if makefile_path.is_file() else ""
mtls_evidence_script = mtls_evidence_script_path.read_text(encoding="utf-8") if mtls_evidence_script_path.is_file() else ""


def require(condition, message):
    if not condition:
        missing.append(message)


def job_block(job_name):
    pattern = rf"(?ms)^  {re.escape(job_name)}:\n(?P<body>.*?)(?=^  [a-zA-Z0-9_-]+:\n|\Z)"
    match = re.search(pattern, workflow)
    return match.group("body") if match else ""


gateway_job = job_block("gateway-profile-contract")
release_job = job_block("release")
branch_audit_job = job_block("branch-protection-audit")

require(workflow, ".github/workflows/ci.yml must exist")
require("required-checks-drift-check:" in makefile, "Makefile must expose required-checks-drift-check")
require("sh $(SCRIPTS_DIR)/check-required-checks-drift.sh" in makefile, "required-checks-drift-check must execute check-required-checks-drift.sh")
require("sh $(SCRIPTS_DIR)/check-generated-rpc-mux-mtls-evidence.sh" in makefile, "rpc-boundary-check must assert generated RPC mux mTLS success evidence locally")
require(mtls_evidence_script, "check-generated-rpc-mux-mtls-evidence.sh must exist")

require(gateway_job, "ci.yml must define gateway-profile-contract job")
require("name: gateway profile contract" in gateway_job, "gateway-profile-contract job must publish required check name")
require("Gateway profile contract gate" in gateway_job, "gateway-profile-contract job must have an explicit gate step")
require("Gateway aggregation diff summary" in gateway_job, "gateway-profile-contract job must publish aggregation markdown summary")
require("Upload gateway aggregation SARIF artifact" in gateway_job, "gateway-profile-contract job must upload aggregation SARIF artifact")
require("Upload gateway aggregation SARIF to Code Scanning" in gateway_job, "gateway-profile-contract job must keep optional Code Scanning upload step")
require("vars.GOFLY_UPLOAD_AGGREGATION_SARIF == 'true'" in gateway_job, "Code Scanning upload must stay opt-in")
require("Generated RPC mux mTLS success release evidence" in gateway_job, "gateway-profile-contract job must assert generated RPC mux mTLS success evidence")
for token in (
    "GatewayProfileValidateCommandJSON",
    "GatewayProfileValidateCommandBreakingAndUsage",
    "GatewayAggregationValidateCommandJSON",
    "GatewayAggregationSARIFRuleTaxonomyContract",
    "ReleaseGatewayProfileContractCheck",
    "ReleaseGatewayAggregationContractCheck",
    "ReleaseGeneratedRPCMuxRetrySmokeCheck",
    "generated-rpc-mux-retry-smoke",
    "generated-rpc-mux-retry-smoke.json",
    "check-generated-rpc-mux-mtls-evidence.sh generated-rpc-mux-retry-smoke.json",
    "check-generated-rpc-mux-mtls-evidence.sh",
    "ExecuteAIManifestJSONEnvelope",
    "ExecuteAIManifestAliasAndText",
    "TestAINewGeneratedProjectVerificationMatrix",
    "--format markdown",
    "--format sarif",
    "gateway-aggregation-breaking.sarif",
    "gateway-aggregation.sarif",
    "gateway-aggregation-invalid.sarif",
    "python3 - gateway-aggregation-breaking.sarif gateway-aggregation-invalid.sarif gateway-aggregation.sarif",
    "gateway-aggregation-sarif",
    "github/codeql-action/upload-sarif@54f647b7e1bb85c95cddabcd46b0c578ec92bc1a",
    "security-events: write",
    "sarif_file: gateway-aggregation.sarif",
    "if-no-files-found: error",
    "edge-openapi-breaking.json",
    "edge-openapi-invalid.json",
    "Intentionally breaking fixture",
    "Invalid request-shaping fixture",
    "Expected invalid fixture to fail",
    "$GITHUB_STEP_SUMMARY",
    "make aiflow-profile-gate-check",
    "make required-checks-drift-check",
):
    require(token in gateway_job, f"gateway-profile-contract job missing {token!r}")

for token in (
    "generated-rpc-mux-retry-smoke",
    "generatedMTLSSuccess",
    "candidateLargePayloadFragmentation",
    "candidateMessagePolicy",
    "candidateFramePolicyDiagnosis",
    "candidatePolicyRiskModeValidation",
    "fragmentBackpressure",
    "fragmentCreditWaitTimeout",
    "fragmentWindowUpdateDiagnosis",
    "fragmentWindowRefillPolicy",
    "fragmentWindowRefillRuntimeDiagnosis",
    "generatedRefillProfileAdminSmoke",
    "fragmentMaxDeferredFailFast",
    "generatedPolicyRiskModeValidation",
    "TestExperimentalMuxCandidateAdapterFragmentsLargePayload",
    "TestExperimentalMuxCandidateAdapterRejectsOversizedMessagePolicy",
    "TestExperimentalMuxCandidateConfigValidateFragmentWindowPolicyRiskModes",
    "TestExperimentalMuxCandidateConfigValidateFragmentWindowRefillPolicy",
    "TestExperimentalMuxTransportFragmentBackpressureWaitsForWindowUpdate",
    "TestExperimentalMuxTransportFragmentCreditWaitTimeout",
    "TestExperimentalMuxTransportFragmentWindowRefillPolicy",
    "TestExperimentalMuxCandidateFragmentBackpressureMetrics",
	"negotiatedProtocol",
	"lifecycleDiagnosis",
	"successProtocol",
	"gofly-mux/generated-mtls-test",
	"generatedSuccessMarkers",
	"mtlsClient.MuxStream(mtlsTraceCtx, \"greeter/Watch\")",
	"mtlsClientOptions := append(tlsCfg.RPC.Mux.ClientOptions(), rpc.WithExperimentalMuxConnectionManager(mtlsManager))",
	"refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.Refills < 1",
	"refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.LastFlowControlEvent != \\\"fragment_window_refill\\\"",
	"refillDiagnosis.Diagnosis.Mux.Events[0].Event != \\\"fragment_window_refill\\\"",
	"mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.MutualTLS",
	"mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.NegotiatedProtocol != \"gofly-mux/generated-mtls-test\"",
	"mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.OpenedStreams != 1",
	"RPCMuxLogConfig{Enabled: true, Diagnosis: true, ExportEvents: true, EventFamily: \"flow-control\", Event: \"fragment-window-refill\"}",
	"mtlsClient.ObserveMuxDiagnosis(mtlsRefillTraceCtx, refillDiagnosis)",
	"mtlsTraceAttrs[\"rpc.mux.candidate.negotiated_protocol\"].AsString() != \"gofly-mux/generated-mtls-test\"",
	"mtlsRefillTraceAttrs[\"rpc.mux.manager.refill_profile.refills.count\"].AsInt64() < 1",
	"mtlsRefillTraceAttrs[\"rpc.mux.manager.refill_profile.stream_window_refill_ratio\"].AsFloat64() != 0.5",
	"mtlsRefillTraceAttrs[\"rpc.mux.manager.refill_profile.connection_window_refill_ratio\"].AsFloat64() != 0.25",
	"mtlsRefillTraceAttrs[\"rpc.mux.manager.refill_profile.max_deferred_fragments\"].AsInt64() != 2",
	"mtlsRefillTraceAttrs[\"rpc.mux.manager.refill_profile.last_flow_control_event\"].AsString() != \"fragment_window_refill\"",
	"mtlsRefillTraceAttrs[\"rpc.mux.event.flow_control.count\"].AsInt64() < 1",
	"negotiated_protocol",
	"gofly-mux/generated-mtls-test",
	"mutual_tls",
	"refill_profile_stream_window_refill_ratio",
	"refill_profile_connection_window_refill_ratio",
	"refill_profile_max_deferred_fragments",
	"refill_profile_last_flow_control_event",
	"rpc mux exported event",
	"event_family",
	"flow_control",
	"event",
	"fragment_window_refill",
	"connection_id",
	"pool_slot",
):
	require(token in mtls_evidence_script, f"generated RPC mux mTLS evidence script missing {token!r}")

require(release_job, "ci.yml must define release job")
release_needs = re.search(r"needs:\s*\[(?P<needs>[^\]]+)\]", release_job)
require(release_needs is not None, "release job must declare explicit needs")
if release_needs is not None:
    needs = {item.strip() for item in release_needs.group("needs").split(",")}
    require("gateway-profile-contract" in needs, "release job must need gateway-profile-contract")

require(branch_audit_job, "ci.yml must define branch-protection-audit job")
require('"gateway profile contract"' in branch_audit_job, "branch protection audit must expect gateway profile contract required check")

if missing:
    print("required-check drift check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("required-check drift ok: gateway-profile-contract is hosted, release-blocking, and branch-protection-audited")
PY
