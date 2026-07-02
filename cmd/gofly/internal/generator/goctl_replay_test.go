package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type goctlReplayFixture struct {
	Schema            string   `json:"schema"`
	ID                string   `json:"id"`
	Module            string   `json:"module"`
	ServiceName       string   `json:"serviceName"`
	Profile           string   `json:"profile"`
	Style             string   `json:"style"`
	API               string   `json:"api"`
	Config            string   `json:"config"`
	DDL               string   `json:"ddl"`
	Cache             bool     `json:"cache"`
	DiffCategories    []string `json:"diffCategories"`
	ExpectedArtifacts []string `json:"expectedArtifacts"`
	RollbackNote      string   `json:"rollbackNote"`
}

func TestGoctlRealProjectFixtureReplay(t *testing.T) {
	fixtureDir := filepath.Join(repositoryRoot(t), "testdata", "goctl-replay", "orderservice")
	fixture := readGoctlReplayFixture(t, fixtureDir)
	if fixture.Schema != "gofly.goctl_real_project_fixture.v1" {
		t.Fatalf("fixture schema = %q, want gofly.goctl_real_project_fixture.v1", fixture.Schema)
	}
	if fixture.ID != "orderservice-goctl-replay" {
		t.Fatalf("fixture id = %q, want orderservice-goctl-replay", fixture.ID)
	}

	firstDir := filepath.Join(t.TempDir(), "first")
	secondDir := filepath.Join(t.TempDir(), "second")
	firstHashes := replayGoctlFixture(t, fixtureDir, fixture, firstDir)
	secondHashes := replayGoctlFixture(t, fixtureDir, fixture, secondDir)
	if got, want := firstHashes, secondHashes; strings.Join(hashPairs(got), "\n") != strings.Join(hashPairs(want), "\n") {
		t.Fatalf("replay hashes drifted:\nfirst:\n%s\nsecond:\n%s", strings.Join(hashPairs(got), "\n"), strings.Join(hashPairs(want), "\n"))
	}

	report := classifyGoctlReplayDiff(fixture)
	for _, want := range []string{
		"deterministic-repeat-generation",
		"compatible-addition",
		"generated-cache-template",
		"breaking-candidate",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("replay diff report missing %q:\n%s", want, report)
		}
	}
}

func readGoctlReplayFixture(t *testing.T, fixtureDir string) goctlReplayFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, "replay.json"))
	if err != nil {
		t.Fatalf("read replay fixture: %v", err)
	}
	var fixture goctlReplayFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode replay fixture: %v\n%s", err, data)
	}
	return fixture
}

func replayGoctlFixture(t *testing.T, fixtureDir string, fixture goctlReplayFixture, outDir string) map[string]string {
	t.Helper()
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create replay output dir: %v", err)
	}
	writeGeneratedModule(t, outDir, fixture.Module)

	configOut := filepath.Join(outDir, fixture.Config)
	configData, err := os.ReadFile(filepath.Join(fixtureDir, fixture.Config))
	if err != nil {
		t.Fatalf("read fixture config: %v", err)
	}
	if err := writeGeneratedFile(configOut, configData); err != nil {
		t.Fatalf("write replay config: %v", err)
	}

	apiFile := filepath.Join(fixtureDir, fixture.API)
	ddlFile := filepath.Join(fixtureDir, fixture.DDL)
	if err := GenerateServiceScaffold(ServiceScaffoldOptions{
		Name:        fixture.ServiceName,
		Module:      fixture.Module,
		Dir:         outDir,
		Style:       fixture.Style,
		Profile:     fixture.Profile,
		Kind:        "api",
		SkipAPISpec: true,
	}); err != nil {
		t.Fatalf("generate goctl-compatible scaffold: %v", err)
	}
	if err := GenerateRESTFromAPI(APIOptions{
		APIFile: apiFile,
		Dir:     outDir,
		Package: "api",
		Profile: fixture.Profile,
	}); err != nil {
		t.Fatalf("generate replay api: %v", err)
	}
	if err := GenerateModelFromDDL(ModelOptions{
		DDLFile: ddlFile,
		Dir:     outDir,
		Package: "model",
		Module:  fixture.Module,
		Style:   "go_zero",
		Strict:  true,
		Cache:   fixture.Cache,
	}); err != nil {
		t.Fatalf("generate replay model: %v", err)
	}
	appendGoflyReplace(t, filepath.Join(outDir, "go.mod"))

	for _, rel := range fixture.ExpectedArtifacts {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Fatalf("replay expected artifact %s: %v", rel, err)
		}
	}
	assertGoctlReplayArtifacts(t, outDir)
	assertReplayRollbackNote(t, fixtureDir, fixture.RollbackNote)
	runGoCommand(t, outDir, 3*time.Minute, "mod", "tidy")
	runGoCommand(t, outDir, 3*time.Minute, "test", "./...")
	return hashReplayArtifacts(t, outDir, fixture.ExpectedArtifacts)
}

func assertGoctlReplayArtifacts(t *testing.T, outDir string) {
	t.Helper()
	handler := readReplayFile(t, outDir, filepath.Join("internal", "handler", "routes.go"))
	for _, want := range []string{"package handler", "RegisterHandlers", `Path: "/ping"`, `rest.WithPrefix("/api/v1")`} {
		if !strings.Contains(handler, want) {
			t.Fatalf("goctl handler routes missing %q:\n%s", want, handler)
		}
	}
	apiRoutes := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "orders_api", "routes.go"))
	for _, want := range []string{
		"RegisterOrdersApiRoutes",
		"RegisterCreateOrderRoute",
		"RegisterGetOrderRoute",
	} {
		if !strings.Contains(apiRoutes, want) {
			t.Fatalf("generated API routes missing %q:\n%s", want, apiRoutes)
		}
	}
	createRoute := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "orders_api", "create_order.go"))
	for _, want := range []string{`Path: "/orders"`, "ctx.BindRequest(&req)"} {
		if !strings.Contains(createRoute, want) {
			t.Fatalf("generated create order route missing %q:\n%s", want, createRoute)
		}
	}
	getRoute := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "orders_api", "get_order.go"))
	for _, want := range []string{`Path: "/orders/:id"`, "ctx.BindRequest(&req)"} {
		if !strings.Contains(getRoute, want) {
			t.Fatalf("generated get order route missing %q:\n%s", want, getRoute)
		}
	}
	repo := readReplayFile(t, outDir, filepath.Join("model", "repo", "order.go"))
	for _, want := range []string{
		"func NewCachedOrderRepo",
		"func NewConsistentCachedOrderRepo",
		"func NewRedisCachedOrderRepo",
		"func (r *OrderRepo) Transact",
		"func (r *OrderRepo) ListAfter",
		"func (r *OrderRepo) UpdateWithVersion",
		"func (c *RedisCachedOrderRepo) UpdateWithInvalidate",
		`"github.com/imajinyun/gofly/cache"`,
		`"github.com/imajinyun/gofly/core/kv/redis"`,
	} {
		if !strings.Contains(repo, want) {
			t.Fatalf("generated model/cache repo missing %q:\n%s", want, repo)
		}
	}
	goMod := readReplayFile(t, outDir, "go.mod")
	if !strings.Contains(goMod, "replace github.com/imajinyun/gofly =>") {
		t.Fatalf("generated module missing local gofly replace:\n%s", goMod)
	}
}

func assertReplayRollbackNote(t *testing.T, fixtureDir string, rel string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, rel))
	if err != nil {
		t.Fatalf("read rollback note: %v", err)
	}
	for _, want := range []string{"gozero-compatible", "generated cache template", "breaking candidates"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("rollback note missing %q:\n%s", want, data)
		}
	}
}

func readReplayFile(t *testing.T, root string, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read replay file %s: %v", rel, err)
	}
	return string(data)
}

func hashReplayArtifacts(t *testing.T, root string, artifacts []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(artifacts))
	for _, rel := range artifacts {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read artifact %s: %v", rel, err)
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
	}
	return out
}

func hashPairs(hashes map[string]string) []string {
	keys := make([]string, 0, len(hashes))
	for key := range hashes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+hashes[key])
	}
	return out
}

func classifyGoctlReplayDiff(fixture goctlReplayFixture) string {
	categories := append([]string(nil), fixture.DiffCategories...)
	sort.Strings(categories)
	return strings.Join(categories, "\n")
}
