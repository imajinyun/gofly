package command

func rootCommandManifestEntries() []commandSpec {
	return []commandSpec{
		{Name: "version", Short: "Print version metadata."},
		{Name: "new", Short: "Scaffold new production, API, or RPC services."},
		{Name: "gen", Aliases: []string{"generate"}, Short: "Run unified code generators."},
		{Name: "handler", Short: "Generate or complete API handlers."},
		{Name: "rpc", Short: "Generate and validate RPC services."},
		{Name: "api", Short: "Generate and manage API definition files."},
		{Name: "model", Short: "Generate model repositories."},
		{Name: "docker", Short: "Generate Dockerfile assets."},
		{Name: "kube", Short: "Generate Kubernetes manifests."},
		{Name: "template", Short: "Manage local or remote generation templates."},
		{Name: "env", Short: "Inspect local toolchain environment."},
		{Name: "completion", Short: "Emit shell completion scripts."},
		{Name: "quickstart", Short: "Create runnable services quickly."},
		{Name: "migrate", Aliases: []string{"migration"}, Short: "Create SQL migration files."},
		{Name: "bug", Short: "Print diagnostic bug reports."},
		{Name: "upgrade", Short: "Print or run upgrade commands."},
		{Name: "config", Short: "Manage .gofly configuration."},
		{Name: "feature", Short: "List or preview scaffold features."},
		{Name: "plugin", Short: "List, install or run generation plugins."},
		{Name: "complete", Short: "Emit legacy completion scripts."},
		{Name: "release", Short: "Run release readiness checks."},
		{Name: "doctor", Short: "Diagnose local environment readiness."},
		{Name: "example", Aliases: []string{"examples"}, Short: "List or run built-in examples."},
		{Name: "ai", Aliases: []string{"tools"}, Short: "Emit machine-readable tool metadata for AI agents."},
	}
}
