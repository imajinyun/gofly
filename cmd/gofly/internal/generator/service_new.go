package generator

import (
	"errors"
	"path/filepath"
	"strings"
)

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
