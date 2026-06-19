package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceScaffoldRendererRendersInStableOrder(t *testing.T) {
	ir := serviceScaffoldIR{
		Data: map[string]string{"Name": "hello"},
		Files: map[string]string{
			"z.txt": "{{.Name}} z",
			"a.txt": "{{.Name}} a",
		},
	}

	files := serviceScaffoldRenderer{}.Render(ir)
	if len(files) != 2 {
		t.Fatalf("rendered file count = %d, want 2", len(files))
	}
	if files[0].Path != "a.txt" || files[0].Content != "hello a" || files[1].Path != "z.txt" || files[1].Content != "hello z" {
		t.Fatalf("rendered files = %#v, want stable sorted rendered output", files)
	}
}

func TestServiceFilesystemSinkWritesAndFormatsGoFiles(t *testing.T) {
	dir := t.TempDir()
	sink := serviceFilesystemSink{Dir: dir}
	err := sink.WriteRendered([]scaffoldRenderedFile{
		{Path: filepath.Join("internal", "pkg", "hello.go"), Content: "package pkg\nfunc Hello( ){return}\n"},
		{Path: "README.txt", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("WriteRendered err = %v", err)
	}

	goData, err := os.ReadFile(filepath.Join(dir, "internal", "pkg", "hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goData), "func Hello() { return }") {
		t.Fatalf("go file was not gofmt formatted:\n%s", goData)
	}
	textData, err := os.ReadFile(filepath.Join(dir, "README.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(textData) != "hello" {
		t.Fatalf("README.txt = %q, want hello", textData)
	}
}

func TestServiceFilesystemSinkRejectsInvalidGoSource(t *testing.T) {
	err := (serviceFilesystemSink{Dir: t.TempDir()}).WriteRendered([]scaffoldRenderedFile{
		{Path: "bad.go", Content: "package"},
	})
	if err == nil || !strings.Contains(err.Error(), "format") {
		t.Fatalf("WriteRendered invalid go err = %v, want format error", err)
	}
}

func TestWriteGeneratedFileCreatesReadableProjectFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "internal", "api", "handler.go")
	if err := writeGeneratedFile(path, []byte("package api\n")); err != nil {
		t.Fatalf("writeGeneratedFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("generated file mode = %v, want 0644", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o755 {
		t.Fatalf("generated directory mode = %v, want 0755", got)
	}
}

func TestWriteGeneratedFileRejectsSymlinkLeaf(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	link := filepath.Join(dir, "handler.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	err := writeGeneratedFile(link, []byte("package api\n"))
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("writeGeneratedFile symlink leaf err = %v, want symlink error", err)
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Fatalf("writeGeneratedFile wrote through symlink target")
	}
}

func TestWriteGeneratedFileUnderConstrainsRelativeTarget(t *testing.T) {
	dir := t.TempDir()
	if err := writeGeneratedFileUnder(dir, filepath.Join("internal", "api", "handler.go"), []byte("package api\n")); err != nil {
		t.Fatalf("writeGeneratedFileUnder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "api", "handler.go")); err != nil {
		t.Fatalf("generated file missing: %v", err)
	}

	escape := filepath.Join(dir, "..", "escape.go")
	err := writeGeneratedFileUnder(dir, filepath.Join("..", "escape.go"), []byte("package escape\n"))
	if err == nil || !strings.Contains(err.Error(), "escapes output directory") {
		t.Fatalf("writeGeneratedFileUnder escape err = %v, want escape error", err)
	}
	if _, statErr := os.Stat(escape); statErr == nil {
		t.Fatalf("writeGeneratedFileUnder created escaping file %s", escape)
	}
}

func TestWriteGeneratedFileUnderRejectsSymlinkParentAndLeaf(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	linkParent := filepath.Join(dir, "link")
	if err := os.Symlink(outside, linkParent); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	err := writeGeneratedFileUnder(dir, filepath.Join("link", "escape.go"), []byte("package escape\n"))
	if err == nil || !strings.Contains(err.Error(), "traverses symlink") {
		t.Fatalf("writeGeneratedFileUnder symlink parent err = %v, want symlink traversal error", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.go")); statErr == nil {
		t.Fatalf("writeGeneratedFileUnder wrote through symlink parent")
	}

	leafTarget := filepath.Join(outside, "leaf.go")
	leafLink := filepath.Join(dir, "leaf.go")
	if err := os.Symlink(leafTarget, leafLink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	err = writeGeneratedFileUnder(dir, "leaf.go", []byte("package leaf\n"))
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("writeGeneratedFileUnder symlink leaf err = %v, want symlink error", err)
	}
	if _, statErr := os.Stat(leafTarget); statErr == nil {
		t.Fatalf("writeGeneratedFileUnder wrote through symlink leaf")
	}
}

func TestServiceFilesystemSinkRejectsEscapingPaths(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..", "escape.txt")
	err := (serviceFilesystemSink{Dir: dir}).WriteRendered([]scaffoldRenderedFile{
		{Path: filepath.Join("..", "escape.txt"), Content: "escaped"},
	})
	if err == nil || !strings.Contains(err.Error(), "escapes output directory") {
		t.Fatalf("WriteRendered escaping path err = %v, want escape error", err)
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Fatalf("WriteRendered created escaping file %s", outside)
	}
}

func TestServiceFilesystemSinkRejectsAbsolutePaths(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "absolute.txt")
	err := (serviceFilesystemSink{Dir: t.TempDir()}).WriteRendered([]scaffoldRenderedFile{
		{Path: abs, Content: "absolute"},
	})
	if err == nil || !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("WriteRendered absolute path err = %v, want relative error", err)
	}
	if _, statErr := os.Stat(abs); statErr == nil {
		t.Fatalf("WriteRendered created absolute file %s", abs)
	}
}

func TestServiceFilesystemSinkRejectsSymlinkParent(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	err := (serviceFilesystemSink{Dir: dir}).WriteRendered([]scaffoldRenderedFile{
		{Path: filepath.Join("link", "escape.txt"), Content: "escaped"},
	})
	if err == nil || !strings.Contains(err.Error(), "traverses symlink") {
		t.Fatalf("WriteRendered symlink path err = %v, want symlink traversal error", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.txt")); statErr == nil {
		t.Fatalf("WriteRendered created file through symlink")
	}
}

func TestNormalizedServicePlugins(t *testing.T) {
	if got := normalizedServicePlugins(nil); got != nil {
		t.Fatalf("nil = %v, want nil", got)
	}
	if got := normalizedServicePlugins([]string{""}); len(got) != 0 {
		t.Fatalf("empty string = %v, want empty", got)
	}
	if got := normalizedServicePlugins([]string{" a ", "", "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("trimmed = %v", got)
	}
}

func TestBuildServiceScaffoldIRValidation(t *testing.T) {
	_, err := buildServiceScaffoldIR(ServiceScaffoldOptions{})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	_, err = buildServiceScaffoldIR(ServiceScaffoldOptions{Name: "hello"})
	if err == nil {
		t.Fatal("expected error for empty module")
	}
	_, err = buildServiceScaffoldIR(ServiceScaffoldOptions{Name: "hello", Module: "example.com/hello", Style: "bogus"})
	if err == nil {
		t.Fatal("expected error for invalid style")
	}
}

func TestBuildServiceScaffoldIRNormalizesInputs(t *testing.T) {
	ir, err := buildServiceScaffoldIR(ServiceScaffoldOptions{
		Name:       "hello",
		Module:     "example.com/hello",
		Dir:        t.TempDir(),
		Style:      ServiceStyleMinimal,
		Kind:       "api",
		ExtraFiles: map[string]string{"extra.txt": "{{.Name}}"},
	})
	if err != nil {
		t.Fatalf("buildServiceScaffoldIR err = %v", err)
	}
	if ir.Style != ServiceStyleMinimal || ir.Data["Name"] != "hello" || ir.Data["Module"] != "example.com/hello" {
		t.Fatalf("IR metadata = %#v, want normalized style/data", ir)
	}
	if _, ok := ir.Files["hello.api"]; !ok {
		t.Fatalf("IR files missing API spec: %#v", ir.Files)
	}
	if ir.Files["extra.txt"] != "{{.Name}}" {
		t.Fatalf("IR files missing extra file: %#v", ir.Files)
	}
}

func TestServiceFilesystemSinkRunPluginsEmpty(t *testing.T) {
	sink := serviceFilesystemSink{Dir: t.TempDir()}
	if err := sink.RunPlugins(serviceScaffoldIR{}); err != nil {
		t.Fatalf("RunPlugins empty: %v", err)
	}
}

func TestServiceFilesystemSinkWriteRenderedEmpty(t *testing.T) {
	sink := serviceFilesystemSink{Dir: t.TempDir()}
	if err := sink.WriteRendered(nil); err != nil {
		t.Fatalf("WriteRendered nil: %v", err)
	}
}
