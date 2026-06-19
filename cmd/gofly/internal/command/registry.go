package command

import (
	"fmt"
	"strings"
)

type commandHandler func([]string) error

type commandSpec struct {
	Name    string
	Aliases []string
	Short   string
	Run     commandHandler
}

type commandRegistry struct {
	commands map[string]commandSpec
	primary  []commandSpec
}

func newCommandRegistry(specs ...commandSpec) commandRegistry {
	registry := commandRegistry{commands: make(map[string]commandSpec, len(specs))}
	for _, spec := range specs {
		registry.register(spec)
	}
	return registry
}

func (r *commandRegistry) register(spec commandSpec) {
	if spec.Name == "" || spec.Run == nil {
		return
	}
	r.primary = append(r.primary, spec)
	r.commands[spec.Name] = spec
	for _, alias := range spec.Aliases {
		if alias != "" {
			r.commands[alias] = spec
		}
	}
}

func (r commandRegistry) dispatch(args []string, usage string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: expected %s", errUsage, usage)
	}
	spec, ok := r.commands[args[0]]
	if !ok {
		return fmt.Errorf("%w: unknown command %q; expected %s", errUsage, args[0], usage)
	}
	return spec.Run(args[1:])
}

func (r commandRegistry) expected() string {
	names := make([]string, 0, len(r.primary))
	for _, spec := range r.primary {
		names = append(names, spec.Name)
		names = append(names, spec.Aliases...)
	}
	return strings.Join(names, "|")
}

func (r commandRegistry) dispatchDefault(args []string, usage string, fallback commandHandler) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fallback(args)
	}
	return r.dispatch(args, usage)
}

var rootCommands = newCommandRegistry(
	commandSpec{Name: "version", Short: "Print version metadata.", Run: versionCommand},
	commandSpec{Name: "new", Short: "Scaffold new API or RPC services.", Run: newCommand},
	commandSpec{Name: "gen", Aliases: []string{"generate"}, Short: "Run unified code generators.", Run: genCommand},
	commandSpec{Name: "handler", Short: "Generate or complete API handlers.", Run: handlerCommand},
	commandSpec{Name: "rpc", Short: "Generate and validate RPC services.", Run: rpcCommand},
	commandSpec{Name: "api", Short: "Generate and manage API definition files.", Run: apiCommand},
	commandSpec{Name: "model", Short: "Generate model repositories.", Run: modelCommand},
	commandSpec{Name: "docker", Short: "Generate Dockerfile assets.", Run: dockerCommand},
	commandSpec{Name: "kube", Short: "Generate Kubernetes manifests.", Run: kubeCommand},
	commandSpec{Name: "template", Short: "Manage local or remote generation templates.", Run: templateCommand},
	commandSpec{Name: "env", Short: "Inspect local toolchain environment.", Run: envCommand},
	commandSpec{Name: "completion", Short: "Emit shell completion scripts.", Run: completionCommand},
	commandSpec{Name: "quickstart", Short: "Create runnable services quickly.", Run: quickstartCommand},
	commandSpec{Name: "migrate", Aliases: []string{"migration"}, Short: "Create SQL migration files.", Run: migrateCommand},
	commandSpec{Name: "bug", Short: "Print diagnostic bug reports.", Run: bugCommand},
	commandSpec{Name: "upgrade", Short: "Print or run upgrade commands.", Run: upgradeCommand},
	commandSpec{Name: "config", Short: "Manage .gofly configuration.", Run: configCommand},
	commandSpec{Name: "feature", Short: "List or preview scaffold features.", Run: featureCommand},
	commandSpec{Name: "plugin", Short: "List, install or run generation plugins.", Run: pluginCommand},
	commandSpec{Name: "complete", Short: "Emit legacy completion scripts.", Run: completeCommand},
	commandSpec{Name: "release", Short: "Run release readiness checks.", Run: releaseCommand},
	commandSpec{Name: "doctor", Short: "Diagnose local environment readiness.", Run: doctorCommand},
	commandSpec{Name: "example", Aliases: []string{"examples"}, Short: "List or run built-in examples.", Run: exampleCommand},
	commandSpec{Name: "ai", Aliases: []string{"tools"}, Short: "Emit machine-readable tool metadata for AI agents.", Run: aiCommand},
)
