package command

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The complete workflow inventory precedes these tests in
// docs/reference/cli-coverage-convergence.md. Commands use real local files;
// no gateway listener or upstream is started by validation.
const cliCoverageProfile = `{"descriptor":"orders.OrderService","descriptorMethod":"GetOrder","requestMappings":[{"source":"body.id","target":"order.id"}]}`

const cliCoverageAggregation = `{"enabled":true,"steps":[{"name":"orders","path":"/orders","fallback":[]}],"shape":{"mappings":[{"source":"body.data.orders","target":"orders"}]}}`

func cliCoverageFile(t *testing.T, root, name, contents string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func cliCoverageRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, stderr bytes.Buffer
	err := ExecuteWithIO(args, IOStreams{Out: &out, Err: &stderr})
	evidence := struct {
		Args   []string `json:"args"`
		Stdout string   `json:"stdout"`
		Stderr string   `json:"stderr"`
		Error  string   `json:"error,omitempty"`
	}{Args: args, Stdout: out.String(), Stderr: stderr.String()}
	if err != nil {
		evidence.Error = err.Error()
	}
	data, marshalErr := json.MarshalIndent(evidence, "", "  ")
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	file, createErr := os.CreateTemp(t.ArtifactDir(), "command-*.json")
	if createErr != nil {
		t.Fatal(createErr)
	}
	if _, writeErr := file.Write(data); writeErr != nil {
		_ = file.Close()
		t.Fatal(writeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err == nil && stderr.Len() != 0 {
		t.Fatalf("successful command %q wrote stderr: %s", args, stderr.String())
	}
	return out.String(), err
}

func cliCoverageJSONError(t *testing.T, want string, args ...string) {
	t.Helper()
	out, err := cliCoverageRun(t, append([]string{"--output=json"}, args...)...)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("command %q error = %v, want %q; output=%s", args, err, want, out)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("command %q must emit one JSON envelope: %v; output=%s", args, err, out)
	}
	if envelope.OK || envelope.Error == nil || !strings.Contains(envelope.Error.Message, want) {
		t.Fatalf("command %q error envelope = %s, want %q", args, out, want)
	}
}

func cliCoverageAggregationConfig(aggregation string) string {
	return `{"gateway":{"routes":[{"name":"home","method":"GET","pathPrefix":"/home","targets":["http://127.0.0.1:1"],"aggregation":` + aggregation + `}]}}`
}

func cliCoverageOpenAPI(aggregation string) string {
	return `{"openapi":"3.0.3","info":{"title":"edge","version":"1"},"paths":{"/home":{"get":{"operationId":"home","parameters":[{"name":"tenant","in":"query","schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}},"x-gofly-aggregation":` + aggregation + `}}}}`
}

func TestCLICoverageGatewayInputFailures(t *testing.T) {
	dir := t.TempDir()
	profile := cliCoverageFile(t, dir, "profile.json", cliCoverageProfile)
	profiles := cliCoverageFile(t, dir, "profiles.json", `{"transcodeProfiles":[`+cliCoverageProfile+`]}`)
	aggregation := cliCoverageFile(t, dir, "aggregation.json", cliCoverageAggregation)
	config := cliCoverageFile(t, dir, "gateway.json", cliCoverageAggregationConfig(cliCoverageAggregation))
	openapi := cliCoverageFile(t, dir, "openapi.json", cliCoverageOpenAPI(cliCoverageAggregation))
	malformed := cliCoverageFile(t, dir, "malformed.json", `{`)
	invalidProfile := cliCoverageFile(t, dir, "invalid-profile.json", `{"transcodeProfiles":[{}]}`)
	invalidRoutes := cliCoverageFile(t, dir, "invalid-routes.json", `{"gateway":{"routes":[{"pathPrefix":"/bad"}]}}`)
	directRoutes := cliCoverageFile(t, dir, "bad-direct-routes.json", `{"routes":"invalid"}`)
	directCandidate := cliCoverageFile(t, dir, "bad-direct-candidate.json", `{"steps":"invalid"}`)
	missing := filepath.Join(dir, "missing.json")
	invalidOpenAPI := cliCoverageFile(t, dir, "invalid-openapi.json", cliCoverageOpenAPI(`{"steps":[{"name":"orders","path":"/orders","request":{"queryMappings":[{"source":"query.missing","target":"id"}]}}]}`))
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"profile flag", []string{"profile", "validate", "--unknown"}, "flag provided but not defined"},
		{"profile config missing", []string{"profile", "validate", missing, profile}, "open gateway config file"},
		{"profile candidate missing", []string{"profile", "validate", profiles, missing}, "open candidate profile file"},
		{"profile malformed config", []string{"profile", "validate", malformed, profile}, "decode gateway config"},
		{"profile malformed candidate", []string{"profile", "validate", profiles, malformed}, "decode candidate profile"},
		{"profile invalid current", []string{"profile", "validate", invalidProfile, profile}, "load current transcode profiles"},
		{"profile invalid format", []string{"profile", "validate", profiles, profile, "--format=xml"}, "unsupported --format"},
		{"aggregation flag", []string{"aggregation", "validate", "--unknown"}, "flag provided but not defined"},
		{"aggregation config missing", []string{"aggregation", "validate", missing, aggregation}, "open gateway config file"},
		{"aggregation candidate missing", []string{"aggregation", "validate", config, missing}, "open candidate aggregation file"},
		{"aggregation malformed config", []string{"aggregation", "validate", malformed, aggregation}, "decode gateway config"},
		{"aggregation direct routes type", []string{"aggregation", "validate", directRoutes, aggregation}, "decode gateway routes"},
		{"aggregation direct candidate type", []string{"aggregation", "validate", config, directCandidate}, "decode candidate aggregation"},
		{"aggregation malformed candidate", []string{"aggregation", "validate", config, malformed}, "decode candidate aggregation"},
		{"aggregation invalid current", []string{"aggregation", "validate", invalidRoutes, aggregation}, "load current gateway aggregation config"},
		{"aggregation invalid format", []string{"aggregation", "validate", config, aggregation, "--format=xml"}, "unsupported --format"},
		{"openapi invalid format", []string{"aggregation", "validate", "--openapi-base", openapi, "--openapi-candidate", openapi, "--format=xml"}, "unsupported --format"},
		{"openapi missing base", []string{"aggregation", "validate", "--openapi-base", missing, "--openapi-candidate", openapi}, "open openapi document file"},
		{"openapi missing candidate", []string{"aggregation", "validate", "--openapi-base", openapi, "--openapi-candidate", missing}, "open openapi document file"},
		{"openapi malformed candidate", []string{"aggregation", "validate", "--openapi-base", openapi, "--openapi-candidate", malformed}, "decode openapi document"},
		{"openapi invalid base import", []string{"aggregation", "validate", "--openapi-base", invalidOpenAPI, "--openapi-candidate", openapi}, "import base openapi aggregation routes"},
		{"openapi invalid candidate import", []string{"aggregation", "validate", "--openapi-base", openapi, "--openapi-candidate", invalidOpenAPI}, "import candidate openapi aggregation routes"},
		{"openapi route absent", []string{"aggregation", "validate", "--openapi-base", openapi, "--openapi-candidate", openapi, "--route=absent"}, `openapi aggregation route "absent" not found`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cliCoverageJSONError(t, tc.want, append([]string{"gateway"}, tc.args...)...)
		})
	}
	if got, err := os.ReadFile(config); err != nil || string(got) != cliCoverageAggregationConfig(cliCoverageAggregation) {
		t.Fatalf("validation changed config: %s; err=%v", got, err)
	}
}

func TestCLICoverageGatewayReports(t *testing.T) {
	tests := []struct {
		name, family, current, candidate, format string
		want                                     []string
	}{
		{"profile compatible", "profile", `{"transcodeProfiles":[` + cliCoverageProfile + `]}`, cliCoverageProfile, "text", []string{"gateway profile validation: compatible"}},
		{"profile breaking", "profile", `{"transcodeProfiles":[` + cliCoverageProfile + `]}`, strings.ReplaceAll(cliCoverageProfile, "order.id", "person.id"), "text", []string{"gateway profile validation: breaking", "change_target", "source=body.id", "target=person.id"}},
		{"profile invalid", "profile", `{"transcodeProfiles":[` + cliCoverageProfile + `]}`, `{}`, "text", []string{"gateway profile validation: invalid", "error:"}},
		{"aggregation compatible text", "aggregation", cliCoverageAggregationConfig(cliCoverageAggregation), cliCoverageAggregation, "text", []string{"gateway aggregation validation: compatible"}},
		{"aggregation breaking text", "aggregation", cliCoverageAggregationConfig(cliCoverageAggregation), strings.ReplaceAll(cliCoverageAggregation, `"target":"orders"`, `"target":"items"`), "text", []string{"gateway aggregation validation: breaking", "change_target", "target=items"}},
		{"aggregation empty markdown", "aggregation", cliCoverageAggregationConfig(cliCoverageAggregation), cliCoverageAggregation, "markdown", []string{"Status: `compatible`", "No aggregation contract changes detected."}},
		{"aggregation invalid markdown", "aggregation", cliCoverageAggregationConfig(cliCoverageAggregation), `{"enabled":true,"steps":[{"name":"orders","path":"/orders","request":{"bodyTemplate":[]}}]}`, "markdown", []string{"Status: `invalid`", "## Errors", "aggregation request body template"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := cliCoverageFile(t, dir, "config.json", tc.current)
			candidate := cliCoverageFile(t, dir, "candidate.json", tc.candidate)
			out, err := cliCoverageRun(t, "gateway", tc.family, "diff", config, candidate, "--format", tc.format)
			if err != nil {
				t.Fatalf("validation: %v; output=%s", err, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("report missing %q:\n%s", want, out)
				}
			}
			if data, err := os.ReadFile(candidate); err != nil || string(data) != tc.candidate {
				t.Fatalf("validation changed candidate: %s; err=%v", data, err)
			}
		})
	}
}

func TestCLICoverageGatewayOpenAPIReports(t *testing.T) {
	dir := t.TempDir()
	baseAggregation := `{"steps":[{"name":"orders","path":"/orders","request":{"queryMappings":[{"source":"query.tenant","target":"tenant"}],"required":["old"],"bodyTemplate":{"old":"value"}}}]}`
	nextAggregation := `{"steps":[{"name":"orders","path":"/orders","request":{"queryMappings":[{"source":"query.tenant","target":"account"}],"required":["new"],"bodyTemplate":{"new":"value"}}}]}`
	base := cliCoverageFile(t, dir, "base.json", cliCoverageOpenAPI(baseAggregation))
	next := cliCoverageFile(t, dir, "candidate.json", cliCoverageOpenAPI(nextAggregation))
	invalid := cliCoverageFile(t, dir, "invalid.json", cliCoverageOpenAPI(strings.ReplaceAll(baseAggregation, "query.tenant", "query.missing")))
	t.Run("breaking text", func(t *testing.T) {
		out, err := cliCoverageRun(t, "gateway", "aggregation", "diff", "--openapi-base", base, "--openapi-candidate", next, "--format=text")
		if err != nil || !strings.Contains(out, "validation: breaking") || !strings.Contains(out, "change_body_template") {
			t.Fatalf("OpenAPI text report: err=%v; output=%s", err, out)
		}
	})
	for _, tc := range []struct {
		name, base, candidate, artifact string
		wantRules                       []string
	}{
		{"breaking SARIF", base, next, next, []string{"aggregation.request_shape.query.change_target", "aggregation.request_shape.required.add_required", "aggregation.request_shape.required.remove_required", "aggregation.request_shape.body_template.change_body_template"}},
		{"base import SARIF", invalid, next, invalid, []string{"aggregation.openapi.invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := cliCoverageRun(t, "gateway", "aggregation", "validate", "--openapi-base", tc.base, "--openapi-candidate", tc.candidate, "--route=home", "--format=sarif")
			if err != nil {
				t.Fatalf("SARIF report: %v; output=%s", err, out)
			}
			var report gatewayAggregationSARIF
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatal(err)
			}
			if report.Version != "2.1.0" || len(report.Runs) != 1 {
				t.Fatalf("invalid SARIF header: %s", out)
			}
			rules := make(map[string]bool)
			for _, result := range report.Runs[0].Results {
				rules[result.RuleID] = true
				if len(result.Locations) != 1 || result.Locations[0].PhysicalLocation.ArtifactLocation.URI != tc.artifact {
					t.Errorf("rule %q location = %+v, want %s", result.RuleID, result.Locations, tc.artifact)
				}
				level := "error"
				if result.RuleID == "aggregation.request_shape.required.add_required" {
					level = "note"
				}
				if result.Level != level {
					t.Errorf("rule %q level = %s, want %s", result.RuleID, result.Level, level)
				}
			}
			for _, rule := range tc.wantRules {
				if !rules[rule] {
					t.Errorf("missing SARIF rule %s; got %v", rule, rules)
				}
			}
		})
	}
}
