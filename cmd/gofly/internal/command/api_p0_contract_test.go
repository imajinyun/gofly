package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteAPICheckResolvesImportedTypes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	shared := filepath.Join(dir, "types", "common.api")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("type Shared {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	apiPath := filepath.Join(dir, "service.api")
	api := "import \"types/common.api\"\n" +
		"type Request {\n  Shared Shared " + tick + "json:\"shared\"" + tick + "\n}\n" +
		"type Response {\n  OK bool " + tick + "json:\"ok\"" + tick + "\n}\n" +
		"service check-api {\n  @handler check\n  post /check (Request) returns (Response)\n}\n"
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := Execute([]string{"api", "check", "--api", apiPath}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "api ok: 3 type(s), 1 service(s)") {
		t.Fatalf("api check output = %q", out)
	}
}

func TestAPIMiddlewareNamesResolveImportsAndDeduplicate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.api")
	if err := os.WriteFile(shared, []byte("@server(middleware: Auth, Audit)\nservice shared-api {\n  @handler ping\n  get /ping returns (Reply)\n}\ntype Reply {\n  OK bool\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root.api")
	if err := os.WriteFile(root, []byte("import \"shared.api\"\n@server(middleware: Audit, Trace)\nservice root-api {\n  @handler get\n  get / returns (Reply)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := apiMiddlewareNames(root)
	if err != nil {
		t.Fatalf("apiMiddlewareNames: %v", err)
	}
	if got := strings.Join(names, ","); got != "Audit,Trace" {
		t.Fatalf("middleware names = %q, want Audit,Trace", got)
	}
	if got := uniqueStrings([]string{" Auth ", "", "Auth", "Trace"}); strings.Join(got, ",") != "Auth,Trace" {
		t.Fatalf("uniqueStrings = %v", got)
	}
	if _, err := apiMiddlewareNames(filepath.Join(dir, "missing.api")); err == nil {
		t.Fatal("apiMiddlewareNames accepted a missing file")
	}
}
