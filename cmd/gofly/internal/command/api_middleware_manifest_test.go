package command

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAPIMiddlewarePresetManifest(t *testing.T) {
	var out bytes.Buffer
	if err := ExecuteWithIO([]string{"ai", "manifest", "--json"}, IOStreams{Out: &out}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Data struct {
			Commands []aiToolCommand `json:"commands"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range result.Data.Commands {
		if cmd.Name != "api middleware" {
			continue
		}
		if !cmd.SupportsDryRun || !cmd.MutatesFilesystem || cmd.RiskLevel != "medium" {
			t.Fatalf("middleware effects=%+v", cmd)
		}
		for _, key := range []string{"preset", "list", "dryRun", "dir", "json", "name", "api"} {
			if _, ok := cmd.InputSchema.Properties[key]; !ok {
				t.Fatalf("missing property %s", key)
			}
		}
		if cmd.OutputContract == nil || len(cmd.SideEffects) == 0 {
			t.Fatal("missing output/side-effect contract")
		}
		return
	}
	t.Fatal("api middleware is absent from AI manifest")
}
