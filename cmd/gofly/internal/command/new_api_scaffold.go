package command

import "github.com/imajinyun/gofly/cmd/gofly/internal/generator"

type newAPIScaffoldOptions struct {
	Dir             string
	ResolvedProfile string
	Plugins         []string
	SkipAPISpec     bool
}

func generateNewAPIScaffold(cfg *generator.Config, opts newAPIScaffoldOptions) error {
	return generator.GenerateServiceScaffold(generator.ServiceScaffoldOptions{
		Name:           cfg.ServiceName,
		Module:         cfg.Module,
		Dir:            opts.Dir,
		Style:          cfg.Style,
		TemplateDir:    cfg.TemplateDir,
		TemplateRemote: cfg.TemplateRemote,
		TemplateBranch: cfg.TemplateBranch,
		Profile:        opts.ResolvedProfile,
		Features:       cfg.Features,
		Plugins:        opts.Plugins,
		SkipAPISpec:    opts.SkipAPISpec,
		Kind:           "api",
	})
}
