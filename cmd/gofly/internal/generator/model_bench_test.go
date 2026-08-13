package generator

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/storage"
)

const fakeModelDatasourceDriver = "fake-model-datasource"

func init() {
	sql.Register(fakeModelDatasourceDriver, fakeDatasourceDriver{})
}

type fakeDatasourceDriver struct{}

func (fakeDatasourceDriver) Open(name string) (driver.Conn, error) {
	return &fakeDatasourceConn{dsn: name}, nil
}

type fakeDatasourceConn struct {
	dsn string
}

func (c *fakeDatasourceConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *fakeDatasourceConn) Close() error                        { return nil }
func (c *fakeDatasourceConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (c *fakeDatasourceConn) Ping(context.Context) error {
	if c.dsn == "ping-error" {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (c *fakeDatasourceConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch c.dsn {
	case "query-error":
		return nil, io.ErrClosedPipe
	case "index-query-error":
		if isFakeDatasourceIndexQuery(query) {
			return nil, io.ErrClosedPipe
		}
		return fakeDatasourceColumnRows(), nil
	case "query-empty":
		if isFakeDatasourceIndexQuery(query) {
			return &fakeDatasourceRows{columns: fakeDatasourceIndexColumnNames()}, nil
		}
		return &fakeDatasourceRows{columns: fakeDatasourceColumnNames()}, nil
	case "multi-table":
		if isFakeDatasourceIndexQuery(query) {
			return fakeDatasourceMultiTableIndexRows(), nil
		}
		return fakeDatasourceMultiTableColumnRows(), nil
	case "postgres-multi-schema":
		if isFakeDatasourceIndexQuery(query) {
			return fakeDatasourcePostgresMultiSchemaIndexRows(), nil
		}
		return fakeDatasourcePostgresMultiSchemaColumnRows(), nil
	case "postgres-expression-index":
		if isFakeDatasourceIndexQuery(query) {
			return fakeDatasourcePostgresExpressionIndexRows(), nil
		}
		return fakeDatasourcePostgresExpressionColumnRows(), nil
	default:
		if isFakeDatasourceIndexQuery(query) {
			return fakeDatasourceIndexRows(), nil
		}
		return fakeDatasourceColumnRows(), nil
	}
}

type fakeDatasourceRows struct {
	columns []string
	values  [][]driver.Value
	idx     int
}

type goctlDatasourceReplayFixture struct {
	Schema            string              `json:"schema"`
	ID                string              `json:"id"`
	Driver            string              `json:"driver"`
	DSN               string              `json:"dsn"`
	Module            string              `json:"module"`
	Package           string              `json:"package"`
	Style             string              `json:"style"`
	Database          string              `json:"database"`
	SchemaName        string              `json:"schemaName"`
	Tables            []string            `json:"tables"`
	Prefix            string              `json:"prefix"`
	IgnoreColumns     []string            `json:"ignoreColumns"`
	Strict            bool                `json:"strict"`
	Cache             bool                `json:"cache"`
	Capabilities      []string            `json:"capabilities"`
	ExpectedArtifacts []string            `json:"expectedArtifacts"`
	Assertions        map[string][]string `json:"assertions"`
}

func readGoctlDatasourceReplayFixture(t *testing.T, name string) goctlDatasourceReplayFixture {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "testdata", "goctl-datasource-replay", name, "replay.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read datasource replay fixture %s: %v", name, err)
	}
	var fixture goctlDatasourceReplayFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode datasource replay fixture %s: %v\n%s", name, err, data)
	}
	if fixture.Schema != "gofly.goctl_datasource_replay_fixture.v1" {
		t.Fatalf("datasource replay fixture %s schema = %q", name, fixture.Schema)
	}
	return fixture
}

func assertGoctlDatasourceReplayFixture(t *testing.T, dir string, fixture goctlDatasourceReplayFixture) {
	t.Helper()
	for _, rel := range fixture.ExpectedArtifacts {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("datasource replay artifact %s: %v", rel, err)
		}
	}
	for rel, needles := range fixture.Assertions {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read datasource replay assertion file %s: %v", rel, err)
		}
		out := string(data)
		for _, needle := range needles {
			if !strings.Contains(out, needle) {
				t.Fatalf("datasource replay %s missing %q:\n%s", rel, needle, out)
			}
		}
	}
}

func modelSchemaIRFromReplayFixture(t *testing.T, fixture goctlDatasourceReplayFixture) ModelSchemaIR {
	t.Helper()
	ir := rawModelSchemaIRFromReplayFixture(t, fixture)
	ir, err := prepareModelSchemaIR(ir, modelGenerationOptions{
		Tables:        fixture.Tables,
		IgnoreColumns: fixture.IgnoreColumns,
		Prefix:        fixture.Prefix,
		Strict:        fixture.Strict,
	}, nil)
	if err != nil {
		t.Fatalf("prepare replay fixture IR %s: %v", fixture.ID, err)
	}
	return ir
}

func rawModelSchemaIRFromReplayFixture(t *testing.T, fixture goctlDatasourceReplayFixture) ModelSchemaIR {
	t.Helper()
	db, err := sql.Open(fakeModelDatasourceDriver, fixture.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tables, err := introspectSQLTables(context.Background(), db, datasourceIntrospectionOptions{
		Driver:   fixture.Driver,
		Tables:   fixture.Tables,
		Database: fixture.Database,
		Schema:   fixture.SchemaName,
	})
	if err != nil {
		t.Fatalf("introspect replay fixture %s: %v", fixture.ID, err)
	}
	return newModelSchemaIR(ModelSchemaSourceReplay, modelDefaultDialect(fixture.Driver), fixture.Driver, fixture.Database, fixture.SchemaName, tables)
}

func (r *fakeDatasourceRows) Columns() []string {
	return append([]string(nil), r.columns...)
}

func (r *fakeDatasourceRows) Close() error { return nil }

func (r *fakeDatasourceRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.idx])
	r.idx++
	return nil
}

func fakeDatasourceColumnNames() []string {
	return []string{"table_name", "column_name", "data_type", "column_key", "is_nullable", "ordinal_position"}
}

func fakeDatasourceIndexColumnNames() []string {
	return []string{"table_name", "index_name", "column_name", "non_unique", "seq_in_index"}
}

func isFakeDatasourceIndexQuery(query string) bool {
	query = strings.ToLower(query)
	return strings.Contains(query, "information_schema.statistics") || strings.Contains(query, "pg_index")
}

func fakeDatasourceColumnRows() driver.Rows {
	return &fakeDatasourceRows{
		columns: fakeDatasourceColumnNames(),
		values: [][]driver.Value{
			{"users", "id", "BIGINT", "PRI", "NO", int64(1)},
			{"users", "email", "character varying", "", "YES", int64(2)},
			{"users", "name", "varchar", "", "NO", int64(3)},
			{"users", "created_at", "timestamp", "", "NO", int64(4)},
			{"audit_logs", "created_at", "timestamp with time zone", "", "NO", int64(1)},
		},
	}
}

func fakeDatasourceIndexRows() driver.Rows {
	return &fakeDatasourceRows{
		columns: fakeDatasourceIndexColumnNames(),
		values: [][]driver.Value{
			{"users", "uk_users_email", "email", int64(0), int64(1)},
			{"users", "idx_users_name_created", "name", int64(1), int64(1)},
			{"users", "idx_users_name_created", "created_at", int64(1), int64(2)},
			{"audit_logs", "idx_audit_created", "created_at", int64(1), int64(1)},
		},
	}
}

func fakeDatasourceMultiTableColumnRows() driver.Rows {
	return &fakeDatasourceRows{
		columns: fakeDatasourceColumnNames(),
		values: [][]driver.Value{
			{"app_customers", "id", "BIGINT", "PRI", "NO", int64(1)},
			{"app_customers", "tenant_id", "BIGINT", "", "NO", int64(2)},
			{"app_customers", "external_id", "VARCHAR", "", "NO", int64(3)},
			{"app_customers", "email", "VARCHAR", "", "YES", int64(4)},
			{"app_customers", "name", "VARCHAR", "", "NO", int64(5)},
			{"app_customers", "version", "BIGINT", "", "NO", int64(6)},
			{"app_customers", "deleted_at", "TIMESTAMP", "", "YES", int64(7)},
			{"app_customers", "created_by", "VARCHAR", "", "YES", int64(8)},
			{"app_customers", "updated_by", "VARCHAR", "", "YES", int64(9)},
			{"app_orders", "id", "BIGINT", "PRI", "NO", int64(1)},
			{"app_orders", "tenant_id", "BIGINT", "", "NO", int64(2)},
			{"app_orders", "customer_id", "BIGINT", "", "NO", int64(3)},
			{"app_orders", "order_no", "VARCHAR", "", "NO", int64(4)},
			{"app_orders", "status", "VARCHAR", "", "NO", int64(5)},
			{"app_orders", "total_amount", "DECIMAL", "", "NO", int64(6)},
			{"app_orders", "version", "BIGINT", "", "NO", int64(7)},
			{"app_orders", "deleted_at", "TIMESTAMP", "", "YES", int64(8)},
			{"app_orders", "created_by", "VARCHAR", "", "YES", int64(9)},
			{"app_orders", "updated_by", "VARCHAR", "", "YES", int64(10)},
		},
	}
}

func fakeDatasourceMultiTableIndexRows() driver.Rows {
	return &fakeDatasourceRows{
		columns: fakeDatasourceIndexColumnNames(),
		values: [][]driver.Value{
			{"app_customers", "idx_customer_tenant_email", "tenant_id", int64(1), int64(1)},
			{"app_customers", "idx_customer_tenant_email", "email", int64(1), int64(2)},
			{"app_customers", "uk_customer_tenant_external", "tenant_id", int64(0), int64(1)},
			{"app_customers", "uk_customer_tenant_external", "external_id", int64(0), int64(2)},
			{"app_orders", "idx_order_customer_status", "customer_id", int64(1), int64(1)},
			{"app_orders", "idx_order_customer_status", "status", int64(1), int64(2)},
			{"app_orders", "idx_order_tenant_status_id", "tenant_id", int64(1), int64(1)},
			{"app_orders", "idx_order_tenant_status_id", "status", int64(1), int64(2)},
			{"app_orders", "idx_order_tenant_status_id", "id", int64(1), int64(3)},
			{"app_orders", "uk_order_tenant_order_no", "tenant_id", int64(0), int64(1)},
			{"app_orders", "uk_order_tenant_order_no", "order_no", int64(0), int64(2)},
		},
	}
}

func fakeDatasourcePostgresMultiSchemaColumnRows() driver.Rows {
	return &fakeDatasourceRows{
		columns: fakeDatasourceColumnNames(),
		values: [][]driver.Value{
			{"billing_accounts", "id", "bigint", "PRI", "NO", int64(1)},
			{"billing_accounts", "tenant_id", "bigint", "", "NO", int64(2)},
			{"billing_accounts", "external_ref", "uuid", "", "NO", int64(3)},
			{"billing_accounts", "email", "character varying", "", "YES", int64(4)},
			{"billing_accounts", "metadata", "jsonb", "", "YES", int64(5)},
			{"billing_accounts", "version", "integer", "", "NO", int64(6)},
			{"billing_accounts", "deleted_at", "timestamp with time zone", "", "YES", int64(7)},
			{"billing_accounts", "created_by", "character varying", "", "YES", int64(8)},
			{"billing_accounts", "updated_by", "character varying", "", "YES", int64(9)},
			{"billing_events", "id", "bigint", "PRI", "NO", int64(1)},
			{"billing_events", "tenant_id", "bigint", "", "NO", int64(2)},
			{"billing_events", "account_id", "bigint", "", "NO", int64(3)},
			{"billing_events", "event_no", "character varying", "", "NO", int64(4)},
			{"billing_events", "status", "character varying", "", "NO", int64(5)},
			{"billing_events", "amount", "numeric", "", "NO", int64(6)},
			{"billing_events", "occurred_at", "timestamp without time zone", "", "NO", int64(7)},
			{"billing_events", "deleted_at", "timestamp with time zone", "", "YES", int64(8)},
			{"billing_events", "created_by", "character varying", "", "YES", int64(9)},
			{"billing_events", "updated_by", "character varying", "", "YES", int64(10)},
		},
	}
}

func fakeDatasourcePostgresMultiSchemaIndexRows() driver.Rows {
	return &fakeDatasourceRows{
		columns: fakeDatasourceIndexColumnNames(),
		values: [][]driver.Value{
			{"billing_accounts", "idx_billing_accounts_tenant_email", "tenant_id", int64(1), int64(1)},
			{"billing_accounts", "idx_billing_accounts_tenant_email", "email", int64(1), int64(2)},
			{"billing_accounts", "uk_billing_accounts_tenant_external", "tenant_id", int64(0), int64(1)},
			{"billing_accounts", "uk_billing_accounts_tenant_external", "external_ref", int64(0), int64(2)},
			{"billing_events", "idx_billing_events_account_status", "account_id", int64(1), int64(1)},
			{"billing_events", "idx_billing_events_account_status", "status", int64(1), int64(2)},
			{"billing_events", "idx_billing_events_tenant_status_occurred", "tenant_id", int64(1), int64(1)},
			{"billing_events", "idx_billing_events_tenant_status_occurred", "status", int64(1), int64(2)},
			{"billing_events", "idx_billing_events_tenant_status_occurred", "occurred_at", int64(1), int64(3)},
			{"billing_events", "uk_billing_events_tenant_event_no", "tenant_id", int64(0), int64(1)},
			{"billing_events", "uk_billing_events_tenant_event_no", "event_no", int64(0), int64(2)},
		},
	}
}

func fakeDatasourcePostgresExpressionColumnRows() driver.Rows {
	return &fakeDatasourceRows{
		columns: fakeDatasourceColumnNames(),
		values: [][]driver.Value{
			{"billing_jobs", "id", "bigint", "PRI", "NO", int64(1)},
			{"billing_jobs", "tenant_id", "bigint", "", "NO", int64(2)},
			{"billing_jobs", "status", "character varying", "", "NO", int64(3)},
			{"billing_jobs", "email", "character varying", "", "YES", int64(4)},
			{"billing_jobs", "deleted_at", "timestamp with time zone", "", "YES", int64(5)},
		},
	}
}

func fakeDatasourcePostgresExpressionIndexRows() driver.Rows {
	return &fakeDatasourceRows{
		columns: fakeDatasourceIndexColumnNames(),
		values: [][]driver.Value{
			{"billing_jobs", "idx_jobs_tenant_status", "tenant_id", int64(1), int64(1)},
			{"billing_jobs", "idx_jobs_tenant_status", "status", int64(1), int64(2)},
			{"billing_jobs", "idx_jobs_lower_email", nil, int64(1), int64(1)},
			{"billing_jobs", "idx_jobs_partial_status", "status", int64(1), int64(0)},
			{"billing_jobs", "uk_jobs_expr_tenant_email", "tenant_id", int64(0), int64(1)},
			{"billing_jobs", "uk_jobs_expr_tenant_email", nil, int64(0), int64(2)},
		},
	}
}

func TestModelHelperBoundaries(t *testing.T) {
	table := SQLTable{
		Name:             "users",
		PrimaryKey:       "id",
		SoftDeleteColumn: "deleted_at",
		Columns: []SQLColumn{
			{Name: "id", Type: "bigint", PrimaryKey: true},
			{Name: "email", Type: "varchar(128)"},
			{Name: "created_at", Type: "timestamp"},
			{Name: "deleted_at", Type: "datetime", Nullable: true},
		},
	}
	if !tablesHaveSoftDelete([]SQLTable{{Name: "orders"}, table}) {
		t.Fatal("tablesHaveSoftDelete = false, want true when any table has soft delete column")
	}
	if tablesHaveSoftDelete([]SQLTable{{Name: "orders"}}) {
		t.Fatal("tablesHaveSoftDelete without soft delete = true, want false")
	}

	nonPrimary := nonPrimaryColumns(table)
	if len(nonPrimary) != 3 || nonPrimary[0].Name != "email" || nonPrimary[1].Name != "created_at" || nonPrimary[2].Name != "deleted_at" {
		t.Fatalf("nonPrimaryColumns = %#v, want all non-id columns", nonPrimary)
	}
	updates := updateColumnsExcept(table, "created_at")
	if len(updates) != 1 || updates[0].Name != "email" {
		t.Fatalf("updateColumnsExcept = %#v, want only email", updates)
	}

	typeTests := []struct {
		name    string
		sqlType string
		want    string
		known   bool
	}{
		{name: "bigint", sqlType: "BIGINT", want: "int64", known: true},
		{name: "varchar with size", sqlType: "varchar(128)", want: "string", known: true},
		{name: "timestamp", sqlType: "timestamp", want: "time.Time", known: true},
		{name: "bytea", sqlType: "bytea", want: "[]byte", known: true},
		{name: "unknown fallback", sqlType: "geography", want: "string", known: false},
	}
	for _, tt := range typeTests {
		t.Run(tt.name, func(t *testing.T) {
			gotKnown, ok := sqlGoTypeKnown(tt.sqlType)
			if ok != tt.known {
				t.Fatalf("sqlGoTypeKnown(%q) known = %v, want %v", tt.sqlType, ok, tt.known)
			}
			if tt.known && gotKnown != tt.want {
				t.Fatalf("sqlGoTypeKnown(%q) = %q, want %q", tt.sqlType, gotKnown, tt.want)
			}
			if got := sqlGoType(tt.sqlType); got != tt.want {
				t.Fatalf("sqlGoType(%q) = %q, want %q", tt.sqlType, got, tt.want)
			}
		})
	}

	singularTests := []struct {
		name string
		want string
	}{
		{name: "users", want: "user"},
		{name: "companies", want: "company"},
		{name: "boxes", want: "boxe"},
		{name: "data", want: "data"},
		{name: "s", want: "s"},
	}
	for _, tt := range singularTests {
		t.Run(tt.name, func(t *testing.T) {
			if got := singularize(tt.name); got != tt.want {
				t.Fatalf("singularize(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestModelModuleAndGoModDependencyBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		driver string
		want   storage.Dialect
	}{
		{name: "postgres", driver: " pg ", want: storage.DialectPostgres},
		{name: "postgresql", driver: "POSTGRESQL", want: storage.DialectPostgres},
		{name: "mysql", driver: "mariadb", want: storage.DialectMySQL},
		{name: "sqlite", driver: "sqlite3", want: storage.DialectSQLite},
		{name: "unknown", driver: "oracle", want: storage.DialectQuestion},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := modelDefaultDialect(test.driver); got != test.want {
				t.Fatalf("modelDefaultDialect(%q) = %q, want %q", test.driver, got, test.want)
			}
		})
	}

	root := t.TempDir()
	modelDir := filepath.Join(root, "internal", "model")
	if err := os.MkdirAll(modelDir, 0o750); err != nil {
		t.Fatal(err)
	}
	goMod := filepath.Join(root, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/orders\n\ngo 1.26\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	module, err := inferModelModule(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	if module != "example.com/orders/internal/model" {
		t.Fatalf("inferred module = %q", module)
	}
	if err := ensureModelGoModDependencies(modelDir, modelStyleSQL); err != nil {
		t.Fatalf("SQL dependencies: %v", err)
	}
	if err := ensureModelGoModDependencies(modelDir, modelStyleGORM); err != nil {
		t.Fatalf("GORM dependencies: %v", err)
	}
	data, err := os.ReadFile(goMod)
	if err != nil {
		t.Fatal(err)
	}
	if !goModHasRequire(data, gormModulePath) || !strings.Contains(string(data), "require gorm.io/gorm v1.31.1") {
		t.Fatalf("go.mod after GORM dependency:\n%s", data)
	}
	info, err := os.Stat(goMod)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("go.mod mode = %o, want 640", info.Mode().Perm())
	}
	if err := ensureModelGoModDependencies(modelDir, modelStyleGORM); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(goMod)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(again, []byte(gormModulePath)) != 1 {
		t.Fatalf("duplicate GORM dependency:\n%s", again)
	}

	if err := ensureGoModRequire(filepath.Join(root, "not-go.mod"), "example.com/dependency", "v1.0.0"); err == nil {
		t.Fatal("ensureGoModRequire accepted non-go.mod path")
	}
	missing := filepath.Join(root, "missing", "go.mod")
	if err := ensureGoModRequire(missing, "example.com/dependency", "v1.0.0"); err == nil || !strings.Contains(err.Error(), "symlinks") {
		t.Fatalf("missing parent error = %v, want symlink resolution error", err)
	}
	directoryGoMod := filepath.Join(root, "directory", "go.mod")
	if err := os.MkdirAll(directoryGoMod, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := ensureGoModRequire(directoryGoMod, "example.com/dependency", "v1.0.0"); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("directory go.mod error = %v", err)
	}
	if _, err := readGoModModule(filepath.Join(root, "absent.mod")); err == nil || !strings.Contains(err.Error(), "read go.mod") {
		t.Fatalf("missing readGoModModule error = %v", err)
	}
	emptyModule := filepath.Join(root, "empty.mod")
	if err := os.WriteFile(emptyModule, []byte("go 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readGoModModule(emptyModule); err == nil || err.Error() != "module is required" {
		t.Fatalf("empty module error = %v", err)
	}
	if _, err := findNearestGoMod(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("findNearestGoMod absent error = %v, want os.ErrNotExist", err)
	}

	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{name: "empty", want: "require example.com/dependency v1.2.3\n"},
		{name: "top-level require block", data: "require (\n\texample.com/old v1.0.0\n)\n", want: "require (\n\texample.com/dependency v1.2.3\n"},
		{name: "module require block", data: "module example.com/app\n\nrequire (\n\texample.com/old v1.0.0\n)\n", want: "\nrequire (\n\texample.com/dependency v1.2.3\n"},
		{name: "append single require", data: "module example.com/app\n\ngo 1.26\n", want: "\n\nrequire example.com/dependency v1.2.3\n"},
	} {
		t.Run("add require "+test.name, func(t *testing.T) {
			got := string(addGoModRequire([]byte(test.data), "example.com/dependency", "v1.2.3"))
			if !strings.Contains(got, test.want) {
				t.Fatalf("addGoModRequire output:\n%s\nwant containing:\n%s", got, test.want)
			}
		})
	}
}

func TestModelCodegenAdvancedRepoBoundaries(t *testing.T) {
	table := SQLTable{
		Name:             "users",
		PrimaryKey:       "id",
		SoftDeleteColumn: "deleted_at",
		Columns: []SQLColumn{
			{Name: "id", Type: "bigint", PrimaryKey: true},
			{Name: "email", Type: "varchar(128)", Unique: true},
			{Name: "name", Type: "varchar(64)"},
			{Name: "version", Type: "int"},
			{Name: "created_at", Type: "datetime"},
			{Name: "deleted_at", Type: "datetime", Nullable: true},
		},
		Indexes: []SQLIndex{{Columns: []string{"name", "created_at"}}},
	}

	version, ok := versionColumn(table)
	if !ok || version.Name != "version" {
		t.Fatalf("versionColumn = %#v %t, want version", version, ok)
	}
	if _, ok := versionColumn(SQLTable{Columns: table.Columns[:3]}); ok {
		t.Fatal("versionColumn without version = true, want false")
	}
	if got := softDeleteValueExpr(table); got != "time.Now().UTC()" {
		t.Fatalf("softDeleteValueExpr(datetime) = %q, want time.Now().UTC()", got)
	}
	intSoftDelete := table
	intSoftDelete.SoftDeleteColumn = "deleted_at_unix"
	intSoftDelete.Columns = append([]SQLColumn(nil), table.Columns...)
	intSoftDelete.Columns[len(intSoftDelete.Columns)-1] = SQLColumn{Name: "deleted_at_unix", Type: "bigint"}
	if got := softDeleteValueExpr(intSoftDelete); got != "time.Now().Unix()" {
		t.Fatalf("softDeleteValueExpr(bigint) = %q, want unix timestamp", got)
	}

	var sql bytes.Buffer
	writeSQLOptimisticLock(&sql, table, "User", "UserRepo")
	sqlOut := sql.String()
	for _, want := range []string{"UpdateWithVersion", "expectedVersion+1", "deleted_at IS NULL", "storage.ErrNotFound"} {
		if !strings.Contains(sqlOut, want) {
			t.Fatalf("writeSQLOptimisticLock output missing %q:\n%s", want, sqlOut)
		}
	}
	var indexedSQL bytes.Buffer
	writeIndexListFinders(&indexedSQL, table, "User", "UserRepo")
	indexedSQLOut := indexedSQL.String()
	for _, want := range []string{
		"FindByName(ctx context.Context, name string, limit int, offset int) ([]entity.User, error)",
		"CountByName(ctx context.Context, name string) (int64, error)",
		`where = where.Eq("name", name)`,
		`where = where.OrderBy("created_at")`,
		`where = where.OrderBy("id")`,
		"storage.SelectWhere",
		"storage.CountWhere",
	} {
		if !strings.Contains(indexedSQLOut, want) {
			t.Fatalf("writeIndexListFinders output missing %q:\n%s", want, indexedSQLOut)
		}
	}
	var noVersion bytes.Buffer
	writeSQLOptimisticLock(&noVersion, SQLTable{PrimaryKey: "id", Columns: table.Columns[:3]}, "User", "UserRepo")
	if noVersion.Len() != 0 {
		t.Fatalf("writeSQLOptimisticLock without version wrote %q", noVersion.String())
	}

	var gorm bytes.Buffer
	writeAdvancedGORMRepoMethods(&gorm, table, "User", "UserRepo")
	gormOut := gorm.String()
	for _, want := range []string{"FindByEmail", "FindByName", "CountByName", "InsertMany", "UpdateFields", "UpdateWithVersion", "ListAfter", "deleted_at IS NULL", `"version": expectedVersion + 1`} {
		if !strings.Contains(gormOut, want) {
			t.Fatalf("writeAdvancedGORMRepoMethods output missing %q:\n%s", want, gormOut)
		}
	}
	var gormNoVersion bytes.Buffer
	writeAdvancedGORMRepoMethods(&gormNoVersion, SQLTable{PrimaryKey: "id", Columns: table.Columns[:3]}, "User", "UserRepo")
	if strings.Contains(gormNoVersion.String(), "UpdateWithVersion") {
		t.Fatalf("writeAdvancedGORMRepoMethods without version emitted optimistic lock:\n%s", gormNoVersion.String())
	}
}

func TestParseSQLModelsKeepsNonUniqueIndexes(t *testing.T) {
	const ddl = `CREATE TABLE invoices (
  id bigint primary key,
  invoice_no varchar(64) unique not null,
  customer_id bigint not null,
  updated_at timestamp,
  deleted_at timestamp,
  UNIQUE KEY uk_invoice_no (invoice_no),
  KEY idx_invoice_customer_updated (customer_id, updated_at),
  INDEX idx_invoice_deleted (deleted_at),
  KEY idx_invoice_id (id)
);`

	tables, err := ParseSQLModels(ddl)
	if err != nil {
		t.Fatalf("ParseSQLModels: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(tables))
	}
	table := tables[0]
	if len(table.Indexes) != 2 {
		t.Fatalf("table indexes = %#v, want customer+updated and deleted indexes", table.Indexes)
	}
	if got := strings.Join(table.Indexes[0].Columns, ","); got != "customer_id,updated_at" {
		t.Fatalf("first index columns = %q, want customer_id,updated_at", got)
	}
	if got := strings.Join(table.Indexes[1].Columns, ","); got != "deleted_at" {
		t.Fatalf("second index columns = %q, want deleted_at", got)
	}

	prefixes := modelIndexPrefixes(table)
	if len(prefixes) != 1 {
		t.Fatalf("model index prefixes = %#v, want one non-soft-delete prefix", prefixes)
	}
	if got := uniqueFinderName(prefixes[0].Columns); got != "CustomerID" {
		t.Fatalf("first prefix name = %q, want CustomerID", got)
	}
}

func TestWriteModelFilesEmptyTablesBoundary(t *testing.T) {
	err := writeModelFiles(nil, t.TempDir(), "model", "example.com/orders", ServiceStyleBasic, false, storage.DialectQuestion)
	if err == nil || !strings.Contains(err.Error(), "model table is required") {
		t.Fatalf("writeModelFiles(nil) error = %v, want model table required", err)
	}
}

func TestModelDatasourceGenerationBoundaries(t *testing.T) {
	if err := GenerateModelFromDatasource(ModelDatasourceOptions{DSN: "ok"}); err == nil || !strings.Contains(err.Error(), "datasource driver is required") {
		t.Fatalf("GenerateModelFromDatasource missing driver error = %v, want driver required", err)
	}
	if err := GenerateModelFromDatasource(ModelDatasourceOptions{Driver: fakeModelDatasourceDriver}); err == nil || !strings.Contains(err.Error(), "datasource dsn is required") {
		t.Fatalf("GenerateModelFromDatasource missing dsn error = %v, want dsn required", err)
	}
	if err := GenerateModelFromDatasource(ModelDatasourceOptions{Driver: fakeModelDatasourceDriver, DSN: "ping-error"}); err == nil || !strings.Contains(err.Error(), "ping datasource") {
		t.Fatalf("GenerateModelFromDatasource ping error = %v, want ping datasource", err)
	}
	if err := GenerateModelFromDatasource(ModelDatasourceOptions{Driver: fakeModelDatasourceDriver, DSN: "ok", Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "unsupported datasource driver") {
		t.Fatalf("GenerateModelFromDatasource unsupported driver error = %v, want introspection driver error", err)
	}
}

func TestModelSchemaIRPrepareMetadataAndTables(t *testing.T) {
	tables := []SQLTable{{
		Name:       "app_users",
		PrimaryKey: "id",
		Columns: []SQLColumn{
			{Name: "id", Type: "bigint", PrimaryKey: true},
			{Name: "email", Type: "varchar", Unique: true},
			{Name: "deleted_at", Type: "timestamp", Nullable: true},
		},
		Indexes: []SQLIndex{{Columns: []string{"email"}}},
	}}
	ir := newModelSchemaIR(ModelSchemaSourceDDL, storage.DialectQuestion, "", "shop", "public", tables)
	prepared, err := prepareModelSchemaIR(ir, modelGenerationOptions{
		Prefix:        "app_",
		IgnoreColumns: []string{"deleted_at"},
		Strict:        true,
	}, map[string]string{"varchar": "string"})
	if err != nil {
		t.Fatalf("prepareModelSchemaIR: %v", err)
	}
	if prepared.Source != ModelSchemaSourceDDL || prepared.Dialect != storage.DialectQuestion || prepared.Database != "shop" || prepared.Schema != "public" {
		t.Fatalf("prepared metadata = %+v, want ddl/question/shop/public", prepared)
	}
	if len(prepared.Tables) != 1 || prepared.Tables[0].Name != "users" {
		t.Fatalf("prepared tables = %+v, want trimmed users table", prepared.Tables)
	}
	users := prepared.Tables[0]
	if len(users.Columns) != 2 || users.Columns[1].GoType != "string" {
		t.Fatalf("prepared columns = %+v, want ignored deleted_at and type map", users.Columns)
	}
}

func TestModelSchemaIRDatasourceMetadata(t *testing.T) {
	db, err := sql.Open(fakeModelDatasourceDriver, "postgres-multi-schema")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tables, err := introspectSQLTables(context.Background(), db, datasourceIntrospectionOptions{
		Driver: "postgres",
		Schema: "billing",
		Tables: []string{"billing_accounts"},
	})
	if err != nil {
		t.Fatalf("introspectSQLTables postgres: %v", err)
	}
	ir := newModelSchemaIR(ModelSchemaSourceDatasource, modelDefaultDialect("postgres"), "postgres", "billingdb", "billing", tables)
	prepared, err := prepareModelSchemaIR(ir, modelGenerationOptions{Tables: []string{"billing_accounts"}}, nil)
	if err != nil {
		t.Fatalf("prepareModelSchemaIR datasource: %v", err)
	}
	if prepared.Source != ModelSchemaSourceDatasource || prepared.Dialect != storage.DialectPostgres || prepared.Driver != "postgres" || prepared.Database != "billingdb" || prepared.Schema != "billing" {
		t.Fatalf("prepared datasource metadata = %+v, want datasource/postgres/billingdb/billing", prepared)
	}
	if len(prepared.Tables) != 1 || prepared.Tables[0].Name != "billing_accounts" {
		t.Fatalf("prepared datasource tables = %+v, want billing_accounts", prepared.Tables)
	}
}

func TestGoctlDatasourceReplayFixtureModelSchemaIR(t *testing.T) {
	t.Run("mysql multi table", func(t *testing.T) {
		fixture := readGoctlDatasourceReplayFixture(t, "mysql-multi-table")
		ir := modelSchemaIRFromReplayFixture(t, fixture)
		if ir.Source != ModelSchemaSourceReplay || ir.Dialect != storage.DialectMySQL || ir.Driver != "mysql" {
			t.Fatalf("mysql replay IR metadata = %+v, want replay/mysql", ir)
		}
		if len(ir.Tables) != 2 || ir.Tables[0].Name != "customers" || ir.Tables[1].Name != "orders" {
			t.Fatalf("mysql replay tables = %+v, want customers/orders after prefix trim", ir.Tables)
		}
		customer := ir.Tables[0]
		if customer.PrimaryKey != "id" || len(customer.Columns) != 7 || hasSQLColumn(customer, "created_by") || hasSQLColumn(customer, "updated_by") {
			t.Fatalf("customer IR = %+v, want pk id and ignored audit columns removed", customer)
		}
		if !hasSQLUniqueIndex(customer, "tenant_id", "external_id") || !hasSQLIndex(customer, "tenant_id", "email") {
			t.Fatalf("customer IR indexes = unique:%+v nonUnique:%+v", customer.UniqueIndexes, customer.Indexes)
		}
		order := ir.Tables[1]
		if order.PrimaryKey != "id" || !hasSQLUniqueIndex(order, "tenant_id", "order_no") || !hasSQLIndex(order, "tenant_id", "status", "id") {
			t.Fatalf("order IR = %+v, want composite unique and tenant/status/id index", order)
		}
	})

	t.Run("postgres multi schema", func(t *testing.T) {
		fixture := readGoctlDatasourceReplayFixture(t, "postgres-multi-schema")
		ir := modelSchemaIRFromReplayFixture(t, fixture)
		if ir.Source != ModelSchemaSourceReplay || ir.Dialect != storage.DialectPostgres || ir.Driver != "postgres" || ir.Schema != "billing" {
			t.Fatalf("postgres replay IR metadata = %+v, want replay/postgres/billing", ir)
		}
		if len(ir.Tables) != 2 || ir.Tables[0].Name != "accounts" || ir.Tables[1].Name != "events" {
			t.Fatalf("postgres replay tables = %+v, want accounts/events after prefix trim", ir.Tables)
		}
		account := ir.Tables[0]
		if account.PrimaryKey != "id" || !hasSQLUniqueIndex(account, "tenant_id", "external_ref") || !hasSQLIndex(account, "tenant_id", "email") {
			t.Fatalf("account IR = %+v, want tenant/external unique and tenant/email index", account)
		}
		event := ir.Tables[1]
		if event.PrimaryKey != "id" || !hasSQLUniqueIndex(event, "tenant_id", "event_no") || !hasSQLIndex(event, "tenant_id", "status", "occurred_at") {
			t.Fatalf("event IR = %+v, want tenant/event unique and tenant/status/occurred index", event)
		}
	})
}

func hasSQLColumn(table SQLTable, name string) bool {
	for _, column := range table.Columns {
		if column.Name == name {
			return true
		}
	}
	return false
}

func hasSQLUniqueIndex(table SQLTable, columns ...string) bool {
	want := strings.Join(columns, ",")
	for _, index := range table.UniqueIndexes {
		if strings.Join(index.Columns, ",") == want {
			return true
		}
	}
	return false
}

func hasSQLIndex(table SQLTable, columns ...string) bool {
	want := strings.Join(columns, ",")
	for _, index := range table.Indexes {
		if strings.Join(index.Columns, ",") == want {
			return true
		}
	}
	return false
}

func TestEmitModelSchemaIRWritesGoZeroLayout(t *testing.T) {
	ir := newModelSchemaIR(ModelSchemaSourceReplay, storage.DialectMySQL, "mysql", "shop", "", []SQLTable{{
		Name:       "customers",
		PrimaryKey: "id",
		Columns: []SQLColumn{
			{Name: "id", Type: "bigint", PrimaryKey: true},
			{Name: "tenant_id", Type: "bigint"},
			{Name: "email", Type: "varchar", Unique: true, Nullable: true},
		},
		Indexes: []SQLIndex{{Columns: []string{"tenant_id"}}},
	}})
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/emit")
	if err := emitModelSchemaIR(ir, modelSchemaEmitOptions{
		Dir:          dir,
		Package:      "model",
		Module:       "example.com/emit",
		Style:        "go_zero",
		Cache:        true,
		GoZeroLayout: true,
	}); err != nil {
		t.Fatalf("emitModelSchemaIR: %v", err)
	}
	for _, rel := range []string{
		"model/customer_gen.go",
		"repo/customer.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("emitModelSchemaIR missing %s: %v", rel, err)
		}
	}
	repo, err := os.ReadFile(filepath.Join(dir, "repo", "customer.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(repo), "d := storage.DialectMySQL") {
		t.Fatalf("emitted repo does not use IR dialect:\n%s", repo)
	}
}

func TestGenerateModelFromSchemaIRPreparesAndWritesGoZeroLayout(t *testing.T) {
	ir := newModelSchemaIR(ModelSchemaSourceReplay, storage.DialectMySQL, "mysql", "shop", "", []SQLTable{{
		Name:       "app_customers",
		PrimaryKey: "id",
		Columns: []SQLColumn{
			{Name: "id", Type: "bigint", PrimaryKey: true},
			{Name: "tenant_id", Type: "bigint"},
			{Name: "email", Type: "varchar", Unique: true, Nullable: true},
			{Name: "updated_by", Type: "varchar", Nullable: true},
		},
		Indexes: []SQLIndex{{Columns: []string{"tenant_id"}}},
	}})
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/schemair")
	if err := generateModelFromSchemaIR(ir, modelSchemaGenerationOptions{
		Prefix:        "app_",
		IgnoreColumns: []string{"updated_by"},
		Strict:        true,
		Emit: modelSchemaEmitOptions{
			Dir:          dir,
			Package:      "model",
			Module:       "example.com/schemair",
			Style:        "go_zero",
			Cache:        true,
			GoZeroLayout: true,
		},
	}); err != nil {
		t.Fatalf("generateModelFromSchemaIR: %v", err)
	}
	entity, err := os.ReadFile(filepath.Join(dir, "model", "customer_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entity), "type Customer struct") || strings.Contains(string(entity), "UpdatedBy") {
		t.Fatalf("generated entity did not apply prefix/ignore options:\n%s", entity)
	}
	repo, err := os.ReadFile(filepath.Join(dir, "repo", "customer.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(repo), "d := storage.DialectMySQL") {
		t.Fatalf("generated repo does not use IR dialect:\n%s", repo)
	}
}

func TestGenerateModelFromSchemaIRBoundaries(t *testing.T) {
	base := []SQLTable{{
		Name:       "app_users",
		PrimaryKey: "id",
		Columns: []SQLColumn{
			{Name: "id", Type: "bigint", PrimaryKey: true},
			{Name: "email", Type: "varchar"},
		},
	}}
	newIR := func(tables []SQLTable) ModelSchemaIR {
		return newModelSchemaIR(ModelSchemaSourceReplay, storage.DialectMySQL, "mysql", "shop", "", tables)
	}
	assertNothingWritten := func(t *testing.T, dir string) {
		t.Helper()
		for _, rel := range []string{"model", "repo"} {
			if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
				t.Fatalf("expected no %s written on error, stat err = %v", rel, err)
			}
		}
	}

	t.Run("empty tables", func(t *testing.T) {
		dir := t.TempDir()
		writeGeneratedModule(t, dir, "example.com/ir-empty")
		err := generateModelFromSchemaIR(newIR(nil), modelSchemaGenerationOptions{
			Emit: modelSchemaEmitOptions{Dir: dir, Module: "example.com/ir-empty"},
		})
		if err == nil || !strings.Contains(err.Error(), "model table is required") {
			t.Fatalf("empty tables error = %v, want model table is required", err)
		}
		assertNothingWritten(t, dir)
	})

	t.Run("strict missing table", func(t *testing.T) {
		dir := t.TempDir()
		err := generateModelFromSchemaIR(newIR(base), modelSchemaGenerationOptions{
			Tables: []string{"missing"},
			Strict: true,
			Emit:   modelSchemaEmitOptions{Dir: dir, Module: "example.com/ir-missing"},
		})
		if err == nil || !strings.Contains(err.Error(), "requested table not found") {
			t.Fatalf("strict missing table error = %v, want requested table not found", err)
		}
		assertNothingWritten(t, dir)
	})

	t.Run("strict unsupported type", func(t *testing.T) {
		tables := []SQLTable{{
			Name:       "geo_shapes",
			PrimaryKey: "id",
			Columns: []SQLColumn{
				{Name: "id", Type: "bigint", PrimaryKey: true},
				{Name: "shape", Type: "geometry"},
			},
		}}
		dir := t.TempDir()
		err := generateModelFromSchemaIR(newIR(tables), modelSchemaGenerationOptions{
			Strict: true,
			Emit:   modelSchemaEmitOptions{Dir: dir, Module: "example.com/ir-type"},
		})
		if err == nil || !strings.Contains(err.Error(), `unknown column type "geometry" for geo_shapes.shape`) {
			t.Fatalf("strict unsupported type error = %v, want unknown column type", err)
		}
		assertNothingWritten(t, dir)
	})
}

func TestGenerateModelFromReplaySchemaIRCompiles(t *testing.T) {
	for _, fixtureName := range []string{"mysql-multi-table", "postgres-multi-schema"} {
		t.Run(fixtureName, func(t *testing.T) {
			fixture := readGoctlDatasourceReplayFixture(t, fixtureName)
			ir := rawModelSchemaIRFromReplayFixture(t, fixture)
			dir := t.TempDir()
			writeGeneratedModule(t, dir, fixture.Module)
			if err := generateModelFromSchemaIR(ir, modelSchemaGenerationOptions{
				Tables:        fixture.Tables,
				IgnoreColumns: fixture.IgnoreColumns,
				Prefix:        fixture.Prefix,
				Strict:        fixture.Strict,
				Emit: modelSchemaEmitOptions{
					Dir:          dir,
					Package:      fixture.Package,
					Module:       fixture.Module,
					Style:        fixture.Style,
					Cache:        fixture.Cache,
					GoZeroLayout: isGoZeroModelStyle(fixture.Style),
				},
			}); err != nil {
				t.Fatalf("generateModelFromSchemaIR replay %s: %v", fixture.ID, err)
			}
			for _, rel := range replayIRGeneratedArtifacts(fixtureName) {
				if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
					t.Fatalf("replay IR generated artifact %s: %v", rel, err)
				}
			}
			runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
			runGoCommand(t, dir, 3*time.Minute, "test", "./...")
		})
	}
}

func replayIRGeneratedArtifacts(name string) []string {
	switch name {
	case "postgres-multi-schema":
		return []string{
			"model/account_gen.go",
			"model/event_gen.go",
			"repo/account.go",
			"repo/event.go",
		}
	default:
		return []string{
			"model/customer_gen.go",
			"model/order_gen.go",
			"repo/customer.go",
			"repo/order.go",
		}
	}
}

func TestIntrospectSQLTablesWithFakeDatasource(t *testing.T) {
	db, err := sql.Open(fakeModelDatasourceDriver, "ok")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tables, err := introspectSQLTables(context.Background(), db, datasourceIntrospectionOptions{Driver: "mysql", Tables: []string{"users", "audit_logs"}})
	if err != nil {
		t.Fatalf("introspectSQLTables: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("tables = %#v, want two tables", tables)
	}
	if tables[0].Name != "users" || tables[0].PrimaryKey != "id" || len(tables[0].Columns) != 4 {
		t.Fatalf("users table = %#v, want id primary key and four columns", tables[0])
	}
	users := tables[0]
	if users.Columns[1].Name != "email" || users.Columns[1].Type != "varchar" || !users.Columns[1].Nullable || !users.Columns[1].Unique {
		t.Fatalf("email column = %#v, want normalized nullable varchar", tables[0].Columns[1])
	}
	if len(users.Indexes) != 1 || strings.Join(users.Indexes[0].Columns, ",") != "name,created_at" {
		t.Fatalf("users indexes = %#v, want name,created_at index", users.Indexes)
	}
	if tables[1].PrimaryKey != "created_at" || !tables[1].Columns[0].PrimaryKey || tables[1].Columns[0].Type != "timestamptz" {
		t.Fatalf("audit table = %#v, want fallback primary key with normalized timestamptz", tables[1])
	}

	emptyDB, err := sql.Open(fakeModelDatasourceDriver, "query-empty")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = emptyDB.Close() })
	if _, err := introspectSQLTables(context.Background(), emptyDB, datasourceIntrospectionOptions{Driver: "mysql"}); err == nil || !strings.Contains(err.Error(), "model table is required") {
		t.Fatalf("introspectSQLTables empty error = %v, want model table required", err)
	}

	queryErrDB, err := sql.Open(fakeModelDatasourceDriver, "query-error")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queryErrDB.Close() })
	if _, err := introspectSQLTables(context.Background(), queryErrDB, datasourceIntrospectionOptions{Driver: "mysql"}); err == nil || !strings.Contains(err.Error(), "query datasource schema") {
		t.Fatalf("introspectSQLTables query error = %v, want query datasource schema", err)
	}

	indexQueryErrDB, err := sql.Open(fakeModelDatasourceDriver, "index-query-error")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = indexQueryErrDB.Close() })
	if _, err := introspectSQLTables(context.Background(), indexQueryErrDB, datasourceIntrospectionOptions{Driver: "mysql"}); err == nil || !strings.Contains(err.Error(), "query datasource indexes") {
		t.Fatalf("introspectSQLTables index query error = %v, want query datasource indexes", err)
	}
}

func TestDatasourceIntrospectionGeneratesIndexAndCacheTemplates(t *testing.T) {
	db, err := sql.Open(fakeModelDatasourceDriver, "ok")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tables, err := introspectSQLTables(context.Background(), db, datasourceIntrospectionOptions{Driver: "mysql", Tables: []string{"users"}})
	if err != nil {
		t.Fatalf("introspectSQLTables: %v", err)
	}
	tables, err = prepareModelTables(tables, modelGenerationOptions{Tables: []string{"users"}})
	if err != nil {
		t.Fatalf("prepareModelTables: %v", err)
	}
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/datasource")
	if err := writeModelFiles(tables, dir, "model", "example.com/datasource", modelStyleSQL, true, storage.DialectQuestion); err != nil {
		t.Fatalf("writeModelFiles: %v", err)
	}
	repo, err := os.ReadFile(filepath.Join(dir, "model", "repo", "user.go"))
	if err != nil {
		t.Fatal(err)
	}
	repoOut := string(repo)
	for _, want := range []string{
		"func (r *UserRepo) FindByEmail(ctx context.Context, email *string) (*entity.User, error)",
		"func (r *UserRepo) FindByName(ctx context.Context, name string, limit int, offset int) ([]entity.User, error)",
		"func (r *UserRepo) CountByName(ctx context.Context, name string) (int64, error)",
		"func (c *CachedUserRepo) FindByEmailCached(ctx context.Context, email *string) (*entity.User, error)",
		"func (c *CachedUserRepo) FindByNameCached(ctx context.Context, name string, limit int, offset int) ([]entity.User, error)",
		"func (c *CachedUserRepo) CountByNameCached(ctx context.Context, name string) (int64, error)",
		"func (c *CachedUserRepo) PageByNameCached(ctx context.Context, name string, limit int, offset int) ([]entity.User, int64, error)",
		"func (c *CachedUserRepo) FindByNameForUpdate(ctx context.Context, name string, limit int, offset int) ([]entity.User, error)",
		"return c.repo.FindByNameForUpdate(ctx, name, limit, offset)",
		"func (c *CachedUserRepo) FindByNameForUpdateSkipLocked(ctx context.Context, name string, limit int, offset int) ([]entity.User, error)",
		"func (c *CachedUserRepo) InsertMany(ctx context.Context, items []*entity.User) error",
		"func (c *CachedUserRepo) UpdateManyWithInvalidate(ctx context.Context, items []*entity.User) error",
		"func (c *CachedUserRepo) DeleteMany(ctx context.Context, ids ...int64) error",
		"func (c *CachedUserRepo) FindOneForUpdate(ctx context.Context, id int64) (*entity.User, error)",
		"return c.repo.FindOneForUpdate(ctx, id)",
		"func (c *CachedUserRepo) FindByEmailForUpdate(ctx context.Context, email *string) (*entity.User, error)",
		"return c.repo.FindByEmailForUpdate(ctx, email)",
		"func (c *CachedUserRepo) FindByEmailForUpdateSkipLocked(ctx context.Context, email *string) (*entity.User, error)",
		"cacheByEmail",
		"cache.WithNegativeCache[*entity.User](30*time.Second, storage.ErrNotFound)",
		"c.cacheByEmail.Cache().GetOrLoad(ctx, key, func(ctx context.Context, key string) (*entity.User, error) {",
		"listCacheByName",
		"countCacheByName",
		"c.cacheByEmail.Cache().Delete(uniqueKeyByEmail(old.Email))",
		"c.listCacheByName.Clear()",
		"c.countCacheByName.Clear()",
		"func (c *RedisCachedUserRepo) FindByNameCached(ctx context.Context, name string, limit int, offset int) ([]entity.User, error)",
		"func (c *RedisCachedUserRepo) CountByNameCached(ctx context.Context, name string) (int64, error)",
		"func (c *RedisCachedUserRepo) PageByNameCached(ctx context.Context, name string, limit int, offset int) ([]entity.User, int64, error)",
		"func (c *RedisCachedUserRepo) FindByNameForUpdate(ctx context.Context, name string, limit int, offset int) ([]entity.User, error)",
		"return c.repo.FindByNameForUpdate(ctx, name, limit, offset)",
		"func (c *RedisCachedUserRepo) FindByNameForUpdateSkipLocked(ctx context.Context, name string, limit int, offset int) ([]entity.User, error)",
		"func (c *RedisCachedUserRepo) InsertMany(ctx context.Context, items []*entity.User) error",
		"func (c *RedisCachedUserRepo) UpdateManyWithInvalidate(ctx context.Context, items []*entity.User) error",
		"func (c *RedisCachedUserRepo) DeleteMany(ctx context.Context, ids ...int64) error",
		"func (c *RedisCachedUserRepo) FindOneForUpdate(ctx context.Context, id int64) (*entity.User, error)",
		"return c.repo.FindOneForUpdate(ctx, id)",
		"func (c *RedisCachedUserRepo) FindByEmailForUpdate(ctx context.Context, email *string) (*entity.User, error)",
		"return c.repo.FindByEmailForUpdate(ctx, email)",
		"func (c *RedisCachedUserRepo) FindByEmailForUpdateSkipLocked(ctx context.Context, email *string) (*entity.User, error)",
		"listVersionByName",
		"key := redisUserIndexListCacheKey(version, indexListKeyByName(name, limit, offset))",
		"key := redisUserIndexListCacheKey(version, indexCountKeyByName(name))",
		"c.listVersionByName.Set(ctx, \"current\", redisUserIndexListVersionValue())",
	} {
		if !strings.Contains(repoOut, want) {
			t.Fatalf("generated datasource model/cache repo missing %q:\n%s", want, repoOut)
		}
	}
	runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
	runGoCommand(t, dir, 3*time.Minute, "test", "./...")
}

func TestDatasourceIntrospectionMultiTableGoctlCacheReplay(t *testing.T) {
	db, err := sql.Open(fakeModelDatasourceDriver, "multi-table")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tables, err := introspectSQLTables(context.Background(), db, datasourceIntrospectionOptions{
		Driver: "mysql",
		Tables: []string{"app_customers", "app_orders"},
	})
	if err != nil {
		t.Fatalf("introspectSQLTables: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("tables = %#v, want two datasource tables", tables)
	}
	customers := tables[0]
	if customers.Name != "app_customers" || customers.PrimaryKey != "id" {
		t.Fatalf("customers table = %#v, want app_customers with id primary key", customers)
	}
	if len(customers.UniqueIndexes) != 1 || strings.Join(customers.UniqueIndexes[0].Columns, ",") != "tenant_id,external_id" {
		t.Fatalf("customers unique indexes = %#v, want tenant_id,external_id", customers.UniqueIndexes)
	}
	if len(customers.Indexes) != 1 || strings.Join(customers.Indexes[0].Columns, ",") != "tenant_id,email" {
		t.Fatalf("customers indexes = %#v, want tenant_id,email", customers.Indexes)
	}
	orders := tables[1]
	if orders.Name != "app_orders" || orders.PrimaryKey != "id" {
		t.Fatalf("orders table = %#v, want app_orders with id primary key", orders)
	}
	if len(orders.UniqueIndexes) != 1 || strings.Join(orders.UniqueIndexes[0].Columns, ",") != "tenant_id,order_no" {
		t.Fatalf("orders unique indexes = %#v, want tenant_id,order_no", orders.UniqueIndexes)
	}
	if len(orders.Indexes) != 2 {
		t.Fatalf("orders indexes = %#v, want two non-unique indexes", orders.Indexes)
	}

	tables, err = prepareModelTables(tables, modelGenerationOptions{
		Tables:        []string{"app_customers", "app_orders"},
		Prefix:        "app_",
		IgnoreColumns: []string{"created_by", "updated_by"},
		Strict:        true,
	})
	if err != nil {
		t.Fatalf("prepareModelTables: %v", err)
	}
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/datasource-multi")
	if err := writeModelFiles(tables, dir, "model", "example.com/datasource-multi", modelStyleSQL, true, storage.DialectMySQL); err != nil {
		t.Fatalf("writeModelFiles: %v", err)
	}

	customerEntity, err := os.ReadFile(filepath.Join(dir, "model", "entity", "customer_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	customerEntityOut := string(customerEntity)
	for _, want := range []string{
		`const CustomerTable = "customers"`,
		`db:"email" json:"email"`,
		`db:"version" json:"version"`,
	} {
		if !strings.Contains(customerEntityOut, want) {
			t.Fatalf("generated datasource customer entity missing %q:\n%s", want, customerEntityOut)
		}
	}
	for _, unexpected := range []string{"CreatedBy", "UpdatedBy"} {
		if strings.Contains(customerEntityOut, unexpected) {
			t.Fatalf("generated datasource customer entity should ignore %q:\n%s", unexpected, customerEntityOut)
		}
	}

	customerRepo, err := os.ReadFile(filepath.Join(dir, "model", "repo", "customer.go"))
	if err != nil {
		t.Fatal(err)
	}
	customerRepoOut := string(customerRepo)
	for _, want := range []string{
		"d := storage.DialectMySQL",
		"func (r *CustomerRepo) FindByTenantIDAndExternalID(ctx context.Context, tenantID int64, externalID string) (*entity.Customer, error)",
		"func (r *CustomerRepo) FindByTenantID(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Customer, error)",
		"func (r *CustomerRepo) CountByTenantID(ctx context.Context, tenantID int64) (int64, error)",
		"func (r *CustomerRepo) UpsertByTenantIDAndExternalID(ctx context.Context, in *entity.Customer) error",
		"storage.Upsert(entity.CustomerTable, entity.CustomerColumns, []string{\"tenant_id\", \"external_id\"}",
		"func (r *CustomerRepo) FindOneForUpdate(ctx context.Context, id int64) (*entity.Customer, error)",
		"storage.SelectForUpdate(entity.CustomerTable, entity.CustomerColumns, \"id\", r.dialect, false)",
		"func (r *CustomerRepo) FindOneForUpdateSkipLocked(ctx context.Context, id int64) (*entity.Customer, error)",
		"storage.SelectForUpdate(entity.CustomerTable, entity.CustomerColumns, \"id\", r.dialect, true)",
		"func (r *CustomerRepo) FindByTenantIDAndExternalIDForUpdate(ctx context.Context, tenantID int64, externalID string) (*entity.Customer, error)",
		"query, args, err := storage.SelectWhere(entity.CustomerTable, entity.CustomerColumns, where, r.dialect)",
		"where = where.Limit(1)",
		`where = where.IsNull("deleted_at")`,
		"args...); err != nil",
		"func (r *CustomerRepo) FindByTenantIDAndExternalIDForUpdateSkipLocked(ctx context.Context, tenantID int64, externalID string) (*entity.Customer, error)",
		`query += " SKIP LOCKED"`,
		"args := make([]any, 0, len(items)*len(entity.CustomerColumns))",
		"query, err := storage.BatchInsert(entity.CustomerTable, entity.CustomerColumns, rows, r.dialect)",
		"func (r *CustomerRepo) FindByTenantIDForUpdate(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Customer, error)",
		"func (r *CustomerRepo) FindByTenantIDForUpdateSkipLocked(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Customer, error)",
		`query += " FOR UPDATE"`,
		`query += " SKIP LOCKED"`,
		"func (c *CachedCustomerRepo) FindByTenantIDAndExternalIDCached(ctx context.Context, tenantID int64, externalID string) (*entity.Customer, error)",
		"func (c *CachedCustomerRepo) FindOneForUpdate(ctx context.Context, id int64) (*entity.Customer, error)",
		"func (c *CachedCustomerRepo) FindByTenantIDAndExternalIDForUpdate(ctx context.Context, tenantID int64, externalID string) (*entity.Customer, error)",
		"func (c *CachedCustomerRepo) FindByTenantIDForUpdate(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Customer, error)",
		"return c.repo.FindByTenantIDForUpdate(ctx, tenantID, limit, offset)",
		"func (c *CachedCustomerRepo) FindByTenantIDForUpdateSkipLocked(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Customer, error)",
		"func (c *CachedCustomerRepo) UpsertByTenantIDAndExternalID(ctx context.Context, in *entity.Customer) error",
		"func (c *CachedCustomerRepo) PageByTenantIDCached(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Customer, int64, error)",
		"func (c *RedisCachedCustomerRepo) FindByTenantIDAndExternalIDForUpdateSkipLocked(ctx context.Context, tenantID int64, externalID string) (*entity.Customer, error)",
		"func (c *RedisCachedCustomerRepo) FindByTenantIDForUpdateSkipLocked(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Customer, error)",
		"func (c *RedisCachedCustomerRepo) UpsertByTenantIDAndExternalID(ctx context.Context, in *entity.Customer) error",
		"func (c *RedisCachedCustomerRepo) PageByTenantIDCached(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Customer, int64, error)",
		"key := redisCustomerIndexListCacheKey(version, indexListKeyByTenantID(tenantID, limit, offset))",
		"c.listVersionByTenantID.Set(ctx, \"current\", redisCustomerIndexListVersionValue())",
		`query += " AND deleted_at IS NULL"`,
	} {
		if !strings.Contains(customerRepoOut, want) {
			t.Fatalf("generated datasource customer repo missing %q:\n%s", want, customerRepoOut)
		}
	}

	orderRepo, err := os.ReadFile(filepath.Join(dir, "model", "repo", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	orderRepoOut := string(orderRepo)
	for _, want := range []string{
		"d := storage.DialectMySQL",
		"func (r *OrderRepo) FindByTenantIDAndOrderNo(ctx context.Context, tenantID int64, orderNo string) (*entity.Order, error)",
		"func (r *OrderRepo) FindByCustomerID(ctx context.Context, customerID int64, limit int, offset int) ([]entity.Order, error)",
		"func (r *OrderRepo) CountByCustomerID(ctx context.Context, customerID int64) (int64, error)",
		"func (r *OrderRepo) UpsertByTenantIDAndOrderNo(ctx context.Context, in *entity.Order) error",
		"storage.Upsert(entity.OrderTable, entity.OrderColumns, []string{\"tenant_id\", \"order_no\"}",
		"func (r *OrderRepo) FindOneForUpdate(ctx context.Context, id int64) (*entity.Order, error)",
		"storage.SelectForUpdate(entity.OrderTable, entity.OrderColumns, \"id\", r.dialect, false)",
		"func (r *OrderRepo) FindOneForUpdateSkipLocked(ctx context.Context, id int64) (*entity.Order, error)",
		"storage.SelectForUpdate(entity.OrderTable, entity.OrderColumns, \"id\", r.dialect, true)",
		"func (r *OrderRepo) FindByTenantIDAndOrderNoForUpdate(ctx context.Context, tenantID int64, orderNo string) (*entity.Order, error)",
		"func (r *OrderRepo) FindByTenantIDAndOrderNoForUpdateSkipLocked(ctx context.Context, tenantID int64, orderNo string) (*entity.Order, error)",
		"args := make([]any, 0, len(items)*len(entity.OrderColumns))",
		"query, err := storage.BatchInsert(entity.OrderTable, entity.OrderColumns, rows, r.dialect)",
		"func (r *OrderRepo) FindByTenantIDAndStatus(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Order, error)",
		"func (r *OrderRepo) FindByTenantIDAndStatusForUpdate(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Order, error)",
		"func (r *OrderRepo) FindByTenantIDAndStatusForUpdateSkipLocked(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Order, error)",
		"func (r *OrderRepo) ClaimByTenantIDAndStatusSkipLocked(ctx context.Context, tenantID int64, status string, nextStatus string, limit int) ([]entity.Order, error)",
		"items, err := txRepo.FindByTenantIDAndStatusForUpdateSkipLocked(ctx, tenantID, status, limit, 0)",
		"func (r *OrderRepo) updateClaimedStatusByID(ctx context.Context, ids []int64, nextStatus string) error",
		`query := "UPDATE " + entity.OrderTable + " SET status = " + storage.Placeholder(r.dialect, 1) + " WHERE id IN (" + strings.Join(placeholders, ", ") + ")"`,
		"if err := txRepo.updateClaimedStatusByID(ctx, ids, nextStatus); err != nil",
		"items[i].Status = nextStatus",
		"func (c *CachedOrderRepo) ClaimByTenantIDAndStatusSkipLocked(ctx context.Context, tenantID int64, status string, nextStatus string, limit int) ([]entity.Order, error)",
		"items, err := txRepo.claimByTenantIDAndStatusSkipLocked(ctx, tenantID, status, nextStatus, limit)",
		"func (c *CachedOrderRepo) claimByTenantIDAndStatusSkipLocked(ctx context.Context, tenantID int64, status string, nextStatus string, limit int) ([]entity.Order, error)",
		"if err := c.repo.updateClaimedStatusByID(ctx, ids, nextStatus); err != nil",
		"if err := c.afterUpdateCommit(ctx, &updatedItems[i], &oldItems[i]); err != nil",
		"func (c *CachedOrderRepo) FindByTenantIDAndOrderNoForUpdate(ctx context.Context, tenantID int64, orderNo string) (*entity.Order, error)",
		"func (c *CachedOrderRepo) FindByTenantIDAndStatusForUpdate(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Order, error)",
		"return c.repo.FindByTenantIDAndStatusForUpdate(ctx, tenantID, status, limit, offset)",
		"func (c *CachedOrderRepo) UpsertByTenantIDAndOrderNo(ctx context.Context, in *entity.Order) error",
		"func (c *CachedOrderRepo) PageByCustomerIDCached(ctx context.Context, customerID int64, limit int, offset int) ([]entity.Order, int64, error)",
		"func (c *CachedOrderRepo) PageByTenantIDAndStatusCached(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Order, int64, error)",
		"func (c *RedisCachedOrderRepo) ClaimByTenantIDAndStatusSkipLocked(ctx context.Context, tenantID int64, status string, nextStatus string, limit int) ([]entity.Order, error)",
		"func (c *RedisCachedOrderRepo) claimByTenantIDAndStatusSkipLocked(ctx context.Context, tenantID int64, status string, nextStatus string, limit int) ([]entity.Order, error)",
		"if err := c.afterUpdateCommit(ctx, &updatedItems[i]); err != nil",
		"func (c *RedisCachedOrderRepo) FindByTenantIDAndOrderNoForUpdateSkipLocked(ctx context.Context, tenantID int64, orderNo string) (*entity.Order, error)",
		"func (c *RedisCachedOrderRepo) FindByTenantIDAndStatusForUpdateSkipLocked(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Order, error)",
		"func (c *RedisCachedOrderRepo) UpsertByTenantIDAndOrderNo(ctx context.Context, in *entity.Order) error",
		"func (c *RedisCachedOrderRepo) PageByTenantIDAndStatusCached(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Order, int64, error)",
		"key := redisOrderIndexListCacheKey(version, indexListKeyByCustomerID(customerID, limit, offset))",
		"key := redisOrderIndexListCacheKey(version, indexListKeyByTenantIDAndStatus(tenantID, status, limit, offset))",
		"c.listVersionByCustomerID.Set(ctx, \"current\", redisOrderIndexListVersionValue())",
		"c.listVersionByTenantIDAndStatus.Set(ctx, \"current\", redisOrderIndexListVersionValue())",
	} {
		if !strings.Contains(orderRepoOut, want) {
			t.Fatalf("generated datasource order repo missing %q:\n%s", want, orderRepoOut)
		}
	}

	runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
	runGoCommand(t, dir, 3*time.Minute, "test", "./...")
}

func TestGenerateModelFromDatasourceMultiTableReplayCompiles(t *testing.T) {
	fixture := readGoctlDatasourceReplayFixture(t, "mysql-multi-table")
	oldDriverName := modelDatasourceDriverName
	modelDatasourceDriverName = func(driver string) string {
		if driver == "mysql" {
			return fakeModelDatasourceDriver
		}
		return oldDriverName(driver)
	}
	t.Cleanup(func() { modelDatasourceDriverName = oldDriverName })

	dir := t.TempDir()
	writeGeneratedModule(t, dir, fixture.Module)
	if err := GenerateModelFromDatasource(ModelDatasourceOptions{
		Driver:        fixture.Driver,
		DSN:           fixture.DSN,
		Dir:           dir,
		Package:       fixture.Package,
		Module:        fixture.Module,
		Tables:        fixture.Tables,
		Prefix:        fixture.Prefix,
		IgnoreColumns: fixture.IgnoreColumns,
		Strict:        fixture.Strict,
		Cache:         fixture.Cache,
	}); err != nil {
		t.Fatalf("GenerateModelFromDatasource multi-table replay: %v", err)
	}
	assertGoctlDatasourceReplayFixture(t, dir, fixture)
	runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
	runGoCommand(t, dir, 3*time.Minute, "test", "./...")
}

func TestGenerateModelFromPostgresDatasourceMultiSchemaReplayCompiles(t *testing.T) {
	fixture := readGoctlDatasourceReplayFixture(t, "postgres-multi-schema")
	oldDriverName := modelDatasourceDriverName
	modelDatasourceDriverName = func(driver string) string {
		if driver == "postgres" || driver == "postgresql" || driver == "pg" {
			return fakeModelDatasourceDriver
		}
		return oldDriverName(driver)
	}
	t.Cleanup(func() { modelDatasourceDriverName = oldDriverName })

	dir := t.TempDir()
	writeGeneratedModule(t, dir, fixture.Module)
	if err := GenerateModelFromDatasource(ModelDatasourceOptions{
		Driver:        fixture.Driver,
		DSN:           fixture.DSN,
		Dir:           dir,
		Package:       fixture.Package,
		Module:        fixture.Module,
		Tables:        fixture.Tables,
		Schema:        fixture.SchemaName,
		Prefix:        fixture.Prefix,
		IgnoreColumns: fixture.IgnoreColumns,
		Strict:        fixture.Strict,
		Cache:         fixture.Cache,
	}); err != nil {
		t.Fatalf("GenerateModelFromDatasource postgres multi-schema replay: %v", err)
	}
	assertGoctlDatasourceReplayFixture(t, dir, fixture)
	runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
	runGoCommand(t, dir, 3*time.Minute, "test", "./...")
}

func TestPostgresDatasourceIntrospectionMultiSchemaCacheReplay(t *testing.T) {
	db, err := sql.Open(fakeModelDatasourceDriver, "postgres-multi-schema")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tables, err := introspectSQLTables(context.Background(), db, datasourceIntrospectionOptions{
		Driver: "postgres",
		Schema: "billing",
		Tables: []string{"billing_accounts", "billing_events"},
	})
	if err != nil {
		t.Fatalf("introspectSQLTables postgres: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("postgres tables = %#v, want two datasource tables", tables)
	}
	accounts := tables[0]
	if accounts.Name != "billing_accounts" || accounts.PrimaryKey != "id" {
		t.Fatalf("accounts table = %#v, want billing_accounts with id primary key", accounts)
	}
	if accounts.Columns[3].Type != "varchar" || !accounts.Columns[3].Nullable {
		t.Fatalf("accounts email column = %#v, want nullable normalized varchar", accounts.Columns[3])
	}
	if accounts.Columns[4].Type != "jsonb" || accounts.Columns[6].Type != "timestamptz" {
		t.Fatalf("accounts postgres types = %#v, want jsonb and timestamptz normalization", accounts.Columns)
	}
	if len(accounts.UniqueIndexes) != 1 || strings.Join(accounts.UniqueIndexes[0].Columns, ",") != "tenant_id,external_ref" {
		t.Fatalf("accounts unique indexes = %#v, want tenant_id,external_ref", accounts.UniqueIndexes)
	}
	if len(accounts.Indexes) != 1 || strings.Join(accounts.Indexes[0].Columns, ",") != "tenant_id,email" {
		t.Fatalf("accounts indexes = %#v, want tenant_id,email", accounts.Indexes)
	}
	events := tables[1]
	if events.Columns[5].Type != "numeric" || events.Columns[6].Type != "timestamp" || events.Columns[7].Type != "timestamptz" {
		t.Fatalf("events postgres types = %#v, want numeric, timestamp and timestamptz normalization", events.Columns)
	}
	if len(events.UniqueIndexes) != 1 || strings.Join(events.UniqueIndexes[0].Columns, ",") != "tenant_id,event_no" {
		t.Fatalf("events unique indexes = %#v, want tenant_id,event_no", events.UniqueIndexes)
	}
	if len(events.Indexes) != 2 {
		t.Fatalf("events indexes = %#v, want two non-unique indexes", events.Indexes)
	}

	tables, err = prepareModelTables(tables, modelGenerationOptions{
		Tables:        []string{"billing_accounts", "billing_events"},
		Prefix:        "billing_",
		IgnoreColumns: []string{"created_by", "updated_by"},
		Strict:        true,
	})
	if err != nil {
		t.Fatalf("prepareModelTables postgres: %v", err)
	}
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/postgres-datasource")
	if err := writeModelFiles(tables, dir, "model", "example.com/postgres-datasource", modelStyleSQL, true, storage.DialectPostgres); err != nil {
		t.Fatalf("writeModelFiles postgres: %v", err)
	}

	accountEntity, err := os.ReadFile(filepath.Join(dir, "model", "entity", "account_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	accountEntityOut := string(accountEntity)
	for _, want := range []string{
		`const AccountTable = "accounts"`,
		`ExternalRef string`,
		`db:"email" json:"email"`,
		`db:"metadata" json:"metadata"`,
		`db:"deleted_at" json:"deletedAt"`,
	} {
		if !strings.Contains(accountEntityOut, want) {
			t.Fatalf("generated postgres account entity missing %q:\n%s", want, accountEntityOut)
		}
	}
	for _, unexpected := range []string{"CreatedBy", "UpdatedBy"} {
		if strings.Contains(accountEntityOut, unexpected) {
			t.Fatalf("generated postgres account entity should ignore %q:\n%s", unexpected, accountEntityOut)
		}
	}

	accountRepo, err := os.ReadFile(filepath.Join(dir, "model", "repo", "account.go"))
	if err != nil {
		t.Fatal(err)
	}
	accountRepoOut := string(accountRepo)
	for _, want := range []string{
		"d := storage.DialectPostgres",
		"func (r *AccountRepo) FindByTenantIDAndExternalRef(ctx context.Context, tenantID int64, externalRef string) (*entity.Account, error)",
		"func (r *AccountRepo) FindByTenantID(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Account, error)",
		"func (r *AccountRepo) UpsertByTenantIDAndExternalRef(ctx context.Context, in *entity.Account) error",
		"storage.Upsert(entity.AccountTable, entity.AccountColumns, []string{\"tenant_id\", \"external_ref\"}",
		"func (r *AccountRepo) FindOneForUpdate(ctx context.Context, id int64) (*entity.Account, error)",
		"storage.SelectForUpdate(entity.AccountTable, entity.AccountColumns, \"id\", r.dialect, false)",
		"func (r *AccountRepo) FindOneForUpdateSkipLocked(ctx context.Context, id int64) (*entity.Account, error)",
		"storage.SelectForUpdate(entity.AccountTable, entity.AccountColumns, \"id\", r.dialect, true)",
		"func (r *AccountRepo) FindByTenantIDAndExternalRefForUpdate(ctx context.Context, tenantID int64, externalRef string) (*entity.Account, error)",
		"query, args, err := storage.SelectWhere(entity.AccountTable, entity.AccountColumns, where, r.dialect)",
		"where = where.Limit(1)",
		`where = where.IsNull("deleted_at")`,
		"args...); err != nil",
		"func (r *AccountRepo) FindByTenantIDAndExternalRefForUpdateSkipLocked(ctx context.Context, tenantID int64, externalRef string) (*entity.Account, error)",
		"args := make([]any, 0, len(items)*len(entity.AccountColumns))",
		"query, err := storage.BatchInsert(entity.AccountTable, entity.AccountColumns, rows, r.dialect)",
		"func (r *AccountRepo) FindByTenantIDForUpdate(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Account, error)",
		"func (r *AccountRepo) FindByTenantIDForUpdateSkipLocked(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Account, error)",
		"func (c *CachedAccountRepo) FindByTenantIDAndExternalRefCached(ctx context.Context, tenantID int64, externalRef string) (*entity.Account, error)",
		"func (c *CachedAccountRepo) FindOneForUpdate(ctx context.Context, id int64) (*entity.Account, error)",
		"func (c *CachedAccountRepo) FindByTenantIDAndExternalRefForUpdate(ctx context.Context, tenantID int64, externalRef string) (*entity.Account, error)",
		"func (c *CachedAccountRepo) FindByTenantIDForUpdate(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Account, error)",
		"return c.repo.FindByTenantIDForUpdate(ctx, tenantID, limit, offset)",
		"func (c *CachedAccountRepo) FindByTenantIDForUpdateSkipLocked(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Account, error)",
		"func (c *CachedAccountRepo) UpsertByTenantIDAndExternalRef(ctx context.Context, in *entity.Account) error",
		"func (c *CachedAccountRepo) PageByTenantIDCached(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Account, int64, error)",
		"func (c *RedisCachedAccountRepo) FindByTenantIDAndExternalRefForUpdateSkipLocked(ctx context.Context, tenantID int64, externalRef string) (*entity.Account, error)",
		"func (c *RedisCachedAccountRepo) FindByTenantIDForUpdateSkipLocked(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Account, error)",
		"func (c *RedisCachedAccountRepo) UpsertByTenantIDAndExternalRef(ctx context.Context, in *entity.Account) error",
		"func (c *RedisCachedAccountRepo) PageByTenantIDCached(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Account, int64, error)",
		"key := redisAccountIndexListCacheKey(version, indexListKeyByTenantID(tenantID, limit, offset))",
		"c.listVersionByTenantID.Set(ctx, \"current\", redisAccountIndexListVersionValue())",
		`query += " AND deleted_at IS NULL"`,
	} {
		if !strings.Contains(accountRepoOut, want) {
			t.Fatalf("generated postgres account repo missing %q:\n%s", want, accountRepoOut)
		}
	}

	eventEntity, err := os.ReadFile(filepath.Join(dir, "model", "entity", "event_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	eventEntityOut := string(eventEntity)
	for _, want := range []string{
		`const EventTable = "events"`,
		`db:"amount" json:"amount"`,
		`db:"occurred_at" json:"occurredAt"`,
		`db:"deleted_at" json:"deletedAt"`,
	} {
		if !strings.Contains(eventEntityOut, want) {
			t.Fatalf("generated postgres event entity missing %q:\n%s", want, eventEntityOut)
		}
	}

	eventRepo, err := os.ReadFile(filepath.Join(dir, "model", "repo", "event.go"))
	if err != nil {
		t.Fatal(err)
	}
	eventRepoOut := string(eventRepo)
	for _, want := range []string{
		"d := storage.DialectPostgres",
		"func (r *EventRepo) FindByTenantIDAndEventNo(ctx context.Context, tenantID int64, eventNo string) (*entity.Event, error)",
		"func (r *EventRepo) FindByAccountID(ctx context.Context, accountID int64, limit int, offset int) ([]entity.Event, error)",
		"func (r *EventRepo) FindByTenantIDAndStatus(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Event, error)",
		"func (r *EventRepo) UpsertByTenantIDAndEventNo(ctx context.Context, in *entity.Event) error",
		"storage.Upsert(entity.EventTable, entity.EventColumns, []string{\"tenant_id\", \"event_no\"}",
		"func (r *EventRepo) FindOneForUpdate(ctx context.Context, id int64) (*entity.Event, error)",
		"storage.SelectForUpdate(entity.EventTable, entity.EventColumns, \"id\", r.dialect, false)",
		"func (r *EventRepo) FindOneForUpdateSkipLocked(ctx context.Context, id int64) (*entity.Event, error)",
		"storage.SelectForUpdate(entity.EventTable, entity.EventColumns, \"id\", r.dialect, true)",
		"func (r *EventRepo) FindByTenantIDAndEventNoForUpdate(ctx context.Context, tenantID int64, eventNo string) (*entity.Event, error)",
		"func (r *EventRepo) FindByTenantIDAndEventNoForUpdateSkipLocked(ctx context.Context, tenantID int64, eventNo string) (*entity.Event, error)",
		"args := make([]any, 0, len(items)*len(entity.EventColumns))",
		"query, err := storage.BatchInsert(entity.EventTable, entity.EventColumns, rows, r.dialect)",
		"func (r *EventRepo) FindByTenantIDAndStatusForUpdate(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Event, error)",
		"func (r *EventRepo) FindByTenantIDAndStatusForUpdateSkipLocked(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Event, error)",
		"func (r *EventRepo) ClaimByTenantIDAndStatusSkipLocked(ctx context.Context, tenantID int64, status string, nextStatus string, limit int) ([]entity.Event, error)",
		"items, err := txRepo.FindByTenantIDAndStatusForUpdateSkipLocked(ctx, tenantID, status, limit, 0)",
		"func (r *EventRepo) updateClaimedStatusByID(ctx context.Context, ids []int64, nextStatus string) error",
		`query := "UPDATE " + entity.EventTable + " SET status = " + storage.Placeholder(r.dialect, 1) + " WHERE id IN (" + strings.Join(placeholders, ", ") + ")"`,
		"if err := txRepo.updateClaimedStatusByID(ctx, ids, nextStatus); err != nil",
		"items[i].Status = nextStatus",
		"func (c *CachedEventRepo) ClaimByTenantIDAndStatusSkipLocked(ctx context.Context, tenantID int64, status string, nextStatus string, limit int) ([]entity.Event, error)",
		"items, err := txRepo.claimByTenantIDAndStatusSkipLocked(ctx, tenantID, status, nextStatus, limit)",
		"func (c *CachedEventRepo) claimByTenantIDAndStatusSkipLocked(ctx context.Context, tenantID int64, status string, nextStatus string, limit int) ([]entity.Event, error)",
		"if err := c.repo.updateClaimedStatusByID(ctx, ids, nextStatus); err != nil",
		"if err := c.afterUpdateCommit(ctx, &updatedItems[i], &oldItems[i]); err != nil",
		"func (c *CachedEventRepo) FindByTenantIDAndEventNoForUpdate(ctx context.Context, tenantID int64, eventNo string) (*entity.Event, error)",
		"func (c *CachedEventRepo) FindByTenantIDAndStatusForUpdate(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Event, error)",
		"return c.repo.FindByTenantIDAndStatusForUpdate(ctx, tenantID, status, limit, offset)",
		"func (c *CachedEventRepo) UpsertByTenantIDAndEventNo(ctx context.Context, in *entity.Event) error",
		"func (c *CachedEventRepo) PageByAccountIDCached(ctx context.Context, accountID int64, limit int, offset int) ([]entity.Event, int64, error)",
		"func (c *CachedEventRepo) PageByTenantIDAndStatusCached(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Event, int64, error)",
		"func (c *RedisCachedEventRepo) ClaimByTenantIDAndStatusSkipLocked(ctx context.Context, tenantID int64, status string, nextStatus string, limit int) ([]entity.Event, error)",
		"func (c *RedisCachedEventRepo) claimByTenantIDAndStatusSkipLocked(ctx context.Context, tenantID int64, status string, nextStatus string, limit int) ([]entity.Event, error)",
		"if err := c.afterUpdateCommit(ctx, &updatedItems[i]); err != nil",
		"func (c *RedisCachedEventRepo) FindByTenantIDAndEventNoForUpdateSkipLocked(ctx context.Context, tenantID int64, eventNo string) (*entity.Event, error)",
		"func (c *RedisCachedEventRepo) FindByTenantIDAndStatusForUpdateSkipLocked(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Event, error)",
		"func (c *RedisCachedEventRepo) UpsertByTenantIDAndEventNo(ctx context.Context, in *entity.Event) error",
		"func (c *RedisCachedEventRepo) PageByTenantIDAndStatusCached(ctx context.Context, tenantID int64, status string, limit int, offset int) ([]entity.Event, int64, error)",
		"key := redisEventIndexListCacheKey(version, indexListKeyByAccountID(accountID, limit, offset))",
		"key := redisEventIndexListCacheKey(version, indexListKeyByTenantIDAndStatus(tenantID, status, limit, offset))",
		"c.listVersionByAccountID.Set(ctx, \"current\", redisEventIndexListVersionValue())",
		"c.listVersionByTenantIDAndStatus.Set(ctx, \"current\", redisEventIndexListVersionValue())",
	} {
		if !strings.Contains(eventRepoOut, want) {
			t.Fatalf("generated postgres event repo missing %q:\n%s", want, eventRepoOut)
		}
	}

	runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
	runGoCommand(t, dir, 3*time.Minute, "test", "./...")
}

func TestPostgresDatasourceIntrospectionSkipsExpressionAndPartialIndexes(t *testing.T) {
	db, err := sql.Open(fakeModelDatasourceDriver, "postgres-expression-index")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tables, err := introspectSQLTables(context.Background(), db, datasourceIntrospectionOptions{
		Driver: "postgres",
		Schema: "billing",
		Tables: []string{"billing_jobs"},
	})
	if err != nil {
		t.Fatalf("introspectSQLTables postgres expression indexes: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("tables = %#v, want billing_jobs", tables)
	}
	table := tables[0]
	if len(table.UniqueIndexes) != 0 {
		t.Fatalf("unique indexes = %#v, want expression-backed unique indexes skipped", table.UniqueIndexes)
	}
	if len(table.Indexes) != 1 || strings.Join(table.Indexes[0].Columns, ",") != "tenant_id,status" {
		t.Fatalf("indexes = %#v, want only tenant_id,status ordinary index", table.Indexes)
	}

	tables, err = prepareModelTables(tables, modelGenerationOptions{
		Tables: []string{"billing_jobs"},
		Prefix: "billing_",
		Strict: true,
	})
	if err != nil {
		t.Fatalf("prepareModelTables postgres expression indexes: %v", err)
	}
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/postgres-expression")
	if err := writeModelFiles(tables, dir, "model", "example.com/postgres-expression", modelStyleSQL, true, storage.DialectPostgres); err != nil {
		t.Fatalf("writeModelFiles postgres expression indexes: %v", err)
	}
	repo, err := os.ReadFile(filepath.Join(dir, "model", "repo", "job.go"))
	if err != nil {
		t.Fatal(err)
	}
	repoOut := string(repo)
	for _, want := range []string{
		"func (r *JobRepo) FindByTenantID(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Job, error)",
		`where = where.OrderBy("status")`,
		"func (c *CachedJobRepo) PageByTenantIDCached(ctx context.Context, tenantID int64, limit int, offset int) ([]entity.Job, int64, error)",
		"key := redisJobIndexListCacheKey(version, indexListKeyByTenantID(tenantID, limit, offset))",
	} {
		if !strings.Contains(repoOut, want) {
			t.Fatalf("generated postgres expression repo missing %q:\n%s", want, repoOut)
		}
	}
	for _, unexpected := range []string{
		"FindByEmail",
		"FindByTenantIDAndEmail",
		"FindByTenantIDAndStatus",
		"UpsertByTenantIDAndEmail",
		"indexListKeyByEmail",
		"indexListKeyByTenantIDAndStatus",
	} {
		if strings.Contains(repoOut, unexpected) {
			t.Fatalf("generated postgres expression repo should not include %q from expression or partial indexes:\n%s", unexpected, repoOut)
		}
	}
	runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
	runGoCommand(t, dir, 3*time.Minute, "test", "./...")
}

func TestPostgresDatasourceIndexQueryFiltersUnsafeIndexShapes(t *testing.T) {
	query, _, err := datasourceIndexesQueryWithScope(datasourceIntrospectionOptions{Driver: "postgres", Schema: "billing"})
	if err != nil {
		t.Fatalf("datasourceIndexesQueryWithScope postgres: %v", err)
	}
	for _, want := range []string{
		"ix.indexprs IS NULL",
		"ix.indpred IS NULL",
		"k.ordinality <= ix.indnkeyatts",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("postgres index query missing %q:\n%s", want, query)
		}
	}
}

func TestGenerateModelFromDatasourceViaMySQLDriverRejectsInvalidDSN(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/models\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := GenerateModelFromDatasource(ModelDatasourceOptions{
		Driver:  "mysql",
		DSN:     "bad-dsn",
		Dir:     dir,
		Timeout: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "open datasource") {
		t.Fatalf("GenerateModelFromDatasource invalid mysql dsn error = %v, want open datasource", err)
	}
}

func TestPrepareModelTablesFilterStrictBoundaries(t *testing.T) {
	base := []SQLTable{{
		Name:       "app_users",
		PrimaryKey: "id",
		Columns: []SQLColumn{
			{Name: "id", Type: "bigint", PrimaryKey: true},
			{Name: "email", Type: "varchar"},
			{Name: "deleted_at", Type: "datetime"},
		},
	}}

	if _, err := prepareModelTables(base, modelGenerationOptions{Tables: []string{"missing"}, Strict: true}); err == nil || !strings.Contains(err.Error(), "requested table not found") {
		t.Fatalf("prepareModelTables missing strict error = %v, want requested table not found", err)
	}
	if _, err := prepareModelTables([]SQLTable{{Name: "app_", Columns: []SQLColumn{{Name: "id"}}}}, modelGenerationOptions{Prefix: "app_", Strict: true}); err == nil || !strings.Contains(err.Error(), "becomes empty") {
		t.Fatalf("prepareModelTables empty prefix error = %v, want becomes empty", err)
	}
	if _, err := prepareModelTables(base, modelGenerationOptions{IgnoreColumns: []string{"id"}, Strict: true}); err == nil || !strings.Contains(err.Error(), "primary key column") {
		t.Fatalf("prepareModelTables ignore pk strict error = %v, want primary key rejection", err)
	}
	if _, err := prepareModelTables(base, modelGenerationOptions{IgnoreColumns: []string{"id", "email", "deleted_at"}}); err == nil || !strings.Contains(err.Error(), "no columns") {
		t.Fatalf("prepareModelTables all ignored error = %v, want no columns", err)
	}

	prepared, err := prepareModelTables(base, modelGenerationOptions{Prefix: "app_", IgnoreColumns: []string{"id"}})
	if err != nil {
		t.Fatalf("prepareModelTables non-strict ignore pk: %v", err)
	}
	if len(prepared) != 1 || prepared[0].Name != "users" || prepared[0].PrimaryKey != "email" || !prepared[0].Columns[0].PrimaryKey || prepared[0].SoftDeleteColumn != "deleted_at" {
		t.Fatalf("prepared tables = %#v, want trimmed users with fallback email primary key and soft delete", prepared)
	}
}

func TestLegacySQLWritersSoftDeleteBranches(t *testing.T) {
	table := SQLTable{
		Name:             "users",
		PrimaryKey:       "id",
		SoftDeleteColumn: "deleted_at",
		Columns: []SQLColumn{
			{Name: "id", Type: "bigint", PrimaryKey: true},
			{Name: "email", Type: "varchar"},
			{Name: "deleted_at", Type: "datetime"},
		},
	}
	var b bytes.Buffer
	writeLegacyFindOne(&b, table, "User", "UserRepo")
	writeLegacyUpdate(&b, table, "User", "UserRepo")
	writeLegacyDelete(&b, table, "User", "UserRepo")
	writeLegacyList(&b, table, "User", "UserRepo")
	writeLegacyCount(&b, table, "User", "UserRepo")
	out := b.String()
	for _, want := range []string{"AND deleted_at IS NULL", "SET deleted_at = ", "WHERE deleted_at IS NULL ORDER BY", "SELECT COUNT(*) FROM "} {
		if !strings.Contains(out, want) {
			t.Fatalf("legacy SQL output missing %q:\n%s", want, out)
		}
	}
}

func BenchmarkParseSQLModels(b *testing.B) {
	const ddl = `CREATE TABLE users (
  id bigint primary key,
  email varchar(128) unique not null,
  name varchar(64) not null,
  version bigint not null,
  deleted_at datetime,
  UNIQUE KEY uk_users_name (name)
);`

	b.ReportAllocs()
	for b.Loop() {
		tables, err := ParseSQLModels(ddl)
		if err != nil {
			b.Fatal(err)
		}
		if len(tables) != 1 {
			b.Fatalf("tables = %d, want 1", len(tables))
		}
	}
}
