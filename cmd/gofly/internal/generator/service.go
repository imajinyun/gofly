package generator

import (
	"errors"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type ServiceOptions struct {
	Name          string
	Module        string
	Dir           string
	Style         string
	FrameworkPath string
}

// ServiceScaffoldOptions 是配置驱动的脚手架选项；包含模板扩展、feature、插件等。
// 与 Config 配合使用：ApplyOverlay(name, module, style, templateDir, features) 把 CLI 参数覆盖在配置之上。
type ServiceScaffoldOptions struct {
	Name                 string
	Module               string
	Dir                  string
	Style                string
	Profile              string
	TemplateDir          string
	TemplateRemote       string
	TemplateBranch       string
	StrictTemplateRemote bool
	Features             []string
	Plugins              []string // 可执行插件（或内部插件名），通过 PluginRunner 运行
	FrameworkPath        string
	ExtraFiles           map[string]string // 额外需要写入的文件，key 是相对路径
	SkipAPISpec          bool              // api new 时使用：是否跳过 .api 文件的生成
	Kind                 string            // "api" 或 "rpc"，决定是否额外写入 .api/.proto
}

const (
	ServiceStyleBasic      = "basic"
	ServiceStyleMinimal    = "minimal"
	ServiceStyleProduction = "production"
)

type HandlerOptions struct {
	Name   string
	Module string
	Dir    string
	Path   string
}

type MiddlewareOptions struct {
	Names []string
	Dir   string
}

type TemplateOptions struct {
	Dir          string
	Remote       string
	Branch       string
	StrictRemote bool
}

type IDLTemplateOptions struct {
	Output      string
	Name        string
	TemplateDir string
	Remote      string
	Branch      string
}

type TemplateFile struct {
	Name string
	Path string
}

type MigrationOptions struct {
	Name string
	Dir  string
	Time time.Time
}

type APINewOptions struct {
	Name          string
	Module        string
	Dir           string
	Style         string
	SkipAPISpec   bool
	FrameworkPath string
}

type RPCNewOptions struct {
	Name          string
	Module        string
	Dir           string
	Profile       string
	FrameworkPath string
}

func GenerateService(opts ServiceOptions) error {
	if opts.Name == "" {
		return errors.New("name is required")
	}
	if opts.Module == "" {
		return errors.New("module is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", opts.Name)
	}
	style, err := normalizeServiceStyle(opts.Style)
	if err != nil {
		return err
	}
	data := map[string]string{
		"Name":             opts.Name,
		"Module":           opts.Module,
		"ReplaceBlock":     frameworkReplaceBlock(opts.FrameworkPath),
		"GoFile":           "./cmd/" + opts.Name,
		"Exe":              opts.Name,
		"GoVersion":        "1.26",
		"BaseImage":        "gcr.io/distroless/static-debian12",
		"Namespace":        "default",
		"Image":            opts.Name + ":latest",
		"Port":             "8080",
		"RPCPort":          "8081",
		"Replicas":         "2",
		"Host":             opts.Name + ".example.com",
		"Path":             "/",
		"Data":             kubeConfigData(nil),
		"RevisionHistory":  "",
		"ImagePullSecrets": "",
		"ServiceAccount":   "",
		"ImagePullPolicy":  "",
		"Resources":        kubeResources("100m", "128Mi", "500m", "512Mi"),
		"ServiceType":      "",
		"NodePort":         "",
		"Autoscale":        kubeAutoscale(opts.Name, "default", "2", "6"),
	}
	if err := cleanupLegacyServiceFiles(opts.Dir); err != nil {
		return err
	}
	ir := serviceScaffoldIR{Dir: opts.Dir, Data: data, Files: serviceFiles(style, opts.Name)}
	rendered := serviceScaffoldRenderer{}.Render(ir)
	return serviceFilesystemSink{Dir: opts.Dir}.WriteRendered(rendered)
}

func GenerateAPINew(opts APINewOptions) error {
	if opts.Name == "" {
		return errors.New("name is required")
	}
	if opts.Module == "" {
		return errors.New("module is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", opts.Name)
	}
	style := opts.Style
	if style == "" {
		style = ServiceStyleBasic
	}
	if err := GenerateService(ServiceOptions{
		Name:          opts.Name,
		Module:        opts.Module,
		Dir:           opts.Dir,
		Style:         style,
		FrameworkPath: opts.FrameworkPath,
	}); err != nil {
		return err
	}
	if opts.SkipAPISpec {
		return nil
	}
	return writeRenderedFile(
		filepath.Join(opts.Dir, opts.Name+".api"),
		apiNewTemplate,
		map[string]string{"Name": opts.Name},
	)
}

func GenerateRPCNew(opts RPCNewOptions) error {
	if opts.Name == "" {
		return errors.New("name is required")
	}
	if opts.Module == "" {
		return errors.New("module is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", opts.Name)
	}
	if strings.TrimSpace(opts.Profile) != "" {
		if err := GenerateServiceScaffold(ServiceScaffoldOptions{
			Name:          opts.Name,
			Module:        opts.Module,
			Dir:           opts.Dir,
			Style:         ServiceStyleProduction,
			Profile:       opts.Profile,
			FrameworkPath: opts.FrameworkPath,
			Kind:          "rpc",
		}); err != nil {
			return err
		}
	} else {
		if err := GenerateService(ServiceOptions{
			Name:          opts.Name,
			Module:        opts.Module,
			Dir:           opts.Dir,
			Style:         ServiceStyleProduction,
			FrameworkPath: opts.FrameworkPath,
		}); err != nil {
			return err
		}
	}
	return writeRenderedFile(
		filepath.Join(opts.Dir, opts.Name+".proto"),
		strings.Replace(rpcNewTemplate, "package {{.Name}}.v1;", "package {{.Name}};", 1),
		map[string]string{"Name": lowerName(opts.Name)},
	)
}

func GenerateTemplateInit(opts TemplateOptions) error {
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", ".gofly", "templates")
	}
	if strings.TrimSpace(opts.Remote) != "" {
		return SyncTemplateRemote(opts)
	}
	files := templateFiles()
	for name, tmpl := range files {
		if err := writeRenderedFile(filepath.Join(opts.Dir, name), tmpl, map[string]string{"Name": "demo"}); err != nil {
			return err
		}
	}
	return nil
}

func GenerateAPITemplate(opts IDLTemplateOptions) error {
	tmpl, err := resolveIDLTemplate(opts, "api.tpl", apiNewTemplate)
	if err != nil {
		return err
	}
	return generateIDLTemplate(opts, "demo.api", tmpl)
}

func GenerateRPCTemplate(opts IDLTemplateOptions) error {
	tmpl, err := resolveIDLTemplate(opts, "rpc.tpl", rpcNewTemplate)
	if err != nil {
		return err
	}
	return generateIDLTemplate(opts, "demo.proto", tmpl)
}

func resolveIDLTemplate(opts IDLTemplateOptions, name, fallback string) (string, error) {
	return resolveNamedTemplate(opts.TemplateDir, opts.Remote, opts.Branch, name, fallback)
}

func resolveNamedTemplate(dir, remote, branch, name, fallback string) (string, error) {
	return resolveNamedTemplates(dir, remote, branch, []string{name, strings.TrimSuffix(name, filepath.Ext(name))}, fallback)
}

func resolveNamedTemplates(dir, remote, branch string, names []string, fallback string) (string, error) {
	dir, err := ResolveTemplateSource(dir, remote, branch, false)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return fallback, nil
	}
	for _, candidate := range names {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		// #nosec G304 -- template names are fixed generator candidates read from an explicit template source directory.
		data, err := os.ReadFile(filepath.Join(dir, candidate))
		if err == nil {
			return string(data), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read template %s: %w", candidate, err)
		}
	}
	return fallback, nil
}

func generateIDLTemplate(opts IDLTemplateOptions, defaultName, tmpl string) error {
	output := strings.TrimSpace(opts.Output)
	if output == "" {
		output = defaultName
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(output), filepath.Ext(output))
	}
	if name == "" || name == "." {
		name = "demo"
	}
	return writeRenderedFile(output, tmpl, map[string]string{"Name": lowerName(name)})
}

func CleanTemplates(opts TemplateOptions) error {
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", ".gofly", "templates")
	}
	if err := os.RemoveAll(opts.Dir); err != nil {
		return fmt.Errorf("clean template directory: %w", err)
	}
	return nil
}

func ListTemplates(opts TemplateOptions) []TemplateFile {
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", ".gofly", "templates")
	}
	files := templateFiles()
	out := make([]TemplateFile, 0, len(files))
	for _, name := range []string{"api.tpl", "rpc.tpl", "model.tpl", "docker.tpl", "kube-deployment.tpl", "kube-service.tpl", "kube-ingress.tpl", "kube-configmap.tpl", "kube-job.tpl", "helm-chart.tpl", "helm-values.tpl"} {
		out = append(out, TemplateFile{Name: name, Path: filepath.Join(opts.Dir, name)})
	}
	return out
}

func ResolveTemplateSource(dir, remote, branch string, strict bool) (string, error) {
	dir = strings.TrimSpace(dir)
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return dir, nil
	}
	if dir == "" {
		dir = filepath.Join(".", ".gofly", "templates")
	}
	err := SyncTemplateRemote(TemplateOptions{Dir: dir, Remote: remote, Branch: branch, StrictRemote: strict})
	if err != nil && strict {
		return "", err
	}
	return dir, nil
}

func SyncTemplateRemote(opts TemplateOptions) error {
	dir := strings.TrimSpace(opts.Dir)
	remote := strings.TrimSpace(opts.Remote)
	if remote == "" {
		return nil
	}
	if dir == "" {
		dir = filepath.Join(".", ".gofly", "templates")
	}
	tmp, err := os.MkdirTemp("", "gofly-template-remote-*")
	if err != nil {
		return fmt.Errorf("create template temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if local, ok := localTemplateRemote(remote); ok {
		info, err := os.Stat(local)
		if err != nil {
			return fmt.Errorf("stat template remote %s: %w", remote, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("template remote %s is not a directory", remote)
		}
		if err := copyDir(local, tmp); err != nil {
			return fmt.Errorf("copy template remote %s: %w", remote, err)
		}
	} else {
		if err := cloneTemplateRemote(remote, strings.TrimSpace(opts.Branch), tmp); err != nil {
			return err
		}
	}
	source := templatePayloadDir(tmp)
	if samePath(source, dir) {
		return nil
	}
	if err := validateTemplateSyncDir(dir); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("prepare template dir %s: %w", dir, err)
	}
	if err := copyDir(source, dir); err != nil {
		return fmt.Errorf("sync template remote %s to %s: %w", remote, dir, err)
	}
	return nil
}

func localTemplateRemote(remote string) (string, bool) {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "file://") {
		path := strings.TrimPrefix(remote, "file://")
		return path, true
	}
	if info, err := os.Stat(remote); err == nil && info.IsDir() {
		return remote, true
	}
	return "", false
}

func cloneTemplateRemote(remote, branch, dir string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git is required for remote template %s: %w", remote, err)
	}
	args := []string{"clone", "--depth", "1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, remote, dir)
	// #nosec G204 -- remote template sync intentionally invokes git with argv-separated clone arguments, not shell input.
	cmd := exec.Command(git, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("clone template remote %s: %w: %s", remote, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func templatePayloadDir(root string) string {
	for _, candidate := range []string{"templates", "gofly/templates", ".gofly/templates"} {
		path := filepath.Join(root, filepath.FromSlash(candidate))
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}
	return root
}

func validateTemplateSyncDir(dir string) error {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return errors.New("template directory is required")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return fmt.Errorf("resolve template directory %s: %w", dir, err)
	}
	abs = filepath.Clean(abs)
	volumeRoot := filepath.VolumeName(abs) + string(filepath.Separator)
	if abs == volumeRoot {
		return fmt.Errorf("template directory %s is too dangerous to replace", dir)
	}
	if home, err := os.UserHomeDir(); err == nil && samePath(abs, home) {
		return fmt.Errorf("template directory %s must not be the user home directory", dir)
	}
	if cwd, err := os.Getwd(); err == nil && samePath(abs, cwd) {
		return fmt.Errorf("template directory %s must not be the current working directory", dir)
	}
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("template directory %s must not be a symlink", dir)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat template directory %s: %w", dir, err)
	}
	return nil
}

func copyDir(src, dst string) error {
	if samePath(src, dst) {
		return nil
	}
	if childPath(src, dst) {
		return fmt.Errorf("copy destination %s must not be inside source %s", dst, src)
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("template source %s must not be a symlink", path)
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") && path != src {
				return filepath.SkipDir
			}
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			return EnsureDirectoryUnderRoot(dst, rel, generatedDirMode, "template copy")
		}
		return CopyFileUnderRoot(src, rel, dst, rel, generatedPublicFileMode, generatedDirMode, "template copy")
	})
}

func copyFile(src, dst string) error {
	if samePath(src, dst) {
		return nil
	}
	return CopyFileToRoot(src, filepath.Dir(dst), filepath.Base(dst), generatedPublicFileMode, generatedDirMode, "template copy")
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return absA == absB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func childPath(parent, child string) bool {
	absParent, errParent := filepath.Abs(parent)
	absChild, errChild := filepath.Abs(child)
	if errParent != nil || errChild != nil {
		absParent = filepath.Clean(parent)
		absChild = filepath.Clean(child)
	}
	rel, err := filepath.Rel(absParent, absChild)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func templateFiles() map[string]string {
	return map[string]string{
		"api.tpl":             apiNewTemplate,
		"rpc.tpl":             rpcNewTemplate,
		"model.tpl":           modelTemplateInitTemplate,
		"docker.tpl":          dockerfileTemplate,
		"kube-deployment.tpl": kubeTemplate,
		"kube-service.tpl":    kubeServiceTemplate,
		"kube-ingress.tpl":    kubeIngressTemplate,
		"kube-configmap.tpl":  kubeConfigMapTemplate,
		"kube-job.tpl":        kubeJobTemplate,
		"helm-chart.tpl":      helmChartTemplate,
		"helm-values.tpl":     helmValuesTemplate,
	}
}

func GenerateMigration(opts MigrationOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return errors.New("migration name is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", "migrations")
	}
	now := opts.Time
	if now.IsZero() {
		now = time.Now()
	}
	name := migrationName(opts.Name)
	stamp := now.Format("20060102150405")
	files := map[string]string{
		filepath.Join(opts.Dir, stamp+"_"+name+".up.sql"):   "-- write forward migration SQL here\n",
		filepath.Join(opts.Dir, stamp+"_"+name+".down.sql"): "-- write rollback migration SQL here\n",
	}
	for path, content := range files {
		if err := writeGeneratedFile(path, []byte(content)); err != nil {
			return fmt.Errorf("write migration file: %w", err)
		}
	}
	return nil
}

var migrationNameRE = regexp.MustCompile(`[^a-z0-9_]+`)

func migrationName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	name = migrationNameRE.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		return "migration"
	}
	return name
}

func GenerateCompletion(shell string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "bash":
		return `# bash completion for gofly
_gofly_completion() {
  local cur prev commands
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  local cmd="${COMP_WORDS[1]}"
  local sub="${COMP_WORDS[2]}"

  # File path completion for flag arguments that expect files or directories.
  case "$prev" in
    --api|--src|--file|--idl|--base|--target|--config)
      COMPREPLY=($(compgen -f -- "$cur"))
      return 0
      ;;
    --dir)
      COMPREPLY=($(compgen -d -- "$cur"))
      return 0
      ;;
  esac

  commands="version new gen generate handler rpc api model docker kube template quickstart migrate migration env bug upgrade config feature plugin completion complete release doctor example examples ai tools"
  case "$cmd" in
    new) commands="api rpc" ;;
    gen) commands="handler rpc api rest middleware model gateway" ;;
    handler) commands="gen complete" ;;
    rpc) commands="new idl inspect thrift thrift2proto client server middleware lint deps gen protoc check doc docs swagger openapi breaking descriptor plugin template tpl" ;;
    generate) commands="handler rpc api rest middleware model gateway" ;;
    api) commands="new go gen check validate breaking break format fmt doc docs swagger client ts typescript js javascript dart java kotlin kt types route routes import diff plugin middleware" ;;
    model) commands="gen mysql pg postgres postgresql mongo" ;;
    kube) commands="deploy deployment service svc ingress ing configmap cm job" ;;
    template) commands="init list ls clean update revert" ;;
    env) commands="check install" ;;
    config) commands="init show get set clean" ;;
    feature) commands="list ls run" ;;
    plugin) commands="list ls install uninstall remove rm run" ;;
    completion) commands="bash zsh fish powershell pwsh" ;;
    complete) commands="handler" ;;
    ai|tools) commands="manifest plan new complete stream doctor" ;;
  esac
  case "$cmd:$sub" in
    rpc:template|rpc:tpl) commands="init list ls clean update revert" ;;
    model:mysql|model:pg|model:postgres|model:postgresql) commands="ddl datasource" ;;
    complete:handler) commands="bash zsh fish powershell pwsh" ;;
  esac
  COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
}
complete -F _gofly_completion gofly
`, nil
	case "zsh":
		return `#compdef gofly
_gofly() {
  local -a commands
  commands=(
    'version:print version metadata'
    'new:scaffold a new api or rpc service'
    'gen:unified generator entrypoint'
    'generate:unified generator entrypoint alias'
    'handler:generate or complete api handler boilerplate'
    'rpc:rpc-file operations'
    'api:api-file operations'
    'model:model generation operations'
    'docker:generate Dockerfile'
    'kube:generate Kubernetes manifests'
    'template:manage local templates'
    'quickstart:create runnable service quickly'
    'migrate:create SQL migrations'
    'migration:create SQL migrations'
    'env:print and check local toolchain environment'
    'bug:print diagnostic bug report'
    'upgrade:print or execute upgrade command'
    'config:manage .gofly/config.json'
    'feature:list or preview scaffold features'
    'plugin:list, install or run gofly plugins'
    'completion:emit shell completion scripts'
    'complete:emit shell completion scripts'
    'release:run release readiness checks'
    'doctor:diagnose local environment readiness'
    'example:list or run built-in examples'
    'examples:list or run built-in examples alias'
    'ai:emit AI tool manifest'
    'tools:emit AI tool manifest alias'
  )
  case "$words[2]" in
    new) commands=('api:create API service' 'rpc:create RPC service') ;;
    gen|generate) commands=('handler:generate REST handler' 'rpc:generate RPC code' 'api:generate REST code' 'rest:generate REST code' 'middleware:generate middleware skeletons' 'model:generate model code' 'gateway:generate API gateway') ;;
    handler) commands=('gen:generate REST handler' 'complete:append missing methods') ;;
    rpc) commands=('new:create RPC service' 'idl:inspect IDL metadata' 'inspect:inspect IDL metadata alias' 'thrift:convert thrift to proto skeleton' 'thrift2proto:convert thrift alias' 'client:generate RPC client wrapper' 'server:generate RPC server stubs' 'middleware:generate gRPC middleware' 'lint:lint IDL contract' 'deps:list IDL imports' 'gen:generate RPC code' 'protoc:run protoc plugins' 'check:validate proto' 'doc:generate OpenAPI docs' 'docs:generate OpenAPI docs alias' 'swagger:generate OpenAPI docs alias' 'openapi:generate OpenAPI docs alias' 'breaking:compare compatibility' 'descriptor:compare runtime descriptors' 'plugin:run RPC plugin' 'template:manage templates' 'tpl:manage templates alias') ;;
    api) commands=('new:create API service' 'go:generate REST code' 'gen:generate REST code' 'check:validate API' 'validate:validate API' 'breaking:detect breaking changes' 'break:detect breaking changes' 'format:format API' 'fmt:format API' 'doc:generate docs' 'docs:generate docs' 'swagger:generate OpenAPI' 'client:generate client' 'ts:generate TypeScript client' 'typescript:generate TypeScript client' 'js:generate JavaScript client' 'javascript:generate JavaScript client' 'dart:generate Dart client' 'java:generate Java client' 'kotlin:generate Kotlin client' 'kt:generate Kotlin client' 'types:generate DTOs' 'route:print routes' 'routes:print routes' 'import:convert OpenAPI' 'diff:compare APIs' 'plugin:run plugin' 'middleware:generate middleware') ;;
    model) commands=('gen:generate SQL model' 'mysql:MySQL model mode' 'pg:PostgreSQL model mode' 'postgres:PostgreSQL model mode' 'postgresql:PostgreSQL model mode' 'mongo:Mongo model mode') ;;
    kube) commands=('deploy:generate Deployment' 'deployment:generate Deployment' 'service:generate Service' 'svc:generate Service' 'ingress:generate Ingress' 'ing:generate Ingress' 'configmap:generate ConfigMap' 'cm:generate ConfigMap' 'job:generate Job') ;;
    template) commands=('init:write default templates' 'list:list templates' 'ls:list templates' 'clean:remove templates' 'update:refresh templates' 'revert:restore templates') ;;
    env) commands=('check:check dependencies' 'install:print installation guidance') ;;
    config) commands=('init:create config' 'show:print config' 'get:read value' 'set:update value' 'clean:remove config') ;;
    feature) commands=('list:list features' 'ls:list features' 'run:preview feature') ;;
    plugin) commands=('list:list plugins' 'ls:list plugins' 'install:install remote plugin' 'uninstall:uninstall remote plugin' 'remove:uninstall remote plugin' 'rm:uninstall remote plugin' 'run:run plugin') ;;
    completion) commands=('bash:bash completion' 'zsh:zsh completion' 'fish:fish completion' 'powershell:powershell completion' 'pwsh:powershell completion alias') ;;
    complete) commands=('handler:emit shell completion scripts') ;;
    ai|tools) commands=('manifest:print AI tool manifest' 'plan:plan AI-first project scaffold' 'new:plan or apply AI-first project scaffold' 'complete:run governed noop completion' 'stream:run governed streaming completion' 'doctor:run AI subsystem diagnostics') ;;
  esac
  case "$words[2]:$words[3]" in
    rpc:template|rpc:tpl) commands=('init:write templates' 'list:list templates' 'ls:list templates' 'clean:remove templates' 'update:refresh templates' 'revert:restore templates') ;;
    model:mysql|model:pg|model:postgres|model:postgresql) commands=('ddl:generate from DDL' 'datasource:generate from datasource') ;;
    complete:handler) commands=('bash:bash completion' 'zsh:zsh completion' 'fish:fish completion' 'powershell:powershell completion' 'pwsh:powershell completion alias') ;;
  esac
  _describe -t commands 'gofly command' commands
}
compdef _gofly gofly
`, nil
	case "fish":
		return `complete -c gofly -f -a "version\tPrint version metadata\nnew\tScaffold a new service\ngen\tUnified generator\ngenerate\tUnified generator alias\nhandler\tHandler generator and completer\nrpc\tRPC file operations\napi\tAPI file operations\nmodel\tModel generation\ndocker\tGenerate Dockerfile assets\nkube\tGenerate Kubernetes manifests\ntemplate\tManage templates\nquickstart\tCreate a runnable service\nmigrate\tCreate SQL migrations\nmigration\tCreate SQL migrations alias\nenv\tCheck toolchain environment\nbug\tPrint diagnostic bug report\nupgrade\tPrint or run upgrade commands\nconfig\tManage .gofly/config.json\nfeature\tList or preview scaffold features\nplugin\tList, install or run plugins\ncompletion\tEmit shell completion scripts\ncomplete\tEmit legacy completion scripts\nrelease\tRun release readiness checks\ndoctor\tDiagnose local environment\nexample\tList or run built-in examples\nexamples\tList or run built-in examples alias\nai\tEmit AI tool manifest\ntools\tEmit AI tool manifest alias"
complete -c gofly -n '__fish_seen_subcommand_from new' -a "api\tCreate an API service\nrpc\tCreate an RPC service"
complete -c gofly -n '__fish_seen_subcommand_from gen' -a "handler\tGenerate REST handler\nrpc\tGenerate RPC code\napi\tGenerate REST code\nrest\tGenerate REST code alias\nmiddleware\tGenerate middleware skeletons\nmodel\tGenerate model code\ngateway\tGenerate API gateway"
complete -c gofly -n '__fish_seen_subcommand_from generate' -a "handler\tGenerate REST handler\nrpc\tGenerate RPC code\napi\tGenerate REST code\nrest\tGenerate REST code alias\nmiddleware\tGenerate middleware skeletons\nmodel\tGenerate model code\ngateway\tGenerate API gateway"
complete -c gofly -n '__fish_seen_subcommand_from handler' -a "gen\tGenerate REST handler\ncomplete\tAppend missing methods"
complete -c gofly -n '__fish_seen_subcommand_from rpc' -a "new\tCreate RPC service\nidl\tInspect IDL metadata\ninspect\tInspect IDL metadata alias\nthrift\tConvert thrift to proto\nthrift2proto\tConvert thrift alias\nclient\tGenerate client wrapper\nserver\tGenerate server stubs\nmiddleware\tGenerate gRPC middleware\nlint\tLint IDL contract\ndeps\tList IDL imports\ngen\tGenerate RPC code\nprotoc\tRun protoc plugins\ncheck\tValidate proto syntax\ndoc\tGenerate OpenAPI docs\ndocs\tGenerate OpenAPI docs alias\nswagger\tGenerate OpenAPI docs alias\nopenapi\tGenerate OpenAPI docs alias\nbreaking\tCompare compatibility\ndescriptor\tCompare runtime descriptors\nplugin\tRun RPC plugin\ntemplate\tManage RPC templates\ntpl\tManage RPC templates alias"
complete -c gofly -n '__fish_seen_subcommand_from api' -a "new\tCreate API service\ngo\tGenerate REST code\ngen\tGenerate REST code\ncheck\tValidate API syntax\nvalidate\tValidate API alias\nbreaking\tDetect breaking changes\nbreak\tDetect breaking changes alias\nformat\tFormat API file\nfmt\tFormat API file alias\ndoc\tGenerate API docs\ndocs\tGenerate API docs alias\nswagger\tGenerate OpenAPI spec\nclient\tGenerate API client\nts\tGenerate TypeScript client\ntypescript\tGenerate TypeScript client alias\njs\tGenerate JavaScript client\njavascript\tGenerate JavaScript client alias\ndart\tGenerate Dart client\njava\tGenerate Java client\nkotlin\tGenerate Kotlin client\nkt\tGenerate Kotlin client alias\ntypes\tGenerate DTOs\nroute\tPrint routes\nroutes\tPrint routes alias\nimport\tConvert OpenAPI to API\ndiff\tCompare API files\nplugin\tRun API plugin\nmiddleware\tGenerate API middleware"
complete -c gofly -n '__fish_seen_subcommand_from model' -a "gen\tGenerate model code\nmysql\tMySQL model mode\npg\tPostgreSQL model mode\npostgres\tPostgreSQL model mode alias\npostgresql\tPostgreSQL model mode alias\nmongo\tMongo model mode"
complete -c gofly -n '__fish_seen_subcommand_from kube' -a "deploy\tGenerate Deployment\ndeployment\tGenerate Deployment alias\nservice\tGenerate Service\nsvc\tGenerate Service alias\ningress\tGenerate Ingress\ning\tGenerate Ingress alias\nconfigmap\tGenerate ConfigMap\ncm\tGenerate ConfigMap alias\njob\tGenerate Job"
complete -c gofly -n '__fish_seen_subcommand_from template' -a "init\tWrite default templates\nlist\tList templates\nls\tList templates alias\nclean\tRemove templates\nupdate\tRefresh templates\nrevert\tRestore default templates"
complete -c gofly -n '__fish_seen_subcommand_from env' -a "check\tCheck dependencies\ninstall\tPrint install guidance"
complete -c gofly -n '__fish_seen_subcommand_from config' -a "init\tCreate config file\nshow\tPrint config\nset\tUpdate config value\nget\tRead config value\nclean\tRemove config file"
complete -c gofly -n '__fish_seen_subcommand_from feature' -a "list\tList available features\nls\tList features alias\nrun\tPreview feature output"
complete -c gofly -n '__fish_seen_subcommand_from plugin' -a "list\tList plugins\nls\tList plugins alias\ninstall\tInstall remote plugin\nuninstall\tUninstall plugin\nremove\tUninstall plugin alias\nrm\tUninstall plugin alias\nrun\tRun plugin"
complete -c gofly -n '__fish_seen_subcommand_from completion' -a "bash\tBash completion\nzsh\tZsh completion\nfish\tFish completion\npowershell\tPowerShell completion\npwsh\tPowerShell completion alias"
complete -c gofly -n '__fish_seen_subcommand_from complete' -a "handler\tEmit completion scripts"
complete -c gofly -n '__fish_seen_subcommand_from ai' -a "manifest\tPrint AI tool manifest\nplan\tPlan AI-first project scaffold\nnew\tPlan or apply AI-first project scaffold\ncomplete\tRun governed noop completion\nstream\tRun governed streaming completion\ndoctor\tRun AI subsystem diagnostics"
complete -c gofly -n '__fish_seen_subcommand_from tools' -a "manifest\tPrint AI tool manifest alias\nplan\tPlan AI-first project scaffold alias\nnew\tPlan or apply AI-first project scaffold alias\ncomplete\tRun governed noop completion alias\nstream\tRun governed streaming completion alias\ndoctor\tRun AI subsystem diagnostics alias"
complete -c gofly -n '__fish_seen_subcommand_from rpc; and __fish_seen_subcommand_from template' -a "init\tWrite default templates\nlist\tList templates\nls\tList templates alias\nclean\tRemove templates\nupdate\tRefresh templates\nrevert\tRestore default templates"
complete -c gofly -n '__fish_seen_subcommand_from rpc; and __fish_seen_subcommand_from tpl' -a "init\tWrite default templates\nlist\tList templates\nls\tList templates alias\nclean\tRemove templates\nupdate\tRefresh templates\nrevert\tRestore default templates"
complete -c gofly -n '__fish_seen_subcommand_from model; and __fish_seen_subcommand_from mysql' -a "ddl\tGenerate from DDL\ndatasource\tGenerate from datasource"
complete -c gofly -n '__fish_seen_subcommand_from model; and __fish_seen_subcommand_from pg' -a "ddl\tGenerate from DDL\ndatasource\tGenerate from datasource"
complete -c gofly -n '__fish_seen_subcommand_from model; and __fish_seen_subcommand_from postgres' -a "ddl\tGenerate from DDL\ndatasource\tGenerate from datasource"
complete -c gofly -n '__fish_seen_subcommand_from model; and __fish_seen_subcommand_from postgresql' -a "ddl\tGenerate from DDL\ndatasource\tGenerate from datasource"
complete -c gofly -n '__fish_seen_subcommand_from complete; and __fish_seen_subcommand_from handler' -a "bash\tBash completion\nzsh\tZsh completion\nfish\tFish completion\npowershell\tPowerShell completion\npwsh\tPowerShell completion alias"
`, nil
	case "powershell", "pwsh":
		return `Register-ArgumentCompleter -Native -CommandName gofly -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $words = $commandAst.CommandElements | ForEach-Object { $_.Value }
  $cmd = if ($words.Count -gt 1) { $words[1] } else { "" }
  $sub = if ($words.Count -gt 2) { $words[2] } else { "" }
  $commands = @("version", "new", "gen", "generate", "handler", "rpc", "api", "model", "docker", "kube", "template", "quickstart", "migrate", "migration", "env", "bug", "upgrade", "config", "feature", "plugin", "completion", "complete", "release", "doctor", "example", "examples", "ai", "tools")
  switch ($cmd) {
    "new" { $commands = @("api", "rpc") }
    "gen" { $commands = @("handler", "rpc", "api", "rest", "middleware", "model", "gateway") }
    "generate" { $commands = @("handler", "rpc", "api", "rest", "middleware", "model", "gateway") }
    "handler" { $commands = @("gen", "complete") }
    "rpc" { $commands = @("new", "idl", "inspect", "thrift", "thrift2proto", "client", "server", "middleware", "lint", "deps", "gen", "protoc", "check", "doc", "docs", "swagger", "openapi", "breaking", "descriptor", "plugin", "template", "tpl") }
    "api" { $commands = @("new", "go", "gen", "check", "validate", "breaking", "break", "format", "fmt", "doc", "docs", "swagger", "client", "ts", "typescript", "js", "javascript", "dart", "java", "kotlin", "kt", "types", "route", "routes", "import", "diff", "plugin", "middleware") }
    "model" { $commands = @("gen", "mysql", "pg", "postgres", "postgresql", "mongo") }
    "kube" { $commands = @("deploy", "deployment", "service", "svc", "ingress", "ing", "configmap", "cm", "job") }
    "template" { $commands = @("init", "list", "ls", "clean", "update", "revert") }
    "env" { $commands = @("check", "install") }
    "config" { $commands = @("init", "show", "get", "set", "clean") }
    "feature" { $commands = @("list", "ls", "run") }
    "plugin" { $commands = @("list", "ls", "install", "uninstall", "remove", "rm", "run") }
    "completion" { $commands = @("bash", "zsh", "fish", "powershell", "pwsh") }
    "complete" { $commands = @("handler") }
    "ai" { $commands = @("manifest", "plan", "new", "complete", "stream", "doctor") }
    "tools" { $commands = @("manifest", "plan", "new", "complete", "stream", "doctor") }
  }
  switch ("$($cmd):$sub") {
    "rpc:template" { $commands = @("init", "list", "ls", "clean", "update", "revert") }
    "rpc:tpl" { $commands = @("init", "list", "ls", "clean", "update", "revert") }
    "model:mysql" { $commands = @("ddl", "datasource") }
    "model:pg" { $commands = @("ddl", "datasource") }
    "model:postgres" { $commands = @("ddl", "datasource") }
    "model:postgresql" { $commands = @("ddl", "datasource") }
    "complete:handler" { $commands = @("bash", "zsh", "fish", "powershell", "pwsh") }
  }
  $commands |
    Where-Object { $_ -like "$wordToComplete*" }
}
`, nil
	default:
		return "", fmt.Errorf("unsupported completion shell %q", shell)
	}
}

func normalizeServiceStyle(style string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "", ServiceStyleProduction:
		return ServiceStyleProduction, nil
	case ServiceStyleBasic:
		return ServiceStyleBasic, nil
	case ServiceStyleMinimal:
		return ServiceStyleMinimal, nil
	default:
		return "", fmt.Errorf("unknown service style %q", style)
	}
}

func serviceFiles(style, name string) map[string]string {
	return serviceFilesForProfile(style, name, ProfileGoflyAI)
}

func serviceFilesForProfile(style, name string, profile GenerationProfile) map[string]string {
	if profile == ProfileGoZeroCompatible {
		return goZeroServiceFiles(style, name)
	}

	files := map[string]string{
		"go.mod":                                                  goModTemplate,
		filepath.Join("cmd", name, "main.go"):                     mainTemplate,
		filepath.Join("etc", name+".json"):                        configTemplate,
		filepath.Join("internal", "config", "config.go"):          configGoTemplate,
		filepath.Join("internal", "config", "config_test.go"):     configTestTemplate,
		filepath.Join("internal", "svc", "service_context.go"):    svcTemplate,
		filepath.Join("internal", "routes", "routes.go"):          routesTemplate,
		filepath.Join("internal", "routes", "routes_test.go"):     routesTestTemplate,
		filepath.Join("internal", "api", "v1", "ping", "ping.go"): pingHandlerTemplate,
		filepath.Join("internal", "middleware", "trim.go"):        trimMiddlewareTemplate,
		filepath.Join("internal", "middleware", "trim_test.go"):   trimMiddlewareTestTemplate,
		filepath.Join("internal", "service", "ping.go"):           pingServiceTemplate,
		filepath.Join("internal", "service", "ping_test.go"):      pingServiceTestTemplate,
	}
	if style == ServiceStyleMinimal || style == ServiceStyleBasic {
		files[filepath.Join("cmd", name, "main.go")] = minimalMainTemplate
		files[filepath.Join("etc", name+".json")] = minimalConfigTemplate
		files[filepath.Join("internal", "config", "config.go")] = minimalConfigGoTemplate
		if style == ServiceStyleBasic {
			files["Dockerfile"] = dockerfileTemplate
			files["Makefile"] = makefileTemplate
		}
		addKitexProfileFiles(files, profile)
		return files
	}
	files[filepath.Join("etc", "governance.json")] = governanceTemplate
	files[filepath.Join("internal", "admin", "admin.go")] = adminServerTemplate
	files[filepath.Join("internal", "admin", "admin_test.go")] = adminServerTestTemplate
	files[filepath.Join("internal", "config", "discovery_test.go")] = configDiscoveryTestTemplate
	files[filepath.Join("internal", "discovery", "registry.go")] = discoveryRegistryTemplate
	files[filepath.Join("internal", "mq", "broker.go")] = mqBrokerTemplate
	files[filepath.Join("internal", "rpc", "greeter.go")] = greeterTemplate
	files[filepath.Join("internal", "rpc", "greeter_client_test.go")] = greeterClientTestTemplate
	files[filepath.Join("internal", "rpc", "greeter_test.go")] = greeterTestTemplate
	files[filepath.Join("internal", "smoke", "service_smoke_test.go")] = smokeTestTemplate
	files["Dockerfile"] = dockerfileTemplate
	files[filepath.Join("deploy", "k8s", name+".yaml")] = kubeTemplate
	files[filepath.Join("deploy", "helm", "Chart.yaml")] = helmChartTemplate
	files[filepath.Join("deploy", "helm", "values.yaml")] = helmValuesTemplate
	files[filepath.Join("deploy", "helm", "templates", "workload.yaml")] = helmWorkloadTemplate
	files[filepath.Join("deploy", "observability", "prometheus.yaml")] = prometheusStackTemplate
	files[filepath.Join("deploy", "observability", "otel-collector.yaml")] = otelCollectorTemplate
	files[filepath.Join("deploy", "observability", "grafana-dashboard.json")] = grafanaDashboardTemplate
	files[filepath.Join("deploy", "observability", "logs-correlation.yaml")] = logsCorrelationTemplate
	files[filepath.Join("bin", "production-check.sh")] = productionCheckScriptTemplate
	files["Makefile"] = makefileTemplate
	files[filepath.Join(".github", "workflows", "ci.yml")] = ciWorkflowTemplate
	addKitexProfileFiles(files, profile)
	return files
}

func addKitexProfileFiles(files map[string]string, profile GenerationProfile) {
	if profile != ProfileKitexCompatible {
		return
	}
	files[filepath.Join("internal", "compat", "kitex", "adapter.go")] = kitexCompatibilityTemplate
}

func goZeroServiceFiles(style, name string) map[string]string {
	files := map[string]string{
		"go.mod":                                                goModTemplate,
		filepath.Join("cmd", name, "main.go"):                   goZeroMainTemplate,
		filepath.Join("etc", name+".json"):                      minimalConfigTemplate,
		filepath.Join("internal", "config", "config.go"):        minimalConfigGoTemplate,
		filepath.Join("internal", "config", "config_test.go"):   configTestTemplate,
		filepath.Join("internal", "svc", "servicecontext.go"):   goZeroSvcTemplate,
		filepath.Join("internal", "types", "types.go"):          goZeroTypesTemplate,
		filepath.Join("internal", "logic", "pinglogic.go"):      goZeroPingLogicTemplate,
		filepath.Join("internal", "handler", "pinghandler.go"):  goZeroPingHandlerTemplate,
		filepath.Join("internal", "handler", "routes.go"):       goZeroRoutesTemplate,
		filepath.Join("internal", "middleware", "trim.go"):      trimMiddlewareTemplate,
		filepath.Join("internal", "middleware", "trim_test.go"): trimMiddlewareTestTemplate,
	}
	if style == ServiceStyleBasic {
		files["Dockerfile"] = dockerfileTemplate
		files["Makefile"] = makefileTemplate
	}
	return files
}

func cleanupLegacyServiceFiles(dir string) error {
	return cleanupLegacyServiceFilesForProfile(dir, ProfileGoflyAI)
}

func cleanupLegacyServiceFilesForProfile(dir string, profile GenerationProfile) error {
	legacyFiles := []string{
		filepath.Join("internal", "handler", "routes_test.go"),
		filepath.Join("internal", "handler", "ping.go"),
		filepath.Join("internal", "handler", "ping_handler.go"),
	}
	if profile == ProfileGoZeroCompatible {
		legacyFiles = append(legacyFiles, filepath.Join("internal", "svc", "service_context.go"))
	} else {
		legacyFiles = append(legacyFiles,
			filepath.Join("internal", "handler", "routes.go"),
			filepath.Join("internal", "handler", "pinghandler.go"),
			filepath.Join("internal", "logic", "pinglogic.go"),
			filepath.Join("internal", "svc", "servicecontext.go"),
			filepath.Join("internal", "types", "types.go"),
		)
	}
	for _, rel := range legacyFiles {
		path := filepath.Join(dir, rel)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove legacy generated file %s: %w", path, err)
		}
	}
	legacyDirs := legacyServiceDirs(profile)
	for _, rel := range legacyDirs {
		path := filepath.Join(dir, rel)
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove legacy generated directory %s: %w", path, err)
		}
	}
	return nil
}

func legacyServiceDirs(profile GenerationProfile) []string {
	switch profile {
	case ProfileGoZeroCompatible:
		return []string{
			filepath.Join("internal", "routes"),
			filepath.Join("internal", "api"),
			filepath.Join("internal", "service"),
		}
	default:
		return []string{
			filepath.Join("internal", "logic"),
			filepath.Join("internal", "handler"),
			filepath.Join("internal", "types"),
		}
	}
}

func frameworkReplaceBlock(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH"))
	}
	if path == "" {
		return ""
	}
	return "\nreplace github.com/imajinyun/gofly => " + path + "\n"
}

func GenerateHandler(opts HandlerOptions) error {
	if opts.Name == "" {
		return errors.New("name is required")
	}
	if opts.Dir == "" {
		opts.Dir = "."
	}
	module := opts.Module
	if module == "" {
		var err error
		module, err = inferModule(opts.Dir)
		if err != nil {
			return err
		}
	}
	subdir, err := cleanHandlerSubdir(opts.Path)
	if err != nil {
		return err
	}
	packageName := handlerPackageName(subdir)
	data := map[string]string{
		"Name":        opts.Name,
		"Module":      module,
		"Package":     packageName,
		"HandlerName": exportName(opts.Name),
	}
	content := render(handlerGenTemplate, data)
	formatted, err := format.Source([]byte(content))
	if err != nil {
		return fmt.Errorf("format handler: %w", err)
	}
	path := filepath.Join(opts.Dir, "internal", "api", subdir, lowerSnake(opts.Name)+".go")
	if err := writeGeneratedFile(path, formatted); err != nil {
		return fmt.Errorf("write handler %s: %w", path, err)
	}
	return nil
}

func GenerateMiddleware(opts MiddlewareOptions) error {
	if opts.Dir == "" {
		opts.Dir = "."
	}
	names := cleanMiddlewareNames(opts.Names)
	if len(names) == 0 {
		return errors.New("middleware name is required")
	}
	for _, name := range names {
		middlewareName := exportName(name)
		if err := writeRenderedFile(
			filepath.Join(opts.Dir, "internal", "middleware", lowerSnake(middlewareName)+".go"),
			middlewareGenTemplate,
			map[string]string{
				"Name":           middlewareName,
				"MiddlewareName": middlewareName + "Middleware",
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func cleanMiddlewareNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := lowerSnake(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

func render(t string, data map[string]string) string {
	for k, v := range data {
		t = strings.ReplaceAll(t, "{{."+k+"}}", v)
	}
	return t
}

func writeRenderedFile(path string, tmpl string, data map[string]string) error {
	return serviceFilesystemSink{Dir: filepath.Dir(path)}.WriteRendered([]scaffoldRenderedFile{{
		Path:    filepath.Base(path),
		Content: render(tmpl, data),
	}})
}

func inferModule(dir string) (string, error) {
	// #nosec G304 -- go.mod is read from the explicit service output directory to infer the generated module path.
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			module := strings.TrimSpace(strings.TrimPrefix(line, "module "))
			if module != "" {
				return module, nil
			}
		}
	}
	return "", errors.New("module is required")
}

func cleanHandlerSubdir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return "", nil
	}
	path = filepath.Clean(path)
	if filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid handler path %q", path)
	}
	return path, nil
}

func handlerPackageName(subdir string) string {
	if subdir == "" {
		return "api"
	}
	return lowerName(filepath.Base(subdir))
}

func lowerSnake(s string) string {
	var parts []string
	var b strings.Builder
	for i, r := range s {
		if r == '-' || r == '_' || r == '.' || r == '/' {
			if b.Len() > 0 {
				parts = append(parts, b.String())
				b.Reset()
			}
			continue
		}
		if i > 0 && r >= 'A' && r <= 'Z' && b.Len() > 0 {
			parts = append(parts, b.String())
			b.Reset()
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	if len(parts) == 0 {
		return "api"
	}
	return strings.ToLower(strings.Join(parts, "_"))
}

func lowerName(s string) string {
	name := lowerSnake(s)
	name = strings.ReplaceAll(name, "_", "")
	if name == "" {
		return "api"
	}
	return name
}

// GenerateServiceScaffold 是配置驱动的脚手架入口，按 IR、renderer、filesystem sink 三层编排生成流程。
func GenerateServiceScaffold(opts ServiceScaffoldOptions) error {
	ir, err := buildServiceScaffoldIR(opts)
	if err != nil {
		return err
	}
	if err := cleanupLegacyServiceFilesForProfile(ir.Dir, ir.Profile); err != nil {
		return err
	}

	rendered := serviceScaffoldRenderer{}.Render(ir)
	sink := serviceFilesystemSink{Dir: ir.Dir, Stderr: os.Stderr}
	if err := sink.WriteRendered(rendered); err != nil {
		return err
	}
	if err := sink.RunPlugins(ir); err != nil {
		return err
	}

	return nil
}
