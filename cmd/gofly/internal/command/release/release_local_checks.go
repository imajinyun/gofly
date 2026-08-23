package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/gateway"
)

func releaseGoAPICompatCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "go-api-compat", Status: "pass"}
	if out, err := runAPIDiffCheck(); err != nil {
		item.Status = "fail"
		item.Detail = string(out)
		item.Blocker = true
		return item, []string{"Go public API incompatible changes detected"}
	} else {
		item.Detail = strings.TrimSpace(string(out))
		if item.Detail == "" {
			item.Detail = "no incompatible changes"
		}
	}
	return item, nil
}

func releaseChangelogVersionCheck(path, version string) (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "changelog-version", Status: "pass"}
	changelogVersion, err := parseChangelogVersion(path)
	if err != nil {
		item.Status = "skip"
		item.Detail = "changelog not found or unparsable"
	} else if changelogVersion != "" && changelogVersion != version {
		item.Status = "fail"
		item.Detail = fmt.Sprintf("CHANGELOG version %q != gofly version %q", changelogVersion, version)
		item.Blocker = true
		return item, []string{item.Detail}
	} else {
		item.Detail = fmt.Sprintf("version %q", changelogVersion)
	}
	return item, nil
}

func releaseGoModTidyCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "go-mod-tidy", Status: "pass"}
	if out, err := exec.Command("go", "mod", "tidy", "-diff").CombinedOutput(); err != nil {
		item.Status = "fail"
		item.Detail = strings.TrimSpace(string(out))
		item.Blocker = true
		return item, []string{"go mod tidy would change go.mod/go.sum"}
	}
	item.Detail = "clean"
	return item, nil
}

func releaseGatewayProfileContractCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "gateway-profile-contract", Status: "pass"}
	dir, err := os.MkdirTemp("", "gofly-release-gateway-profile-*")
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway profile contract check failed"}
	}
	defer os.RemoveAll(dir)

	projectDir := filepath.Join(dir, "edge")
	if err := generator.GenerateGateway(generator.GatewayOptions{Name: "edge", Module: "example.com/edge", Dir: projectDir}); err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway profile contract check failed"}
	}
	profiles, err := readReleaseGatewayProfiles(filepath.Join(projectDir, "etc", "edge.json"))
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway profile contract check failed"}
	}
	candidate, err := readReleaseGatewayProfileCandidate(filepath.Join(projectDir, "etc", "edge-profile-candidate.json"))
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway profile contract check failed"}
	}
	gw, err := gateway.New([]gateway.Route{{PathPrefix: "/_release_profile_check", Targets: []string{"http://127.0.0.1:1"}}}, gateway.WithTranscodeProfiles(profiles...))
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway profile contract check failed"}
	}
	report := gw.ValidateTranscodeProfile(candidate)
	switch {
	case !report.OK:
		item.Status = "fail"
		item.Detail = strings.Join(report.Errors, "; ")
		item.Blocker = true
		return item, []string{"generated gateway profile candidate is invalid"}
	case !report.Compatible:
		item.Status = "fail"
		item.Detail = "generated gateway profile candidate contains breaking mapping changes"
		item.Blocker = true
		return item, []string{"generated gateway profile candidate has breaking mapping changes"}
	default:
		item.Detail = fmt.Sprintf("compatible profile diff with %d change(s)", len(report.Changes))
		return item, nil
	}
}

func releaseGatewayAggregationContractCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "gateway-aggregation-contract", Status: "pass"}
	dir, err := os.MkdirTemp("", "gofly-release-gateway-aggregation-*")
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway aggregation contract check failed"}
	}
	defer os.RemoveAll(dir)

	projectDir := filepath.Join(dir, "edge")
	if err := generator.GenerateGateway(generator.GatewayOptions{Name: "edge", Module: "example.com/edge", Dir: projectDir}); err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway aggregation contract check failed"}
	}
	current, err := readReleaseGatewayConfig(filepath.Join(projectDir, "etc", "edge.json"))
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway aggregation contract check failed"}
	}
	candidate, err := readReleaseGatewayAggregationCandidate(filepath.Join(projectDir, "etc", "edge-aggregation-candidate.json"))
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway aggregation contract check failed"}
	}
	gw, err := gateway.NewFromConfig(current, nil)
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway aggregation contract check failed"}
	}
	report := gw.ValidateAggregation("bff-home", candidate)
	item.Evidence = map[string]any{
		"aggregation-json-diff": releaseAggregationEvidence(report, gatewayAggregationSARIFContext{Route: "bff-home"}),
	}
	switch {
	case !report.OK:
		item.Status = "fail"
		item.Detail = strings.Join(report.Errors, "; ")
		item.Blocker = true
		return item, []string{"generated gateway aggregation candidate is invalid"}
	case !report.Compatible:
		item.Status = "fail"
		item.Detail = "generated gateway aggregation candidate contains breaking changes"
		item.Blocker = true
		return item, []string{"generated gateway aggregation candidate has breaking changes"}
	}
	openAPIReport, openAPIContext, err := releaseGatewayOpenAPIAggregationReport(projectDir)
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway OpenAPI aggregation contract check failed"}
	}
	item.Evidence["aggregation-openapi-diff"] = releaseAggregationEvidence(openAPIReport, openAPIContext)
	switch {
	case !openAPIReport.OK:
		item.Status = "fail"
		item.Detail = strings.Join(openAPIReport.Errors, "; ")
		item.Blocker = true
		return item, []string{"generated gateway OpenAPI aggregation candidate is invalid"}
	case !openAPIReport.Compatible:
		item.Status = "fail"
		item.Detail = "generated gateway OpenAPI aggregation candidate contains breaking changes"
		item.Blocker = true
		return item, []string{"generated gateway OpenAPI aggregation candidate has breaking changes"}
	default:
		item.Detail = fmt.Sprintf("compatible aggregation diff with %d change(s); compatible OpenAPI aggregation diff with %d change(s)", len(report.Changes), len(openAPIReport.Changes))
		return item, nil
	}
}

func releaseAggregationEvidence(report gateway.AggregationValidationReport, context gatewayAggregationSARIFContext) map[string]any {
	return map[string]any{
		"ok":            report.OK,
		"compatible":    report.Compatible,
		"changes":       len(report.Changes),
		"errors":        len(report.Errors),
		"changeDetails": gatewayAggregationValidationView(report, context).Changes,
	}
}

func releaseRPCMuxAdapterEvidenceCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "rpc-mux-adapter-evidence", Status: "pass"}
	evidence, err := readReleaseJSONFile(filepath.Join("bench", "rpc_mux_adapter_evidence.json"), "rpc mux adapter evidence")
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"rpc mux adapter evidence is unavailable"}
	}
	schema, _ := evidence["schema"].(string)
	benchmark, _ := evidence["benchmark"].(string)
	status, _ := evidence["status"].(string)
	decision, _ := evidence["decision"].(map[string]any)
	allocationMode, _ := decision["allocationMode"].(string)
	latencyMode, _ := decision["latencyMode"].(string)
	promotionStatus, _ := decision["promotionStatus"].(string)
	baseline, _ := evidence["baseline"].(map[string]any)
	current, _ := evidence["current"].(map[string]any)
	if schema != "gofly.benchmark_rpc_mux_adapter_evidence.v1" ||
		benchmark != "BenchmarkRPCExperimentalMuxAdapterOpenSendReceiveClose" ||
		status != "report-only" ||
		allocationMode != "report-only" ||
		latencyMode != "report-only" ||
		promotionStatus != "blocked" {
		item.Status = "fail"
		item.Detail = "rpc mux adapter evidence contract drifted"
		item.Blocker = true
		return item, []string{"rpc mux adapter evidence contract drifted"}
	}
	item.Detail = "rpc mux adapter evidence attached as report-only"
	item.Evidence = map[string]any{
		"rpc-mux-adapter-evidence": map[string]any{
			"schema":          schema,
			"benchmark":       benchmark,
			"status":          status,
			"allocationMode":  allocationMode,
			"latencyMode":     latencyMode,
			"promotionStatus": promotionStatus,
			"baseline":        baseline,
			"current":         current,
			"decision":        decision,
			"runtimeEvidence": map[string]any{
				"family":           "generated-rpc-mux-retry-smoke",
				"openBeforeRetry":  true,
				"postOpenNoReplay": true,
				"cooldownBackoff":  true,
				"gate":             "gofly release check --json --evidence generated-rpc-mux-retry-smoke",
			},
		},
	}
	return item, nil
}

func releaseGeneratedRPCMuxRetrySmokeCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "generated-rpc-mux-retry-smoke", Status: "pass"}
	path := filepath.Join("cmd", "gofly", "internal", "generator", "templates.go")
	resolved, err := resolveReleaseEvidencePath(path)
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated RPC mux retry smoke source is unavailable"}
	}
	data, err := os.ReadFile(resolved) // #nosec G304 -- release check reads a generator template from the repository.
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated RPC mux retry smoke source is unavailable"}
	}
	source := string(data)
	openBeforeMarkers := []string{
		`badMuxEndpoint := "tcp://" + badMuxListener.Addr().String()`,
		`rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(cfg.RPC.Mux.MaxOpenRetries)`,
		`rpc.WithExperimentalMuxConnectionManagerHealthBackoffMultiplier(cfg.RPC.Mux.HealthBackoffMultiplier)`,
		`rpc.WithExperimentalMuxConnectionManagerHealthMaxCooldown(cfg.RPC.Mux.HealthMaxCooldown)`,
		`diagnosis.OpenRetries != 1`,
		`diagnosis.RetryReasons["dial_failure"] != 1`,
		`diagnosis.HealthBackoffMultiplier != 2`,
		`diagnosis.HealthMaxCooldown != 30*time.Second`,
	}
	postOpenMarkers := []string{
		`"greeter/FailAfterOpen"`,
		`rpc.NewError(rpc.CodeUnavailable, "generated stream failed after open")`,
		`rpc.CodeOf(err) != rpc.CodeUnavailable`,
		`diagnosis.RetryReasons["open_stream"] != 0`,
	}
	negotiationSummaryMarkers := []string{
		`/rpc/diagnosis?eventFamily=negotiation&event=frame-policy-mismatch`,
		`negotiationDiagnosis.Diagnosis.Mux.Negotiation.Failures != 1`,
		`negotiationDiagnosis.Diagnosis.Mux.Negotiation.FramePolicyMismatch != 1`,
		`negotiationDiagnosis.Diagnosis.Mux.Negotiation.LastEvent != "frame_policy_mismatch"`,
	}
	generatedSuccessMarkers := []string{
		`mtlsClient.MuxStream(mtlsTraceCtx, "greeter/Watch")`,
		`mtlsPayload := []byte(strings.Repeat("probe-", 50))`,
		`"mtls:"+string(mtlsPayload)`,
		`mtlsClient.DiagnosisHandler().ServeHTTP(mtlsRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis", nil))`,
		`mtlsClient.DiagnosisHandler().ServeHTTP(refillRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?flowControlEvent=fragment-window-refill&eventFamily=flow-control&event=fragment-window-refill", nil))`,
		`refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.Refills < 1`,
		`refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.StreamWindowRefillRatio != 0.5`,
		`refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.ConnectionWindowRefillRatio != 0.25`,
		`refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.MaxDeferredFragments != 2`,
		`refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.LastFlowControlEvent != "fragment_window_refill"`,
		`refillDiagnosis.Diagnosis.Mux.Events[0].Event != "fragment_window_refill"`,
		`mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.TLS`,
		`mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.MutualTLS`,
		`mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.NegotiatedProtocol != "gofly-mux/generated-mtls-test"`,
		`mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Candidate.NegotiatedProtocol != "gofly-mux/generated-mtls-test"`,
		`mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.OpenedStreams != 1`,
		`mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.ClosedStreams != 1`,
		`mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.ActiveStreams != 0`,
		`RPCMuxLogConfig{Enabled: true, Diagnosis: true, ExportEvents: true, EventFamily: "flow-control", Event: "fragment-window-refill"}`,
		`RPCMuxOTelCompatibleLogConfig{Enabled: true, Sink: "slog", Profile: "generated-mtls-refill"}`,
		`mtlsClientOptions := append(tlsCfg.RPC.Mux.ClientOptions(),`,
		`rpc.WithExperimentalMuxConnectionManager(mtlsManager),`,
		`mtlsClient.MuxStream(mtlsTraceCtx, "greeter/Watch")`,
		`mtlsClient.ObserveMuxDiagnosis(mtlsRefillTraceCtx, refillDiagnosis)`,
		`rpc.ValidateRPCMuxDiagnosisSinkSetConfig`,
		`rpc.NewRPCMuxDiagnosisSinkSet`,
		`rpc.WithMuxDiagnosisEventExporter(sinkSet`,
		`rpc.WithServerMuxDiagnosisEventExporter(sinkSet`,
		`ValidateRPCMuxConfigWithWarnings(cfg.RPC.Mux)`,
		`addGeneratedControlPlaneConfig("rpcMuxConfigWarnings", rpcMuxConfigWarnings)`,
		`addGeneratedControlPlaneConfig("rpcMuxConfigWarningSchema", json.RawMessage(RPCMuxConfigWarningJSONSchema))`,
		`addGeneratedControlPlaneConfig("controlPlaneSchemaChecksums", generatedControlPlaneSchemaChecksums())`,
		`snapshot.Configs["generated.rpcMuxConfigWarnings"]`,
		`snapshot.Configs["generated.rpcMuxConfigWarningSchema"]`,
		`generated.controlPlaneSchemaChecksums`,
		`generated.rpcMuxOperatorAuditSchemas`,
		`aiManifestSchema`,
		`const RPCMuxConfigWarningSchema = "gofly.rpc_mux_config_warning.v1"`,
		`const RPCMuxConfigWarningJSONSchema = `,
		`"required":["schema","field","message","current","recommended"]`,
		`ReloadSinkSet(ctx context.Context, sinkSet *rpc.RPCMuxDiagnosisSinkSet)`,
		`BreakerFailureThreshold`,
		`BreakerCooldown`,
		`RPCMuxDiagnosisExporterDeliveryConfig`,
		`func (c RPCMuxConfig) ServerOptionsWithSinkSet(sinkSet *rpc.RPCMuxDiagnosisSinkSet) []rpc.ServerOption`,
		`func TestRPCMuxConfigValidatesOTelCompatibleSink`,
		`mtlsTraceAttrs["rpc.mux.candidate.tls"].AsBool()`,
		`mtlsTraceAttrs["rpc.mux.candidate.mutual_tls"].AsBool()`,
		`mtlsTraceAttrs["rpc.mux.candidate.negotiated_protocol"].AsString() != "gofly-mux/generated-mtls-test"`,
		`mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.refills.count"].AsInt64() < 1`,
		`mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.stream_window_refill_ratio"].AsFloat64() != 0.5`,
		`mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.connection_window_refill_ratio"].AsFloat64() != 0.25`,
		`mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.max_deferred_fragments"].AsInt64() != 2`,
		`mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.last_flow_control_event"].AsString() != "fragment_window_refill"`,
		`mtlsRefillTraceAttrs["rpc.mux.event.flow_control.count"].AsInt64() < 1`,
		`restoreRecommendedSmokeConfig(t, repo, recommendedRestAddr, recommendedRPCAddr, recommendedAdminAddr)`,
		`assertControlPlaneMuxConfigWarningsCleared(t, recommendedControlPlane)`,
		`assertControlPlaneSchemaChecksumConfig(t, configs)`,
		`"\"negotiated_protocol\":\"gofly-mux/generated-mtls-test\""`,
		`"\"mutual_tls\":true"`,
		`"\"refill_profile_stream_window_refill_ratio\":0.5"`,
		`"\"refill_profile_connection_window_refill_ratio\":0.25"`,
		`"\"refill_profile_max_deferred_fragments\":2"`,
		`"\"refill_profile_last_flow_control_event\":\"fragment_window_refill\""`,
		`"\"msg\":\"rpc mux runtime event\""`,
		`"\"event_family\":\"flow_control\""`,
		`"\"event\":\"fragment_window_refill\""`,
		`"\"connection_id\":\""`,
		`"\"pool_slot\":1"`,
		`"\"msg\":\"rpc mux otel log event\""`,
		`"\"otel_log_name\":\"rpc.mux.diagnosis_event\""`,
		`"\"otel_log_profile\":\"generated-mtls-refill\""`,
		`"\"rpc_mux_event_family\":\"flow_control\""`,
		`"\"rpc_mux_event_name\":\"fragment_window_refill\""`,
		`"\"rpc_mux_connection_id\":\""`,
		`"\"rpc_mux_pool_slot\":1"`,
	}
	markers := append(append(openBeforeMarkers, postOpenMarkers...), negotiationSummaryMarkers...)
	markers = append(markers, generatedSuccessMarkers...)
	missing := missingReleaseMarkers(source, markers)
	if len(missing) > 0 {
		item.Status = "fail"
		item.Detail = "generated RPC mux retry smoke markers missing"
		item.Blocker = true
		item.Evidence = map[string]any{"generated-rpc-mux-retry-smoke": map[string]any{"missing": missing}}
		return item, []string{"generated RPC mux retry smoke markers missing"}
	}
	runtimeTests := []string{
		"TestExperimentalMuxConnectionManagerRetriesOpenBeforeStreamAfterPoolExhaustion",
		"TestExperimentalMuxConnectionManagerDoesNotReplayAfterStreamOpen",
		"TestExperimentalMuxConnectionManagerEndpointHealthBackoffCooldown",
		"TestExperimentalMuxCandidateAdapterFragmentsLargePayload",
		"TestExperimentalMuxCandidateAdapterRejectsOversizedMessagePolicy",
		"TestExperimentalMuxTransportFragmentBackpressureWaitsForWindowUpdate",
		"TestExperimentalMuxTransportFragmentCreditWaitTimeout",
		"TestExperimentalMuxTransportFragmentWindowRefillPolicy",
		"TestExperimentalMuxCandidateFragmentBackpressureMetrics",
		"TestExperimentalMuxCandidateConfigValidateFragmentWindowRefillPolicy",
		"TestExperimentalMuxCandidateConfigValidateFragmentWindowPolicyRiskModes",
		"TestExperimentalMuxConnectionManagerTLSFailureDiagnosisEvents",
		"TestExperimentalMuxConnectionManagerALPNMismatchDiagnosisEvents",
		"TestExperimentalMuxConnectionManagerCandidateDowngradeDiagnostics",
	}
	runtimeCommand := []string{"test", "-count=1", "-shuffle=on", "./rpc", "-run", strings.Join(runtimeTests, "|")}
	runtimeOutput, runtimeErr := runReleaseGoCommand(runtimeCommand...)
	if runtimeErr != nil {
		item.Status = "fail"
		item.Detail = strings.TrimSpace(string(runtimeOutput))
		if item.Detail == "" {
			item.Detail = runtimeErr.Error()
		}
		item.Blocker = true
		item.Evidence = map[string]any{"generated-rpc-mux-retry-smoke": map[string]any{
			"runtimeCommand": append([]string{"go"}, runtimeCommand...),
			"runtimeOutput":  strings.TrimSpace(string(runtimeOutput)),
		}}
		return item, []string{"generated RPC mux retry runtime proof failed"}
	}
	generatedProjectCommand, generatedProjectOutput, generatedProjectErr := runGeneratedRPCMuxAdminSmokeReleaseProof()
	if generatedProjectErr != nil {
		item.Status = "fail"
		item.Detail = strings.TrimSpace(string(generatedProjectOutput))
		if item.Detail == "" {
			item.Detail = generatedProjectErr.Error()
		}
		item.Blocker = true
		item.Evidence = map[string]any{"generated-rpc-mux-retry-smoke": map[string]any{
			"runtimeCommand":          append([]string{"go"}, runtimeCommand...),
			"runtimeOutput":           strings.TrimSpace(string(runtimeOutput)),
			"generatedProjectCommand": generatedProjectCommand,
			"generatedProjectOutput":  strings.TrimSpace(string(generatedProjectOutput)),
		}}
		return item, []string{"generated RPC mux admin smoke proof failed"}
	}
	negotiationSummaryPhases := []string{"tls_failure", "alpn_mismatch", "frame_policy_mismatch"}
	configWarningSchemaChecksum := fmt.Sprintf("%x", sha256.Sum256([]byte("gofly.rpc_mux_config_warning.v1")))
	item.Detail = "generated RPC mux smoke covers retry boundary and TLS/ALPN/frame-policy negotiation summary admin diagnosis"
	item.Evidence = map[string]any{
		"generated-rpc-mux-retry-smoke": map[string]any{
			"schema":                               "gofly.generated_rpc_mux_retry_smoke.v1",
			"source":                               path,
			"verifyCommand":                        "go test ./...",
			"runtimeCommand":                       append([]string{"go"}, runtimeCommand...),
			"runtimeOutput":                        strings.TrimSpace(string(runtimeOutput)),
			"generatedProjectCommand":              generatedProjectCommand,
			"generatedProjectOutput":               strings.TrimSpace(string(generatedProjectOutput)),
			"runtimeProof":                         true,
			"runtimeProofs":                        runtimeTests,
			"generatedProjectProof":                true,
			"openBeforeRetry":                      true,
			"postOpenNoReplay":                     true,
			"cooldownBackoff":                      true,
			"candidateLargePayloadFragmentation":   true,
			"candidateMessagePolicy":               true,
			"candidateFramePolicyDiagnosis":        true,
			"candidatePolicyRiskModeValidation":    true,
			"fragmentBackpressure":                 true,
			"fragmentCreditWaitTimeout":            true,
			"fragmentWindowUpdateDiagnosis":        true,
			"fragmentWindowRefillPolicy":           true,
			"fragmentWindowRefillRuntimeDiagnosis": true,
			"generatedRefillProfileAdminSmoke":     true,
			"generatedConfigWarningContract":       true,
			"generatedConfigWarningSchema":         "gofly.rpc_mux_config_warning.v1",
			"generatedConfigWarningSchemaChecksum": configWarningSchemaChecksum,
			"generatedConfigWarningSnapshotKey":    "generated.rpcMuxConfigWarnings",
			"generatedConfigWarningSchemaKey":      "generated.rpcMuxConfigWarningSchema",
			"fragmentMaxDeferredFailFast":          true,
			"generatedPolicyRiskModeValidation":    true,
			"generatedMTLSSuccess":                 true,
			"negotiatedProtocol":                   true,
			"lifecycleDiagnosis":                   true,
			"successProtocol":                      "gofly-mux/generated-mtls-test",
			"negotiationSummary":                   true,
			"tlsFailureSummary":                    true,
			"alpnMismatchSummary":                  true,
			"negotiationSummaryPhases":             negotiationSummaryPhases,
			"negotiationSummarySurface":            "/rpc/diagnosis",
			"openBeforeMarkers":                    openBeforeMarkers,
			"postOpenMarkers":                      postOpenMarkers,
			"negotiationSummaryMarkers":            negotiationSummaryMarkers,
			"generatedSuccessMarkers":              generatedSuccessMarkers,
		},
	}
	return item, nil
}

func runGeneratedRPCMuxAdminSmokeReleaseProof() ([]string, []byte, error) {
	root, err := releaseRepoRoot()
	if err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "gofly-release-rpc-mux-admin-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	projectDir := filepath.Join(dir, "greeter")
	if err := generator.GenerateService(generator.ServiceOptions{
		Name:          "greeter",
		Module:        "example.com/greeter",
		Dir:           projectDir,
		FrameworkPath: root,
	}); err != nil {
		return nil, nil, err
	}
	var output bytes.Buffer
	tidyCommand := []string{"go", "mod", "tidy"}
	tidyOutput, err := runReleaseGoCommandInDir(projectDir, tidyCommand[1:]...)
	output.Write(tidyOutput)
	if err != nil {
		return tidyCommand, output.Bytes(), err
	}
	command := []string{"go", "test", "-count=1", "./internal/admin", "-run", "TestAdminDiagnostics"}
	testOutput, err := runReleaseGoCommandInDir(projectDir, command[1:]...)
	output.Write(testOutput)
	return command, output.Bytes(), err
}

func missingReleaseMarkers(source string, markers []string) []string {
	missing := make([]string, 0)
	for _, marker := range markers {
		if !strings.Contains(source, marker) {
			missing = append(missing, marker)
		}
	}
	return missing
}

func runReleaseGoCommand(args ...string) ([]byte, error) {
	goCmd := strings.TrimSpace(os.Getenv("GO"))
	if goCmd == "" {
		goCmd = "go"
	}
	root, err := releaseRepoRoot()
	if err != nil {
		return nil, err
	}
	// #nosec G204 G702 -- release check invokes the configured Go binary with fixed argv segments.
	cmd := exec.Command(goCmd, args...)
	cmd.Dir = root
	return cmd.CombinedOutput()
}

func runReleaseGoCommandInDir(dir string, args ...string) ([]byte, error) {
	goCmd := strings.TrimSpace(os.Getenv("GO"))
	if goCmd == "" {
		goCmd = "go"
	}
	// #nosec G204 G702 -- release check invokes the configured Go binary with fixed argv segments in a generated temp project.
	cmd := exec.Command(goCmd, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func releaseRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) &&
			fileExists(filepath.Join(dir, "cmd", "gofly")) &&
			fileExists(filepath.Join(dir, "rpc")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root not found")
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readReleaseJSONFile(path string, label string) (map[string]any, error) {
	resolved, err := resolveReleaseEvidencePath(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", label, err)
	}
	data, err := os.ReadFile(resolved) // #nosec G304 -- release check reads explicit repository evidence files.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	return out, nil
}

func resolveReleaseEvidencePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("release evidence path must be relative")
	}
	path = filepath.Clean(path)
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(current, path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", os.ErrNotExist
}

func releaseGatewayOpenAPIAggregationReport(projectDir string) (gateway.AggregationValidationReport, gatewayAggregationSARIFContext, error) {
	baseDoc, err := readGatewayOpenAPIDocument(filepath.Join(projectDir, "etc", "edge-openapi-base.json"))
	if err != nil {
		return gateway.AggregationValidationReport{}, gatewayAggregationSARIFContext{}, err
	}
	candidateDoc, err := readGatewayOpenAPIDocument(filepath.Join(projectDir, "etc", "edge-openapi-candidate.json"))
	if err != nil {
		return gateway.AggregationValidationReport{}, gatewayAggregationSARIFContext{}, err
	}
	context := gatewayOpenAPIAggregationSARIFContext(candidateDoc, "home")
	importOptions := gateway.OpenAPIRouteOptions{GatewayPrefix: "/", Service: "openapi", Targets: []string{"http://127.0.0.1:1"}}
	baseRoutes, err := gateway.RouteConfigsFromOpenAPI(baseDoc, importOptions)
	if err != nil {
		return gateway.AggregationValidationReport{}, context, fmt.Errorf("import base openapi aggregation routes: %w", err)
	}
	candidateRoutes, err := gateway.RouteConfigsFromOpenAPI(candidateDoc, importOptions)
	if err != nil {
		return gateway.AggregationValidationReport{}, context, fmt.Errorf("import candidate openapi aggregation routes: %w", err)
	}
	candidateAggregation, err := gatewayAggregationFromRoutes(candidateRoutes, "home")
	if err != nil {
		return gateway.AggregationValidationReport{}, context, err
	}
	gw, err := gateway.NewFromConfig(gateway.Config{Routes: baseRoutes}, nil)
	if err != nil {
		return gateway.AggregationValidationReport{}, context, fmt.Errorf("load base openapi aggregation config: %w", err)
	}
	return gw.ValidateAggregation("home", candidateAggregation), context, nil
}

func readReleaseGatewayConfig(path string) (gateway.Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- release check reads a generated file from a temporary project directory it just created.
	if err != nil {
		return gateway.Config{}, fmt.Errorf("read gateway config: %w", err)
	}
	var config struct {
		Gateway gateway.Config `json:"gateway"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return gateway.Config{}, fmt.Errorf("decode gateway config: %w", err)
	}
	return config.Gateway, nil
}

func readReleaseGatewayAggregationCandidate(path string) (gateway.AggregationConfig, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- release check reads a generated file from a temporary project directory it just created.
	if err != nil {
		return gateway.AggregationConfig{}, fmt.Errorf("read candidate aggregation: %w", err)
	}
	var candidate gateway.AggregationConfig
	if err := json.Unmarshal(data, &candidate); err != nil {
		return gateway.AggregationConfig{}, fmt.Errorf("decode candidate aggregation: %w", err)
	}
	return candidate, nil
}

func readReleaseGatewayProfiles(path string) ([]gateway.TranscodeProfile, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- release check reads a generated file from a temporary project directory it just created.
	if err != nil {
		return nil, fmt.Errorf("read gateway config: %w", err)
	}
	var config struct {
		TranscodeProfiles []gateway.TranscodeProfile `json:"transcodeProfiles"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode gateway config: %w", err)
	}
	return config.TranscodeProfiles, nil
}

func readReleaseGatewayProfileCandidate(path string) (gateway.TranscodeProfile, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- release check reads a generated file from a temporary project directory it just created.
	if err != nil {
		return gateway.TranscodeProfile{}, fmt.Errorf("read candidate profile: %w", err)
	}
	var candidate gateway.TranscodeProfile
	if err := json.Unmarshal(data, &candidate); err != nil {
		return gateway.TranscodeProfile{}, fmt.Errorf("decode candidate profile: %w", err)
	}
	return candidate, nil
}

func GatewayProfileContractCheck() (CheckItem, []string) {
	return releaseGatewayProfileContractCheck()
}

func GatewayAggregationContractCheck() (CheckItem, []string) {
	return releaseGatewayAggregationContractCheck()
}

func RPCMuxAdapterEvidenceCheck() (CheckItem, []string) {
	return releaseRPCMuxAdapterEvidenceCheck()
}

func GeneratedRPCMuxRetrySmokeCheck() (CheckItem, []string) {
	return releaseGeneratedRPCMuxRetrySmokeCheck()
}

func ResolveEvidencePath(path string) (string, error) {
	return resolveReleaseEvidencePath(path)
}

func ReadJSONFile(path, label string) (map[string]any, error) {
	return readReleaseJSONFile(path, label)
}

func ReadGatewayConfig(path string) (gateway.Config, error) {
	return readReleaseGatewayConfig(path)
}

func ReadGatewayAggregationCandidate(path string) (gateway.AggregationConfig, error) {
	return readReleaseGatewayAggregationCandidate(path)
}

func ReadGatewayProfiles(path string) ([]gateway.TranscodeProfile, error) {
	return readReleaseGatewayProfiles(path)
}

func ReadGatewayProfileCandidate(path string) (gateway.TranscodeProfile, error) {
	return readReleaseGatewayProfileCandidate(path)
}
