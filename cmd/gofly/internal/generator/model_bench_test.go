package generator

import "testing"

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
