//go:build integration

package command

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/migration"
)

// The caller supplies a disposable, empty database explicitly. No default DSN is used.
func TestMigrationDatabaseLifecycle(t *testing.T) {
	driver := os.Getenv("GOFLY_MIGRATION_TEST_DRIVER")
	dsn := os.Getenv("GOFLY_MIGRATION_TEST_DSN")
	if driver == "" || dsn == "" {
		t.Skip("requires disposable GOFLY_MIGRATION_TEST_DRIVER and GOFLY_MIGRATION_TEST_DSN")
	}
	t.Setenv("GOFLY_MIGRATE_DSN", dsn)
	dbDriver := driver
	if driver == "postgres" {
		dbDriver = "pgx"
	}
	db, err := sql.Open(dbDriver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	out := filepath.Join(dir, "migrations")
	input := filepath.Join(dir, "schema.sql")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(input, "CREATE TABLE migration_test_users(id BIGINT PRIMARY KEY, name VARCHAR(80) NOT NULL);\n")
	var outputs []json.RawMessage
	run := func(wantError bool, args ...string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		args = append([]string{"--output", "json", "migrate"}, args...)
		err := ExecuteWithIO(args, IOStreams{Out: &stdout, Err: &stderr})
		if (err != nil) != wantError {
			t.Fatalf("args=%v error=%v output=%s", args, err, &stdout)
		}
		if !json.Valid(stdout.Bytes()) {
			t.Fatalf("not JSON: %s", &stdout)
		}
		outputs = append(outputs, append(json.RawMessage(nil), stdout.Bytes()...))
	}
	run(false, "gen", "init", "--from-ddl", input, "--dialect", driver, "--dir", out, "--version", "1")
	run(true, "up", "--driver", driver, "--steps", "99", "--dir", out)
	run(false, "up", "--driver", driver, "--steps", "1", "--dir", out)
	assertMigrationMetadataTables(t, db, driver, true)
	renameMigrationMetadataToLegacy(t, db, driver)
	run(false, "status", "--driver", driver, "--dir", out)
	assertMigrationMetadataTables(t, db, driver, true)
	if _, err := db.Exec("INSERT INTO migration_test_users(id,name) VALUES(1,'preserved')"); err != nil {
		t.Fatal(err)
	}
	run(true, "down", "--driver", driver, "--steps", "99", "--dir", out)
	run(false, "up", "--driver", driver, "--dir", out)
	write(filepath.Join(out, "2_email.up.sql"), "ALTER TABLE migration_test_users ADD email VARCHAR(80);\n")
	write(filepath.Join(out, "2_email.down.sql"), "ALTER TABLE migration_test_users DROP COLUMN email;\n")
	run(false, "up", "--driver", driver, "--dir", out)
	run(false, "status", "--driver", driver, "--dir", out)
	write(filepath.Join(out, "1_init.up.sql"), "DROP TABLE migration_test_users;\n")
	run(true, "up", "--driver", driver, "--dir", out)
	write(filepath.Join(out, "1_init.up.sql"), "CREATE TABLE migration_test_users(id BIGINT PRIMARY KEY, name VARCHAR(80) NOT NULL);\n")
	run(false, "down", "--driver", driver, "--steps", "1", "--dir", out)
	var name string
	if err := db.QueryRow("SELECT name FROM migration_test_users WHERE id=1").Scan(&name); err != nil || name != "preserved" {
		t.Fatalf("retained row = %q, %v", name, err)
	}
	run(false, "up", "--driver", driver, "--dir", out)
	write(filepath.Join(out, "3_failure.up.sql"), "INSERT INTO missing_migration_table VALUES('DO_NOT_LEAK_SQL_SECRET');\n")
	write(filepath.Join(out, "3_failure.down.sql"), "SELECT 1;\n")
	run(true, "up", "--driver", driver, "--dir", out)
	run(true, "up", "--driver", driver, "--dir", out)
	run(false, "status", "--driver", driver, "--dir", out)
	// Simulate operator-reviewed recovery in this disposable fixture, then verify
	// caller cancellation interrupts an in-flight statement, not just the next file.
	if _, err := db.Exec("UPDATE migrations SET version=2, dirty=false"); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(out, "3_failure.up.sql"), "BEGIN; CREATE TABLE migration_uncommitted(id INT);\n")
	run(true, "up", "--driver", driver, "--dir", out)
	sleepSQL := "SELECT SLEEP(2);\n"
	if driver == "postgres" {
		sleepSQL = "SELECT pg_sleep(2);\n"
	}
	write(filepath.Join(out, "3_failure.up.sql"), sleepSQL)
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	started := time.Now()
	_, cancelErr := migration.Run(ctx, "up", migration.RunOptions{Dir: out, Driver: driver, DSN: dsn, Timeout: 5 * time.Second})
	if !errors.Is(cancelErr, context.Canceled) || time.Since(started) > time.Second {
		t.Fatalf("in-flight cancellation took %s, error=%v", time.Since(started), cancelErr)
	}
	for _, output := range outputs {
		if strings.Contains(string(output), "DO_NOT_LEAK_SQL_SECRET") || strings.Contains(string(output), dsn) {
			t.Fatal("output leaked SQL or DSN")
		}
	}
	if path := os.Getenv("GOFLY_MIGRATION_TEST_REPORT"); path != "" {
		data, err := json.MarshalIndent(struct {
			Schema               string            `json:"schema"`
			Driver               string            `json:"driver"`
			Commands             []json.RawMessage `json:"commands"`
			CancellationVerified bool              `json:"cancellationVerified"`
		}{"gofly.migration_e2e.v1", driver, outputs, true}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		write(path, string(data)+"\n")
	}
}

func assertMigrationMetadataTables(t *testing.T, db *sql.DB, driver string, wantNew bool) {
	t.Helper()
	for _, table := range []string{"migrations", "checksums"} {
		if got := migrationTableExists(t, db, driver, table); got != wantNew {
			t.Fatalf("metadata table %q exists=%t, want %t", table, got, wantNew)
		}
	}
	for _, table := range []string{"schema_migrations", "gofly_migration_checksums"} {
		if got := migrationTableExists(t, db, driver, table); got == wantNew {
			t.Fatalf("legacy metadata table %q exists=%t, want %t", table, got, !wantNew)
		}
	}
}

func renameMigrationMetadataToLegacy(t *testing.T, db *sql.DB, driver string) {
	t.Helper()
	if driver == "postgres" {
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin metadata legacy rename: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec("ALTER TABLE migrations RENAME TO schema_migrations"); err != nil {
			t.Fatalf("rename migration version metadata to legacy name: %v", err)
		}
		if _, err := tx.Exec("ALTER TABLE checksums RENAME TO gofly_migration_checksums"); err != nil {
			t.Fatalf("rename migration checksum metadata to legacy name: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit metadata legacy rename: %v", err)
		}
	} else if _, err := db.Exec("RENAME TABLE migrations TO schema_migrations, checksums TO gofly_migration_checksums"); err != nil {
		t.Fatalf("rename migration metadata to legacy names: %v", err)
	}
	assertMigrationMetadataTables(t, db, driver, false)
}

func migrationTableExists(t *testing.T, db *sql.DB, driver, table string) bool {
	t.Helper()
	if driver == "postgres" {
		var exists bool
		if err := db.QueryRow("SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)", table).Scan(&exists); err != nil {
			t.Fatalf("check metadata table %q: %v", table, err)
		}
		return exists
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&count); err != nil {
		t.Fatalf("check metadata table %q: %v", table, err)
	}
	return count > 0
}
