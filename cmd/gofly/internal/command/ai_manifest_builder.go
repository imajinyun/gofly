package command

func buildAIManifestDocs() []aiManifestLink {
	return []aiManifestLink{
		{Title: "AI manifest", Path: "docs/concepts/ai-manifest.md"},
		{Title: "CLI JSON contracts", Path: "docs/reference/cli-json-contracts.md"},
		{Title: "Stable API surface", Path: "docs/reference/api-surface.md"},
		{Title: "Compatibility policy", Path: "docs/reference/compatibility.md"},
		{Title: "P1 growth roadmap", Path: "docs/reference/p1-growth-roadmap.md"},
	}
}

func buildAIManifestExamples() []aiManifestLink {
	return []aiManifestLink{
		{Title: "Examples catalog", Path: "examples/README.md"},
		{Title: "AI governed service", Path: "examples/ai-governed-service/README.md"},
		{Title: "Microshop", Path: "examples/microshop/README.md"},
		{Title: "Production orders", Path: "examples/production-orders/README.md"},
	}
}

func aiManifestOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope by default; text summary only when --format text is explicitly requested",
		Envelope: []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		EventFields: []string{
			"schemaVersion", "tool", "version", "description", "invocation", "output", "controlPlane", "llmGovernance", "featureLibrary", "commands",
		},
		Semantics: map[string]string{
			"command": "ai.manifest",
			"schema":  "--schema jsonschema emits the JSON Schema contract for the manifest envelope data",
			"secrets": "provider secret values are never included; only secret environment variable names and resolution policy are exposed",
		},
	}
}

func manifestCommand(name string, aliases []string, description, usage string, properties map[string]aiInputProperty, outputFormats, sideEffects []string, riskLevel string, supportsDryRun, mutatesFilesystem bool, examples []string) aiToolCommand {
	return aiToolCommand{
		Name:              name,
		Aliases:           aliases,
		Description:       description,
		Usage:             usage,
		InputSchema:       aiInputSchema{Type: "object", Properties: properties, AdditionalProperties: false},
		OutputFormats:     append([]string(nil), outputFormats...),
		SideEffects:       append([]string(nil), sideEffects...),
		RiskLevel:         riskLevel,
		SupportsDryRun:    supportsDryRun,
		MutatesFilesystem: mutatesFilesystem,
		Examples:          append([]string(nil), examples...),
	}
}

func apiServiceScaffoldProperties() map[string]aiInputProperty {
	return map[string]aiInputProperty{
		"name":   stringProperty("Service name."),
		"module": stringProperty("Go module path."),
		"dir":    stringProperty("Output directory."),
		"style":  enumStringProperty("Scaffold style.", "minimal", "basic", "production"),
		"dryRun": boolProperty("Print a plan without writing scaffold files, config, or plugin output."),
	}
}

func rpcServiceScaffoldProperties() map[string]aiInputProperty {
	props := apiServiceScaffoldProperties()
	props["profile"] = enumStringProperty("Generation profile.", "gofly-ai", "gozero-compatible", "kitex-compatible")
	return props
}

func fileInputProperties(name string) map[string]aiInputProperty {
	return map[string]aiInputProperty{name: stringProperty("Input file path.")}
}

func stringProperty(description string) aiInputProperty {
	return aiInputProperty{Type: "string", Description: description}
}

func boolProperty(description string) aiInputProperty {
	return aiInputProperty{Type: "boolean", Description: description}
}

func intProperty(description string) aiInputProperty {
	return aiInputProperty{Type: "integer", Description: description}
}

func enumStringProperty(description string, values ...string) aiInputProperty {
	return aiInputProperty{Type: "string", Description: description, Enum: append([]string(nil), values...)}
}

func inferTopLevelRisk(name string) string {
	switch name {
	case "version", "env", "bug", "doctor", "feature", "completion", "complete", "release", "ai":
		return "read"
	case "plugin", "template", "upgrade":
		return "high"
	case "new", "gen", "handler", "rpc", "api", "model", "docker", "kube", "quickstart", "migrate", "config", "example":
		return "medium"
	default:
		return "medium"
	}
}

func topLevelMayMutate(name string) bool {
	switch name {
	case "version", "env", "bug", "doctor", "feature", "completion", "complete", "release", "ai":
		return false
	default:
		return true
	}
}
