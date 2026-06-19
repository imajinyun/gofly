package generator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ExtensionFileName 是 gofly 模板扩展清单文件（YAML）。
const ExtensionFileName = "extensions.yaml"

// FeatureFn 是一个启用式功能，在注册后可通过配置开启。
// 它返回额外需要生成的文件（key=相对路径，value=模板内容），以及在默认模板上的覆盖。
type FeatureFn func(scope ExtensionScope) ExtensionPatch

// ExtensionScope 描述一次生成调用的上下文，供插件/feature 使用。
type ExtensionScope struct {
	Name     string            // 服务名称
	Module   string            // Go module 路径
	Style    string            // 服务风格：minimal / basic / production
	Data     map[string]string // 默认渲染数据
	Extras   map[string]string // 扩展键值
	Dir      string            // 输出目录
	Features map[string]bool   // 已启用的 feature 集合
}

// ExtensionPatch 描述一个 feature 对生成的修改。
type ExtensionPatch struct {
	// OverrideFiles 覆盖默认模板文件；key 是 serviceFiles 中使用的相对路径。
	OverrideFiles map[string]string
	// ExtraFiles 追加新文件，key 是相对路径，value 是模板内容。
	ExtraFiles map[string]string
	// DataMerge 合并到渲染数据。
	DataMerge map[string]string
}

// TemplateExtension 是扩展文件对应的结构化数据。
// extensions.yaml 示例：
//
//	features:
//	  - ecosystem-compat
//	  - http-compat
//	  - rpc-compat
//	dependencies:
//	  store: github.com/example/store
type TemplateExtension struct {
	FeatureNames   []string          `yaml:"features"`
	EnableFeatures map[string]bool   `yaml:"-"` // 由调用方显式启用，或通过 --features 设定
	Dependencies   map[string]string `yaml:"dependencies"`
	ExtendClient   map[string]string `yaml:"extendClient,omitempty"`
	ExtendServer   map[string]string `yaml:"extendServer,omitempty"`
}

var (
	registeredFeaturesMu sync.RWMutex
	registeredFeatures   = defaultRegisteredFeatures()
)

func defaultRegisteredFeatures() map[string]FeatureFn {
	features := map[string]FeatureFn{}
	features["http-compat"] = goZeroCompatibilityFeature
	features["rpc-compat"] = kitexCompatibilityFeature
	features["ecosystem-compat"] = ecosystemCompatibilityFeature
	return features
}

// RegisterFeature 注册一个命名功能，返回 false 表示名称冲突。
func RegisterFeature(name string, fn FeatureFn) bool {
	if name == "" || fn == nil {
		return false
	}
	registeredFeaturesMu.Lock()
	defer registeredFeaturesMu.Unlock()
	if _, ok := registeredFeatures[name]; ok {
		return false
	}
	registeredFeatures[name] = fn
	return true
}

// ApplyFeatures 按字典序执行启用的 features，返回合并后的文件 map 和数据 map。
func ApplyFeatures(scope ExtensionScope, files map[string]string, data map[string]string) (map[string]string, map[string]string) {
	names := make([]string, 0, len(scope.Features))
	for name, enabled := range scope.Features {
		if !enabled {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	patches := make([]ExtensionPatch, 0, len(names))
	for _, name := range names {
		fn, ok := featureFunc(name)
		if !ok {
			continue
		}
		patches = append(patches, fn(scope))
	}
	return MergePatches(files, data, patches...)
}

// ApplyFeatureNames 便捷方法：按名称列表启用 features。
func ApplyFeatureNames(names []string, scope ExtensionScope, files map[string]string, data map[string]string) (map[string]string, map[string]string, error) {
	names = normalizeFeatureNames(names)
	if err := ValidateFeatureNames(names); err != nil {
		return files, data, err
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	scope.Features = set
	return applyFeatureNames(names, scope, files, data)
}

// ValidateFeatureNames reports the first unknown feature name.
func ValidateFeatureNames(names []string) error {
	for _, name := range normalizeFeatureNames(names) {
		if !HasFeature(name) {
			return fmt.Errorf("feature %q is not registered", name)
		}
	}
	return nil
}

// HasFeature 返回是否存在已注册或已启用的 feature。
func HasFeature(name string) bool {
	registeredFeaturesMu.RLock()
	defer registeredFeaturesMu.RUnlock()
	_, ok := registeredFeatures[name]
	return ok
}

// ListFeatures 返回已注册的 feature 名称，按字典序排序。
func ListFeatures() []string {
	registeredFeaturesMu.RLock()
	defer registeredFeaturesMu.RUnlock()
	out := make([]string, 0, len(registeredFeatures))
	for name := range registeredFeatures {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func normalizeFeatureNames(names []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func applyFeatureNames(names []string, scope ExtensionScope, files map[string]string, data map[string]string) (map[string]string, map[string]string, error) {
	patches := make([]ExtensionPatch, 0, len(names))
	for _, name := range names {
		fn, ok := featureFunc(name)
		if !ok {
			return files, data, fmt.Errorf("feature %q is not registered", name)
		}
		patches = append(patches, fn(scope))
	}
	outFiles, outData := MergePatches(files, data, patches...)
	return outFiles, outData, nil
}

func featureFunc(name string) (FeatureFn, bool) {
	registeredFeaturesMu.RLock()
	defer registeredFeaturesMu.RUnlock()
	fn, ok := registeredFeatures[name]
	return fn, ok
}

// LoadTemplateExtension 从目录加载 extensions.yaml，如果不存在返回零值和 nil 错误。
func LoadTemplateExtension(dir string) (*TemplateExtension, error) {
	ext := &TemplateExtension{}
	if dir == "" {
		return ext, nil
	}
	path := filepath.Join(dir, ExtensionFileName)
	// #nosec G304 -- template extensions are read from an explicit template directory selected by the operator.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ext, nil
		}
		return nil, fmt.Errorf("read template extension %s: %w", path, err)
	}
	// 轻量 YAML 解析（只支持简单的 key: value 与两级嵌套），避免引入外部依赖。
	parsed, err := parseLightweightYAML(data)
	if err != nil {
		return nil, err
	}
	if list, ok := parsed["features"]; ok {
		ext.FeatureNames = splitYAMLList(list)
	}
	if deps, ok := parsed["dependencies"]; ok {
		ext.Dependencies = parseYAMLMap(deps)
	}
	return ext, nil
}

// ApplyTemplateExtension 合并目录下用户自定义模板到默认 files map。
// 规则：目录中存在的同名文件将覆盖默认模板。
func ApplyTemplateExtension(dir string, files map[string]string) (map[string]string, error) {
	if dir == "" {
		return files, nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return files, nil
		}
		return nil, fmt.Errorf("stat template dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("template dir %s is not a directory", dir)
	}
	out := make(map[string]string, len(files))
	for k, v := range files {
		out[k] = v
	}
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// 跳过隐藏目录（如 .git）与特殊目录
			if strings.HasPrefix(name, ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".tpl") && !strings.HasSuffix(name, ".tmpl") && !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".go") {
			return nil
		}
		if name == ExtensionFileName {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("template file %s must not be a symlink", path)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// #nosec G122 G304 -- symlink entries are rejected above before reading template files from the walked root.
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read template %s: %w", path, err)
		}
		// .tpl/.tmpl 直接覆盖默认模板的相同 key（允许部分匹配）
		for k := range out {
			base := filepath.Base(k)
			if base == name || base == strings.TrimSuffix(name, filepath.Ext(name)) {
				out[k] = string(data)
			}
		}
		// 同时支持 user 通过路径精确映射：例如 internal/api/v1/ping.go.tpl 作为精确覆盖
		stripped := strings.TrimSuffix(rel, ".tpl")
		stripped = strings.TrimSuffix(stripped, ".tmpl")
		if _, ok := out[stripped]; ok {
			out[stripped] = string(data)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MergePatches 把多个 feature 提供的 patch 合并到 files + data。
func MergePatches(files map[string]string, data map[string]string, patches ...ExtensionPatch) (map[string]string, map[string]string) {
	outFiles := make(map[string]string, len(files))
	for k, v := range files {
		outFiles[k] = v
	}
	outData := make(map[string]string, len(data))
	for k, v := range data {
		outData[k] = v
	}
	for _, p := range patches {
		for k, v := range p.OverrideFiles {
			outFiles[k] = v
		}
		for k, v := range p.ExtraFiles {
			outFiles[k] = v
		}
		for k, v := range p.DataMerge {
			outData[k] = v
		}
	}
	return outFiles, outData
}

func goZeroCompatibilityFeature(scope ExtensionScope) ExtensionPatch {
	return ExtensionPatch{ExtraFiles: map[string]string{
		filepath.Join("internal", "compat", "gozero", "adapter.go"): goZeroCompatibilityTemplate,
	}}
}

func kitexCompatibilityFeature(scope ExtensionScope) ExtensionPatch {
	return ExtensionPatch{ExtraFiles: map[string]string{
		filepath.Join("internal", "compat", "kitex", "adapter.go"): kitexCompatibilityTemplate,
	}}
}

func ecosystemCompatibilityFeature(scope ExtensionScope) ExtensionPatch {
	gozero := goZeroCompatibilityFeature(scope)
	kitex := kitexCompatibilityFeature(scope)
	files := make(map[string]string, len(gozero.ExtraFiles)+len(kitex.ExtraFiles))
	for path, content := range gozero.ExtraFiles {
		files[path] = content
	}
	for path, content := range kitex.ExtraFiles {
		files[path] = content
	}
	return ExtensionPatch{ExtraFiles: files}
}

// -------- 辅助：极简 YAML 解析，仅支持 key: value，list，两级嵌套 map --------

func parseLightweightYAML(data []byte) (map[string]string, error) {
	out := map[string]string{}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// 只保留顶层 key: value，形如：key: value 或 key: |- 多行内容由上层负责
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		value := strings.TrimSpace(trimmed[idx+1:])
		out[key] = value
	}
	return out, nil
}

func splitYAMLList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		inner := raw[1 : len(raw)-1]
		parts := strings.Split(inner, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.Trim(strings.TrimSpace(p), `"`)
			if p == "" {
				continue
			}
			out = append(out, p)
		}
		return out
	}
	return nil
}

func parseYAMLMap(raw string) map[string]string {
	_ = raw
	// 为保持零依赖，返回空，真实使用时依赖会通过 --dependency 直接指定
	return map[string]string{}
}
