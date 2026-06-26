package generator

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
