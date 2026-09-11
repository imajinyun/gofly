package generator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type goctlCacheKeyOracle struct {
	Schema       string   `json:"schema"`
	GoctlVersion string   `json:"goctlVersion"`
	Primary      string   `json:"primary"`
	Unique       []string `json:"unique"`
}

func TestGoctlCacheKeyOracleFixture(t *testing.T) {
	fixtureDir := filepath.Join(repositoryRoot(t), "testdata", "goctl-model-cache-key-oracle", "users")
	data, err := os.ReadFile(filepath.Join(fixtureDir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle goctlCacheKeyOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.Schema != "gofly.goctl_model_cache_key_oracle.v1" || oracle.GoctlVersion != "v1.10.3" {
		t.Fatalf("oracle metadata = %#v", oracle)
	}
	if oracle.Primary != "cache:users:id:%v" {
		t.Fatalf("primary oracle = %q", oracle.Primary)
	}
	if got := strings.Join(oracle.Unique, "|"); got != "cache:users:email:%v|cache:users:tenantId:email:%v:%v" {
		t.Fatalf("unique oracle = %q", got)
	}

	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/cacheoracle")
	ddlPath := filepath.Join(fixtureDir, "schema.sql")
	if err := GenerateModelFromDDL(ModelOptions{
		DDLFile: ddlPath,
		Dir:     dir,
		Module:  "example.com/cacheoracle",
		Style:   "go_zero",
		Cache:   true,
		Prefix:  "cache",
	}); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "repo", "usermodel_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), `cache.WithModelKeyPrefix[*User, int64](entity.UserCacheKeyPrefix + ":" + "id")`) {
		t.Fatalf("primary cache key differs from goctl oracle:\n%s", generated)
	}
	advancedOutput, err := os.ReadFile(filepath.Join(dir, "repo", "user.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, advanced := range []string{"uniqueKeyByEmail", "uniqueKeyByTenantIDAndEmail"} {
		if !strings.Contains(string(advancedOutput), advanced) {
			t.Fatalf("expected gofly advanced cache helper %q:\n%s", advanced, advancedOutput)
		}
	}
}
