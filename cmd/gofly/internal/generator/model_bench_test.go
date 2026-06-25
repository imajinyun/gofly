package generator

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bitsUTModelDatasourceDriver = "bitsut-model-datasource"

func init() {
	sql.Register(bitsUTModelDatasourceDriver, bitsUTDatasourceDriver{})
}

type bitsUTDatasourceDriver struct{}

func (bitsUTDatasourceDriver) Open(name string) (driver.Conn, error) {
	return &bitsUTDatasourceConn{dsn: name}, nil
}

type bitsUTDatasourceConn struct {
	dsn string
}

func (c *bitsUTDatasourceConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *bitsUTDatasourceConn) Close() error                        { return nil }
func (c *bitsUTDatasourceConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (c *bitsUTDatasourceConn) Ping(context.Context) error {
	if c.dsn == "ping-error" {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (c *bitsUTDatasourceConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	switch c.dsn {
	case "query-error":
		return nil, io.ErrClosedPipe
	case "query-empty":
		return &bitsUTDatasourceRows{}, nil
	default:
		return &bitsUTDatasourceRows{values: [][]driver.Value{
			{"users", "id", "BIGINT", "PRI", "NO", int64(1)},
			{"users", "email", "character varying", "", "YES", int64(2)},
			{"audit_logs", "created_at", "timestamp with time zone", "", "NO", int64(1)},
		}}, nil
	}
}

type bitsUTDatasourceRows struct {
	values [][]driver.Value
	idx    int
}

func (r *bitsUTDatasourceRows) Columns() []string {
	return []string{"table_name", "column_name", "data_type", "column_key", "is_nullable", "ordinal_position"}
}

func (r *bitsUTDatasourceRows) Close() error { return nil }

func (r *bitsUTDatasourceRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.idx])
	r.idx++
	return nil
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
			{Name: "deleted_at", Type: "datetime", Nullable: true},
		},
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
	var noVersion bytes.Buffer
	writeSQLOptimisticLock(&noVersion, SQLTable{PrimaryKey: "id", Columns: table.Columns[:3]}, "User", "UserRepo")
	if noVersion.Len() != 0 {
		t.Fatalf("writeSQLOptimisticLock without version wrote %q", noVersion.String())
	}

	var gorm bytes.Buffer
	writeAdvancedGORMRepoMethods(&gorm, table, "User", "UserRepo")
	gormOut := gorm.String()
	for _, want := range []string{"FindByEmail", "InsertMany", "UpdateFields", "UpdateWithVersion", "ListAfter", "deleted_at IS NULL", `"version": expectedVersion + 1`} {
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

func TestWriteModelFilesEmptyTablesBoundary(t *testing.T) {
	err := writeModelFiles(nil, t.TempDir(), "model", "example.com/orders", ServiceStyleBasic, false)
	if err == nil || !strings.Contains(err.Error(), "model table is required") {
		t.Fatalf("writeModelFiles(nil) error = %v, want model table required", err)
	}
}

func TestModelDatasourceGenerationBoundaries(t *testing.T) {
	if err := GenerateModelFromDatasource(ModelDatasourceOptions{DSN: "ok"}); err == nil || !strings.Contains(err.Error(), "datasource driver is required") {
		t.Fatalf("GenerateModelFromDatasource missing driver error = %v, want driver required", err)
	}
	if err := GenerateModelFromDatasource(ModelDatasourceOptions{Driver: bitsUTModelDatasourceDriver}); err == nil || !strings.Contains(err.Error(), "datasource dsn is required") {
		t.Fatalf("GenerateModelFromDatasource missing dsn error = %v, want dsn required", err)
	}
	if err := GenerateModelFromDatasource(ModelDatasourceOptions{Driver: bitsUTModelDatasourceDriver, DSN: "ping-error"}); err == nil || !strings.Contains(err.Error(), "ping datasource") {
		t.Fatalf("GenerateModelFromDatasource ping error = %v, want ping datasource", err)
	}
	if err := GenerateModelFromDatasource(ModelDatasourceOptions{Driver: bitsUTModelDatasourceDriver, DSN: "ok", Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "unsupported datasource driver") {
		t.Fatalf("GenerateModelFromDatasource unsupported driver error = %v, want introspection driver error", err)
	}
}

func TestIntrospectSQLTablesWithFakeDatasource(t *testing.T) {
	db, err := sql.Open(bitsUTModelDatasourceDriver, "ok")
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
	if tables[0].Name != "users" || tables[0].PrimaryKey != "id" || len(tables[0].Columns) != 2 {
		t.Fatalf("users table = %#v, want id primary key and two columns", tables[0])
	}
	if tables[0].Columns[1].Type != "varchar" || !tables[0].Columns[1].Nullable {
		t.Fatalf("email column = %#v, want normalized nullable varchar", tables[0].Columns[1])
	}
	if tables[1].PrimaryKey != "created_at" || !tables[1].Columns[0].PrimaryKey || tables[1].Columns[0].Type != "timestamptz" {
		t.Fatalf("audit table = %#v, want fallback primary key with normalized timestamptz", tables[1])
	}

	emptyDB, err := sql.Open(bitsUTModelDatasourceDriver, "query-empty")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = emptyDB.Close() })
	if _, err := introspectSQLTables(context.Background(), emptyDB, datasourceIntrospectionOptions{Driver: "mysql"}); err == nil || !strings.Contains(err.Error(), "model table is required") {
		t.Fatalf("introspectSQLTables empty error = %v, want model table required", err)
	}

	queryErrDB, err := sql.Open(bitsUTModelDatasourceDriver, "query-error")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queryErrDB.Close() })
	if _, err := introspectSQLTables(context.Background(), queryErrDB, datasourceIntrospectionOptions{Driver: "mysql"}); err == nil || !strings.Contains(err.Error(), "query datasource schema") {
		t.Fatalf("introspectSQLTables query error = %v, want query datasource schema", err)
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

func BenchmarkParseSQLModels_BitsBench(b *testing.B) {
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
