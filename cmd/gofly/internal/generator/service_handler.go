package generator

import (
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
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
