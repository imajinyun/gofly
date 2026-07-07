package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	// Capture output by redirecting stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := releaseCheckCommand([]string{
		"--api-base", baseAPI,
		"--api-target", targetAPI,
		"--changelog", filepath.Join(dir, "no-changelog"),
	})

	w.Close()
	os.Stdout = old

	if err == nil {
		t.Fatal("expected release check to fail with breaking API changes")
	}

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
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
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--changelog", changelog, "--json"})
	}); err != nil {
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
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--changelog", changelog, "--json", "--evidence", "gateway-aggregation-contract"})
	}); err != nil {
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
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--changelog", changelog, "--json", "--evidence", "rpc-mux-adapter-evidence"})
	}); err != nil {
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
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--changelog", changelog, "--json", "--evidence", "generated-rpc-mux-retry-smoke"})
	}); err != nil {
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
	err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--changelog", changelog, "--json"})
	})
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
		evidence["openBeforeRetry"] != true ||
		evidence["postOpenNoReplay"] != true ||
		evidence["cooldownBackoff"] != true ||
		evidence["verifyCommand"] != "go test ./..." {
		t.Fatalf("generated rpc mux retry smoke evidence payload = %#v", evidence)
	}
}

func TestReleaseCheckGlobalJSONDoesNotDuplicateError(t *testing.T) {
	t.Setenv("API_BASE_REF", "definitely-missing-release-base-ref")
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n## v9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := ExecuteWithIO([]string{"--output=json", "release", "check", "--changelog", changelog}, IOStreams{Out: &out})
	if err == nil || !strings.Contains(err.Error(), "release check failed") || !errors.Is(err, errJSONAlreadyReported) {
		t.Fatalf("ExecuteWithIO release check error = %v, want reported release check failure", err)
	}
	var envelope struct {
		OK      bool               `json:"ok"`
		Command string             `json:"command"`
		Data    releaseCheckReport `json:"data"`
		Error   *jsonError         `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("ExecuteWithIO release check json decode: %v\n%s", err, out.String())
	}
	if envelope.OK || envelope.Command != "release.check" || envelope.Error == nil || envelope.Error.Code != "RELEASE_CHECK_FAILED" {
		t.Fatalf("ExecuteWithIO release check envelope = %+v, want one structured release failure", envelope)
	}
	if strings.Count(out.String(), `"command"`) != 1 {
		t.Fatalf("ExecuteWithIO release check emitted duplicate JSON envelopes:\n%s", out.String())
	}
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
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--api-base", baseAPI, "--api-target", targetAPI, "--rpc-base", baseProto, "--rpc-target", targetProto, "--changelog", changelog})
	}); err != nil {
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
	err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--rpc-base", baseProto, "--rpc-target", removedProto, "--changelog", changelog})
	})
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
	err = withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--rpc-base", baseProto, "--rpc-target", badProto, "--changelog", changelog})
	})
	if err == nil || !strings.Contains(err.Error(), "release check failed") {
		t.Fatalf("releaseCheckCommand bad rpc error = %v, want release check failed", err)
	}
	if !strings.Contains(out.String(), "rpc breaking check error") && !strings.Contains(out.String(), "rpc-breaking") {
		t.Fatalf("bad rpc release output = %s, want rpc error branch", out.String())
	}
}

func TestGoReleaserUsesCurrentScriptPath(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", ".goreleaser.yml"))
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
