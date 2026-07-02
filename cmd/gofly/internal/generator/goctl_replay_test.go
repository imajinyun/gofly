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
	Capabilities      []string `json:"capabilities"`
	DiffCategories    []string `json:"diffCategories"`
	ExpectedArtifacts []string `json:"expectedArtifacts"`
	RollbackNote      string   `json:"rollbackNote"`
}

func TestGoctlRealProjectFixtureReplay(t *testing.T) {
	fixtureRoot := filepath.Join(repositoryRoot(t), "testdata", "goctl-replay")
	for _, fixtureDir := range goctlReplayFixtureDirs(t, fixtureRoot) {
		fixture := readGoctlReplayFixture(t, fixtureDir)
		t.Run(fixture.ID, func(t *testing.T) {
			if fixture.Schema != "gofly.goctl_real_project_fixture.v1" {
				t.Fatalf("fixture schema = %q, want gofly.goctl_real_project_fixture.v1", fixture.Schema)
			}
			if fixture.ID == "" {
				t.Fatalf("fixture id is required for %s", fixtureDir)
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
		})
	}
}

func goctlReplayFixtureDirs(t *testing.T, fixtureRoot string) []string {
	t.Helper()
	entries, err := os.ReadDir(fixtureRoot)
	if err != nil {
		t.Fatalf("read goctl replay fixtures: %v", err)
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(fixtureRoot, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "replay.json")); err != nil {
			continue
		}
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	if len(dirs) < 2 {
		t.Fatalf("goctl replay fixture matrix needs at least 2 fixtures, got %d", len(dirs))
	}
	return dirs
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
	assertGoctlReplayArtifacts(t, outDir, fixture)
	assertReplayRollbackNote(t, fixtureDir, fixture.RollbackNote)
	runGoCommand(t, outDir, 3*time.Minute, "mod", "tidy")
	runGoCommand(t, outDir, 3*time.Minute, "test", "./...")
	return hashReplayArtifacts(t, outDir, fixture.ExpectedArtifacts)
}

func assertGoctlReplayArtifacts(t *testing.T, outDir string, fixture goctlReplayFixture) {
	t.Helper()
	handler := readReplayFile(t, outDir, filepath.Join("internal", "handler", "routes.go"))
	for _, want := range []string{"package handler", "RegisterHandlers", `Path: "/ping"`, `rest.WithPrefix("/api/v1")`} {
		if !strings.Contains(handler, want) {
			t.Fatalf("goctl handler routes missing %q:\n%s", want, handler)
		}
	}
	goMod := readReplayFile(t, outDir, "go.mod")
	if !strings.Contains(goMod, "replace github.com/imajinyun/gofly =>") {
		t.Fatalf("generated module missing local gofly replace:\n%s", goMod)
	}
	switch fixture.ID {
	case "orderservice-goctl-replay":
		assertOrdersGoctlReplayArtifacts(t, outDir)
	case "inventoryservice-imported-multigroup-replay":
		assertInventoryGoctlReplayArtifacts(t, outDir, fixture)
	default:
		t.Fatalf("missing replay artifact assertions for fixture %q", fixture.ID)
	}
}

func assertOrdersGoctlReplayArtifacts(t *testing.T, outDir string) {
	t.Helper()
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
		"func NewOrderRepoWithCluster",
		"cluster *storage.Cluster",
		"func (r *OrderRepo) Transact",
		"return r.cluster.Transact(ctx, opts",
		"store := r.cluster.Writer()",
		"store := r.cluster.ForQuery(query)",
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
}

func assertInventoryGoctlReplayArtifacts(t *testing.T, outDir string, fixture goctlReplayFixture) {
	t.Helper()
	requireGoctlReplayCapabilities(t, fixture, []string{
		"api-import",
		"multi-service-group",
		"multi-middleware",
		"complex-model",
		"soft-delete",
		"optimistic-lock",
		"composite-unique-key",
		"cache-template",
	})
	typesData := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "types.go"))
	for _, want := range []string{
		"type AuditMeta struct",
		"type PageReq struct",
		"type PageResp struct",
		"type CreateInventoryReq struct",
		"Meta",
		"AuditMeta",
		"Page",
		"PageResp",
		"Items []GetInventoryResp",
	} {
		if !strings.Contains(typesData, want) {
			t.Fatalf("generated imported/matrix types missing %q:\n%s", want, typesData)
		}
	}
	inventoryRoutes := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "inventory_api", "routes.go"))
	for _, want := range []string{
		"RegisterInventoryApiRoutes",
		"RegisterCreateInventoryRoute",
		"RegisterGetInventoryRoute",
		"RegisterListInventoryRoute",
	} {
		if !strings.Contains(inventoryRoutes, want) {
			t.Fatalf("generated inventory routes missing %q:\n%s", want, inventoryRoutes)
		}
	}
	adminRoutes := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "admin_api", "routes.go"))
	for _, want := range []string{
		"RegisterAdminApiRoutes",
		"RegisterAdjustInventoryRoute",
	} {
		if !strings.Contains(adminRoutes, want) {
			t.Fatalf("generated admin routes missing %q:\n%s", want, adminRoutes)
		}
	}
	adjustRoute := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "admin_api", "adjust_inventory.go"))
	for _, want := range []string{`Path: "/inventory/:id/adjust"`, "ctx.BindRequest(&req)"} {
		if !strings.Contains(adjustRoute, want) {
			t.Fatalf("generated adjust inventory route missing %q:\n%s", want, adjustRoute)
		}
	}
	repo := readReplayFile(t, outDir, filepath.Join("model", "repo", "inventory_item.go"))
	for _, want := range []string{
		"func NewCachedInventoryItemRepo",
		"func NewConsistentCachedInventoryItemRepo",
		"func NewRedisCachedInventoryItemRepo",
		"func NewInventoryItemRepoWithCluster",
		"cluster *storage.Cluster",
		"func (r *InventoryItemRepo) Transact",
		"func (r *InventoryItemRepo) UpdateWithVersion",
		"func (r *InventoryItemRepo) ListAfter",
		`AND deleted_at IS NULL LIMIT 1`,
		`query += " AND deleted_at IS NULL"`,
		"return r.cluster.Transact(ctx, opts",
		"store := r.cluster.Writer()",
		"store := r.cluster.ForQuery(query)",
		"func (c *RedisCachedInventoryItemRepo) UpdateWithInvalidate",
	} {
		if !strings.Contains(repo, want) {
			t.Fatalf("generated inventory model/cache repo missing %q:\n%s", want, repo)
		}
	}
	for _, unexpected := range []string{
		"func (r *InventoryItemRepo) FindByTenantID",
		"func (r *InventoryItemRepo) FindBySku",
		"func (r *InventoryItemRepo) FindByWarehouseID",
	} {
		if strings.Contains(repo, unexpected) {
			t.Fatalf("generated inventory repo should not create single-column finders for composite unique key %q:\n%s", unexpected, repo)
		}
	}
	entity := readReplayFile(t, outDir, filepath.Join("model", "entity", "inventory_item_gen.go"))
	for _, want := range []string{
		"type InventoryItem struct",
		"Version",
		`db:"version"`,
		`const InventoryItemTable = "inventory_items"`,
	} {
		if !strings.Contains(entity, want) {
			t.Fatalf("generated inventory entity missing %q:\n%s", want, entity)
		}
	}
}

func requireGoctlReplayCapabilities(t *testing.T, fixture goctlReplayFixture, required []string) {
	t.Helper()
	capabilities := make(map[string]struct{}, len(fixture.Capabilities))
	for _, capability := range fixture.Capabilities {
		capabilities[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := capabilities[capability]; !ok {
			t.Fatalf("fixture %s missing capability %q", fixture.ID, capability)
		}
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
