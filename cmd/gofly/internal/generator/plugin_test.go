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
