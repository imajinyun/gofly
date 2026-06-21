package generator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 以下常量定义 gofly 自有插件协议。
const (
	pluginMagic   = "GOFLY_PLUGIN"
	pluginVersion = "1"

	defaultPluginTimeout     = 30 * time.Second
	maxDownloadedPluginBytes = 100 << 20
	maxPluginCacheNameLength = 80
	maxPluginOutputBytes     = 10 << 20
	maxPluginErrorBytes      = 64 << 10

	// EnvPluginMode 作为环境变量传递给插件以启用 gofly 插件模式。
	EnvPluginMode = "GOFLY_PLUGIN_MODE"
	// EnvPluginArgs 存放插件调用参数（JSON）。
	EnvPluginArgs = "GOFLY_PLUGIN_ARGS"
	// PluginProtocolVersion is the current stable external generator plugin protocol.
	PluginProtocolVersion = pluginVersion
)

const (
	// PluginCapabilityGenerateFile allows a plugin to return relative file writes.
	PluginCapabilityGenerateFile = "generate:file"
	// PluginCapabilityPatchFile allows a plugin to return anchored file patches.
	PluginCapabilityPatchFile = "generate:patch"
	// PluginPermissionWriteRelative declares that a plugin only writes host-validated relative paths.
	PluginPermissionWriteRelative = "filesystem:write-relative"
)

// PluginProtocolSchema is the JSON Schema for gofly external generator plugin
// manifest, request, and response payloads. The host still performs Go-side
// validation for path containment and compatibility negotiation; this schema is
// published so third-party authors can validate payload shape in any language.
const PluginProtocolSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://gofly.dev/schemas/plugin-protocol.v1.json",
  "title": "gofly generator plugin protocol v1",
  "type": "object",
  "additionalProperties": false,
  "required": ["manifest", "request", "response"],
  "properties": {
    "manifest": { "$ref": "#/$defs/manifest" },
    "request": { "$ref": "#/$defs/request" },
    "response": { "$ref": "#/$defs/response" }
  },
  "$defs": {
    "manifest": {
      "type": "object",
      "additionalProperties": false,
      "required": ["name", "version", "compatibleVersions", "capabilities"],
      "properties": {
        "name": { "type": "string", "minLength": 1 },
        "version": { "type": "string", "minLength": 1 },
        "compatibleVersions": {
          "type": "array",
          "minItems": 1,
          "items": { "type": "string", "enum": ["1"] }
        },
        "capabilities": {
          "type": "array",
          "minItems": 1,
          "items": { "type": "string", "enum": ["generate:file", "generate:patch"] }
        },
        "permissions": {
          "type": "array",
          "items": { "type": "string", "enum": ["filesystem:write-relative"] }
        },
        "requiresDryRun": { "type": "boolean" }
      }
    },
    "request": {
      "type": "object",
      "additionalProperties": false,
      "required": ["magic", "version", "command", "service", "module", "style", "dir"],
      "properties": {
        "magic": { "type": "string", "const": "GOFLY_PLUGIN" },
        "version": { "type": "string", "const": "1" },
        "command": { "type": "string", "enum": ["service", "handler", "model", "api", "rpc"] },
        "service": { "type": "string" },
        "module": { "type": "string" },
        "style": { "type": "string" },
        "dir": { "type": "string" },
        "input": { "type": "object", "additionalProperties": { "type": "string" } },
        "idl": { "type": "string", "contentEncoding": "base64" },
        "idlFormat": { "type": "string", "enum": ["", "proto", "api", "openapi", "thrift"] },
        "config": { "type": ["object", "null"] },
        "dryRun": { "type": "boolean" }
      }
    },
    "response": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "version": { "type": "string", "enum": ["", "1"] },
        "manifest": { "$ref": "#/$defs/manifest" },
        "files": { "type": "array", "items": { "$ref": "#/$defs/file" } },
        "patches": { "type": "array", "items": { "$ref": "#/$defs/patch" } },
        "message": { "type": "string" },
        "error": { "type": "string" }
      }
    },
    "file": {
      "type": "object",
      "additionalProperties": false,
      "required": ["path", "content"],
      "properties": {
        "path": { "type": "string", "minLength": 1, "not": { "pattern": "(^/|^\\\\|(^|[\\\\/])\\.\\.([\\\\/]|$)|:)" } },
        "content": { "type": "string" }
      }
    },
    "patch": {
      "type": "object",
      "additionalProperties": false,
      "required": ["path", "patch"],
      "properties": {
        "path": { "type": "string", "minLength": 1, "not": { "pattern": "(^/|^\\\\|(^|[\\\\/])\\.\\.([\\\\/]|$)|:)" } },
        "patch": { "type": "string" },
        "insertAfter": { "type": "string" }
      }
    }
  }
}`

// PluginRequest 描述 gofly 发给插件的请求。
type PluginRequest struct {
	Magic     string            `json:"magic"`
	Version   string            `json:"version"`
	Command   string            `json:"command"` // "service", "handler", "model"
	Service   string            `json:"service"`
	Module    string            `json:"module"`
	Style     string            `json:"style"`
	Dir       string            `json:"dir"`
	Input     map[string]string `json:"input,omitempty"`
	IDL       []byte            `json:"idl,omitempty"`
	IDLFormat string            `json:"idlFormat,omitempty"` // "proto", "api", "openapi"
	Config    *Config           `json:"config,omitempty"`
	DryRun    bool              `json:"dryRun,omitempty"`
}

// PluginManifest declares an external generator plugin's compatibility,
// capabilities, and security posture before it is executed for generation.
type PluginManifest struct {
	Name               string   `json:"name"`
	Version            string   `json:"version"`
	CompatibleVersions []string `json:"compatibleVersions"`
	Capabilities       []string `json:"capabilities,omitempty"`
	Permissions        []string `json:"permissions,omitempty"`
	RequiresDryRun     bool     `json:"requiresDryRun,omitempty"`
}

// PluginFile 描述插件希望写入的文件。
type PluginFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// PluginPatch 描述插件对已有文件的修改。
type PluginPatch struct {
	Path        string `json:"path"`
	Patch       string `json:"patch"`
	InsertAfter string `json:"insertAfter,omitempty"`
}

// PluginResponse 是插件返回的结果。
type PluginResponse struct {
	Version  string          `json:"version,omitempty"`
	Manifest *PluginManifest `json:"manifest,omitempty"`
	Files    []PluginFile    `json:"files,omitempty"`
	Patches  []PluginPatch   `json:"patches,omitempty"`
	Message  string          `json:"message,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// InstalledPlugin describes a plugin cached by `gofly plugin install`.
type InstalledPlugin struct {
	Remote       string          `json:"remote"`
	Version      string          `json:"version"`
	Hash         string          `json:"hash"`
	Binary       string          `json:"binary"`
	BinaryDigest string          `json:"binary_digest"`
	Installed    string          `json:"installed"`
	Manifest     *PluginManifest `json:"manifest,omitempty"`
}

// PluginRegistryEntry describes one discoverable plugin in a registry index.
type PluginRegistryEntry struct {
	Name        string         `json:"name"`
	Remote      string         `json:"remote"`
	Version     string         `json:"version"`
	Description string         `json:"description,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Manifest    PluginManifest `json:"manifest"`
}

// PluginRegistryIndex is a JSON registry document consumed by `gofly plugin search`.
type PluginRegistryIndex struct {
	Version string                `json:"version"`
	Plugins []PluginRegistryEntry `json:"plugins"`
}

// Plugin 是 gofly 内部插件接口。
type Plugin interface {
	Name() string
	Generate(req PluginRequest) (PluginResponse, error)
}

// PluginRunner 负责运行插件。
type PluginRunner struct {
	Stdout io.Writer
	Stderr io.Writer
	// Timeout bounds external plugin execution so generators cannot hang forever.
	Timeout time.Duration
}

// NewPluginRunner 以默认设置创建一个插件执行器。
func NewPluginRunner() *PluginRunner {
	return &PluginRunner{Stdout: os.Stdout, Stderr: os.Stderr, Timeout: defaultPluginTimeout}
}

// Run 运行一个外部或内部插件。
//
// plugin 可以是:
//  1. 一个在 PATH 中可执行的二进制。
//  2. 一个以 http(s):// 开头的 URL（会被下载到用户缓存目录再运行）。
//  3. 已注册的内部插件名称（通过 RegisterInternalPlugin）。
func (r *PluginRunner) Run(plugin string, req PluginRequest) (PluginResponse, error) {
	plugin, extraArgs := splitPluginArgs(plugin)
	if plugin == "" {
		return PluginResponse{}, errors.New("empty plugin")
	}

	if p, ok := getInternalPlugin(plugin); ok {
		resp, err := p.Generate(req)
		if err != nil {
			return PluginResponse{}, err
		}
		return validatePluginResponse(plugin, resp)
	}

	bin, err := r.resolveBinary(plugin)
	if err != nil {
		return PluginResponse{}, err
	}
	if isPluginURL(plugin) {
		defer func() { _ = os.Remove(bin) }()
	}

	if req.Magic == "" {
		req.Magic = pluginMagic
	}
	if req.Version == "" {
		req.Version = pluginVersion
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return PluginResponse{}, fmt.Errorf("marshal plugin request: %w", err)
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultPluginTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// #nosec G204 -- plugin execution is an explicit CLI feature; binary is resolved via registry/path/remote cache and arguments are passed without a shell.
	cmd := exec.CommandContext(ctx, bin, extraArgs...)
	cmd.Env = pluginEnvironment(string(payload))
	cmd.Stdin = bytes.NewReader(payload)
	stdout := newLimitedPluginBuffer("stdout", maxPluginOutputBytes)
	stderr := newLimitedPluginBuffer("stderr", maxPluginErrorBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if stdout.exceeded() {
			return PluginResponse{}, fmt.Errorf("plugin %s stdout exceeds %d bytes", plugin, maxPluginOutputBytes)
		}
		if stderr.exceeded() {
			return PluginResponse{}, fmt.Errorf("plugin %s stderr exceeds %d bytes", plugin, maxPluginErrorBytes)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return PluginResponse{}, fmt.Errorf("plugin %s timed out after %s", plugin, timeout)
		}
		return PluginResponse{}, fmt.Errorf("plugin %s: %w: %s", plugin, err, stderr.String())
	}

	out := stdout.Bytes()
	if len(out) == 0 {
		return PluginResponse{}, nil
	}
	var resp PluginResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		// 不强制要求返回 JSON，允许插件直接输出文本作为单个文件的内容。
		return PluginResponse{Files: []PluginFile{{Path: "", Content: string(out)}}}, nil
	}
	return validatePluginResponse(plugin, resp)
}

func validatePluginResponse(plugin string, resp PluginResponse) (PluginResponse, error) {
	if resp.Version != "" && resp.Version != pluginVersion {
		return PluginResponse{}, fmt.Errorf("plugin %s protocol version %s is incompatible with gofly plugin protocol %s", plugin, resp.Version, pluginVersion)
	}
	if resp.Manifest != nil {
		if err := ValidatePluginManifest(*resp.Manifest); err != nil {
			return PluginResponse{}, fmt.Errorf("plugin %s manifest: %w", plugin, err)
		}
	}
	if msg := strings.TrimSpace(resp.Error); msg != "" {
		return PluginResponse{}, fmt.Errorf("plugin %s returned error: %s", plugin, msg)
	}
	for _, f := range resp.Files {
		if f.Path == "" {
			continue
		}
		if err := validatePluginOutputPath(f.Path); err != nil {
			return PluginResponse{}, fmt.Errorf("plugin %s file: %w", plugin, err)
		}
	}
	for _, p := range resp.Patches {
		if p.Path == "" {
			continue
		}
		if err := validatePluginOutputPath(p.Path); err != nil {
			return PluginResponse{}, fmt.Errorf("plugin %s patch: %w", plugin, err)
		}
	}
	return resp, nil
}

// ValidatePluginManifest checks a plugin manifest before the host trusts its
// declared capabilities. It performs compatibility negotiation and rejects
// unknown capability or permission strings so manifests remain auditable.
func ValidatePluginManifest(manifest PluginManifest) error {
	if strings.TrimSpace(manifest.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return fmt.Errorf("plugin %s version is required", manifest.Name)
	}
	if _, ok := NegotiatePluginProtocol(manifest.CompatibleVersions); !ok {
		return fmt.Errorf("plugin %s is incompatible with gofly plugin protocol %s", manifest.Name, pluginVersion)
	}
	if len(manifest.Capabilities) == 0 {
		return fmt.Errorf("plugin %s must declare at least one capability", manifest.Name)
	}
	for _, capability := range manifest.Capabilities {
		switch strings.TrimSpace(capability) {
		case PluginCapabilityGenerateFile, PluginCapabilityPatchFile:
		default:
			return fmt.Errorf("plugin %s declares unsupported capability %q", manifest.Name, capability)
		}
	}
	for _, permission := range manifest.Permissions {
		switch strings.TrimSpace(permission) {
		case PluginPermissionWriteRelative:
		default:
			return fmt.Errorf("plugin %s declares unsupported permission %q", manifest.Name, permission)
		}
	}
	return nil
}

// NegotiatePluginProtocol returns the first host-supported protocol version
// from a plugin's compatibility list.
func NegotiatePluginProtocol(versions []string) (string, bool) {
	for _, version := range versions {
		if strings.TrimSpace(version) == pluginVersion {
			return pluginVersion, true
		}
	}
	return "", false
}

func validatePluginOutputPath(name string) error {
	if strings.TrimSpace(name) == "" || filepath.IsAbs(name) {
		return fmt.Errorf("path %q must be relative", name)
	}
	if strings.Contains(name, ":") {
		return fmt.Errorf("path %q must be relative", name)
	}
	cleanName := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(name, `\`, "/")))
	if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes output directory", name)
	}
	for _, part := range strings.FieldsFunc(cleanName, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return fmt.Errorf("path %q escapes output directory", name)
		}
	}
	return nil
}

func pluginEnvironment(payload string) []string {
	env := []string{
		EnvPluginMode + "=1",
		EnvPluginArgs + "=" + payload,
	}
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "TMPDIR", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

type limitedPluginBuffer struct {
	name  string
	limit int
	buf   bytes.Buffer
	over  bool
}

func newLimitedPluginBuffer(name string, limit int) *limitedPluginBuffer {
	return &limitedPluginBuffer{name: name, limit: limit}
}

func (b *limitedPluginBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.buf.Write(p)
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.over = true
		return 0, fmt.Errorf("plugin %s exceeds %d bytes", b.name, b.limit)
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.over = true
		return remaining, fmt.Errorf("plugin %s exceeds %d bytes", b.name, b.limit)
	}
	return b.buf.Write(p)
}

func (b *limitedPluginBuffer) Bytes() []byte { return b.buf.Bytes() }

func (b *limitedPluginBuffer) String() string { return b.buf.String() }

func (b *limitedPluginBuffer) exceeded() bool { return b.over }

// WriteFiles 把插件返回的文件写入目标目录，返回成功写入的文件数量。
func (resp PluginResponse) WriteFiles(dir string) (int, error) {
	if dir == "" {
		return 0, errors.New("output directory is required")
	}
	count := 0
	for _, f := range resp.Files {
		if f.Path == "" {
			continue
		}
		if err := writeGeneratedFileUnder(dir, f.Path, []byte(f.Content)); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// ApplyPatches 对目标目录应用补丁（简单插入式插入）。
func (resp PluginResponse) ApplyPatches(dir string) error {
	if dir == "" {
		return errors.New("output directory is required")
	}
	for _, p := range resp.Patches {
		if p.Path == "" {
			continue
		}
		target, err := safePluginTarget(dir, p.Path)
		if err != nil {
			return err
		}
		if err := rejectExistingSymlinkTarget(target, "plugin"); err != nil {
			return err
		}
		// #nosec G304 -- target is constrained by safePluginTarget before reading patch targets.
		data, err := os.ReadFile(target)
		if err != nil {
			return fmt.Errorf("read target for patch: %w", err)
		}
		content := string(data)
		if p.InsertAfter != "" {
			idx := strings.Index(content, p.InsertAfter)
			if idx < 0 {
				return fmt.Errorf("plugin patch anchor %q not found in %s", p.InsertAfter, p.Path)
			}
			insertAt := idx + len(p.InsertAfter)
			content = content[:insertAt] + "\n" + p.Patch + content[insertAt:]
		} else {
			content += "\n" + p.Patch
		}
		// #nosec G306 G703 -- target is constrained by safePluginTarget before patching; generated project files are intentionally user-readable.
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write patched file: %w", err)
		}
	}
	return nil
}

func safePluginTarget(root, name string) (string, error) {
	return safeRelativeTarget(root, name, "plugin")
}

func safeRelativeTarget(root, name string, label string) (string, error) {
	if root == "" {
		return "", errors.New("output directory is required")
	}
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("%s path %q must be relative", label, name)
	}
	cleanName := filepath.Clean(name)
	if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path %q escapes output directory", label, name)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve output directory: %w", err)
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve output directory symlinks: %w", err)
	}
	if err := rejectSymlinkParent(absRoot, cleanName, label); err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(absRoot, cleanName))
	if err != nil {
		return "", fmt.Errorf("resolve %s path %q: %w", label, name, err)
	}
	rel, err := filepath.Rel(absRoot, target)
	if err != nil {
		return "", fmt.Errorf("rel %s path %q: %w", label, name, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path %q escapes output directory", label, name)
	}
	return target, nil
}

func rejectPluginSymlinkParent(root, cleanName string) error {
	return rejectSymlinkParent(root, cleanName, "plugin")
}

func rejectSymlinkParent(root, cleanName string, label string) error {
	current := root
	parts := strings.Split(cleanName, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("inspect %s path %q: %w", label, current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s path %q traverses symlink %q", label, cleanName, part)
		}
	}
	return nil
}

func rejectExistingSymlinkTarget(target, label string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect %s target %q: %w", label, target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s target %q is a symlink", label, target)
	}
	return nil
}

func (r *PluginRunner) resolveBinary(plugin string) (string, error) {
	if isPluginURL(plugin) {
		return r.downloadPlugin(plugin)
	}
	path, err := exec.LookPath(plugin)
	if err != nil {
		return "", fmt.Errorf("lookup plugin %s: %w", plugin, err)
	}
	return path, nil
}

func isPluginURL(plugin string) bool {
	return strings.HasPrefix(plugin, "http://") || strings.HasPrefix(plugin, "https://")
}

func (r *PluginRunner) downloadPlugin(url string) (string, error) {
	parsed, err := urlpkgParse(url)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "https" && !isLocalPluginURL(parsed) {
		return "", fmt.Errorf("download plugin: insecure URL scheme %q", parsed.Scheme)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download plugin: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download plugin: status %d", resp.StatusCode)
	}
	f, err := os.CreateTemp("", "gofly-plugin-"+pluginCacheFilename(parsed)+"-*.tmp")
	if err != nil {
		return "", fmt.Errorf("write plugin: %w", err)
	}
	tmp := f.Name()
	installed := false
	defer func() {
		if !installed {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o755); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("chmod plugin temp file: %w", err)
	}
	closeFile := func() error {
		if f == nil {
			return nil
		}
		err := f.Close()
		f = nil
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadedPluginBytes+1)); err != nil {
		_ = closeFile()
		return "", fmt.Errorf("copy plugin binary: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = closeFile()
		return "", fmt.Errorf("stat plugin binary: %w", err)
	}
	if info.Size() > maxDownloadedPluginBytes {
		_ = closeFile()
		return "", fmt.Errorf("download plugin: binary exceeds %d bytes", maxDownloadedPluginBytes)
	}
	if err := closeFile(); err != nil {
		return "", fmt.Errorf("close plugin binary: %w", err)
	}
	installed = true
	return tmp, nil
}

// InstallRemotePlugin downloads or copies a version-pinned plugin into
// ~/.cache/gofly/plugins/<hash>. The remote must use <repo-or-url>@<version>
// so cache entries are reproducible and never silently float.
func InstallRemotePlugin(remote string) (InstalledPlugin, error) {
	spec, err := parseRemotePluginSpec(remote)
	if err != nil {
		return InstalledPlugin{}, err
	}
	dir, err := remotePluginCacheDir(spec.hash)
	if err != nil {
		return InstalledPlugin{}, err
	}
	bin := filepath.Join(dir, "plugin")
	meta := filepath.Join(dir, "plugin.json")
	if err := prepareRemotePluginCacheDir(dir); err != nil {
		return InstalledPlugin{}, err
	}
	tmp, err := os.CreateTemp(dir, "plugin-*.tmp")
	if err != nil {
		return InstalledPlugin{}, fmt.Errorf("create plugin cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	installed := false
	defer func() {
		if !installed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return InstalledPlugin{}, fmt.Errorf("chmod plugin cache temp file: %w", err)
	}
	if err := copyRemotePluginPayload(tmp, spec); err != nil {
		_ = tmp.Close()
		return InstalledPlugin{}, err
	}
	if err := tmp.Close(); err != nil {
		return InstalledPlugin{}, fmt.Errorf("close plugin cache temp file: %w", err)
	}
	if err := os.Rename(tmpPath, bin); err != nil {
		return InstalledPlugin{}, fmt.Errorf("install plugin cache %s: %w", dir, err)
	}
	installed = true
	digest, err := fileSHA256(bin)
	if err != nil {
		return InstalledPlugin{}, fmt.Errorf("digest plugin binary %s: %w", bin, err)
	}
	info := InstalledPlugin{Remote: spec.remote, Version: spec.version, Hash: spec.hash, Binary: bin, BinaryDigest: digest, Installed: time.Now().UTC().Format(time.RFC3339)}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return InstalledPlugin{}, fmt.Errorf("marshal plugin metadata: %w", err)
	}
	if err := os.WriteFile(meta, append(data, '\n'), 0o600); err != nil {
		return InstalledPlugin{}, fmt.Errorf("write plugin metadata %s: %w", meta, err)
	}
	return info, nil
}

// ResolveRemotePlugin returns the cached binary for a remote plugin, installing
// it when the version-pinned cache entry is missing.
func ResolveRemotePlugin(remote string) (InstalledPlugin, error) {
	spec, err := parseRemotePluginSpec(remote)
	if err != nil {
		return InstalledPlugin{}, err
	}
	dir, err := remotePluginCacheDir(spec.hash)
	if err != nil {
		return InstalledPlugin{}, err
	}
	meta := filepath.Join(dir, "plugin.json")
	bin := filepath.Join(dir, "plugin")
	// #nosec G304 -- plugin metadata is read from the hash-derived gofly plugin cache directory.
	data, err := os.ReadFile(meta)
	if err == nil {
		var info InstalledPlugin
		if err := json.Unmarshal(data, &info); err != nil {
			return InstalledPlugin{}, fmt.Errorf("read plugin cache metadata %s: %w", meta, err)
		}
		if info.Remote != spec.remote || info.Version != spec.version || info.Hash != spec.hash {
			return InstalledPlugin{}, fmt.Errorf("plugin cache metadata mismatch for %s@%s hash=%s", spec.remote, spec.version, spec.hash)
		}
		if st, err := os.Stat(bin); err != nil {
			return InstalledPlugin{}, fmt.Errorf("stat cached plugin %s: %w", bin, err)
		} else if st.IsDir() || st.Mode()&0o111 == 0 {
			return InstalledPlugin{}, fmt.Errorf("cached plugin %s is not executable", bin)
		}
		digest, err := fileSHA256(bin)
		if err != nil {
			return InstalledPlugin{}, fmt.Errorf("digest cached plugin %s: %w", bin, err)
		}
		if info.BinaryDigest != "" && info.BinaryDigest != digest {
			return InstalledPlugin{}, fmt.Errorf("cached plugin %s digest mismatch: metadata=%s actual=%s", bin, info.BinaryDigest, digest)
		}
		info.Binary = bin
		if info.BinaryDigest == "" {
			info.BinaryDigest = digest
		}
		return info, nil
	}
	if !os.IsNotExist(err) {
		return InstalledPlugin{}, fmt.Errorf("read plugin cache metadata %s: %w", meta, err)
	}
	return InstallRemotePlugin(remote)
}

func UninstallRemotePlugin(remote string) (string, error) {
	spec, err := parseRemotePluginSpec(remote)
	if err != nil {
		return "", err
	}
	dir, err := remotePluginCacheDir(spec.hash)
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("remove plugin cache %s: %w", dir, err)
	}
	return dir, nil
}

func ListInstalledPlugins() ([]InstalledPlugin, error) {
	root, err := remotePluginCacheRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugin cache root %s: %w", root, err)
	}
	out := make([]InstalledPlugin, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta := filepath.Join(root, entry.Name(), "plugin.json")
		// #nosec G304 -- installed plugin metadata is enumerated from entries inside the gofly plugin cache root.
		data, err := os.ReadFile(meta)
		if err != nil {
			continue
		}
		var info InstalledPlugin
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		info.Binary = filepath.Join(root, entry.Name(), "plugin")
		if info.BinaryDigest == "" {
			digest, err := fileSHA256(info.Binary)
			if err == nil {
				info.BinaryDigest = digest
			}
		}
		out = append(out, info)
	}
	sortInstalledPlugins(out)
	return out, nil
}

// LoadPluginRegistryIndex loads and validates a JSON plugin registry from an
// HTTPS URL, localhost HTTP URL, or local filesystem path.
func LoadPluginRegistryIndex(location string) (PluginRegistryIndex, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return PluginRegistryIndex{}, errors.New("plugin registry location is required")
	}
	var data []byte
	if isPluginURL(location) {
		parsed, err := urlpkgParse(location)
		if err != nil {
			return PluginRegistryIndex{}, err
		}
		if parsed.Scheme != "https" && !isLocalPluginURL(parsed) {
			return PluginRegistryIndex{}, fmt.Errorf("plugin registry: insecure URL scheme %q", parsed.Scheme)
		}
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(location)
		if err != nil {
			return PluginRegistryIndex{}, fmt.Errorf("read plugin registry %s: %w", location, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return PluginRegistryIndex{}, fmt.Errorf("read plugin registry %s: status %d", location, resp.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return PluginRegistryIndex{}, fmt.Errorf("read plugin registry %s: %w", location, err)
		}
	} else {
		var err error
		data, err = os.ReadFile(location) // #nosec G304 -- registry path is an explicit operator-selected JSON index.
		if err != nil {
			return PluginRegistryIndex{}, fmt.Errorf("read plugin registry %s: %w", location, err)
		}
	}
	var index PluginRegistryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return PluginRegistryIndex{}, fmt.Errorf("decode plugin registry %s: %w", location, err)
	}
	if err := ValidatePluginRegistryIndex(index); err != nil {
		return PluginRegistryIndex{}, err
	}
	SortPluginRegistryEntries(index.Plugins)
	return index, nil
}

// ValidatePluginRegistryIndex checks registry entry manifests and remote specs.
func ValidatePluginRegistryIndex(index PluginRegistryIndex) error {
	if strings.TrimSpace(index.Version) == "" {
		return errors.New("plugin registry version is required")
	}
	seen := map[string]struct{}{}
	for _, entry := range index.Plugins {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			return errors.New("plugin registry entry name is required")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("plugin registry entry %s is duplicated", name)
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(entry.Remote) == "" || strings.TrimSpace(entry.Version) == "" {
			return fmt.Errorf("plugin registry entry %s requires remote and version", name)
		}
		if _, err := parseRemotePluginSpec(entry.Remote + "@" + entry.Version); err != nil {
			return fmt.Errorf("plugin registry entry %s remote: %w", name, err)
		}
		manifest := entry.Manifest
		if strings.TrimSpace(manifest.Name) == "" {
			manifest.Name = name
		}
		if err := ValidatePluginManifest(manifest); err != nil {
			return fmt.Errorf("plugin registry entry %s manifest: %w", name, err)
		}
	}
	return nil
}

// FilterPluginRegistryEntries returns registry entries whose name, description,
// tag, remote, or manifest capability contains query.
func FilterPluginRegistryEntries(entries []PluginRegistryEntry, query string) []PluginRegistryEntry {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		out := append([]PluginRegistryEntry(nil), entries...)
		SortPluginRegistryEntries(out)
		return out
	}
	out := make([]PluginRegistryEntry, 0, len(entries))
	for _, entry := range entries {
		if pluginRegistryEntryMatches(entry, query) {
			out = append(out, entry)
		}
	}
	SortPluginRegistryEntries(out)
	return out
}

// SortPluginRegistryEntries sorts registry entries by name and version.
func SortPluginRegistryEntries(entries []PluginRegistryEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && pluginRegistryEntryKey(entries[j-1]) > pluginRegistryEntryKey(entries[j]); j-- {
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
}

func pluginRegistryEntryMatches(entry PluginRegistryEntry, query string) bool {
	fields := []string{entry.Name, entry.Description, entry.Remote, entry.Version, entry.Manifest.Name, entry.Manifest.Version}
	fields = append(fields, entry.Tags...)
	fields = append(fields, entry.Manifest.Capabilities...)
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func pluginRegistryEntryKey(entry PluginRegistryEntry) string {
	return entry.Name + "@" + entry.Version
}

func ResolveGoPluginPaths(root string) ([]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("go plugin path is required")
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve go plugin path %s: %w", root, err)
	}
	info, err := os.Stat(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("stat go plugin path %s: %w", root, err)
	}
	if !info.IsDir() {
		if info.Mode()&0o111 == 0 {
			return nil, fmt.Errorf("go plugin %s is not executable", root)
		}
		return []string{cleanRoot}, nil
	}
	var paths []string
	err = filepath.WalkDir(cleanRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk go plugin path %s: %w", root, err)
	}
	sortStrings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("go plugin path %s contains no executable plugins", root)
	}
	return paths, nil
}

type remotePluginSpec struct {
	remote  string
	version string
	hash    string
}

func parseRemotePluginSpec(raw string) (remotePluginSpec, error) {
	raw = strings.TrimSpace(raw)
	idx := strings.LastIndex(raw, "@")
	if idx <= 0 || idx == len(raw)-1 {
		return remotePluginSpec{}, fmt.Errorf("remote plugin must be <repo-or-url>@<version>")
	}
	remote := strings.TrimSpace(raw[:idx])
	version := strings.TrimSpace(raw[idx+1:])
	if remote == "" || version == "" {
		return remotePluginSpec{}, fmt.Errorf("remote plugin must be <repo-or-url>@<version>")
	}
	if strings.ContainsAny(version, `/\`) || strings.Contains(version, "..") {
		return remotePluginSpec{}, fmt.Errorf("remote plugin version %q is invalid", version)
	}
	sum := sha256.Sum256([]byte(remote + "@" + version))
	return remotePluginSpec{remote: remote, version: version, hash: hex.EncodeToString(sum[:])}, nil
}

func remotePluginCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for plugin cache: %w", err)
	}
	return filepath.Join(home, ".cache", "gofly", "plugins"), nil
}

func remotePluginCacheDir(hash string) (string, error) {
	root, err := remotePluginCacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, hash), nil
}

func prepareRemotePluginCacheDir(dir string) error {
	root := filepath.Dir(dir)
	if err := rejectExistingSymlinkTarget(root, "plugin cache root"); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("prepare plugin cache root %s: %w", root, err)
	}
	if err := rejectExistingSymlinkTarget(dir, "plugin cache"); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		if os.IsExist(err) {
			info, statErr := os.Stat(dir)
			if statErr != nil {
				return fmt.Errorf("stat plugin cache %s: %w", dir, statErr)
			}
			if !info.IsDir() {
				return fmt.Errorf("plugin cache %s is not a directory", dir)
			}
			return nil
		}
		return fmt.Errorf("prepare plugin cache %s: %w", dir, err)
	}
	return nil
}

func copyRemotePluginPayload(dst *os.File, spec remotePluginSpec) error {
	if isPluginURL(spec.remote) {
		return downloadRemotePluginPayload(dst, spec)
	}
	src := strings.TrimPrefix(spec.remote, "file://")
	paths, err := ResolveGoPluginPaths(src)
	if err != nil {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s: %w", spec.remote, spec.version, spec.hash, err)
	}
	return copyFileToPluginCache(dst, paths[0], spec)
}

func downloadRemotePluginPayload(dst *os.File, spec remotePluginSpec) error {
	parsed, err := urlpkgParse(spec.remote)
	if err != nil {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s: %w", spec.remote, spec.version, spec.hash, err)
	}
	if parsed.Scheme != "https" && !isLocalPluginURL(parsed) {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s: insecure URL scheme %q", spec.remote, spec.version, spec.hash, parsed.Scheme)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(spec.remote)
	if err != nil {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s url=%s: %w", spec.remote, spec.version, spec.hash, spec.remote, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s url=%s: status %d", spec.remote, spec.version, spec.hash, spec.remote, resp.StatusCode)
	}
	if _, err := io.Copy(dst, io.LimitReader(resp.Body, maxDownloadedPluginBytes+1)); err != nil {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s: copy binary: %w", spec.remote, spec.version, spec.hash, err)
	}
	info, err := dst.Stat()
	if err != nil {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s: stat binary: %w", spec.remote, spec.version, spec.hash, err)
	}
	if info.Size() > maxDownloadedPluginBytes {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s: binary exceeds %d bytes", spec.remote, spec.version, spec.hash, maxDownloadedPluginBytes)
	}
	return nil
}

func copyFileToPluginCache(dst *os.File, src string, spec remotePluginSpec) error {
	// #nosec G304 -- local plugin installation reads an explicit plugin binary path selected by the operator.
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s source=%s: %w", spec.remote, spec.version, spec.hash, src, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(dst, io.LimitReader(f, maxDownloadedPluginBytes+1)); err != nil {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s source=%s: copy binary: %w", spec.remote, spec.version, spec.hash, src, err)
	}
	info, err := dst.Stat()
	if err != nil {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s source=%s: stat binary: %w", spec.remote, spec.version, spec.hash, src, err)
	}
	if info.Size() > maxDownloadedPluginBytes {
		return fmt.Errorf("install plugin remote=%s version=%s hash=%s source=%s: binary exceeds %d bytes", spec.remote, spec.version, spec.hash, src, maxDownloadedPluginBytes)
	}
	return nil
}

func sortInstalledPlugins(s []InstalledPlugin) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].Remote+"@"+s[j-1].Version > s[j].Remote+"@"+s[j].Version; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func fileSHA256(path string) (string, error) {
	// #nosec G304 -- digesting reads a plugin binary path already resolved from cache metadata or explicit operator input.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func pluginCacheFilename(parsed *url.URL) string {
	name := path.Base(parsed.Path)
	if name == "/" || name == "." || name == ".." || strings.TrimSpace(name) == "" {
		name = "plugin"
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, name)
	if len(name) > maxPluginCacheNameLength {
		name = name[:maxPluginCacheNameLength]
	}
	sum := sha256.Sum256([]byte(parsed.String()))
	return fmt.Sprintf("%s-%x", name, sum[:8])
}

func urlpkgParse(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse plugin URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("parse plugin URL: missing scheme or host")
	}
	return parsed, nil
}

func isLocalPluginURL(parsed *url.URL) bool {
	host := parsed.Hostname()
	return parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
}

func splitPluginArgs(arg string) (string, []string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", nil
	}
	parts := strings.SplitN(arg, " ", 2)
	if len(parts) == 1 {
		return parts[0], nil
	}
	args := []string{}
	for _, s := range strings.Split(parts[1], " ") {
		if s == "" {
			continue
		}
		args = append(args, s)
	}
	return parts[0], args
}

// internalPlugins 是内部插件注册表（默认空）。
var (
	internalPluginsMu sync.RWMutex
	internalPlugins   = map[string]Plugin{}
)

func getInternalPlugin(name string) (Plugin, bool) {
	internalPluginsMu.RLock()
	defer internalPluginsMu.RUnlock()
	p, ok := internalPlugins[name]
	return p, ok
}

// RegisterInternalPlugin 允许其他包注册内部插件（主要供扩展点）。
func RegisterInternalPlugin(p Plugin) bool {
	if p == nil || p.Name() == "" {
		return false
	}
	internalPluginsMu.Lock()
	defer internalPluginsMu.Unlock()
	if _, ok := internalPlugins[p.Name()]; ok {
		return false
	}
	internalPlugins[p.Name()] = p
	return true
}

// ListInternalPlugins 返回已注册内部插件名称列表（按名称排序）。
func ListInternalPlugins() []string {
	internalPluginsMu.RLock()
	defer internalPluginsMu.RUnlock()
	out := make([]string, 0, len(internalPlugins))
	for name := range internalPlugins {
		out = append(out, name)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
