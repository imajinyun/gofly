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
