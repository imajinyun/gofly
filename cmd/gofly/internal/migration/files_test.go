package migration

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMigrationInitialDDL(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dir := t.TempDir()
			ddl := "-- initial schema\nCREATE TABLE parents (id BIGINT PRIMARY KEY, note VARCHAR(80) DEFAULT 'a;b');\nCREATE TABLE children (a BIGINT, b BIGINT, PRIMARY KEY(a,b), CONSTRAINT fk_parent FOREIGN KEY(a) REFERENCES parents(id));\n"
			input := filepath.Join(dir, "schema.sql")
			migrationWrite(t, input, ddl)
			out := filepath.Join(dir, "migrations")
			report, err := Create(CreateOptions{Name: "Initial Tables", Dir: out, Version: "1", DDLFile: input, Dialect: dialect})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Files) != 2 {
				t.Fatalf("files = %v, want pair", report.Files)
			}
			up, err := os.ReadFile(filepath.Join(out, "1_initial_tables.up.sql"))
			if err != nil || string(up) != ddl {
				t.Fatalf("up = %q, %v; want original DDL", up, err)
			}
			down, err := os.ReadFile(filepath.Join(out, "1_initial_tables.down.sql"))
			if err != nil || strings.Index(string(down), "children") >= strings.Index(string(down), "parents") {
				t.Fatalf("down = %q, %v; want reversed table order", down, err)
			}
			if _, err := Validate(out); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationRejectsInitialDDL(t *testing.T) {
	t.Run("postgres quoted delimiter", func(t *testing.T) {
		_, err := initialDown([]byte("CREATE TABLE \"foo\\\" (id INT); DROP TABLE victims; --\" (id INT);"), "postgres")
		if err == nil {
			t.Fatal("accepted a quoted-identifier statement escape")
		}
	})
	tests := []struct{ name, sql string }{
		{"empty", "-- no tables"},
		{"insert", "CREATE TABLE users (id INT); INSERT INTO users VALUES(1);"},
		{"alter", "ALTER TABLE users ADD name TEXT;"},
		{"duplicate", "CREATE TABLE users (id INT); CREATE TABLE users (id INT);"},
		{"conditional", "CREATE TABLE IF NOT EXISTS users (id INT);"},
		{"select", "CREATE TABLE users AS SELECT 1;"},
		{"unclosed string", "CREATE TABLE users (name TEXT DEFAULT 'oops);"},
		{"unclosed comment", "CREATE TABLE users (id INT); /*"},
		{"executable comment", "/*! CREATE TABLE bad(id INT) */ CREATE TABLE users(id INT);"},
		{"metadata", "CREATE TABLE schema_migrations (id INT);"},
		{"ambiguous quoted identifier", "CREATE TABLE \"foo\\\" (id INT); DROP TABLE victims; --\" (id INT);"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "schema.sql")
			migrationWrite(t, input, tt.sql)
			out := filepath.Join(dir, "out")
			_, err := Create(CreateOptions{Name: "init", Dir: out, Version: "1", DDLFile: input, Dialect: "mysql"})
			if err == nil {
				t.Fatalf("accepted %q", tt.sql)
			}
			files, _ := os.ReadDir(out)
			if len(files) != 0 {
				t.Fatalf("failed generation wrote %v", files)
			}
		})
	}
}

func TestMigrationFileBoundaries(t *testing.T) {
	t.Run("collision preserves content", func(t *testing.T) {
		dir := t.TempDir()
		opts := CreateOptions{Name: "users", Dir: dir, Version: "1"}
		if _, err := Create(opts); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "1_users.up.sql")
		migrationWrite(t, path, "SELECT 42;\n")
		if _, err := Create(opts); err == nil {
			t.Fatal("overwrote an existing version")
		}
		opts.Name = "different"
		if _, err := Create(opts); err == nil {
			t.Fatal("accepted duplicate version with different name")
		}
		got, _ := os.ReadFile(path)
		if string(got) != "SELECT 42;\n" {
			t.Fatalf("content = %q", got)
		}
	})
	t.Run("concurrent create", func(t *testing.T) {
		dir := t.TempDir()
		var successes atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if _, err := Create(CreateOptions{Name: "users", Dir: dir, Version: "1"}); err == nil {
					successes.Add(1)
				}
			})
		}
		wg.Wait()
		if successes.Load() != 1 {
			t.Fatalf("successful creates = %d, want 1", successes.Load())
		}
	})
	t.Run("symlink input", func(t *testing.T) {
		dir := t.TempDir()
		migrationWrite(t, filepath.Join(dir, "real.sql"), "CREATE TABLE users(id INT);")
		if err := os.Symlink("real.sql", filepath.Join(dir, "link.sql")); err != nil {
			t.Fatal(err)
		}
		if _, err := Create(CreateOptions{Name: "init", Dir: filepath.Join(dir, "out"), DDLFile: filepath.Join(dir, "link.sql"), Dialect: "mysql"}); err == nil {
			t.Fatal("accepted input symlink")
		}
	})
	t.Run("symlink output parent", func(t *testing.T) {
		dir := t.TempDir()
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		if _, err := Create(CreateOptions{Name: "init", Dir: filepath.Join(dir, "link", "out")}); err == nil {
			t.Fatal("accepted output parent symlink")
		}
	})
	t.Run("timestamp stable", func(t *testing.T) {
		dir := t.TempDir()
		_, err := Create(CreateOptions{Name: "users", Dir: dir, Time: time.Date(2026, 10, 1, 1, 2, 3, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "20261001010203_users.up.sql")); err != nil {
			t.Fatal(err)
		}
	})
	for _, version := range []string{"0", "-1", "1/../../2", "9223372036854775808", "abc"} {
		t.Run("version "+version, func(t *testing.T) {
			if _, err := Create(CreateOptions{Name: "init", Dir: t.TempDir(), Version: version}); err == nil {
				t.Fatalf("accepted version %q", version)
			}
		})
	}
}

func TestMigrationCatalogBoundaries(t *testing.T) {
	t.Run("writer lock blocks snapshot", func(t *testing.T) {
		dir := t.TempDir()
		migrationWrite(t, filepath.Join(dir, ".gofly-migration.lock"), "")
		migrationWrite(t, filepath.Join(dir, "1_users.up.sql"), "SELECT 1;")
		migrationWrite(t, filepath.Join(dir, "1_users.down.sql"), "")
		if _, err := Validate(dir); err == nil {
			t.Fatal("snapshot accepted a pair still being written")
		}
	})
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"missing down", map[string]string{"1_users.up.sql": "SELECT 1;"}},
		{"mismatched names", map[string]string{"1_users.up.sql": "SELECT 1;", "1_other.down.sql": "SELECT 1;"}},
		{"duplicate version", map[string]string{"1_users.up.sql": "SELECT 1;", "1_users.down.sql": "SELECT 1;", "01_other.up.sql": "SELECT 1;", "01_other.down.sql": "SELECT 1;"}},
		{"invalid filename", map[string]string{"users.sql": "SELECT 1;"}},
		{"oversized", map[string]string{"1_users.up.sql": strings.Repeat("a", maxSQLBytes+1), "1_users.down.sql": "SELECT 1;"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tt.files {
				migrationWrite(t, filepath.Join(dir, name), body)
			}
			if _, err := Validate(dir); err == nil {
				t.Fatal("accepted invalid catalog")
			}
		})
	}
	t.Run("symlink migration", func(t *testing.T) {
		dir := t.TempDir()
		migrationWrite(t, filepath.Join(dir, "real.txt"), "SELECT 1;")
		if err := os.Symlink("real.txt", filepath.Join(dir, "1_users.up.sql")); err != nil {
			t.Fatal(err)
		}
		migrationWrite(t, filepath.Join(dir, "1_users.down.sql"), "SELECT 1;")
		if _, err := Validate(dir); err == nil {
			t.Fatal("accepted symlink")
		}
	})
}

func migrationWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
