package command

import "github.com/imajinyun/gofly/cmd/gofly/internal/generator"

type newServiceScaffoldOptions struct {
	Dir     string
	Plugins []string
}

func generateNewServiceScaffold(cfg *generator.Config, opts newServiceScaffoldOptions) error {
	return generator.GenerateServiceScaffold(generator.ServiceScaffoldOptions{
		Name:           cfg.ServiceName,
		Module:         cfg.Module,
		Dir:            opts.Dir,
		Style:          cfg.Style,
		TemplateDir:    cfg.TemplateDir,
		TemplateRemote: cfg.TemplateRemote,
		TemplateBranch: cfg.TemplateBranch,
		Features:       cfg.Features,
		Plugins:        opts.Plugins,
		Kind:           "service",
	})
}
