package command

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayProfileValidateCommandJSON(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "gateway.json")
	candidatePath := filepath.Join(dir, "candidate.json")
	if err := os.WriteFile(configPath, []byte(`{
		"transcodeProfiles": [{
			"descriptor": "orders.OrderService",
			"descriptorMethod": "GetOrder",
			"requestMappings": [{"source": "body.id", "target": "order.id"}],
			"responseMappings": [{"source": "body.id", "target": "data.id"}]
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(candidatePath, []byte(`{
		"descriptor": "orders.OrderService",
		"descriptorMethod": "GetOrder",
		"requestMappings": [{"source": "body.id", "target": "order.id"}, {"source": "body.trace", "target": "meta.trace"}],
		"responseMappings": [{"source": "body.id", "target": "data.id"}]
	}`), 0o600); err != nil {
		t.Fatalf("write candidate: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := ExecuteWithIO([]string{"gateway", "profile", "validate", "--config", configPath, "--candidate", candidatePath, "--json"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
		t.Fatalf("gateway profile validate: %v stderr=%s", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			OK         bool `json:"ok"`
			Compatible bool `json:"compatible"`
			Changes    []struct {
				Kind     string `json:"kind"`
				Severity string `json:"severity"`
				Source   string `json:"source"`
			} `json:"changes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, stdout.String())
	}
	if !envelope.OK || envelope.Command != "gateway.profile.validate" || !envelope.Data.OK || !envelope.Data.Compatible {
		t.Fatalf("envelope = %+v, want compatible gateway.profile.validate report", envelope)
	}
	if len(envelope.Data.Changes) != 1 || envelope.Data.Changes[0].Kind != "add_mapping" || envelope.Data.Changes[0].Severity != "info" || envelope.Data.Changes[0].Source != "body.trace" {
		t.Fatalf("changes = %+v, want additive body.trace mapping", envelope.Data.Changes)
	}
}

func TestGatewayProfileValidateCommandBreakingAndUsage(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "gateway.json")
	candidatePath := filepath.Join(dir, "candidate.json")
	if err := os.WriteFile(configPath, []byte(`{"transcodeProfiles":[{"descriptor":"orders.OrderService","descriptorMethod":"GetOrder","requestMappings":[{"source":"body.id","target":"order.id"}]}]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(candidatePath, []byte(`{"descriptor":"orders.OrderService","descriptorMethod":"GetOrder","requestMappings":[{"source":"body.id","target":"person.id"}]}`), 0o600); err != nil {
		t.Fatalf("write candidate: %v", err)
	}

	var stdout bytes.Buffer
	if err := ExecuteWithIO([]string{"gateway", "profile", "validate", configPath, candidatePath, "--json"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatalf("gateway profile validate positional: %v", err)
	}
	var envelope struct {
		Data struct {
			Compatible bool `json:"compatible"`
			Changes    []struct {
				Severity string `json:"severity"`
			} `json:"changes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode breaking envelope: %v\n%s", err, stdout.String())
	}
	if envelope.Data.Compatible || len(envelope.Data.Changes) != 1 || envelope.Data.Changes[0].Severity != "breaking" {
		t.Fatalf("breaking envelope = %+v, want one breaking change", envelope)
	}

	err := ExecuteWithIO([]string{"gateway", "profile", "validate", "--config", configPath}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "--config and --candidate are required") {
		t.Fatalf("missing candidate error = %v, want usage error", err)
	}
}

func TestGatewayAggregationValidateCommandJSON(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "gateway.json")
	candidatePath := filepath.Join(dir, "candidate.json")
	if err := os.WriteFile(configPath, []byte(`{
		"gateway": {"routes": [{
			"name": "home-bff",
			"method": "GET",
			"pathPrefix": "/bff",
			"targets": ["http://127.0.0.1:1"],
			"aggregation": {
				"enabled": true,
				"shape": {"mappings": [
					{"source": "body.data.profile", "target": "profile"},
					{"source": "body.data.orders", "target": "orders"}
				]},
				"steps": [
					{"name": "profile", "path": "/profile", "fallback": {"id": "anonymous"}},
					{"name": "orders", "path": "/orders", "request": {"headerMappings": [{"source": "header.x-tenant", "target": "X-Tenant"}]}, "fallback": []}
				]
			}
		}]}
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(candidatePath, []byte(`{
		"enabled": true,
		"shape": {"mappings": [
			{"source": "body.data.profile", "target": "profile"},
			{"source": "body.data.orders", "target": "items"},
			{"source": "body.degraded", "target": "meta.degraded"}
		]},
		"steps": [
			{"name": "profile", "path": "/profile", "fallback": {"id": "anonymous"}},
			{"name": "orders", "path": "/orders", "request": {"headerMappings": [{"source": "header.x-tenant", "target": "X-Account"}]}},
			{"name": "recommendations", "path": "/recommendations", "fallback": []}
		]
	}`), 0o600); err != nil {
		t.Fatalf("write candidate: %v", err)
	}

	var stdout bytes.Buffer
	if err := ExecuteWithIO([]string{"gateway", "aggregation", "validate", "--config", configPath, "--route", "home-bff", "--candidate", candidatePath, "--json"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatalf("gateway aggregation validate: %v", err)
	}
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Compatible bool `json:"compatible"`
			Changes    []struct {
				Kind     string `json:"kind"`
				Severity string `json:"severity"`
				Scope    string `json:"scope"`
				Source   string `json:"source"`
			} `json:"changes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, stdout.String())
	}
	if !envelope.OK || envelope.Command != "gateway.aggregation.validate" || envelope.Data.Compatible {
		t.Fatalf("envelope = %+v, want breaking aggregation validation", envelope)
	}
	var sawRemoveFallback, sawChangeTarget, sawAddStep, sawRequestTarget bool
	for _, change := range envelope.Data.Changes {
		switch change.Kind {
		case "remove_fallback":
			sawRemoveFallback = change.Severity == "breaking" && change.Source == "orders"
		case "change_target":
			sawChangeTarget = change.Severity == "breaking" && change.Scope == "aggregation_shape"
			if change.Severity == "breaking" && change.Scope == "aggregation_request_header/orders" {
				sawRequestTarget = true
			}
		case "add_step":
			sawAddStep = change.Severity == "info" && change.Source == "recommendations"
		}
	}
	if !sawRemoveFallback || !sawChangeTarget || !sawRequestTarget || !sawAddStep {
		t.Fatalf("changes = %+v, want fallback removal, shape target change, request target change, and additive step", envelope.Data.Changes)
	}

	stdout.Reset()
	if err := ExecuteWithIO([]string{"gateway", "aggregation", "validate", "--config", configPath, "--route", "home-bff", "--candidate", candidatePath, "--format", "markdown"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatalf("gateway aggregation validate markdown: %v", err)
	}
	if !strings.Contains(stdout.String(), "# Gateway Aggregation Contract") ||
		!strings.Contains(stdout.String(), "| Severity | Scope | Kind | Source | Target | Message |") ||
		!strings.Contains(stdout.String(), "remove_fallback") ||
		!strings.Contains(stdout.String(), "change_target") {
		t.Fatalf("markdown output = %s", stdout.String())
	}

	stdout.Reset()
	if err := ExecuteWithIO([]string{"gateway", "aggregation", "validate", "--config", configPath, "--route", "home-bff", "--candidate", candidatePath, "--format", "sarif"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatalf("gateway aggregation validate sarif: %v", err)
	}
	var sarif struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []struct {
				RuleID     string            `json:"ruleId"`
				Level      string            `json:"level"`
				Properties map[string]string `json:"properties"`
				Locations  []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sarif); err != nil {
		t.Fatalf("decode sarif: %v\n%s", err, stdout.String())
	}
	if sarif.Version != "2.1.0" || len(sarif.Runs) != 1 {
		t.Fatalf("sarif header = %+v", sarif)
	}
	var sawFallbackError, sawResponseTargetError, sawRequestTargetError bool
	for _, result := range sarif.Runs[0].Results {
		if result.RuleID == "aggregation.step.remove_fallback" && result.Level == "error" {
			sawFallbackError = true
		}
		if result.RuleID == "aggregation.response_shape.change_target" && result.Level == "error" {
			sawResponseTargetError = true
		}
		if result.RuleID == "aggregation.request_shape.header.change_target" && result.Level == "error" {
			sawRequestTargetError = true
		}
	}
	if !sawFallbackError || !sawResponseTargetError || !sawRequestTargetError {
		t.Fatalf("sarif results = %+v, want breaking fallback, response target and request target errors", sarif.Runs[0].Results)
	}
	if len(sarif.Runs[0].Results[0].Locations) == 0 || sarif.Runs[0].Results[0].Locations[0].PhysicalLocation.ArtifactLocation.URI != candidatePath {
		t.Fatalf("sarif location = %+v, want candidate path %s", sarif.Runs[0].Results[0].Locations, candidatePath)
	}
	if sarif.Runs[0].Results[0].Locations[0].PhysicalLocation.Region.StartLine == 0 {
		t.Fatalf("sarif region = %+v, want startLine", sarif.Runs[0].Results[0].Locations[0].PhysicalLocation.Region)
	}
}

func TestGatewayAggregationValidateCommandOpenAPIDiff(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base-openapi.json")
	candidatePath := filepath.Join(dir, "candidate-openapi.json")
	base := `{
	  "openapi": "3.0.3",
	  "info": {"title": "edge", "version": "1.0.0"},
	  "paths": {"/home": {"get": {
	    "operationId": "home",
	    "responses": {"200": {"description": "ok"}},
	    "x-gofly-aggregation": {
	      "shape": {"mappings": [
	        {"source": "body.data.profile", "target": "profile"},
	        {"source": "body.data.orders", "target": "orders"}
	      ]},
	      "steps": [
	        {"name": "profile", "path": "/profile", "fallback": {"id": "anonymous"}},
	        {"name": "orders", "path": "/orders", "fallback": []}
	      ]
	    }
	  }}}
	}`
	candidate := `{
	  "openapi": "3.0.3",
	  "info": {"title": "edge", "version": "1.0.0"},
	  "paths": {"/home": {"get": {
	    "operationId": "home",
	    "responses": {"200": {"description": "ok"}},
	    "x-gofly-aggregation": {
	      "shape": {"mappings": [
	        {"source": "body.data.profile", "target": "profile"},
	        {"source": "body.data.orders", "target": "items"}
	      ]},
	      "steps": [
	        {"name": "profile", "path": "/profile", "fallback": {"id": "anonymous"}},
	        {"name": "orders", "path": "/orders"}
	      ]
	    }
	  }}}
	}`
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatalf("write base openapi: %v", err)
	}
	if err := os.WriteFile(candidatePath, []byte(candidate), 0o600); err != nil {
		t.Fatalf("write candidate openapi: %v", err)
	}

	var stdout bytes.Buffer
	if err := ExecuteWithIO([]string{"gateway", "aggregation", "validate", "--openapi-base", basePath, "--openapi-candidate", candidatePath, "--route", "home", "--json"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatalf("gateway aggregation openapi validate: %v", err)
	}
	var envelope struct {
		Command string `json:"command"`
		Data    struct {
			Compatible bool `json:"compatible"`
			Changes    []struct {
				Kind string `json:"kind"`
			} `json:"changes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode openapi aggregation envelope: %v\n%s", err, stdout.String())
	}
	if envelope.Command != "gateway.aggregation.validate" || envelope.Data.Compatible {
		t.Fatalf("openapi aggregation envelope = %+v, want breaking validation", envelope)
	}
	var sawRemoveFallback, sawChangeTarget bool
	for _, change := range envelope.Data.Changes {
		if change.Kind == "remove_fallback" {
			sawRemoveFallback = true
		}
		if change.Kind == "change_target" {
			sawChangeTarget = true
		}
	}
	if !sawRemoveFallback || !sawChangeTarget {
		t.Fatalf("openapi aggregation changes = %+v, want remove_fallback and change_target", envelope.Data.Changes)
	}

	invalidPath := filepath.Join(dir, "invalid-openapi.json")
	invalid := strings.Replace(candidate, `"name": "orders", "path": "/orders"`, `"name": "orders", "path": "/orders", "request": {"queryMappings": [{"source": "query.missing", "target": "missing"}]}`, 1)
	if err := os.WriteFile(invalidPath, []byte(invalid), 0o600); err != nil {
		t.Fatalf("write invalid openapi: %v", err)
	}
	stdout.Reset()
	if err := ExecuteWithIO([]string{"gateway", "aggregation", "validate", "--openapi-base", basePath, "--openapi-candidate", invalidPath, "--route", "home", "--format", "sarif"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatalf("gateway aggregation invalid openapi sarif: %v", err)
	}
	var invalidSARIF struct {
		Runs []struct {
			Results []struct {
				RuleID  string `json:"ruleId"`
				Level   string `json:"level"`
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
				Properties map[string]string `json:"properties"`
				Locations  []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &invalidSARIF); err != nil {
		t.Fatalf("decode invalid sarif: %v\n%s", err, stdout.String())
	}
	if len(invalidSARIF.Runs) != 1 || len(invalidSARIF.Runs[0].Results) != 1 ||
		invalidSARIF.Runs[0].Results[0].RuleID != "aggregation.openapi.invalid" ||
		invalidSARIF.Runs[0].Results[0].Level != "error" ||
		!strings.Contains(invalidSARIF.Runs[0].Results[0].Message.Text, "unknown OpenAPI query parameter") ||
		invalidSARIF.Runs[0].Results[0].Properties["openapiPath"] != "/home" ||
		invalidSARIF.Runs[0].Results[0].Properties["openapiMethod"] != "GET" ||
		invalidSARIF.Runs[0].Results[0].Properties["errorMarker"] == "" ||
		len(invalidSARIF.Runs[0].Results[0].Locations) == 0 ||
		invalidSARIF.Runs[0].Results[0].Locations[0].PhysicalLocation.ArtifactLocation.URI != invalidPath ||
		invalidSARIF.Runs[0].Results[0].Locations[0].PhysicalLocation.Region.StartLine == 0 {
		t.Fatalf("invalid sarif = %+v, want aggregation.openapi.invalid error", invalidSARIF)
	}
}
