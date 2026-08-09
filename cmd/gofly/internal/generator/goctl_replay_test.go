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
	NativeOracle      bool     `json:"nativeOracle"`
	Capabilities      []string `json:"capabilities"`
	DiffCategories    []string `json:"diffCategories"`
	ExpectedArtifacts []string `json:"expectedArtifacts"`
	RollbackNote      string   `json:"rollbackNote"`
}

func TestGoctlRealProjectFixtureReplay(t *testing.T) {
	fixtureRoot := filepath.Join(repositoryRoot(t), "testdata", "goctl-replay")
	for _, fixtureDir := range goctlReplayFixtureDirs(t, fixtureRoot) {
		fixture := readGoctlReplayFixture(t, fixtureDir)
		if fixture.NativeOracle {
			continue
		}
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
	case "billingservice-transitive-import-replay":
		assertBillingGoctlReplayArtifacts(t, outDir, fixture)
	case "userservice-crud-query-replay":
		assertUserGoctlReplayArtifacts(t, outDir, fixture)
	case "taskservice-admin-delete-replay":
		assertTaskGoctlReplayArtifacts(t, outDir, fixture)
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
		"func (r *OrderRepo) FindByIDs(ctx context.Context, ids []int64) ([]entity.Order, error)",
		"func (r *OrderRepo) ListAfter",
		"func (r *OrderRepo) UpdateWithVersion",
		"func (c *CachedOrderRepo) FindByIDsCached(ctx context.Context, ids []int64) ([]entity.Order, error)",
		"if item, ok := c.cache.Peek(id); ok",
		"items, err := c.repo.FindByIDs(ctx, missing)",
		"c.cache.Set(item.ID, &item)",
		"func (c *CachedOrderRepo) InsertMany(ctx context.Context, items []*entity.Order) error",
		"func (c *CachedOrderRepo) UpdateManyWithInvalidate(ctx context.Context, items []*entity.Order) error",
		"func (c *CachedOrderRepo) DeleteMany(ctx context.Context, ids ...int64) error",
		"if err := c.afterInsertCommit(ctx, item); err != nil",
		"if err := c.afterUpdateCommit(ctx, item, old); err != nil",
		"if err := c.afterDeleteCommit(ctx, id, old); err != nil",
		"func (c *CachedOrderRepo) UpdateFieldsWithInvalidate",
		"func (c *CachedOrderRepo) UpdateWithVersion",
		"func (c *CachedOrderRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *CachedOrderRepo) error) error",
		"return fn(ctx, c.cloneWithRepo(txRepo, &afterCommit))",
		"func (c *CachedOrderRepo) FlushAfterCommit(ctx context.Context) error",
		"func (c *CachedOrderRepo) DiscardAfterCommit()",
		"*c.afterCommit = append(*c.afterCommit, fn)",
		"func (c *CachedOrderRepo) afterUpdateCommit(ctx context.Context, in *entity.Order, old *entity.Order) error",
		"func (c *RedisCachedOrderRepo) UpdateFields",
		"func (c *RedisCachedOrderRepo) FindByIDsCached(ctx context.Context, ids []int64) ([]entity.Order, error)",
		"item, ok, err := c.cache.Peek(ctx, id)",
		"if err := c.cache.Set(ctx, item.ID, &item); err != nil",
		"func (c *RedisCachedOrderRepo) InsertMany(ctx context.Context, items []*entity.Order) error",
		"func (c *RedisCachedOrderRepo) UpdateManyWithInvalidate(ctx context.Context, items []*entity.Order) error",
		"func (c *RedisCachedOrderRepo) DeleteMany(ctx context.Context, ids ...int64) error",
		"func (c *RedisCachedOrderRepo) UpdateWithVersion",
		"func (c *RedisCachedOrderRepo) UpdateWithInvalidate",
		"func (c *RedisCachedOrderRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *RedisCachedOrderRepo) error) error",
		"func (c *RedisCachedOrderRepo) FlushAfterCommit(ctx context.Context) error",
		"func (c *RedisCachedOrderRepo) afterUpdateCommit(ctx context.Context, in *entity.Order) error",
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
		"type PageRequest struct",
		"type PageResponse struct",
		"type CreateInventoryRequest struct",
		"Meta",
		"AuditMeta",
		"Page",
		"PageResponse",
		"Items []GetInventoryResponse",
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
		"func (r *InventoryItemRepo) FindByIDs(ctx context.Context, ids []int64) ([]entity.InventoryItem, error)",
		"func (r *InventoryItemRepo) ListAfter",
		"func (r *InventoryItemRepo) FindByStatus(ctx context.Context, status string, limit int, offset int) ([]entity.InventoryItem, error)",
		"func (r *InventoryItemRepo) CountByStatus(ctx context.Context, status string) (int64, error)",
		"listCacheByStatus",
		"countCacheByStatus",
		"*cache.Cache[[]entity.InventoryItem]",
		"*cache.Cache[int64]",
		"out.listCacheByStatus = cache.New[[]entity.InventoryItem]",
		"out.countCacheByStatus = cache.New[int64]",
		"func (c *CachedInventoryItemRepo) FindByStatusCached(ctx context.Context, status string, limit int, offset int) ([]entity.InventoryItem, error)",
		"func (c *CachedInventoryItemRepo) CountByStatusCached(ctx context.Context, status string) (int64, error)",
		"func (c *CachedInventoryItemRepo) PageByStatusCached(ctx context.Context, status string, limit int, offset int) ([]entity.InventoryItem, int64, error)",
		"key := indexListKeyByStatus(status, limit, offset)",
		"key := indexCountKeyByStatus(status)",
		"func indexListKeyByStatus(status string, limit int, offset int) string",
		"func indexCountKeyByStatus(status string) string",
		`"limit=" + strconv.Itoa(limit)`,
		`"offset=" + strconv.Itoa(offset)`,
		"c.listCacheByStatus.Clear()",
		"c.countCacheByStatus.Clear()",
		"func (r *InventoryItemRepo) FindByTenantIDAndSkuAndWarehouseID(ctx context.Context, tenantID int64, sku string, warehouseID int64) (*entity.InventoryItem, error)",
		"cacheByTenantIDAndSkuAndWarehouseID",
		"*cache.ModelCache[*entity.InventoryItem, string]",
		"func (c *CachedInventoryItemRepo) FindByTenantIDAndSkuAndWarehouseIDCached(ctx context.Context, tenantID int64, sku string, warehouseID int64) (*entity.InventoryItem, error)",
		"func uniqueKeyByTenantIDAndSkuAndWarehouseID(tenantID int64, sku string, warehouseID int64) string",
		"func (c *CachedInventoryItemRepo) UpdateFieldsWithInvalidate(ctx context.Context, id int64, fields map[string]any) error",
		"func (c *CachedInventoryItemRepo) UpdateWithVersion(ctx context.Context, in *entity.InventoryItem, expectedVersion int64) error",
		"func (c *CachedInventoryItemRepo) WithTx(tx *sql.Tx) *CachedInventoryItemRepo",
		"func (c *CachedInventoryItemRepo) FindByIDsCached(ctx context.Context, ids []int64) ([]entity.InventoryItem, error)",
		"func (c *CachedInventoryItemRepo) InsertMany(ctx context.Context, items []*entity.InventoryItem) error",
		"func (c *CachedInventoryItemRepo) UpdateManyWithInvalidate(ctx context.Context, items []*entity.InventoryItem) error",
		"func (c *CachedInventoryItemRepo) DeleteMany(ctx context.Context, ids ...int64) error",
		"if item, ok := c.cache.Peek(id); ok",
		"func (c *CachedInventoryItemRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *CachedInventoryItemRepo) error) error",
		"func (c *CachedInventoryItemRepo) afterUpdateFieldsCommit(ctx context.Context, id int64, old *entity.InventoryItem) error",
		"c.cacheByTenantIDAndSkuAndWarehouseID.Cache().Delete(uniqueKeyByTenantIDAndSkuAndWarehouseID(old.TenantID, old.Sku, old.WarehouseID))",
		"c.cacheByTenantIDAndSkuAndWarehouseID.Cache().Set(uniqueKeyByTenantIDAndSkuAndWarehouseID(in.TenantID, in.Sku, in.WarehouseID), in)",
		"query, args, err := storage.SelectWhere(entity.InventoryItemTable, entity.InventoryItemColumns, where, r.dialect)",
		"where = where.Limit(1)",
		`where = where.IsNull("deleted_at")`,
		"return r.cluster.Transact(ctx, opts",
		"store := r.cluster.Writer()",
		"store := r.cluster.ForQuery(query)",
		"listVersionByStatus",
		"*cache.RedisModelCache[[]entity.InventoryItem, string]",
		"*cache.RedisModelCache[int64, string]",
		"*cache.RedisModelCache[string, string]",
		"func (c *RedisCachedInventoryItemRepo) FindByStatusCached(ctx context.Context, status string, limit int, offset int) ([]entity.InventoryItem, error)",
		"func (c *RedisCachedInventoryItemRepo) CountByStatusCached(ctx context.Context, status string) (int64, error)",
		"func (c *RedisCachedInventoryItemRepo) PageByStatusCached(ctx context.Context, status string, limit int, offset int) ([]entity.InventoryItem, int64, error)",
		"func (c *RedisCachedInventoryItemRepo) FindByIDsCached(ctx context.Context, ids []int64) ([]entity.InventoryItem, error)",
		"item, ok, err := c.cache.Peek(ctx, id)",
		"func (c *RedisCachedInventoryItemRepo) InsertMany(ctx context.Context, items []*entity.InventoryItem) error",
		"func (c *RedisCachedInventoryItemRepo) UpdateManyWithInvalidate(ctx context.Context, items []*entity.InventoryItem) error",
		"func (c *RedisCachedInventoryItemRepo) DeleteMany(ctx context.Context, ids ...int64) error",
		"key := redisInventoryItemIndexListCacheKey(version, indexListKeyByStatus(status, limit, offset))",
		"key := redisInventoryItemIndexListCacheKey(version, indexCountKeyByStatus(status))",
		"c.listVersionByStatus.Set(ctx, \"current\", redisInventoryItemIndexListVersionValue())",
		"func (c *RedisCachedInventoryItemRepo) UpdateWithInvalidate",
		"func (c *RedisCachedInventoryItemRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *RedisCachedInventoryItemRepo) error) error",
		"func (c *RedisCachedInventoryItemRepo) afterUpdateFieldsCommit(ctx context.Context, id int64) error",
	} {
		if !strings.Contains(repo, want) {
			t.Fatalf("generated inventory model/cache repo missing %q:\n%s", want, repo)
		}
	}
	for _, unexpected := range []string{
		"func (r *InventoryItemRepo) FindByTenantID(ctx context.Context",
		"func (r *InventoryItemRepo) FindBySku(ctx context.Context",
		"func (r *InventoryItemRepo) FindByWarehouseID(ctx context.Context",
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

func assertBillingGoctlReplayArtifacts(t *testing.T, outDir string, fixture goctlReplayFixture) {
	t.Helper()
	requireGoctlReplayCapabilities(t, fixture, []string{
		"api-import",
		"transitive-api-import",
		"multi-service-group",
		"multi-middleware",
		"api-comments-tags",
		"nested-type-composition",
		"request-response-naming",
		"complex-model",
		"soft-delete",
		"optimistic-lock",
		"single-column-unique-key",
		"composite-unique-key",
		"cache-template",
	})
	typesData := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "types.go"))
	for _, want := range []string{
		"type RequestMeta struct",
		"type MoneyAmount struct",
		"type InvoiceLine struct",
		"type InvoiceSummary struct",
		"type CreateInvoiceRequest struct",
		"type CreateInvoiceResponse struct",
		"[]InvoiceLine",
		"RequestMeta",
		"InvoiceSummary",
		"CursorPageResponse",
		"`header:\"X-Request-ID\"`",
		"`path:\"id\"`",
		"`form:\"customerId\"`",
		"`form:\"status,optional\"`",
	} {
		if !strings.Contains(typesData, want) {
			t.Fatalf("generated billing imported/nested types missing %q:\n%s", want, typesData)
		}
	}
	for _, unexpected := range []string{
		"type CreateInvoiceR" + "eq struct",
		"type CreateInvoiceR" + "esp struct",
		"type CaptureInvoiceR" + "eq struct",
		"type CaptureInvoiceR" + "esp struct",
	} {
		if strings.Contains(typesData, unexpected) {
			t.Fatalf("generated billing types should use Request/Response names, found %q:\n%s", unexpected, typesData)
		}
	}
	billingRoutes := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "billing_api", "routes.go"))
	for _, want := range []string{
		"RegisterBillingApiRoutes",
		"RegisterCreateInvoiceRoute",
		"RegisterGetInvoiceRoute",
		"RegisterListInvoicesRoute",
	} {
		if !strings.Contains(billingRoutes, want) {
			t.Fatalf("generated billing routes missing %q:\n%s", want, billingRoutes)
		}
	}
	captureRoutes := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "capture_api", "routes.go"))
	for _, want := range []string{
		"RegisterCaptureApiRoutes",
		"RegisterCaptureInvoiceRoute",
	} {
		if !strings.Contains(captureRoutes, want) {
			t.Fatalf("generated capture routes missing %q:\n%s", want, captureRoutes)
		}
	}
	captureRoute := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "capture_api", "capture_invoice.go"))
	for _, want := range []string{`Path: "/invoices/:id/capture"`, "ctx.BindRequest(&req)"} {
		if !strings.Contains(captureRoute, want) {
			t.Fatalf("generated capture invoice route missing %q:\n%s", want, captureRoute)
		}
	}
	repo := readReplayFile(t, outDir, filepath.Join("model", "repo", "invoice.go"))
	for _, want := range []string{
		"func NewCachedInvoiceRepo",
		"func NewConsistentCachedInvoiceRepo",
		"func NewRedisCachedInvoiceRepo",
		"func NewInvoiceRepoWithCluster",
		"func (r *InvoiceRepo) FindByInvoiceNo(ctx context.Context, invoiceNo string) (*entity.Invoice, error)",
		"func (r *InvoiceRepo) FindByTenantIDAndCustomerIDAndStatus(ctx context.Context, tenantID int64, customerID int64, status string) (*entity.Invoice, error)",
		"func (r *InvoiceRepo) FindByCustomerID(ctx context.Context, customerID int64, limit int, offset int) ([]entity.Invoice, error)",
		"func (r *InvoiceRepo) CountByCustomerID(ctx context.Context, customerID int64) (int64, error)",
		"func (r *InvoiceRepo) FindByIDs(ctx context.Context, ids []int64) ([]entity.Invoice, error)",
		"listCacheByCustomerID",
		"countCacheByCustomerID",
		"*cache.Cache[[]entity.Invoice]",
		"*cache.Cache[int64]",
		"out.listCacheByCustomerID = cache.New[[]entity.Invoice]",
		"out.countCacheByCustomerID = cache.New[int64]",
		"func (c *CachedInvoiceRepo) FindByCustomerIDCached(ctx context.Context, customerID int64, limit int, offset int) ([]entity.Invoice, error)",
		"func (c *CachedInvoiceRepo) FindByIDsCached(ctx context.Context, ids []int64) ([]entity.Invoice, error)",
		"if item, ok := c.cache.Peek(id); ok",
		"func (c *CachedInvoiceRepo) InsertMany(ctx context.Context, items []*entity.Invoice) error",
		"func (c *CachedInvoiceRepo) UpdateManyWithInvalidate(ctx context.Context, items []*entity.Invoice) error",
		"func (c *CachedInvoiceRepo) DeleteMany(ctx context.Context, ids ...int64) error",
		"func (c *CachedInvoiceRepo) CountByCustomerIDCached(ctx context.Context, customerID int64) (int64, error)",
		"func (c *CachedInvoiceRepo) PageByCustomerIDCached(ctx context.Context, customerID int64, limit int, offset int) ([]entity.Invoice, int64, error)",
		"key := indexListKeyByCustomerID(customerID, limit, offset)",
		"key := indexCountKeyByCustomerID(customerID)",
		"func indexListKeyByCustomerID(customerID int64, limit int, offset int) string",
		"func indexCountKeyByCustomerID(customerID int64) string",
		"c.listCacheByCustomerID.Clear()",
		"c.countCacheByCustomerID.Clear()",
		"cacheByInvoiceNo",
		"cacheByTenantIDAndCustomerIDAndStatus",
		"*cache.ModelCache[*entity.Invoice, string]",
		"func (c *CachedInvoiceRepo) FindByInvoiceNoCached(ctx context.Context, invoiceNo string) (*entity.Invoice, error)",
		"func (c *CachedInvoiceRepo) FindByTenantIDAndCustomerIDAndStatusCached(ctx context.Context, tenantID int64, customerID int64, status string) (*entity.Invoice, error)",
		"func (r *InvoiceRepo) UpdateWithVersion",
		"func (c *CachedInvoiceRepo) UpdateFieldsWithInvalidate(ctx context.Context, id int64, fields map[string]any) error",
		"func (c *CachedInvoiceRepo) UpdateWithVersion(ctx context.Context, in *entity.Invoice, expectedVersion int64) error",
		"func (c *CachedInvoiceRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *CachedInvoiceRepo) error) error",
		"func (c *CachedInvoiceRepo) afterDeleteCommit(ctx context.Context, id int64, old *entity.Invoice) error",
		"c.cacheByInvoiceNo.Cache().Delete(uniqueKeyByInvoiceNo(old.InvoiceNo))",
		"c.cacheByTenantIDAndCustomerIDAndStatus.Cache().Set(uniqueKeyByTenantIDAndCustomerIDAndStatus(in.TenantID, in.CustomerID, in.Status), in)",
		"func (r *InvoiceRepo) ListAfter",
		"query, args, err := storage.SelectWhere(entity.InvoiceTable, entity.InvoiceColumns, where, r.dialect)",
		"where = where.Limit(1)",
		`where = where.IsNull("deleted_at")`,
		"listVersionByCustomerID",
		"*cache.RedisModelCache[[]entity.Invoice, string]",
		"*cache.RedisModelCache[int64, string]",
		"*cache.RedisModelCache[string, string]",
		"func (c *RedisCachedInvoiceRepo) FindByCustomerIDCached(ctx context.Context, customerID int64, limit int, offset int) ([]entity.Invoice, error)",
		"func (c *RedisCachedInvoiceRepo) FindByIDsCached(ctx context.Context, ids []int64) ([]entity.Invoice, error)",
		"item, ok, err := c.cache.Peek(ctx, id)",
		"func (c *RedisCachedInvoiceRepo) InsertMany(ctx context.Context, items []*entity.Invoice) error",
		"func (c *RedisCachedInvoiceRepo) UpdateManyWithInvalidate(ctx context.Context, items []*entity.Invoice) error",
		"func (c *RedisCachedInvoiceRepo) DeleteMany(ctx context.Context, ids ...int64) error",
		"func (c *RedisCachedInvoiceRepo) CountByCustomerIDCached(ctx context.Context, customerID int64) (int64, error)",
		"func (c *RedisCachedInvoiceRepo) PageByCustomerIDCached(ctx context.Context, customerID int64, limit int, offset int) ([]entity.Invoice, int64, error)",
		"key := redisInvoiceIndexListCacheKey(version, indexListKeyByCustomerID(customerID, limit, offset))",
		"key := redisInvoiceIndexListCacheKey(version, indexCountKeyByCustomerID(customerID))",
		"c.listVersionByCustomerID.Set(ctx, \"current\", redisInvoiceIndexListVersionValue())",
		"func (c *RedisCachedInvoiceRepo) UpdateWithInvalidate",
		"func (c *RedisCachedInvoiceRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *RedisCachedInvoiceRepo) error) error",
		"func (c *RedisCachedInvoiceRepo) afterDeleteCommit(ctx context.Context, id int64) error",
	} {
		if !strings.Contains(repo, want) {
			t.Fatalf("generated billing model/cache repo missing %q:\n%s", want, repo)
		}
	}
	for _, unexpected := range []string{
		"func (r *InvoiceRepo) FindByTenantID(ctx context.Context",
		"func (r *InvoiceRepo) FindByStatus(ctx context.Context",
	} {
		if strings.Contains(repo, unexpected) {
			t.Fatalf("generated billing repo should not create single-column finders for composite unique key %q:\n%s", unexpected, repo)
		}
	}
	entity := readReplayFile(t, outDir, filepath.Join("model", "entity", "invoice_gen.go"))
	for _, want := range []string{
		"type Invoice struct",
		"InvoiceNo",
		"Version",
		`db:"invoice_no"`,
		`db:"version"`,
		`const InvoiceTable = "invoices"`,
	} {
		if !strings.Contains(entity, want) {
			t.Fatalf("generated billing entity missing %q:\n%s", want, entity)
		}
	}
}

func assertUserGoctlReplayArtifacts(t *testing.T, outDir string, fixture goctlReplayFixture) {
	t.Helper()
	requireGoctlReplayCapabilities(t, fixture, []string{
		"crud-route-set",
		"optional-query-params",
		"single-column-unique-key",
		"non-unique-index",
		"soft-delete",
		"optimistic-lock",
		"cache-template",
	})
	typesData := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "types.go"))
	for _, want := range []string{
		"type CreateUserRequest struct",
		"type SearchUsersRequest struct",
		"type SearchUsersResponse struct",
		"`header:\"X-Request-ID,optional\"`",
		"`form:\"status,optional\"`",
		"Items []GetUserResponse",
	} {
		if !strings.Contains(typesData, want) {
			t.Fatalf("generated user types missing %q:\n%s", want, typesData)
		}
	}
	userRoutes := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "user_api", "routes.go"))
	for _, want := range []string{
		"RegisterUserApiRoutes",
		"RegisterCreateUserRoute",
		"RegisterGetUserRoute",
		"RegisterSearchUsersRoute",
		"RegisterUpdateUserRoute",
	} {
		if !strings.Contains(userRoutes, want) {
			t.Fatalf("generated user routes missing %q:\n%s", want, userRoutes)
		}
	}
	searchRoute := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "user_api", "search_users.go"))
	for _, want := range []string{`Path: "/users"`, "ctx.BindRequest(&req)"} {
		if !strings.Contains(searchRoute, want) {
			t.Fatalf("generated search users route missing %q:\n%s", want, searchRoute)
		}
	}
	updateRoute := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "user_api", "update_user.go"))
	for _, want := range []string{`Path: "/users/:id"`, "ctx.BindRequest(&req)"} {
		if !strings.Contains(updateRoute, want) {
			t.Fatalf("generated update user route missing %q:\n%s", want, updateRoute)
		}
	}
	repo := readReplayFile(t, outDir, filepath.Join("model", "repo", "user.go"))
	for _, want := range []string{
		"func NewCachedUserRepo",
		"func NewRedisCachedUserRepo",
		"func (r *UserRepo) FindByEmail(ctx context.Context, email string) (*entity.User, error)",
		"func (r *UserRepo) FindByStatus(ctx context.Context, status string, limit int, offset int) ([]entity.User, error)",
		"func (r *UserRepo) CountByStatus(ctx context.Context, status string) (int64, error)",
		"func (c *CachedUserRepo) FindByEmailCached(ctx context.Context, email string) (*entity.User, error)",
		"func (c *CachedUserRepo) FindByStatusCached(ctx context.Context, status string, limit int, offset int) ([]entity.User, error)",
		"func (c *CachedUserRepo) PageByStatusCached(ctx context.Context, status string, limit int, offset int) ([]entity.User, int64, error)",
		"func (c *CachedUserRepo) UpdateWithVersion(ctx context.Context, in *entity.User, expectedVersion int64) error",
		"func indexListKeyByStatus(status string, limit int, offset int) string",
		"cacheByEmail",
		"listCacheByStatus",
		"countCacheByStatus",
		"listVersionByStatus",
		`where = where.IsNull("deleted_at")`,
	} {
		if !strings.Contains(repo, want) {
			t.Fatalf("generated user repo missing %q:\n%s", want, repo)
		}
	}
	entity := readReplayFile(t, outDir, filepath.Join("model", "entity", "user_gen.go"))
	for _, want := range []string{"type User struct", "Email", "Version", `db:"email"`, `const UserTable = "users"`} {
		if !strings.Contains(entity, want) {
			t.Fatalf("generated user entity missing %q:\n%s", want, entity)
		}
	}
}

func assertTaskGoctlReplayArtifacts(t *testing.T, outDir string, fixture goctlReplayFixture) {
	t.Helper()
	requireGoctlReplayCapabilities(t, fixture, []string{
		"multi-service-group",
		"multi-middleware",
		"crud-route-set",
		"optional-query-params",
		"composite-unique-key",
		"non-unique-index",
		"soft-delete",
		"optimistic-lock",
		"cache-template",
	})
	typesData := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "types.go"))
	for _, want := range []string{
		"type CreateTaskRequest struct",
		"type ListTasksRequest struct",
		"type CompleteTaskRequest struct",
		"type DeleteTaskRequest struct",
		"Items []TaskSummary",
		"`form:\"status,optional\"`",
		"`path:\"id\"`",
	} {
		if !strings.Contains(typesData, want) {
			t.Fatalf("generated task types missing %q:\n%s", want, typesData)
		}
	}
	taskRoutes := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "task_api", "routes.go"))
	for _, want := range []string{
		"RegisterTaskApiRoutes",
		"RegisterCreateTaskRoute",
		"RegisterListTasksRoute",
		"RegisterCompleteTaskRoute",
	} {
		if !strings.Contains(taskRoutes, want) {
			t.Fatalf("generated task routes missing %q:\n%s", want, taskRoutes)
		}
	}
	adminRoutes := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "task_admin_api", "routes.go"))
	for _, want := range []string{"RegisterTaskAdminApiRoutes", "RegisterDeleteTaskRoute"} {
		if !strings.Contains(adminRoutes, want) {
			t.Fatalf("generated task admin routes missing %q:\n%s", want, adminRoutes)
		}
	}
	completeRoute := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "task_api", "complete_task.go"))
	for _, want := range []string{`Path: "/tasks/:id/complete"`, "ctx.BindRequest(&req)"} {
		if !strings.Contains(completeRoute, want) {
			t.Fatalf("generated complete task route missing %q:\n%s", want, completeRoute)
		}
	}
	deleteRoute := readReplayFile(t, outDir, filepath.Join("internal", "api", "v1", "task_admin_api", "delete_task.go"))
	for _, want := range []string{`Path: "/tasks/:id"`, "ctx.BindRequest(&req)"} {
		if !strings.Contains(deleteRoute, want) {
			t.Fatalf("generated delete task route missing %q:\n%s", want, deleteRoute)
		}
	}
	repo := readReplayFile(t, outDir, filepath.Join("model", "repo", "task.go"))
	for _, want := range []string{
		"func NewCachedTaskRepo",
		"func NewRedisCachedTaskRepo",
		"func (r *TaskRepo) FindByOwnerIDAndTitle(ctx context.Context, ownerID int64, title string) (*entity.Task, error)",
		"func (r *TaskRepo) FindByOwnerID(ctx context.Context, ownerID int64, limit int, offset int) ([]entity.Task, error)",
		"func (r *TaskRepo) CountByOwnerID(ctx context.Context, ownerID int64) (int64, error)",
		"func (r *TaskRepo) FindByPriority(ctx context.Context, priority string, limit int, offset int) ([]entity.Task, error)",
		"func (r *TaskRepo) CountByPriority(ctx context.Context, priority string) (int64, error)",
		"func (c *CachedTaskRepo) FindByOwnerIDAndTitleCached(ctx context.Context, ownerID int64, title string) (*entity.Task, error)",
		"func (c *CachedTaskRepo) PageByOwnerIDCached(ctx context.Context, ownerID int64, limit int, offset int) ([]entity.Task, int64, error)",
		"func (c *CachedTaskRepo) PageByPriorityCached(ctx context.Context, priority string, limit int, offset int) ([]entity.Task, int64, error)",
		"func (c *CachedTaskRepo) UpdateWithVersion(ctx context.Context, in *entity.Task, expectedVersion int64) error",
		"func uniqueKeyByOwnerIDAndTitle(ownerID int64, title string) string",
		"func indexListKeyByOwnerID(ownerID int64, limit int, offset int) string",
		"func indexListKeyByPriority(priority string, limit int, offset int) string",
		"cacheByOwnerIDAndTitle",
		"listCacheByOwnerID",
		"countCacheByOwnerID",
		"listVersionByPriority",
		`where = where.IsNull("deleted_at")`,
	} {
		if !strings.Contains(repo, want) {
			t.Fatalf("generated task repo missing %q:\n%s", want, repo)
		}
	}
	entity := readReplayFile(t, outDir, filepath.Join("model", "entity", "task_gen.go"))
	for _, want := range []string{"type Task struct", "OwnerID", "Priority", "Version", `db:"owner_id"`, `const TaskTable = "tasks"`} {
		if !strings.Contains(entity, want) {
			t.Fatalf("generated task entity missing %q:\n%s", want, entity)
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
