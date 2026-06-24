package generator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type bitsUTInternalPlugin struct{ name string }

func (p bitsUTInternalPlugin) Name() string { return p.name }

func (p bitsUTInternalPlugin) Generate(req PluginRequest) (PluginResponse, error) {
	return PluginResponse{Version: pluginVersion, Message: req.Service}, nil
}

type bitsUTScaffoldPlugin struct{ name string }

func (p bitsUTScaffoldPlugin) Name() string { return p.name }

func (p bitsUTScaffoldPlugin) Generate(req PluginRequest) (PluginResponse, error) {
	return PluginResponse{
		Version: pluginVersion,
		Message: req.Service + ":" + req.Input["kind"],
		Files: []PluginFile{{
			Path:    filepath.Join("plugin", "request.txt"),
			Content: req.Command + "|" + req.Module + "|" + req.Style,
		}},
		Patches: []PluginPatch{{
			Path:        filepath.Join("cmd", "main.go"),
			InsertAfter: "package main\n",
			Patch:       "// patched by scaffold plugin\n",
		}},
	}, nil
}

func TestRemotePluginInstallCoverageBuffer_BitsUT(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("installs local http plugin into isolated cache", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/plugins/auth-jwt" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte("#!/bin/sh\necho plugin\n"))
		}))
		defer server.Close()

		installed, err := InstallRemotePlugin(server.URL + "/plugins/auth-jwt@v0.1.0")
		if err != nil {
			t.Fatalf("InstallRemotePlugin http: %v", err)
		}
		if installed.Version != "v0.1.0" || installed.BinaryDigest == "" || !strings.Contains(installed.Binary, filepath.Join(".cache", "gofly", "plugins")) {
			t.Fatalf("installed plugin metadata = %+v", installed)
		}
		data, err := os.ReadFile(installed.Binary)
		if err != nil {
			t.Fatalf("read installed binary: %v", err)
		}
		if !bytes.Contains(data, []byte("echo plugin")) {
			t.Fatalf("installed binary = %q, want downloaded payload", data)
		}
	})

	t.Run("installs file plugin and rejects invalid specs", func(t *testing.T) {
		pluginFile := filepath.Join(t.TempDir(), "local-plugin")
		if err := os.WriteFile(pluginFile, []byte("#!/bin/sh\necho local\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		installed, err := InstallRemotePlugin("file://" + pluginFile + "@v1.2.3")
		if err != nil {
			t.Fatalf("InstallRemotePlugin file: %v", err)
		}
		if installed.Remote != "file://"+pluginFile || installed.Version != "v1.2.3" {
			t.Fatalf("file installed metadata = %+v", installed)
		}
		for _, remote := range []string{"missing-version", "@v1", "file://plugin@../bad"} {
			if _, err := InstallRemotePlugin(remote); err == nil {
				t.Fatalf("InstallRemotePlugin(%q) succeeded, want error", remote)
			}
		}
	})

	t.Run("download rejects insecure and bad status remotes", func(t *testing.T) {
		tmp, err := os.CreateTemp(t.TempDir(), "plugin-*.tmp")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tmp.Close() }()
		if err := downloadRemotePluginPayload(tmp, remotePluginSpec{remote: "ftp://example.com/plugin", version: "v1", hash: "hash"}); err == nil || !strings.Contains(err.Error(), "insecure URL scheme") {
			t.Fatalf("insecure download error = %v", err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "missing", http.StatusNotFound)
		}))
		defer server.Close()
		if err := downloadRemotePluginPayload(tmp, remotePluginSpec{remote: server.URL + "/missing", version: "v1", hash: "hash"}); err == nil || !strings.Contains(err.Error(), "status 404") {
			t.Fatalf("bad status download error = %v", err)
		}
	})
}

func TestPluginResponseWriteFilesRejectsEscapingPaths(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")

	tests := []struct {
		name string
		path string
	}{
		{name: "parent traversal", path: "../outside.txt"},
		{name: "absolute path", path: outside},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := PluginResponse{Files: []PluginFile{{Path: tt.path, Content: "owned"}}}
			if _, err := resp.WriteFiles(dir); err == nil {
				t.Fatalf("WriteFiles(%q) succeeded, want error", tt.path)
			}
		})
	}

	if data, err := os.ReadFile(outside); err == nil {
		t.Fatalf("outside file was written with %q", data)
	} else if !os.IsNotExist(err) {
		t.Fatalf("read outside file: %v", err)
	}
}

func TestPluginResponseWriteFilesAllowsRelativePaths(t *testing.T) {
	dir := t.TempDir()
	resp := PluginResponse{Files: []PluginFile{{Path: "internal/plugin.txt", Content: "ok"}}}

	count, err := resp.WriteFiles(dir)
	if err != nil {
		t.Fatalf("WriteFiles returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("WriteFiles count = %d, want 1", count)
	}
	data, err := os.ReadFile(filepath.Join(dir, "internal", "plugin.txt"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("written file = %q, want ok", data)
	}
}

func TestPluginArgumentAndCacheHelpersBoundaries(t *testing.T) {
	path, args := splitPluginArgs("  ")
	if path != "" || args != nil {
		t.Fatalf("blank splitPluginArgs = %q/%#v, want empty/nil", path, args)
	}

	path, args = splitPluginArgs("plugin --service users  --verbose")
	if path != "plugin" || len(args) != 3 || args[0] != "--service" || args[1] != "users" || args[2] != "--verbose" {
		t.Fatalf("splitPluginArgs = %q/%#v, want normalized args", path, args)
	}

	if _, err := parseRemotePluginSpec("missing-version"); err == nil {
		t.Fatal("parseRemotePluginSpec without version succeeded, want error")
	}
	if _, err := parseRemotePluginSpec("repo@../main"); err == nil {
		t.Fatal("parseRemotePluginSpec path-like version succeeded, want error")
	}
	parsedSpec, err := parseRemotePluginSpec("https://example.com/plugin@v1.2.3")
	if err != nil {
		t.Fatalf("parseRemotePluginSpec valid: %v", err)
	}
	if parsedSpec.remote != "https://example.com/plugin" || parsedSpec.version != "v1.2.3" || parsedSpec.hash == "" {
		t.Fatalf("remote spec = %#v, want normalized remote/version/hash", parsedSpec)
	}

	root := t.TempDir()
	cache := filepath.Join(root, "plugins", "hash")
	if err := prepareRemotePluginCacheDir(cache); err != nil {
		t.Fatalf("prepareRemotePluginCacheDir create: %v", err)
	}
	if err := prepareRemotePluginCacheDir(cache); err != nil {
		t.Fatalf("prepareRemotePluginCacheDir existing dir: %v", err)
	}

	fileCache := filepath.Join(root, "plugins", "file-cache")
	if err := os.WriteFile(fileCache, []byte("not-dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareRemotePluginCacheDir(fileCache); err == nil {
		t.Fatal("prepareRemotePluginCacheDir existing file succeeded, want error")
	}

	parsed, err := url.Parse("https://example.com/a path/plugin?.bin")
	if err != nil {
		t.Fatal(err)
	}
	name := pluginCacheFilename(parsed)
	if strings.Contains(name, " ") || !strings.Contains(name, "plugin") {
		t.Fatalf("pluginCacheFilename = %q, want sanitized plugin filename", name)
	}
}

func TestRemotePluginDownloadBoundaries(t *testing.T) {
	tmp := t.TempDir()

	insecureFile, err := os.Create(filepath.Join(tmp, "insecure"))
	if err != nil {
		t.Fatal(err)
	}
	defer insecureFile.Close()
	insecure := remotePluginSpec{remote: "http://example.com/plugin", version: "v1", hash: "hash"}
	if err := downloadRemotePluginPayload(insecureFile, insecure); err == nil || !strings.Contains(err.Error(), "insecure URL scheme") {
		t.Fatalf("downloadRemotePluginPayload insecure err = %v, want insecure URL error", err)
	}

	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plugin-binary"))
	}))
	defer okServer.Close()

	okFile, err := os.Create(filepath.Join(tmp, "ok-plugin"))
	if err != nil {
		t.Fatal(err)
	}
	defer okFile.Close()
	if err := downloadRemotePluginPayload(okFile, remotePluginSpec{remote: okServer.URL + "/plugin", version: "v1", hash: "ok"}); err != nil {
		t.Fatalf("downloadRemotePluginPayload local server: %v", err)
	}
	data, err := os.ReadFile(okFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "plugin-binary" {
		t.Fatalf("downloaded payload = %q, want plugin-binary", data)
	}

	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusTeapot)
	}))
	defer badServer.Close()

	badFile, err := os.Create(filepath.Join(tmp, "bad-plugin"))
	if err != nil {
		t.Fatal(err)
	}
	defer badFile.Close()
	if err := downloadRemotePluginPayload(badFile, remotePluginSpec{remote: badServer.URL, version: "v1", hash: "bad"}); err == nil || !strings.Contains(err.Error(), "status 418") {
		t.Fatalf("downloadRemotePluginPayload status err = %v, want status 418", err)
	}

	oversizedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxDownloadedPluginBytes+1)))
	}))
	defer oversizedServer.Close()

	oversizedFile, err := os.Create(filepath.Join(tmp, "oversized-plugin"))
	if err != nil {
		t.Fatal(err)
	}
	defer oversizedFile.Close()
	if err := downloadRemotePluginPayload(oversizedFile, remotePluginSpec{remote: oversizedServer.URL, version: "v1", hash: "large"}); err == nil || !strings.Contains(err.Error(), "binary exceeds") {
		t.Fatalf("downloadRemotePluginPayload oversized err = %v, want size error", err)
	}

	if _, err := urlpkgParse("not-a-url"); err == nil {
		t.Fatal("urlpkgParse missing scheme/host succeeded, want error")
	}
	parsed, err := urlpkgParse(okServer.URL + "/plugin")
	if err != nil {
		t.Fatalf("urlpkgParse local server URL: %v", err)
	}
	if !isLocalPluginURL(parsed) {
		t.Fatalf("isLocalPluginURL(%q) = false, want true", parsed.String())
	}

	src := filepath.Join(tmp, "source-plugin")
	if err := os.WriteFile(src, []byte("local-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyDst, err := os.Create(filepath.Join(tmp, "copy-plugin"))
	if err != nil {
		t.Fatal(err)
	}
	defer copyDst.Close()
	if err := copyFileToPluginCache(copyDst, src, remotePluginSpec{remote: "file://" + src, version: "v1", hash: "copy"}); err != nil {
		t.Fatalf("copyFileToPluginCache: %v", err)
	}
	copied, err := os.ReadFile(copyDst.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != "local-plugin" {
		t.Fatalf("copied plugin = %q, want local-plugin", copied)
	}

	missingDst, err := os.Create(filepath.Join(tmp, "missing-copy"))
	if err != nil {
		t.Fatal(err)
	}
	defer missingDst.Close()
	if err := copyFileToPluginCache(missingDst, filepath.Join(tmp, "missing"), remotePluginSpec{remote: "missing", version: "v1", hash: "missing"}); err == nil {
		t.Fatal("copyFileToPluginCache missing source succeeded, want error")
	}
}

func TestPluginSortingAndDigestHelpers(t *testing.T) {
	plugins := []InstalledPlugin{
		{Remote: "z", Version: "v2"},
		{Remote: "a", Version: "v2"},
		{Remote: "a", Version: "v1"},
	}
	sortInstalledPlugins(plugins)
	gotOrder := []string{plugins[0].Remote + "@" + plugins[0].Version, plugins[1].Remote + "@" + plugins[1].Version, plugins[2].Remote + "@" + plugins[2].Version}
	wantOrder := []string{"a@v1", "a@v2", "z@v2"}
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("sortInstalledPlugins = %v, want %v", gotOrder, wantOrder)
	}

	values := []string{"b", "a", "c"}
	sortStrings(values)
	if strings.Join(values, ",") != "a,b,c" {
		t.Fatalf("sortStrings = %v, want a,b,c", values)
	}

	path := filepath.Join(t.TempDir(), "plugin")
	if err := os.WriteFile(path, []byte("digest-me"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("fileSHA256: %v", err)
	}
	wantBytes := sha256.Sum256([]byte("digest-me"))
	want := hex.EncodeToString(wantBytes[:])
	if got != want {
		t.Fatalf("fileSHA256 = %q, want %q", got, want)
	}
	if _, err := fileSHA256(filepath.Join(filepath.Dir(path), "missing")); err == nil {
		t.Fatal("fileSHA256 missing file succeeded, want error")
	}
}

func TestResolveGoPluginPathsBoundaries(t *testing.T) {
	if _, err := ResolveGoPluginPaths(" "); err == nil {
		t.Fatal("ResolveGoPluginPaths blank root succeeded, want error")
	}

	dir := t.TempDir()
	nonExecutable := filepath.Join(dir, "plain-plugin")
	if err := os.WriteFile(nonExecutable, []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveGoPluginPaths(nonExecutable); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("ResolveGoPluginPaths non-executable file error = %v, want not executable", err)
	}

	executable := filepath.Join(dir, "single-plugin")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths, err := ResolveGoPluginPaths(executable)
	if err != nil {
		t.Fatalf("ResolveGoPluginPaths executable file: %v", err)
	}
	if len(paths) != 1 || paths[0] != executable {
		t.Fatalf("ResolveGoPluginPaths file = %#v, want %q", paths, executable)
	}

	nested := filepath.Join(dir, "plugins", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"z-plugin", "a-plugin"} {
		if err := os.WriteFile(filepath.Join(nested, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nested, "readme.txt"), []byte("docs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(nested, "a-plugin"), filepath.Join(nested, "linked-plugin")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	paths, err = ResolveGoPluginPaths(filepath.Join(dir, "plugins"))
	if err != nil {
		t.Fatalf("ResolveGoPluginPaths directory: %v", err)
	}
	want := []string{filepath.Join(nested, "a-plugin"), filepath.Join(nested, "z-plugin")}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("ResolveGoPluginPaths directory = %#v, want %#v", paths, want)
	}

	emptyDir := filepath.Join(dir, "empty")
	if err := os.Mkdir(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveGoPluginPaths(emptyDir); err == nil || !strings.Contains(err.Error(), "contains no executable plugins") {
		t.Fatalf("ResolveGoPluginPaths empty dir error = %v, want no executable plugins", err)
	}
}

func TestPluginSymlinkParentBoundaries(t *testing.T) {
	root := t.TempDir()
	if err := rejectPluginSymlinkParent(root, filepath.Join("missing", "plugin.txt")); err != nil {
		t.Fatalf("rejectPluginSymlinkParent missing parent = %v, want nil", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "safe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rejectPluginSymlinkParent(root, filepath.Join("safe", "plugin.txt")); err != nil {
		t.Fatalf("rejectPluginSymlinkParent safe parent = %v, want nil", err)
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := rejectPluginSymlinkParent(root, filepath.Join("link", "plugin.txt")); err == nil || !strings.Contains(err.Error(), "traverses symlink") {
		t.Fatalf("rejectPluginSymlinkParent symlink error = %v, want traverses symlink", err)
	}
	if err := rejectSymlinkParent(root, filepath.Join("link", "nested", "plugin.txt"), "template"); err == nil || !strings.Contains(err.Error(), "template path") {
		t.Fatalf("rejectSymlinkParent label error = %v, want labelled symlink error", err)
	}

	missingTarget := filepath.Join(root, "new-plugin")
	if err := rejectExistingSymlinkTarget(missingTarget, "plugin"); err != nil {
		t.Fatalf("rejectExistingSymlinkTarget missing target = %v, want nil", err)
	}
	targetLink := filepath.Join(root, "target-link")
	if err := os.Symlink(outside, targetLink); err != nil {
		t.Skipf("target symlink unsupported: %v", err)
	}
	if err := rejectExistingSymlinkTarget(targetLink, "plugin"); err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("rejectExistingSymlinkTarget symlink error = %v, want symlink target rejection", err)
	}
	regular := filepath.Join(root, "regular-plugin")
	if err := os.WriteFile(regular, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rejectExistingSymlinkTarget(regular, "plugin"); err != nil {
		t.Fatalf("rejectExistingSymlinkTarget regular target = %v, want nil", err)
	}
}

func TestPluginResponseApplyPatchesRejectsEscapingPaths(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "target.txt")
	if err := os.WriteFile(outside, []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := PluginResponse{Patches: []PluginPatch{{Path: outside, Patch: "patched"}}}
	if err := resp.ApplyPatches(dir); err == nil {
		t.Fatal("ApplyPatches with absolute path succeeded, want error")
	}

	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside target: %v", err)
	}
	if strings.Contains(string(data), "patched") {
		t.Fatalf("outside file was patched: %q", data)
	}
}

func TestPluginResponseApplyPatchesRejectsMissingAnchor(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "handler.go")
	if err := os.WriteFile(target, []byte("package handler\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := PluginResponse{Patches: []PluginPatch{{Path: "handler.go", InsertAfter: "func Missing() {}", Patch: "// generated"}}}
	err := resp.ApplyPatches(dir)
	if err == nil || !strings.Contains(err.Error(), "anchor") || !strings.Contains(err.Error(), "handler.go") {
		t.Fatalf("ApplyPatches missing anchor error = %v, want anchor path error", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package handler\n" {
		t.Fatalf("missing-anchor patch mutated target: %q", data)
	}
}

func TestPluginRunnerReturnsPluginProtocolError(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin")
	if err := os.WriteFile(plugin, []byte("#!/bin/sh\nprintf '%s' '{\"error\":\"boom\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := NewPluginRunner().Run(plugin, PluginRequest{Command: "api"})
	if err == nil || !strings.Contains(err.Error(), "returned error") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("PluginRunner.Run protocol error = %v, want plugin returned error", err)
	}
}

func TestLimitedPluginBufferRejectsOversizedOutput(t *testing.T) {
	buf := newLimitedPluginBuffer("stdout", 4)
	n, err := buf.Write([]byte("abcdef"))
	if err == nil || !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("limited buffer write err = %v, want limit error", err)
	}
	if n != 4 {
		t.Fatalf("limited buffer wrote %d bytes, want 4", n)
	}
	if !buf.exceeded() {
		t.Fatal("limited buffer exceeded() = false, want true")
	}
	if got := buf.String(); got != "abcd" {
		t.Fatalf("limited buffer retained %q, want truncated prefix", got)
	}
}

func TestPluginResponseRejectsSymlinkParentTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	resp := PluginResponse{Files: []PluginFile{{Path: filepath.Join("link", "owned.txt"), Content: "owned"}}}
	if _, err := resp.WriteFiles(dir); err == nil {
		t.Fatal("WriteFiles through symlink parent succeeded, want error")
	}
	if _, err := os.Stat(filepath.Join(outside, "owned.txt")); err == nil {
		t.Fatal("outside file was written through symlink parent")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat outside file: %v", err)
	}

	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchResp := PluginResponse{Patches: []PluginPatch{{Path: filepath.Join("link", "target.txt"), Patch: "patched"}}}
	if err := patchResp.ApplyPatches(dir); err == nil {
		t.Fatal("ApplyPatches through symlink parent succeeded, want error")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "base" {
		t.Fatalf("symlink patch mutated outside file: %q", data)
	}
}

func TestPluginResponseRejectsSymlinkLeaf(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "leaf.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	resp := PluginResponse{Files: []PluginFile{{Path: "leaf.txt", Content: "owned"}}}
	if _, err := resp.WriteFiles(dir); err == nil {
		t.Fatal("WriteFiles to symlink leaf succeeded, want error")
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "base" {
		t.Fatalf("symlink leaf write mutated outside file: %q", data)
	}

	patchResp := PluginResponse{Patches: []PluginPatch{{Path: "leaf.txt", Patch: "patched"}}}
	if err := patchResp.ApplyPatches(dir); err == nil {
		t.Fatal("ApplyPatches to symlink leaf succeeded, want error")
	}
	data, err = os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "base" {
		t.Fatalf("symlink leaf patch mutated outside file: %q", data)
	}
}

func TestPluginRunnerDownloadPluginDoesNotReuseLocalCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plugin-a"))
	}))
	t.Cleanup(serverA.Close)
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plugin-b"))
	}))
	t.Cleanup(serverB.Close)

	runner := NewPluginRunner()
	pathA, err := runner.downloadPlugin(serverA.URL + "/plugin")
	if err != nil {
		t.Fatalf("download plugin A: %v", err)
	}
	pathB, err := runner.downloadPlugin(serverB.URL + "/plugin")
	if err != nil {
		t.Fatalf("download plugin B: %v", err)
	}
	if pathA == pathB {
		t.Fatalf("downloadPlugin cache path collision: %s", pathA)
	}
	dataA, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatalf("read plugin A: %v", err)
	}
	dataB, err := os.ReadFile(pathB)
	if err != nil {
		t.Fatalf("read plugin B: %v", err)
	}
	if string(dataA) != "plugin-a" || string(dataB) != "plugin-b" {
		t.Fatalf("downloaded plugin data A=%q B=%q, want distinct content", dataA, dataB)
	}

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte("plugin"))
	}))
	t.Cleanup(server.Close)
	first, err := runner.downloadPlugin(server.URL + "/plugin")
	if err != nil {
		t.Fatalf("first download plugin: %v", err)
	}
	second, err := runner.downloadPlugin(server.URL + "/plugin")
	if err != nil {
		t.Fatalf("second download plugin: %v", err)
	}
	if first == second {
		t.Fatalf("downloadPlugin reused local path %q, want fresh temp files", first)
	}
	if calls != 2 {
		t.Fatalf("download calls = %d, want 2 without local cache reuse", calls)
	}
}

func TestPluginCacheFilenameSanitizesAndBoundsName(t *testing.T) {
	parsed, err := url.Parse("https://example.com/plugins/" + strings.Repeat("x", 200) + " unsafe$name?.v=1")
	if err != nil {
		t.Fatal(err)
	}

	got := pluginCacheFilename(parsed)
	parts := strings.Split(got, "-")
	if len(parts) < 2 {
		t.Fatalf("pluginCacheFilename() = %q, want name-hash", got)
	}
	name := strings.TrimSuffix(got, "-"+parts[len(parts)-1])
	if len(name) > maxPluginCacheNameLength {
		t.Fatalf("cache name length = %d, want <= %d", len(name), maxPluginCacheNameLength)
	}
	for _, r := range got {
		allowed := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if !allowed {
			t.Fatalf("cache filename %q contains unsafe rune %q", got, r)
		}
	}
}

func TestPluginCacheFilenameFallsBackAndSeparatesQuery(t *testing.T) {
	root, err := url.Parse("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if got := pluginCacheFilename(root); !strings.HasPrefix(got, "plugin-") {
		t.Fatalf("root cache filename = %q, want plugin fallback prefix", got)
	}

	first, err := url.Parse("https://example.com/plugins/?v=1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := url.Parse("https://example.com/plugins/?v=2")
	if err != nil {
		t.Fatal(err)
	}

	nameA := pluginCacheFilename(first)
	nameB := pluginCacheFilename(second)
	if !strings.HasPrefix(nameA, "plugins-") || !strings.HasPrefix(nameB, "plugins-") {
		t.Fatalf("fallback cache filenames = %q, %q, want plugin prefix", nameA, nameB)
	}
	if nameA == nameB {
		t.Fatalf("pluginCacheFilename collision for query-distinct URLs: %q", nameA)
	}
}

func TestPluginRunnerDownloadPluginIgnoresUserCache(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("HOME", cacheRoot)
	urlText := "http://localhost/plugin"
	parsed, err := url.Parse(urlText)
	if err != nil {
		t.Fatal(err)
	}
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(userCacheDir, "gofly", "plugins")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheDir, pluginCacheFilename(parsed))
	if err := os.WriteFile(cachePath, []byte("poison"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = NewPluginRunner().downloadPlugin(urlText)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("downloadPlugin error = %v, want network download attempt instead of user-cache hit", err)
	}
}

func TestPluginRunnerDownloadPluginUsesUniqueTempFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plugin"))
	}))
	t.Cleanup(server.Close)

	parsed, err := url.Parse(server.URL + "/plugin")
	if err != nil {
		t.Fatal(err)
	}
	legacyTempPath := filepath.Join(os.TempDir(), "gofly-plugin-"+pluginCacheFilename(parsed)+".tmp")
	if err := os.WriteFile(legacyTempPath, []byte("do not clobber"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := NewPluginRunner().downloadPlugin(server.URL + "/plugin")
	if err != nil {
		t.Fatalf("downloadPlugin: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(got), "gofly-plugin-"+pluginCacheFilename(parsed)+"-") {
		t.Fatalf("downloadPlugin path = %q, want unique temp filename with plugin prefix", got)
	}
	data, err := os.ReadFile(legacyTempPath)
	if err != nil {
		t.Fatalf("read legacy temp path: %v", err)
	}
	if string(data) != "do not clobber" {
		t.Fatalf("legacy temp path was clobbered: %q", data)
	}
}

func TestInstallRemotePluginCachesUnderHomeHashAndUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(t.TempDir(), "my-plugin")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf '%s' '{\"message\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	remote := source + "@v1.2.3"

	info, err := InstallRemotePlugin(remote)
	if err != nil {
		t.Fatalf("InstallRemotePlugin: %v", err)
	}
	wantPrefix := filepath.Join(home, ".cache", "gofly", "plugins", info.Hash)
	wantDigestBytes := sha256.Sum256([]byte("#!/bin/sh\nprintf '%s' '{\"message\":\"ok\"}'\n"))
	wantDigest := hex.EncodeToString(wantDigestBytes[:])
	if info.Remote != source || info.Version != "v1.2.3" || info.Hash == "" || info.Binary != filepath.Join(wantPrefix, "plugin") || info.BinaryDigest != wantDigest {
		t.Fatalf("installed plugin info = %+v, want cache under %s", info, wantPrefix)
	}
	if _, err := os.Stat(info.Binary); err != nil {
		t.Fatalf("cached plugin binary missing: %v", err)
	}

	cached, err := ResolveRemotePlugin(remote)
	if err != nil {
		t.Fatalf("ResolveRemotePlugin: %v", err)
	}
	if cached.Binary != info.Binary || cached.Hash != info.Hash || cached.Version != info.Version || cached.BinaryDigest != wantDigest {
		t.Fatalf("resolved cached plugin = %+v, want %+v", cached, info)
	}

	removed, err := UninstallRemotePlugin(remote)
	if err != nil {
		t.Fatalf("UninstallRemotePlugin: %v", err)
	}
	if removed != wantPrefix {
		t.Fatalf("removed cache dir = %q, want %q", removed, wantPrefix)
	}
	if _, err := os.Stat(removed); err == nil || !os.IsNotExist(err) {
		t.Fatalf("cache dir still exists or unexpected stat error: %v", err)
	}
}

func TestResolveRemotePluginRejectsDigestMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	source := filepath.Join(t.TempDir(), "my-plugin")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	remote := source + "@v1.0.0"
	info, err := InstallRemotePlugin(remote)
	if err != nil {
		t.Fatalf("InstallRemotePlugin: %v", err)
	}
	if err := os.WriteFile(info.Binary, []byte("#!/bin/sh\nprintf tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = ResolveRemotePlugin(remote)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("ResolveRemotePlugin after tamper error = %v, want digest mismatch", err)
	}
}

func TestResolveRemotePluginRejectsBadCacheMetadata(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		source := filepath.Join(t.TempDir(), "my-plugin")
		if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf ok\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		remote := source + "@v1.0.0"
		info, err := InstallRemotePlugin(remote)
		if err != nil {
			t.Fatalf("InstallRemotePlugin: %v", err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(info.Binary), "plugin.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err = ResolveRemotePlugin(remote)
		if err == nil || !strings.Contains(err.Error(), "read plugin cache metadata") {
			t.Fatalf("ResolveRemotePlugin invalid metadata error = %v, want metadata read error", err)
		}
	})

	t.Run("identity mismatch", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		source := filepath.Join(t.TempDir(), "my-plugin")
		if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf ok\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		remote := source + "@v1.0.0"
		info, err := InstallRemotePlugin(remote)
		if err != nil {
			t.Fatalf("InstallRemotePlugin: %v", err)
		}
		info.Version = "v9.9.9"
		data, err := json.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(info.Binary), "plugin.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}

		_, err = ResolveRemotePlugin(remote)
		if err == nil || !strings.Contains(err.Error(), "metadata mismatch") {
			t.Fatalf("ResolveRemotePlugin mismatched metadata error = %v, want metadata mismatch", err)
		}
	})
}

func TestResolveRemotePluginRejectsSymlinkedCachedBinary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	source := filepath.Join(t.TempDir(), "my-plugin")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	remote := source + "@v1.0.0"
	info, err := InstallRemotePlugin(remote)
	if err != nil {
		t.Fatalf("InstallRemotePlugin: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside-plugin")
	outsideData := []byte("#!/bin/sh\nprintf outside\n")
	if err := os.WriteFile(outside, outsideData, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(info.Binary); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, info.Binary); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	wantDigestBytes := sha256.Sum256(outsideData)
	info.BinaryDigest = hex.EncodeToString(wantDigestBytes[:])
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(info.Binary), "plugin.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = ResolveRemotePlugin(remote)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ResolveRemotePlugin symlinked binary error = %v, want symlink rejection", err)
	}
}

func TestInstallRemotePluginRejectsSymlinkCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(t.TempDir(), "my-plugin")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	remote := source + "@v1.0.0"
	spec, err := parseRemotePluginSpec(remote)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(home, ".cache", "gofly", "plugins")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cacheRoot, spec.hash)); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err = InstallRemotePlugin(remote)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("InstallRemotePlugin with symlink cache dir error = %v, want symlink rejection", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "plugin")); err == nil {
		t.Fatal("plugin was written through symlink cache dir")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat outside plugin: %v", err)
	}
}

func TestInstallRemotePluginRejectsSymlinkCacheRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(t.TempDir(), "my-plugin")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cacheParent := filepath.Join(home, ".cache", "gofly")
	if err := os.MkdirAll(cacheParent, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cacheParent, "plugins")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err := InstallRemotePlugin(source + "@v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("InstallRemotePlugin with symlink cache root error = %v, want symlink rejection", err)
	}
	if entries, err := os.ReadDir(outside); err != nil {
		t.Fatalf("read outside cache root: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("plugin cache root symlink target was written: %#v", entries)
	}
}

func TestInstallRemotePluginDownloadErrorIsReproducible(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusTeapot)
	}))
	t.Cleanup(server.Close)
	remote := server.URL + "/plugin@v9"
	spec, err := parseRemotePluginSpec(remote)
	if err != nil {
		t.Fatal(err)
	}

	_, err = InstallRemotePlugin(remote)
	if err == nil {
		t.Fatal("InstallRemotePlugin with failing download succeeded, want error")
	}
	for _, want := range []string{server.URL + "/plugin", "version=v9", "hash=" + spec.hash, "status 418"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("download error %q missing reproducible detail %q", err, want)
		}
	}
}

func TestResolveGoPluginPathsTraversesExecutableFiles(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a-plugin")
	secondDir := filepath.Join(dir, "nested")
	second := filepath.Join(secondDir, "b-plugin")
	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "not-executable"), []byte("skip"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsidePlugin := filepath.Join(outside, "outside-plugin")
	if err := os.WriteFile(outsidePlugin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Logf("symlink unsupported: %v", err)
	}

	paths, err := ResolveGoPluginPaths(dir)
	if err != nil {
		t.Fatalf("ResolveGoPluginPaths: %v", err)
	}
	want := []string{first, second}
	if len(paths) != len(want) {
		t.Fatalf("plugin paths = %#v, want %#v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("plugin paths = %#v, want %#v", paths, want)
		}
	}
}

func TestPluginRunnerRejectsProtocolVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin")
	if err := os.WriteFile(plugin, []byte("#!/bin/sh\nprintf '%s' '{\"version\":\"999\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := NewPluginRunner().Run(plugin, PluginRequest{Command: "api"})
	if err == nil || !strings.Contains(err.Error(), "protocol version 999") || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("PluginRunner.Run version error = %v, want incompatible version", err)
	}
}

func TestPluginManifestContractValidation(t *testing.T) {
	valid := PluginManifest{
		Name:               "example",
		Version:            "v0.1.0",
		CompatibleVersions: []string{PluginProtocolVersion},
		Capabilities:       []string{PluginCapabilityGenerateFile, PluginCapabilityPatchFile},
		Permissions:        []string{PluginPermissionWriteRelative},
		RequiresDryRun:     true,
	}
	if err := ValidatePluginManifest(valid); err != nil {
		t.Fatalf("ValidatePluginManifest(valid): %v", err)
	}
	if got, ok := NegotiatePluginProtocol([]string{"999", PluginProtocolVersion}); !ok || got != PluginProtocolVersion {
		t.Fatalf("NegotiatePluginProtocol = %q/%v, want %s/true", got, ok, PluginProtocolVersion)
	}

	tests := []struct {
		name string
		edit func(*PluginManifest)
		want string
	}{
		{name: "missing name", edit: func(m *PluginManifest) { m.Name = "" }, want: "name is required"},
		{name: "missing version", edit: func(m *PluginManifest) { m.Version = "" }, want: "version is required"},
		{name: "incompatible", edit: func(m *PluginManifest) { m.CompatibleVersions = []string{"999"} }, want: "incompatible"},
		{name: "missing capability", edit: func(m *PluginManifest) { m.Capabilities = nil }, want: "at least one capability"},
		{name: "unknown capability", edit: func(m *PluginManifest) { m.Capabilities = []string{"network:egress"} }, want: "unsupported capability"},
		{name: "unknown permission", edit: func(m *PluginManifest) { m.Permissions = []string{"filesystem:write-anywhere"} }, want: "unsupported permission"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := valid
			tt.edit(&manifest)
			err := ValidatePluginManifest(manifest)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidatePluginManifest() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPluginProtocolCompatibilityMatrix_BitsUT(t *testing.T) {
	tests := []struct {
		name     string
		versions []string
		wantOK   bool
	}{
		{name: "old protocol only", versions: []string{"0"}, wantOK: false},
		{name: "current protocol", versions: []string{PluginProtocolVersion}, wantOK: true},
		{name: "future plus current protocol", versions: []string{"2", PluginProtocolVersion}, wantOK: true},
		{name: "future protocol only", versions: []string{"2"}, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, ok := NegotiatePluginProtocol(tt.versions)
			if ok != tt.wantOK {
				t.Fatalf("NegotiatePluginProtocol(%v) ok = %v, want %v", tt.versions, ok, tt.wantOK)
			}
			if ok && selected != PluginProtocolVersion {
				t.Fatalf("NegotiatePluginProtocol(%v) selected = %q, want %s", tt.versions, selected, PluginProtocolVersion)
			}
		})
	}
}

func TestPluginProtocolSchemaContract(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(PluginProtocolSchema), &schema); err != nil {
		t.Fatalf("PluginProtocolSchema is not valid JSON: %v", err)
	}
	defs, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("PluginProtocolSchema missing $defs: %#v", schema)
	}
	for _, name := range []string{"manifest", "request", "response", "file", "patch"} {
		if _, ok := defs[name]; !ok {
			t.Fatalf("PluginProtocolSchema missing $defs.%s", name)
		}
	}
	manifestDef := defs["manifest"].(map[string]any)
	required := stringSetFromAnySlice(manifestDef["required"].([]any))
	for _, field := range []string{"name", "version", "compatibleVersions", "capabilities"} {
		if !required[field] {
			t.Fatalf("manifest schema required = %#v, want %s", required, field)
		}
	}
	requestDef := defs["request"].(map[string]any)
	requestRequired := stringSetFromAnySlice(requestDef["required"].([]any))
	for _, field := range []string{"magic", "version", "command", "service", "module", "style", "dir"} {
		if !requestRequired[field] {
			t.Fatalf("request schema required = %#v, want %s", requestRequired, field)
		}
	}
	requestProperties := requestDef["properties"].(map[string]any)
	magic := requestProperties["magic"].(map[string]any)
	if magic["const"] != pluginMagic {
		t.Fatalf("request magic const = %v, want %s", magic["const"], pluginMagic)
	}
	version := requestProperties["version"].(map[string]any)
	if version["const"] != PluginProtocolVersion {
		t.Fatalf("request version const = %v, want %s", version["const"], PluginProtocolVersion)
	}
	manifestProperties := manifestDef["properties"].(map[string]any)
	capabilities := manifestProperties["capabilities"].(map[string]any)["items"].(map[string]any)
	capabilityEnum := stringSetFromAnySlice(capabilities["enum"].([]any))
	for _, capability := range []string{PluginCapabilityGenerateFile, PluginCapabilityPatchFile} {
		if !capabilityEnum[capability] {
			t.Fatalf("schema capability enum = %#v, want %s", capabilityEnum, capability)
		}
	}
	permissions := manifestProperties["permissions"].(map[string]any)["items"].(map[string]any)
	permissionEnum := stringSetFromAnySlice(permissions["enum"].([]any))
	if !permissionEnum[PluginPermissionWriteRelative] {
		t.Fatalf("schema permission enum = %#v, want %s", permissionEnum, PluginPermissionWriteRelative)
	}
}

func stringSetFromAnySlice(values []any) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if ok {
			out[text] = true
		}
	}
	return out
}

func TestPluginRunnerValidatesManifestAndOutputPaths(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin")
	script := `#!/bin/sh
case "$1" in
  --bad-manifest)
    printf '%s' '{"version":"1","manifest":{"name":"bad","version":"v0.1.0","compatibleVersions":["999"],"capabilities":["generate:file"]}}'
    ;;
  --bad-path)
    printf '%s' '{"version":"1","files":[{"path":"../escape.txt","content":"owned"}]}'
    ;;
  *)
    printf '%s' '{"version":"1","manifest":{"name":"ok","version":"v0.1.0","compatibleVersions":["1"],"capabilities":["generate:file"],"permissions":["filesystem:write-relative"]},"files":[{"path":"internal/plugin.txt","content":"ok"}]}'
    ;;
esac
`
	if err := os.WriteFile(plugin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	resp, err := NewPluginRunner().Run(plugin, PluginRequest{Command: "service"})
	if err != nil {
		t.Fatalf("PluginRunner.Run valid manifest: %v", err)
	}
	if resp.Manifest == nil || resp.Manifest.Name != "ok" || len(resp.Files) != 1 {
		t.Fatalf("PluginRunner.Run valid manifest response = %+v, want manifest and one file", resp)
	}

	if _, err := NewPluginRunner().Run(plugin+" --bad-manifest", PluginRequest{Command: "service"}); err == nil || !strings.Contains(err.Error(), "manifest") || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("PluginRunner.Run bad manifest error = %v, want incompatible manifest", err)
	}
	if _, err := NewPluginRunner().Run(plugin+" --bad-path", PluginRequest{Command: "service"}); err == nil || !strings.Contains(err.Error(), "escapes output directory") {
		t.Fatalf("PluginRunner.Run bad path error = %v, want path escape", err)
	}
}

func TestPluginRegistryIndexValidationAndFiltering(t *testing.T) {
	const registryChecksum = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	index := PluginRegistryIndex{
		Version: "v1",
		Plugins: []PluginRegistryEntry{
			{
				Name:        "redis-cache",
				Remote:      "https://example.com/redis-cache",
				Version:     "v0.2.0",
				Protocol:    PluginProtocolVersion,
				Checksum:    registryChecksum,
				Source:      "https://github.com/example/gofly-redis-cache",
				Description: "Redis feature generator",
				Tags:        []string{"cache", "redis"},
				Manifest: PluginManifest{
					Name:               "redis-cache",
					Version:            "v0.2.0",
					CompatibleVersions: []string{PluginProtocolVersion},
					Capabilities:       []string{PluginCapabilityGenerateFile},
					Permissions:        []string{PluginPermissionWriteRelative},
				},
			},
			{
				Name:        "auth-jwt",
				Remote:      "https://example.com/auth-jwt",
				Version:     "v0.1.0",
				Protocol:    PluginProtocolVersion,
				Checksum:    registryChecksum,
				Source:      "https://github.com/example/gofly-auth-jwt",
				Description: "JWT auth generator",
				Tags:        []string{"auth", "jwt"},
				Manifest: PluginManifest{
					Name:               "auth-jwt",
					Version:            "v0.1.0",
					CompatibleVersions: []string{PluginProtocolVersion},
					Capabilities:       []string{PluginCapabilityGenerateFile},
					Permissions:        []string{PluginPermissionWriteRelative},
				},
			},
		},
	}
	if err := ValidatePluginRegistryIndex(index); err != nil {
		t.Fatalf("ValidatePluginRegistryIndex(valid): %v", err)
	}
	matches := FilterPluginRegistryEntries(index.Plugins, "redis")
	if len(matches) != 1 || matches[0].Name != "redis-cache" {
		t.Fatalf("FilterPluginRegistryEntries(redis) = %#v, want redis-cache", matches)
	}
	all := FilterPluginRegistryEntries(index.Plugins, "")
	if len(all) != 2 || all[0].Name != "auth-jwt" || all[1].Name != "redis-cache" {
		t.Fatalf("FilterPluginRegistryEntries(empty) = %#v, want sorted entries", all)
	}

	duplicate := index
	duplicate.Plugins = append(duplicate.Plugins, duplicate.Plugins[0])
	if err := ValidatePluginRegistryIndex(duplicate); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("ValidatePluginRegistryIndex(duplicate) error = %v, want duplicated", err)
	}
	badManifest := index
	badManifest.Plugins = append([]PluginRegistryEntry(nil), index.Plugins...)
	badManifest.Plugins[0].Manifest.Capabilities = []string{"network:egress"}
	if err := ValidatePluginRegistryIndex(badManifest); err == nil || !strings.Contains(err.Error(), "unsupported capability") {
		t.Fatalf("ValidatePluginRegistryIndex(badManifest) error = %v, want unsupported capability", err)
	}

	invalidCases := []struct {
		name  string
		index PluginRegistryIndex
		want  string
	}{
		{name: "missing registry version", index: PluginRegistryIndex{}, want: "version is required"},
		{name: "missing entry name", index: PluginRegistryIndex{Version: "v1", Plugins: []PluginRegistryEntry{{Remote: "https://example.com/plugin", Version: "v1"}}}, want: "entry name is required"},
		{name: "missing remote", index: PluginRegistryIndex{Version: "v1", Plugins: []PluginRegistryEntry{{Name: "missing-remote", Version: "v1"}}}, want: "requires remote and version"},
		{name: "missing protocol", index: PluginRegistryIndex{Version: "v1", Plugins: []PluginRegistryEntry{{Name: "missing-protocol", Remote: "https://example.com/plugin", Version: "v1"}}}, want: "requires protocol"},
		{name: "invalid checksum", index: PluginRegistryIndex{Version: "v1", Plugins: []PluginRegistryEntry{{Name: "bad-checksum", Remote: "https://example.com/plugin", Version: "v1", Protocol: PluginProtocolVersion, Checksum: "md5:bad", Source: "https://github.com/example/plugin"}}}, want: "sha256"},
		{name: "missing source", index: PluginRegistryIndex{Version: "v1", Plugins: []PluginRegistryEntry{{Name: "missing-source", Remote: "https://example.com/plugin", Version: "v1", Protocol: PluginProtocolVersion, Checksum: registryChecksum}}}, want: "source is required"},
		{name: "invalid remote spec", index: PluginRegistryIndex{Version: "v1", Plugins: []PluginRegistryEntry{{Name: "bad-remote", Remote: "https://example.com/plugin", Version: "../main", Protocol: PluginProtocolVersion, Checksum: registryChecksum, Source: "https://github.com/example/plugin"}}}, want: "remote plugin version"},
	}
	for _, tt := range invalidCases {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePluginRegistryIndex(tt.index)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidatePluginRegistryIndex(%s) error = %v, want %q", tt.name, err, tt.want)
			}
		})
	}
}

func TestLoadPluginRegistryIndexFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	data := `{
  "version": "v1",
  "plugins": [
    {
      "name": "auth-jwt",
      "remote": "https://example.com/auth-jwt",
      "version": "v0.1.0",
      "protocol": "1",
      "checksum": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "source": "https://github.com/example/gofly-auth-jwt",
      "manifest": {
        "name": "auth-jwt",
        "version": "v0.1.0",
        "compatibleVersions": ["1"],
        "capabilities": ["generate:file"],
        "permissions": ["filesystem:write-relative"]
      }
    }
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := LoadPluginRegistryIndex(path)
	if err != nil {
		t.Fatalf("LoadPluginRegistryIndex(file): %v", err)
	}
	if index.Version != "v1" || len(index.Plugins) != 1 || index.Plugins[0].Name != "auth-jwt" {
		t.Fatalf("LoadPluginRegistryIndex(file) = %#v, want auth-jwt registry", index)
	}

	badJSON := filepath.Join(t.TempDir(), "bad-registry.json")
	if err := os.WriteFile(badJSON, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPluginRegistryIndex(badJSON); err == nil || !strings.Contains(err.Error(), "decode plugin registry") {
		t.Fatalf("LoadPluginRegistryIndex(bad json) error = %v, want decode error", err)
	}
	badMetadata := filepath.Join(t.TempDir(), "bad-metadata.json")
	if err := os.WriteFile(badMetadata, []byte(`{"version":"v1","plugins":[{"name":"bad","remote":"https://example.com/bad","version":"v1","protocol":"1","checksum":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","source":"https://github.com/example/bad","manifest":{"name":"bad","version":"v1","compatibleVersions":["999"],"capabilities":["generate:file"]}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPluginRegistryIndex(badMetadata); err == nil || !strings.Contains(err.Error(), "manifest") || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("LoadPluginRegistryIndex(bad metadata) error = %v, want manifest incompatibility", err)
	}
}

func TestLoadPluginRegistryIndexFromLocalURL_BitsUT(t *testing.T) {
	registryJSON := `{
  "version":"v1",
  "plugins":[{
    "name":"auth-jwt",
    "remote":"https://example.com/auth-jwt",
    "version":"v0.1.0",
    "protocol":"1",
    "checksum":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "source":"https://github.com/example/gofly-auth-jwt",
    "manifest":{"name":"auth-jwt","version":"v0.1.0","compatibleVersions":["1"],"capabilities":["generate:file"],"permissions":["filesystem:write-relative"]}
  }]
}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/registry.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(registryJSON))
		case "/bad.json":
			_, _ = w.Write([]byte(`{`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	index, err := LoadPluginRegistryIndex(server.URL + "/registry.json")
	if err != nil {
		t.Fatalf("LoadPluginRegistryIndex(local URL): %v", err)
	}
	if index.Version != "v1" || len(index.Plugins) != 1 || index.Plugins[0].Name != "auth-jwt" {
		t.Fatalf("index = %#v, want auth-jwt registry", index)
	}
	if _, err := LoadPluginRegistryIndex(server.URL + "/missing.json"); err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("missing registry URL error = %v, want status 404", err)
	}
	if _, err := LoadPluginRegistryIndex(server.URL + "/bad.json"); err == nil || !strings.Contains(err.Error(), "decode plugin registry") {
		t.Fatalf("bad registry URL error = %v, want decode plugin registry", err)
	}
	if _, err := LoadPluginRegistryIndex("http://example.com/registry.json"); err == nil || !strings.Contains(err.Error(), "insecure URL scheme") {
		t.Fatalf("remote http registry error = %v, want insecure URL scheme", err)
	}
}

func TestPluginRunnerExternalExecutionBranches_BitsUT(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin")
	script := `#!/bin/sh
case "$1" in
  --plain)
    printf 'plain output'
    ;;
  --empty)
    exit 0
    ;;
  --fail)
    printf 'boom stderr' >&2
    exit 7
    ;;
  --sleep)
    sleep 1
    printf '%s' '{"version":"1"}'
    ;;
  *)
    printf '{"version":"1","message":"%s:%s"}' "$1" "$2"
    ;;
esac
`
	if err := os.WriteFile(plugin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &PluginRunner{Timeout: 0}
	resp, err := runner.Run(plugin+" --one two", PluginRequest{Command: "service", Service: "hello"})
	if err != nil {
		t.Fatalf("PluginRunner.Run json plugin: %v", err)
	}
	if resp.Version != pluginVersion || resp.Message != "--one:two" {
		t.Fatalf("json plugin response = %+v, want parsed version and argv message", resp)
	}

	resp, err = runner.Run(plugin+" --plain", PluginRequest{Command: "service"})
	if err != nil {
		t.Fatalf("PluginRunner.Run plain plugin: %v", err)
	}
	if len(resp.Files) != 1 || resp.Files[0].Content != "plain output" {
		t.Fatalf("plain plugin response = %+v, want fallback file content", resp)
	}

	resp, err = runner.Run(plugin+" --empty", PluginRequest{Command: "service"})
	if err != nil {
		t.Fatalf("PluginRunner.Run empty plugin: %v", err)
	}
	if resp.Version != "" || resp.Message != "" || resp.Error != "" || len(resp.Files) != 0 || len(resp.Patches) != 0 {
		t.Fatalf("empty plugin response = %+v, want zero response", resp)
	}

	if _, err := runner.Run("   ", PluginRequest{}); err == nil || !strings.Contains(err.Error(), "empty plugin") {
		t.Fatalf("PluginRunner.Run empty name error = %v, want empty plugin", err)
	}
	if _, err := runner.Run(plugin+" --fail", PluginRequest{}); err == nil || !strings.Contains(err.Error(), "boom stderr") {
		t.Fatalf("PluginRunner.Run failing plugin error = %v, want stderr", err)
	}
	if _, err := (&PluginRunner{Timeout: time.Millisecond}).Run(plugin+" --sleep", PluginRequest{}); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("PluginRunner.Run timeout error = %v, want timed out", err)
	}
}

func TestInternalPluginRegistryAndRunner(t *testing.T) {
	name := "bits-ut-" + strings.ReplaceAll(t.Name(), "/", "-")
	if !RegisterInternalPlugin(bitsUTInternalPlugin{name: name}) {
		t.Fatal("RegisterInternalPlugin first registration returned false")
	}
	if RegisterInternalPlugin(bitsUTInternalPlugin{name: name}) {
		t.Fatal("RegisterInternalPlugin duplicate returned true")
	}
	if RegisterInternalPlugin(nil) {
		t.Fatal("RegisterInternalPlugin nil returned true")
	}
	plugins := ListInternalPlugins()
	found := false
	for _, plugin := range plugins {
		if plugin == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListInternalPlugins() = %v, want %s", plugins, name)
	}
	resp, err := NewPluginRunner().Run(name, PluginRequest{Command: "service", Service: "hello"})
	if err != nil {
		t.Fatalf("run internal plugin: %v", err)
	}
	if resp.Message != "hello" {
		t.Fatalf("internal plugin response = %+v", resp)
	}
}

func TestServiceFilesystemSinkRunPluginsWritesFilesPatchesAndMessages(t *testing.T) {
	name := "bits-ut-scaffold-" + strings.ReplaceAll(t.Name(), "/", "-")
	if !RegisterInternalPlugin(bitsUTScaffoldPlugin{name: name}) {
		t.Fatal("RegisterInternalPlugin scaffold plugin returned false")
	}
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "cmd", "main.go")
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	err := serviceFilesystemSink{Dir: dir, Stderr: &stderr}.RunPlugins(serviceScaffoldIR{
		Name:    "hello",
		Module:  "example.com/hello",
		Dir:     dir,
		Style:   ServiceStyleProduction,
		Kind:    "service",
		Plugins: []string{name},
	})
	if err != nil {
		t.Fatalf("RunPlugins: %v", err)
	}
	if !strings.Contains(stderr.String(), "[gofly] plugin "+name+": hello:service") {
		t.Fatalf("plugin stderr = %q", stderr.String())
	}
	requestData, err := os.ReadFile(filepath.Join(dir, "plugin", "request.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(requestData) != "service|example.com/hello|production" {
		t.Fatalf("plugin request file = %q", requestData)
	}
	mainData, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainData), "// patched by scaffold plugin") {
		t.Fatalf("plugin patch was not applied:\n%s", mainData)
	}
}

func TestListInstalledPluginsSortsAndBackfillsDigest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".cache", "gofly", "plugins")
	entries := []InstalledPlugin{
		{Remote: "z.example/plugin", Version: "v2", Hash: "hash-z", Installed: "2026-01-02T00:00:00Z"},
		{Remote: "a.example/plugin", Version: "v1", Hash: "hash-a", Installed: "2026-01-01T00:00:00Z"},
	}
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Hash)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin"), []byte(entry.Remote), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "not-a-dir"), []byte("skip"), 0o644); err != nil {
		t.Fatal(err)
	}
	badDir := filepath.Join(root, "bad-json")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "plugin.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	installed, err := ListInstalledPlugins()
	if err != nil {
		t.Fatalf("ListInstalledPlugins: %v", err)
	}
	if len(installed) != 2 {
		t.Fatalf("installed plugins = %+v, want two valid entries", installed)
	}
	if installed[0].Remote != "a.example/plugin" || installed[1].Remote != "z.example/plugin" {
		t.Fatalf("installed plugin order = %+v, want sorted by remote/version", installed)
	}
	for _, plugin := range installed {
		if plugin.BinaryDigest == "" || plugin.Binary != filepath.Join(root, plugin.Hash, "plugin") {
			t.Fatalf("installed plugin metadata missing digest/binary: %+v", plugin)
		}
	}
}
