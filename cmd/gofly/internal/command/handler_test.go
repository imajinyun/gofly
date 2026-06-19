package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteHandlerGenWithPath(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"new", "api", "--name", "hello", "--module", "example.com/hello", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"handler", "gen", "--name", "CreateUser", "--dir", dir, "--path", "v1/user"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "internal", "api", "v1", "user", "create_user.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"package user", "func CreateUserHandler", `"example.com/hello/internal/svc"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("generated handler missing %q:\n%s", want, data)
		}
	}
}

func TestExecuteHandlerGenAcceptsMixedPositionalsAndFlags(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"new", "api", "--name", "hello", "--module", "example.com/hello", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"handler", "gen", "CreateOrder", "--dir", dir, "--path", "v1/order"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "internal", "api", "v1", "order", "create_order.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "func CreateOrderHandler") {
		t.Fatalf("generated handler = %s", data)
	}
}

func TestExecuteHandlerGenDefaultPath(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"new", "api", "--name", "hello", "--module", "example.com/hello", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"handler", "gen", "--name", "status", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "internal", "api", "status.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "package api") {
		t.Fatalf("generated handler = %s", data)
	}
}
