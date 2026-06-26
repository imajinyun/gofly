package command

import (
	"flag"
	"fmt"
	"strings"
)

const aiToolManifestSchemaVersion = "gofly.ai.tool-manifest.v1"

type aiToolManifest struct {
	SchemaVersion  string                   `json:"schemaVersion"`
	Tool           string                   `json:"tool"`
	Version        string                   `json:"version"`
	Description    string                   `json:"description"`
	Invocation     string                   `json:"invocation"`
	Docs           []aiManifestLink         `json:"docs"`
	Examples       []aiManifestLink         `json:"examples"`
	VerifyCommands []string                 `json:"verifyCommands"`
	Output         aiOutputSchema           `json:"output"`
	ControlPlane   aiControlPlaneManifest   `json:"controlPlane"`
	LLMGovernance  aiLLMGovernance          `json:"llmGovernance"`
	FeatureLibrary aiFeatureLibraryManifest `json:"featureLibrary"`
	Commands       []aiToolCommand          `json:"commands"`
}

type aiManifestLink struct {
	Title string `json:"title"`
	Path  string `json:"path"`
}

type aiOutputSchema struct {
	Mode        string   `json:"mode"`
	Envelope    []string `json:"envelope"`
	ErrorFields []string `json:"errorFields"`
}

func aiManifestCommand(args []string) error {
	fs := flag.NewFlagSet("ai manifest", flag.ContinueOnError)
	formatName := fs.String("format", outputJSON, "output format: json or text")
	schemaName := fs.String("schema", "", "output manifest schema: jsonschema")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("%w: ai manifest does not accept positional arguments: %s", errUsage, strings.Join(remaining, " "))
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputJSON
	}
	if format != outputJSON && format != outputText {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	schema := strings.ToLower(strings.TrimSpace(*schemaName))
	if schema != "" {
		if schema != "jsonschema" {
			return fmt.Errorf("%w: unsupported --schema %q", errUsage, *schemaName)
		}
		return printJSONEnvelope("ai.manifest.schema", buildAIToolManifestJSONSchema())
	}
	manifest := buildAIToolManifest()
	if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
		return printJSONEnvelope("ai.manifest", manifest)
	}
	cliOutputfIf("gofly AI tool manifest (%s)\n", manifest.SchemaVersion)
	for _, cmd := range manifest.Commands {
		cliOutputfIf("%s\t%s\tdry-run=%t\trisk=%s\n", cmd.Name, strings.Join(cmd.OutputFormats, ","), cmd.SupportsDryRun, cmd.RiskLevel)
	}
	return nil
}
