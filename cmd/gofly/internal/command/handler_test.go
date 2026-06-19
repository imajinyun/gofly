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

func TestMergeListsBoundaries_BitsUT(t *testing.T) {
	tests := []struct {
		name string
		a    []string
		b    []string
		want []string
	}{
		{
			name: "preserve order and deduplicate across lists",
			a:    []string{" rpc ", "api", "rpc", ""},
			b:    []string{"api", " model ", "", "rpc-compat"},
			want: []string{"rpc", "api", "model", "rpc-compat"},
		},
		{
			name: "empty inputs",
			a:    nil,
			b:    []string{" ", ""},
			want: []string{},
		},
		{
			name: "first occurrence wins",
			a:    []string{"trace", "auth"},
			b:    []string{"auth", "trace", "cors"},
			want: []string{"trace", "auth", "cors"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeLists(tt.a, tt.b)
			if len(got) != len(tt.want) {
				t.Fatalf("mergeLists() = %#v, want %#v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("mergeLists()[%d] = %q, want %q; full=%#v", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}
