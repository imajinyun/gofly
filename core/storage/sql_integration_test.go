//go:build integration

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestSQLStoreIntegration_MySQLAndPostgres(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		start  func(*testing.T, context.Context) string
	}{
		{name: "mysql", driver: "mysql", start: startMySQL},
		{name: "postgres", driver: "pgx", start: startPostgres},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			store, err := Open(ctx, Config{Driver: tt.driver, DSN: tt.start(t, ctx), Ping: 30 * time.Second})
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			defer store.Close()

			if _, err := store.Exec(ctx, "CREATE TABLE users (id BIGINT PRIMARY KEY, name VARCHAR(64) NOT NULL)"); err != nil {
				t.Fatalf("create schema: %v", err)
			}
			insert := "INSERT INTO users (id, name) VALUES (?, ?)"
			selectOne := "SELECT name FROM users WHERE id = ?"
			if tt.driver == "pgx" {
				insert = "INSERT INTO users (id, name) VALUES ($1, $2)"
				selectOne = "SELECT name FROM users WHERE id = $1"
			}
			if _, err := store.Exec(ctx, insert, 1, "gofly"); err != nil {
				t.Fatalf("insert row: %v", err)
			}
			var name string
			if err := store.QueryOne(ctx, selectOne, func(row *sql.Row) error {
				return row.Scan(&name)
			}, 1); err != nil {
				t.Fatalf("query row: %v", err)
			}
			if name != "gofly" {
				t.Fatalf("name = %q, want gofly", name)
			}
		})
	}
}

func startMySQL(t *testing.T, ctx context.Context) string {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "mysql:8.4",
		ExposedPorts: []string{"3306/tcp"},
		Env: map[string]string{
			"MYSQL_ROOT_PASSWORD": "pass",
			"MYSQL_DATABASE":      "gofly",
		},
		WaitingFor: wait.ForListeningPort("3306/tcp").WithStartupTimeout(2 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("start mysql container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("mysql host: %v", err)
	}
	port, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("mysql port: %v", err)
	}
	return fmt.Sprintf("root:pass@tcp(%s:%s)/gofly?parseTime=true", host, port.Port())
}

func startPostgres(t *testing.T, ctx context.Context) string {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_PASSWORD": "pass",
			"POSTGRES_USER":     "gofly",
			"POSTGRES_DB":       "gofly",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(2 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("postgres host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("postgres port: %v", err)
	}
	return fmt.Sprintf("postgres://gofly:pass@%s:%s/gofly?sslmode=disable", host, port.Port())
}
