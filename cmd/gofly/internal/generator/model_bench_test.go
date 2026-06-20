package generator

import (
	"bytes"
	"strings"
	"testing"
)

func TestModelHelperBoundaries_BitsUT(t *testing.T) {
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

func TestModelCodegenAdvancedRepoBoundaries_BitsUT(t *testing.T) {
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

func TestWriteModelFilesEmptyTablesBoundary_BitsUT(t *testing.T) {
	err := writeModelFiles(nil, t.TempDir(), "model", "example.com/orders", ServiceStyleBasic, false)
	if err == nil || !strings.Contains(err.Error(), "model table is required") {
		t.Fatalf("writeModelFiles(nil) error = %v, want model table required", err)
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
