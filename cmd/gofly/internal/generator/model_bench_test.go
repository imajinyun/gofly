package generator

import "testing"

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
