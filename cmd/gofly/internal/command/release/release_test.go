package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/gateway"
	"github.com/imajinyun/gofly/rest"
)

func TestParseChangelogVersion(t *testing.T) {
	dir := t.TempDir()

	// No file.
	if _, err := parseChangelogVersion(filepath.Join(dir, "nope.md")); err == nil {
		t.Fatal("expected error for missing file")
	}

	// File without version header.
	plain := filepath.Join(dir, "plain.md")
	if err := os.WriteFile(plain, []byte("# Changelog\n\nSome text.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := parseChangelogVersion(plain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "" {
		t.Fatalf("expected empty version, got %q", v)
	}

	// File with version header.
	versioned := filepath.Join(dir, "versioned.md")
	if err := os.WriteFile(versioned, []byte("# Changelog\n\n## v1.2.3\n\n- fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err = parseChangelogVersion(versioned)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "1.2.3" {
		t.Fatalf("expected 1.2.3, got %q", v)
	}
}

func TestRecommendSemver(t *testing.T) {
	cases := []struct {
		blockers []string
		warnings []string
		want     string
	}{
		{nil, nil, "patch"},
		{nil, []string{"w"}, "minor"},
		{[]string{"API breaking: 1 change(s)"}, nil, "major"},
		{[]string{"something else"}, nil, "minor (with blockers)"},
	}
	for _, tc := range cases {
		got := recommendSemver(tc.blockers, tc.warnings)
		if got != tc.want {
			t.Fatalf("recommendSemver(%v, %v) = %q, want %q", tc.blockers, tc.warnings, got, tc.want)
		}
	}
}

func TestReleaseCheckCommandWithBreakingAPI(t *testing.T) {
	dir := t.TempDir()
	baseAPI := filepath.Join(dir, "base.api")
	targetAPI := filepath.Join(dir, "target.api")
	if err := os.WriteFile(baseAPI, []byte(`type User { ID int Name string }
service UserService { @handler getUser GET /users/{id} (User) returns (User) }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetAPI, []byte(`type User { ID int }
service UserService { @handler getUser POST /users/{id} (User) returns (User) }`), 0o644); err != nil {
		t.Fatal(err)
	}

	var outBuf bytes.Buffer
	err := CheckCommand([]string{
		"--api-base", baseAPI,
		"--api-target", targetAPI,
		"--changelog", filepath.Join(dir, "no-changelog"),
	}, testHooks(&outBuf))

	if err == nil {
		t.Fatal("expected release check to fail with breaking API changes")
	}

	out := outBuf.String()
	if !strings.Contains(out, "BLOCKED") {
		t.Fatalf("expected BLOCKED in output, got:\n%s", out)
	}
	if !strings.Contains(out, "api-breaking") {
		t.Fatalf("expected api-breaking in output, got:\n%s", out)
	}
}

func TestReleaseCheckCommandJSONAndChangelogBlocker(t *testing.T) {
	t.Setenv("API_BASE_REF", "definitely-missing-release-base-ref")
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\nUnreleased notes only.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := CheckCommand([]string{"--changelog", changelog, "--json"}, testHooks(&out)); err != nil {
		t.Fatalf("releaseCheckCommand json pass: %v", err)
	}
	var passEnvelope struct {
		OK      bool               `json:"ok"`
		Command string             `json:"command"`
		Data    releaseCheckReport `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &passEnvelope); err != nil {
		t.Fatalf("releaseCheckCommand json pass decode: %v\n%s", err, out.String())
	}
	if !passEnvelope.OK || passEnvelope.Command != "release.check" || !strings.Contains(passEnvelope.Data.Summary, "PASS") || len(passEnvelope.Data.Checks) == 0 {
		t.Fatalf("releaseCheckCommand json pass envelope = %+v, want ok release.check report", passEnvelope)
	}
	out.Reset()
	if err := CheckCommand([]string{"--changelog", changelog, "--json", "--evidence", "gateway-aggregation-contract"}, testHooks(&out)); err != nil {
		t.Fatalf("releaseCheckCommand evidence json: %v", err)
	}
	var evidenceEnvelope struct {
		OK      bool               `json:"ok"`
		Command string             `json:"command"`
		Data    releaseCheckReport `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &evidenceEnvelope); err != nil {
		t.Fatalf("releaseCheckCommand evidence json decode: %v\n%s", err, out.String())
	}
	if !evidenceEnvelope.OK || len(evidenceEnvelope.Data.Checks) != 1 || evidenceEnvelope.Data.Checks[0].Name != "gateway-aggregation-contract" || evidenceEnvelope.Data.Checks[0].Evidence["aggregation-openapi-diff"] == nil {
		t.Fatalf("releaseCheckCommand evidence envelope = %+v, want aggregation evidence only", evidenceEnvelope)
	}
	out.Reset()
	if err := CheckCommand([]string{"--changelog", changelog, "--json", "--evidence", "rpc-mux-adapter-evidence"}, testHooks(&out)); err != nil {
		t.Fatalf("releaseCheckCommand rpc mux evidence json: %v", err)
	}
	if err := json.Unmarshal(out.Bytes(), &evidenceEnvelope); err != nil {
		t.Fatalf("releaseCheckCommand rpc mux evidence json decode: %v\n%s", err, out.String())
	}
	if !evidenceEnvelope.OK ||
		len(evidenceEnvelope.Data.Checks) != 1 ||
		evidenceEnvelope.Data.Checks[0].Name != "rpc-mux-adapter-evidence" ||
		evidenceEnvelope.Data.Checks[0].Evidence["rpc-mux-adapter-evidence"] == nil {
		t.Fatalf("releaseCheckCommand rpc mux evidence envelope = %+v, want rpc mux evidence only", evidenceEnvelope)
	}
	out.Reset()
	if err := CheckCommand([]string{"--changelog", changelog, "--json", "--evidence", "generated-rpc-mux-retry-smoke"}, testHooks(&out)); err != nil {
		t.Fatalf("releaseCheckCommand generated rpc mux retry evidence json: %v", err)
	}
	if err := json.Unmarshal(out.Bytes(), &evidenceEnvelope); err != nil {
		t.Fatalf("releaseCheckCommand generated rpc mux retry evidence json decode: %v\n%s", err, out.String())
	}
	if !evidenceEnvelope.OK ||
		len(evidenceEnvelope.Data.Checks) != 1 ||
		evidenceEnvelope.Data.Checks[0].Name != "generated-rpc-mux-retry-smoke" ||
		evidenceEnvelope.Data.Checks[0].Evidence["generated-rpc-mux-retry-smoke"] == nil {
		t.Fatalf("releaseCheckCommand generated rpc mux retry evidence envelope = %+v, want generated rpc mux retry evidence only", evidenceEnvelope)
	}
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n## v9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := CheckCommand([]string{"--changelog", changelog, "--json", "--evidence", "changelog-version"}, testHooks(&out))
	if err == nil || !strings.Contains(err.Error(), "release check failed") {
		t.Fatalf("releaseCheckCommand changelog blocker error = %v, want release check failed", err)
	}
	var failEnvelope struct {
		OK      bool               `json:"ok"`
		Command string             `json:"command"`
		Data    releaseCheckReport `json:"data"`
		Error   *jsonError         `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &failEnvelope); err != nil {
		t.Fatalf("releaseCheckCommand blocker json decode: %v\n%s", err, out.String())
	}
	if failEnvelope.OK || failEnvelope.Command != "release.check" || failEnvelope.Error == nil || failEnvelope.Error.Code != "RELEASE_CHECK_FAILED" || !strings.Contains(failEnvelope.Data.Summary, "BLOCKED") || !strings.Contains(out.String(), `9.9.9`) {
		t.Fatalf("releaseCheckCommand blocker envelope = %+v, want structured blocker", failEnvelope)
	}
}

func TestReleaseGatewayProfileContractCheck(t *testing.T) {
	item, blockers := releaseGatewayProfileContractCheck()
	if item.Name != "gateway-profile-contract" || item.Status != "pass" || item.Blocker || len(blockers) != 0 || !strings.Contains(item.Detail, "compatible profile diff") {
		t.Fatalf("gateway profile release check = %+v blockers=%v, want pass", item, blockers)
	}
}

func TestReleaseGatewayAggregationContractCheck(t *testing.T) {
	item, blockers := releaseGatewayAggregationContractCheck()
	if item.Name != "gateway-aggregation-contract" || item.Status != "pass" || item.Blocker || len(blockers) != 0 || !strings.Contains(item.Detail, "compatible aggregation diff") {
		t.Fatalf("gateway aggregation release check = %+v blockers=%v, want pass", item, blockers)
	}
	jsonEvidence, ok := item.Evidence["aggregation-json-diff"].(map[string]any)
	if !ok || jsonEvidence["compatible"] != true || jsonEvidence["changes"] == nil {
		t.Fatalf("aggregation json evidence = %#v", item.Evidence["aggregation-json-diff"])
	}
	jsonDetails, ok := jsonEvidence["changeDetails"].([]gatewayAggregationChangeView)
	if !ok || len(jsonDetails) == 0 || jsonDetails[0].Location.Route != "bff-home" {
		t.Fatalf("aggregation json change details = %#v", jsonEvidence["changeDetails"])
	}
	openAPIEvidence, ok := item.Evidence["aggregation-openapi-diff"].(map[string]any)
	if !ok || openAPIEvidence["compatible"] != true || openAPIEvidence["changes"] == nil {
		t.Fatalf("aggregation openapi evidence = %#v", item.Evidence["aggregation-openapi-diff"])
	}
	openAPIDetails, ok := openAPIEvidence["changeDetails"].([]gatewayAggregationChangeView)
	if !ok || len(openAPIDetails) == 0 {
		t.Fatalf("aggregation openapi change details = %#v", openAPIEvidence["changeDetails"])
	}
	var sawOpenAPILocator bool
	for _, detail := range openAPIDetails {
		if detail.Location.Path == "/home" &&
			detail.Location.Method == "GET" &&
			detail.Location.Mapping == "default:meta.source -> meta.source" &&
			detail.Location.MappingSource == "default:meta.source" &&
			detail.Location.MappingTarget == "meta.source" {
			sawOpenAPILocator = true
			break
		}
	}
	if !sawOpenAPILocator {
		t.Fatalf("aggregation openapi change details = %+v, want path/method/mapping locator", openAPIDetails)
	}
}

func TestReleaseRPCMuxAdapterEvidenceCheck(t *testing.T) {
	item, blockers := releaseRPCMuxAdapterEvidenceCheck()
	if item.Name != "rpc-mux-adapter-evidence" ||
		item.Status != "pass" ||
		item.Blocker ||
		len(blockers) != 0 ||
		!strings.Contains(item.Detail, "report-only") {
		t.Fatalf("rpc mux adapter evidence check = %+v blockers=%v, want report-only pass", item, blockers)
	}
	evidence, ok := item.Evidence["rpc-mux-adapter-evidence"].(map[string]any)
	if !ok {
		t.Fatalf("rpc mux adapter evidence = %#v", item.Evidence["rpc-mux-adapter-evidence"])
	}
	if evidence["benchmark"] != "BenchmarkRPCExperimentalMuxAdapterOpenSendReceiveClose" ||
		evidence["status"] != "report-only" ||
		evidence["allocationMode"] != "report-only" ||
		evidence["latencyMode"] != "report-only" ||
		evidence["promotionStatus"] != "blocked" ||
		evidence["baseline"] == nil ||
		evidence["current"] == nil {
		t.Fatalf("rpc mux adapter evidence = %#v, want report-only benchmark evidence", evidence)
	}
	runtimeEvidence, ok := evidence["runtimeEvidence"].(map[string]any)
	if !ok ||
		runtimeEvidence["family"] != "generated-rpc-mux-retry-smoke" ||
		runtimeEvidence["openBeforeRetry"] != true ||
		runtimeEvidence["postOpenNoReplay"] != true ||
		runtimeEvidence["cooldownBackoff"] != true {
		t.Fatalf("rpc mux adapter runtime evidence = %#v, want generated mux retry boundary reference", evidence["runtimeEvidence"])
	}
}

func TestReleaseGeneratedRPCMuxRetrySmokeCheck(t *testing.T) {
	item, blockers := releaseGeneratedRPCMuxRetrySmokeCheck()
	if item.Name != "generated-rpc-mux-retry-smoke" ||
		item.Status != "pass" ||
		item.Blocker ||
		len(blockers) != 0 {
		t.Fatalf("generated rpc mux retry smoke item=%+v blockers=%v, want pass without blockers", item, blockers)
	}
	evidence, ok := item.Evidence["generated-rpc-mux-retry-smoke"].(map[string]any)
	if !ok {
		t.Fatalf("generated rpc mux retry smoke evidence = %#v", item.Evidence["generated-rpc-mux-retry-smoke"])
	}
	if evidence["schema"] != "gofly.generated_rpc_mux_retry_smoke.v1" ||
		evidence["runtimeProof"] != true ||
		evidence["runtimeCommand"] == nil ||
		evidence["runtimeProofs"] == nil ||
		evidence["generatedProjectCommand"] == nil ||
		evidence["generatedProjectProof"] != true ||
		evidence["openBeforeRetry"] != true ||
		evidence["postOpenNoReplay"] != true ||
		evidence["cooldownBackoff"] != true ||
		evidence["candidateLargePayloadFragmentation"] != true ||
		evidence["candidateMessagePolicy"] != true ||
		evidence["candidateFramePolicyDiagnosis"] != true ||
		evidence["candidatePolicyRiskModeValidation"] != true ||
		evidence["fragmentBackpressure"] != true ||
		evidence["fragmentCreditWaitTimeout"] != true ||
		evidence["fragmentWindowUpdateDiagnosis"] != true ||
		evidence["fragmentWindowRefillPolicy"] != true ||
		evidence["fragmentWindowRefillRuntimeDiagnosis"] != true ||
		evidence["generatedRefillProfileAdminSmoke"] != true ||
		evidence["generatedConfigWarningContract"] != true ||
		evidence["generatedConfigWarningSchema"] != "gofly.rpc_mux_config_warning.v1" ||
		evidence["generatedConfigWarningSchemaChecksum"] == "" ||
		evidence["generatedConfigWarningSnapshotKey"] != "generated.rpcMuxConfigWarnings" ||
		evidence["generatedConfigWarningSchemaKey"] != "generated.rpcMuxConfigWarningSchema" ||
		evidence["fragmentMaxDeferredFailFast"] != true ||
		evidence["generatedPolicyRiskModeValidation"] != true ||
		evidence["generatedMTLSSuccess"] != true ||
		evidence["negotiatedProtocol"] != true ||
		evidence["lifecycleDiagnosis"] != true ||
		evidence["successProtocol"] != "gofly-mux/generated-mtls-test" ||
		evidence["negotiationSummary"] != true ||
		evidence["tlsFailureSummary"] != true ||
		evidence["alpnMismatchSummary"] != true ||
		evidence["negotiationSummarySurface"] != "/rpc/diagnosis" ||
		evidence["verifyCommand"] != "go test ./..." {
		t.Fatalf("generated rpc mux retry smoke evidence payload = %#v", evidence)
	}
	phases, ok := evidence["negotiationSummaryPhases"].([]string)
	if !ok || len(phases) != 3 ||
		phases[0] != "tls_failure" ||
		phases[1] != "alpn_mismatch" ||
		phases[2] != "frame_policy_mismatch" {
		t.Fatalf("generated rpc mux retry smoke negotiation phases = %#v", evidence["negotiationSummaryPhases"])
	}
}

func TestReleaseGeneratedRPCMuxRetrySmokeCheckFailureContracts(t *testing.T) {
	t.Run("source unavailable", func(t *testing.T) {
		root := t.TempDir()
		for _, path := range []string{"cmd/gofly", "rpc"} {
			if err := os.MkdirAll(filepath.Join(root, path), 0o750); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/missing\n\ngo 1.26\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Chdir(root)
		item, blockers := releaseGeneratedRPCMuxRetrySmokeCheck()
		if item.Status != "fail" || !item.Blocker || len(blockers) != 1 ||
			blockers[0] != "generated RPC mux retry smoke source is unavailable" ||
			item.Detail == "" {
			t.Fatalf("source unavailable item=%+v blockers=%v", item, blockers)
		}
	})

	t.Run("missing generated warning marker fails", func(t *testing.T) {
		sourcePath, err := resolveReleaseEvidencePath(filepath.Join("cmd", "gofly", "internal", "generator", "templates.go"))
		if err != nil {
			t.Fatal(err)
		}
		sourceData, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		for _, path := range []string{filepath.Join("cmd", "gofly", "internal", "generator"), "rpc"} {
			if err := os.MkdirAll(filepath.Join(root, path), 0o750); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/imajinyun/gofly\n\ngo 1.26\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		source := string(sourceData)
		source = strings.ReplaceAll(source, `snapshot.Configs["generated.rpcMuxConfigWarnings"]`, "")
		if err := os.WriteFile(filepath.Join(root, "cmd", "gofly", "internal", "generator", "templates.go"), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Chdir(root)
		t.Setenv("GO", writeReleaseGoShim(t, "#!/bin/sh\necho skipped\nexit 0\n"))
		item, blockers := releaseGeneratedRPCMuxRetrySmokeCheck()
		if item.Status != "fail" || !item.Blocker || len(blockers) != 1 ||
			blockers[0] != "generated RPC mux retry smoke markers missing" {
			t.Fatalf("missing warning marker item=%+v blockers=%v", item, blockers)
		}
		evidence, ok := item.Evidence["generated-rpc-mux-retry-smoke"].(map[string]any)
		missing, _ := evidence["missing"].([]string)
		if !ok || !containsReleaseString(missing, `snapshot.Configs["generated.rpcMuxConfigWarnings"]`) {
			t.Fatalf("missing warning marker evidence = %#v", item.Evidence)
		}
	})

	t.Run("runtime proof failure", func(t *testing.T) {
		shim := writeReleaseGoShim(t, `#!/bin/sh
echo "runtime proof failed"
exit 23
`)
		t.Setenv("GO", shim)
		item, blockers := releaseGeneratedRPCMuxRetrySmokeCheck()
		if item.Status != "fail" || !item.Blocker || len(blockers) != 1 ||
			item.Detail != "runtime proof failed" {
			t.Fatalf("runtime failure item=%+v blockers=%v", item, blockers)
		}
		evidence, ok := item.Evidence["generated-rpc-mux-retry-smoke"].(map[string]any)
		if !ok || evidence["runtimeCommand"] == nil || evidence["runtimeOutput"] != "runtime proof failed" {
			t.Fatalf("runtime failure evidence = %#v", item.Evidence)
		}
	})

	t.Run("generated project tidy failure", func(t *testing.T) {
		shim := writeReleaseGoShim(t, `#!/bin/sh
if [ "$1" = "test" ] && [ "$2" = "-count=1" ] && [ "$3" = "-shuffle=on" ]; then
  echo "runtime proof ok"
  exit 0
fi
echo "generated tidy failed"
exit 24
`)
		t.Setenv("GO", shim)
		item, blockers := releaseGeneratedRPCMuxRetrySmokeCheck()
		if item.Status != "fail" || !item.Blocker || len(blockers) != 1 ||
			item.Detail != "generated tidy failed" {
			t.Fatalf("generated failure item=%+v blockers=%v", item, blockers)
		}
		evidence, ok := item.Evidence["generated-rpc-mux-retry-smoke"].(map[string]any)
		if !ok || evidence["generatedProjectCommand"] == nil ||
			evidence["generatedProjectOutput"] != "generated tidy failed" {
			t.Fatalf("generated failure evidence = %#v", item.Evidence)
		}
	})
}

func writeReleaseGoShim(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReleaseCheckCommandAPIAndRPCPassAndErrorBranches(t *testing.T) {
	t.Setenv("API_BASE_REF", "definitely-missing-release-base-ref")
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseAPI := filepath.Join(dir, "base.api")
	targetAPI := filepath.Join(dir, "target.api")
	if err := os.WriteFile(baseAPI, []byte(`type PingResponse {
  Message string
}
service ping-api {
  @handler ping
  get /ping returns (PingResponse)
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetAPI, []byte(`type PingResponse {
  Message string
}
type PongResponse {
  Message string
}
service ping-api {
  @handler ping
  get /ping returns (PingResponse)
  @handler pong
  get /pong returns (PongResponse)
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	baseProto := filepath.Join(dir, "base.proto")
	targetProto := filepath.Join(dir, "target.proto")
	if err := os.WriteFile(baseProto, []byte(`syntax = "proto3";
package demo;
message PingRequest { string name = 1; }
message PingResponse { string message = 1; }
service Greeter { rpc Ping (PingRequest) returns (PingResponse); }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetProto, []byte(`syntax = "proto3";
package demo;
message PingRequest { string name = 1; }
message PingResponse { string message = 1; }
message PongRequest { string name = 1; }
message PongResponse { string message = 1; }
service Greeter {
  rpc Ping (PingRequest) returns (PingResponse);
  rpc Pong (PongRequest) returns (PongResponse);
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := CheckCommand([]string{"--api-base", baseAPI, "--api-target", targetAPI, "--rpc-base", baseProto, "--rpc-target", targetProto, "--changelog", changelog}, testHooks(&out)); err != nil {
		t.Fatalf("releaseCheckCommand added API/RPC pass: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "PASS") || !strings.Contains(out.String(), "api-breaking") || !strings.Contains(out.String(), "rpc-breaking") || !strings.Contains(out.String(), "go-mod-tidy") {
		t.Fatalf("release output = %s, want pass report with api/rpc/tidy", out.String())
	}

	removedProto := filepath.Join(dir, "removed.proto")
	if err := os.WriteFile(removedProto, []byte(`syntax = "proto3";
package demo;
message PingRequest { string name = 1; }
message PingResponse { string message = 1; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := CheckCommand([]string{"--rpc-base", baseProto, "--rpc-target", removedProto, "--changelog", changelog}, testHooks(&out))
	if err == nil || !strings.Contains(err.Error(), "release check failed") {
		t.Fatalf("releaseCheckCommand rpc breaking error = %v, want release check failed", err)
	}
	if !strings.Contains(out.String(), "RPC breaking") || !strings.Contains(out.String(), "Blocking:") || !strings.Contains(out.String(), "[BLOCKER]") {
		t.Fatalf("rpc breaking release output = %s, want rpc blocker report", out.String())
	}

	badProto := filepath.Join(dir, "bad.proto")
	if err := os.WriteFile(badProto, []byte("syntax = \"proto3\"; service"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = CheckCommand([]string{"--rpc-base", baseProto, "--rpc-target", badProto, "--changelog", changelog}, testHooks(&out))
	if err == nil || !strings.Contains(err.Error(), "release check failed") {
		t.Fatalf("releaseCheckCommand bad rpc error = %v, want release check failed", err)
	}
	if !strings.Contains(out.String(), "rpc breaking check error") && !strings.Contains(out.String(), "rpc-breaking") {
		t.Fatalf("bad rpc release output = %s, want rpc error branch", out.String())
	}
}

func TestGoReleaserUsesCurrentScriptPath(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", ".goreleaser.yml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "sh bin/scripts/check-mod-tidy.sh") {
		t.Fatalf("goreleaser config missing current tidy script path:\n%s", content)
	}
	if strings.Contains(content, "sh scripts/check-mod-tidy.sh") {
		t.Fatalf("goreleaser config still uses stale script path:\n%s", content)
	}
}

func containsReleaseString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestReleaseEvidenceReaders(t *testing.T) {
	readers := []struct {
		name string
		read func(string) error
	}{
		{"gateway config", func(p string) error { _, err := ReadGatewayConfig(p); return err }},
		{"aggregation", func(p string) error { _, err := ReadGatewayAggregationCandidate(p); return err }},
		{"profiles", func(p string) error { _, err := ReadGatewayProfiles(p); return err }},
		{"profile", func(p string) error { _, err := ReadGatewayProfileCandidate(p); return err }},
		{"openapi", func(p string) error { _, err := readGatewayOpenAPIDocument(p); return err }},
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.json")
			if err := reader.read(path); err == nil || !strings.Contains(err.Error(), "read") {
				t.Fatalf("missing err=%v", err)
			}
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := reader.read(path); err == nil || !strings.Contains(err.Error(), "decode") {
				t.Fatalf("malformed err=%v", err)
			}
			if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := reader.read(path); err != nil {
				t.Fatalf("empty object err=%v", err)
			}
		})
	}
	t.Run("ancestor evidence resolution", func(t *testing.T) {
		root := t.TempDir()
		child := filepath.Join(root, "nested")
		if err := os.Mkdir(child, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "proof.json")
		if err := os.WriteFile(path, []byte(`{"schema":"test"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Chdir(child)
		resolved, err := ResolveEvidencePath(" proof.json ")
		if err != nil || filepath.Base(resolved) != "proof.json" {
			t.Fatalf("resolved=%s err=%v", resolved, err)
		}
		data, err := ReadJSONFile("proof.json", "proof")
		if err != nil || data["schema"] != "test" {
			t.Fatalf("data=%v err=%v", data, err)
		}
		for _, name := range []string{"", path, "missing-unique-proof.json"} {
			if _, err := ReadJSONFile(name, "proof"); err == nil {
				t.Fatalf("invalid path %q accepted", name)
			}
		}
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadJSONFile("proof.json", "proof"); err == nil || !strings.Contains(err.Error(), "decode proof") {
			t.Fatalf("decode err=%v", err)
		}
		if _, err := ReadJSONFile(".", "directory"); err == nil || !strings.Contains(err.Error(), "read directory") {
			t.Fatalf("directory err=%v", err)
		}
	})
}

func TestReleaseLocalFailures(t *testing.T) {
	t.Run("temporary directory unavailable", func(t *testing.T) {
		root, err := releaseRepoRoot()
		if err != nil {
			t.Fatal(err)
		}
		t.Chdir(root)
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
		for _, check := range []func() (CheckItem, []string){GatewayProfileContractCheck, GatewayAggregationContractCheck} {
			item, blockers := check()
			if item.Status != "fail" || !item.Blocker || len(blockers) != 1 {
				t.Fatalf("item=%+v blockers=%v", item, blockers)
			}
		}
		if _, _, err := runGeneratedRPCMuxAdminSmokeReleaseProof(); err == nil {
			t.Fatal("missing temp accepted")
		}
	})
	t.Run("outside repository", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if _, err := releaseRepoRoot(); err == nil {
			t.Fatal("outside directory accepted")
		}
		if _, err := runReleaseGoCommand("version"); err == nil {
			t.Fatal("go command ran outside repository")
		}
		if _, _, err := runGeneratedRPCMuxAdminSmokeReleaseProof(); err == nil {
			t.Fatal("generated proof ran outside repository")
		}
		for _, check := range []func() (CheckItem, []string){RPCMuxAdapterEvidenceCheck, GeneratedRPCMuxRetrySmokeCheck} {
			item, blockers := check()
			if item.Status != "fail" || !item.Blocker || len(blockers) != 1 {
				t.Fatalf("item=%+v blockers=%v", item, blockers)
			}
		}
	})
	t.Run("invalid adapter evidence", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		if err := os.Mkdir("bench", 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("bench", "rpc_mux_adapter_evidence.json"), []byte(`{"status":"promoted"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		item, blockers := RPCMuxAdapterEvidenceCheck()
		if item.Status != "fail" || item.Detail != "rpc mux adapter evidence contract drifted" || len(blockers) != 1 {
			t.Fatalf("item=%+v blockers=%v", item, blockers)
		}
	})
	t.Run("unreadable retry source", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		if err := os.MkdirAll(filepath.Join("cmd", "gofly", "internal", "generator", "templates.go"), 0o700); err != nil {
			t.Fatal(err)
		}
		item, blockers := GeneratedRPCMuxRetrySmokeCheck()
		if item.Status != "fail" || !item.Blocker || len(blockers) != 1 {
			t.Fatalf("item=%+v blockers=%v", item, blockers)
		}
	})
	t.Run("missing retry markers", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		path := filepath.Join("cmd", "gofly", "internal", "generator", "templates.go")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package generator"), 0o600); err != nil {
			t.Fatal(err)
		}
		item, blockers := GeneratedRPCMuxRetrySmokeCheck()
		if item.Detail != "generated RPC mux retry smoke markers missing" || !item.Blocker || len(blockers) != 1 {
			t.Fatalf("item=%+v blockers=%v", item, blockers)
		}
	})
	t.Run("tidy diff blocks", func(t *testing.T) {
		shim := writeReleaseGoShim(t, "#!/bin/sh\nprintf 'module diff\\n'\nexit 1\n")
		t.Setenv("PATH", filepath.Dir(shim))
		item, blockers := releaseGoModTidyCheck()
		if item.Status != "fail" || item.Detail != "module diff" || len(blockers) != 1 {
			t.Fatalf("item=%+v blockers=%v", item, blockers)
		}
	})
}

func TestReleaseReportBoundaries(t *testing.T) {
	t.Run("changelog matches", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "changelog.txt")
		if err := os.WriteFile(path, []byte("## 1.2.3\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if version, err := ParseChangelogVersion(path); err != nil || version != "1.2.3" {
			t.Fatalf("version=%s err=%v", version, err)
		}
		item, blockers := releaseChangelogVersionCheck(path, "1.2.3")
		if item.Status != "pass" || len(blockers) != 0 || item.Detail != `version "1.2.3"` {
			t.Fatalf("item=%+v blockers=%v", item, blockers)
		}
	})
	t.Run("json output error", func(t *testing.T) {
		want := errors.New("writer failed")
		if err := printReleaseCheckJSON(Hooks{PrintJSON: func(any) error { return want }}, releaseCheckReport{}, false); !errors.Is(err, want) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("default hooks", func(t *testing.T) {
		h := normalizeHooks(Hooks{})
		if err := h.PrintJSON(make(chan int)); err == nil {
			t.Fatal("unsupported JSON accepted")
		}
		if err := h.PrintJSON(map[string]string{"status": "pass"}); err != nil {
			t.Fatal(err)
		}
		h.PrintText("")
		h.PrintTextf("%s", "")
		h.PrintTextln()
		if err := Command(nil, Hooks{PrintHelp: func(string, []string) bool { return true }}); err != nil {
			t.Fatal(err)
		}
		if err := Command([]string{"--unknown"}, Hooks{}); err == nil {
			t.Fatal("unknown flag accepted")
		}
		if err := CheckCommand([]string{"--evidence", "unknown", "--json"}, Hooks{}); err == nil {
			t.Fatal("unknown evidence accepted")
		}
	})
	t.Run("semver and argv", func(t *testing.T) {
		if got := RecommendSemver([]string{"incompatible RPC"}, nil); got != "major" {
			t.Fatalf("semver=%s", got)
		}
		cmd := APIDiffCommand("", "-m", "example.com/a;not-a-shell")
		if !reflect.DeepEqual(cmd.Args, []string{"go", "tool", "apidiff", "-m", "example.com/a;not-a-shell"}) {
			t.Fatalf("argv=%v", cmd.Args)
		}
	})
}

func TestReleaseAggregationLocations(t *testing.T) {
	for _, tc := range []struct{ name, source, target, want string }{
		{"both", " x ", " y ", "x -> y"}, {"source", "x", "", "x"}, {"target", "", "y", "y"}, {"empty", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gatewayAggregationMappingID(tc.source, tc.target); got != tc.want {
				t.Fatalf("mapping=%q want=%q", got, tc.want)
			}
		})
	}
	if got := gatewayAggregationChangeStep(gateway.TranscodeProfileChange{Scope: "aggregation_step", Source: " step "}); got != "step" {
		t.Fatalf("step=%s", got)
	}
	for _, scope := range []string{"aggregation_request_header/a", "aggregation_request_query/a", "aggregation_request_body/a", "aggregation_request_required/a", "aggregation_request_body_template/a"} {
		if got := aggregationStepFromScope(scope); got != "a" {
			t.Fatalf("%s=%s", scope, got)
		}
	}
	if got := gatewayAggregationCleanPrefix("api/"); got != "/api" {
		t.Fatalf("prefix=%s", got)
	}
	if got := gatewayAggregationCleanPrefix(""); got != "/" {
		t.Fatalf("empty prefix=%s", got)
	}
	if _, err := gatewayAggregationFromRoutes([]gateway.RouteConfig{{Name: "plain"}}, "plain"); err == nil {
		t.Fatal("nonaggregation route accepted")
	}
	if got := gatewayOpenAPIAggregationSARIFContext(rest.OpenAPIDocument{}, "missing"); got.Route != "missing" {
		t.Fatalf("context=%+v", got)
	}
}

func TestReleaseUnchangedContracts(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		check         func(string, string) (releaseCheckItem, []string, []string)
	}{
		{"api", "type Ping {\n Message string\n}\nservice ping {\n @handler ping\n get /ping returns (Ping)\n}", releaseAPIBreakingCheck},
		{"proto", `syntax = "proto3"; package demo; message Ping {} service Greeter { rpc Call(Ping) returns (Ping); }`, releaseRPCBreakingCheck},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "contract."+tc.name)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			item, blockers, warnings := tc.check(path, path)
			if item.Status != "pass" || item.Detail != "no changes" || item.Blocker || len(blockers) != 0 || len(warnings) != 0 {
				t.Fatalf("unchanged contract: item=%+v blockers=%v warnings=%v", item, blockers, warnings)
			}
		})
	}
	report := filterReleaseCheckEvidence(releaseCheckReport{Checks: []releaseCheckItem{
		{Name: "other", Status: "fail", Blocker: true}, {Name: "selected", Status: "pass"},
	}}, " selected ")
	if len(report.Checks) != 1 || report.Checks[0].Name != "selected" || len(report.Blocking) != 0 {
		t.Fatalf("filtered report=%+v", report)
	}
}

func TestReleaseSmokeFailureWithoutOutput(t *testing.T) {
	for _, tc := range []struct{ name, script, blocker, outputKey string }{
		{"runtime", "#!/bin/sh\nexit 23\n", "generated RPC mux retry runtime proof failed", "runtimeOutput"},
		{"generated project", "#!/bin/sh\nif [ \"$1\" = \"test\" ]; then exit 0; fi\nexit 23\n", "generated RPC mux admin smoke proof failed", "generatedProjectOutput"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GO", writeReleaseGoShim(t, tc.script))
			item, blockers := releaseGeneratedRPCMuxRetrySmokeCheck()
			if item.Status != "fail" || !item.Blocker || item.Detail != "exit status 23" || !reflect.DeepEqual(blockers, []string{tc.blocker}) {
				t.Fatalf("item=%+v blockers=%v", item, blockers)
			}
			evidence, ok := item.Evidence["generated-rpc-mux-retry-smoke"].(map[string]any)
			if !ok || evidence[tc.outputKey] != "" {
				t.Fatalf("evidence=%#v", item.Evidence)
			}
		})
	}
}

func TestReleaseOpenAPIAggregationFailures(t *testing.T) {
	const valid = `{"paths":{"/":{"get":{"operationId":"home"}}}}`
	for _, tc := range []struct{ name, base, candidate, want string }{
		{"missing base", "", "", "read openapi document"},
		{"invalid base JSON", "{", "", "decode openapi document"},
		{"missing candidate", valid, "", "read openapi document"},
		{"invalid candidate JSON", valid, "{", "decode openapi document"},
		{"base without paths", "{}", valid, "import base openapi aggregation routes"},
		{"candidate without paths", valid, "{}", "import candidate openapi aggregation routes"},
		{"candidate without aggregation", valid, valid, `openapi aggregation route "home" not found`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "etc"), 0o700); err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string]string{"base": tc.base, "candidate": tc.candidate} {
				if content != "" {
					if err := os.WriteFile(filepath.Join(root, "etc", "edge-openapi-"+name+".json"), []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, _, err := releaseGatewayOpenAPIAggregationReport(root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want=%q", err, tc.want)
			}
		})
	}
	location := buildGatewayAggregationChangeLocation(gatewayAggregationSARIFContext{Route: "home"}, gateway.TranscodeProfileChange{
		Scope: "aggregation_request_header/profile", Source: "user", Target: "X-User",
	})
	if location.Route != "home" || location.Step != "profile" || location.Mapping != "user -> X-User" {
		t.Fatalf("location=%+v", location)
	}
}

func testHooks(out *bytes.Buffer) Hooks {
	if out == nil {
		out = &bytes.Buffer{}
	}
	return Hooks{
		PrintHelp: func(string, []string) bool { return false },
		PrintJSON: func(value any) error {
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return err
			}
			out.Write(data)
			out.WriteByte('\n')
			return nil
		},
		PrintText: func(args ...any) {
			for _, arg := range args {
				_, _ = fmt.Fprint(out, arg)
			}
		},
		PrintTextf: func(format string, args ...any) {
			_, _ = fmt.Fprintf(out, format, args...)
		},
		PrintTextln: func(args ...any) {
			_, _ = fmt.Fprintln(out, args...)
		},
		Version:         "0.0.0-test",
		AlreadyReported: errJSONAlreadyReported,
	}
}

var errJSONAlreadyReported = errors.New("json error already reported")
