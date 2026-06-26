package command

func buildAIToolManifestJSONSchema() map[string]any {
	stringArraySchema := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	linkSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"title", "path"},
		"additionalProperties": false,
		"properties": map[string]any{
			"title": map[string]any{"type": "string"},
			"path":  map[string]any{"type": "string"},
		},
	}
	linkArraySchema := map[string]any{"type": "array", "items": linkSchema}
	propertySchema := map[string]any{
		"type":                 "object",
		"required":             []string{"type", "description"},
		"additionalProperties": false,
		"properties": map[string]any{
			"type":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"enum":        stringArraySchema,
		},
	}
	outputContractSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"mode", "envelope"},
		"additionalProperties": false,
		"properties": map[string]any{
			"mode":        map[string]any{"type": "string"},
			"envelope":    stringArraySchema,
			"eventFields": stringArraySchema,
			"semantics":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		},
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://gofly.dev/schemas/ai-tool-manifest.schema.json",
		"title":                "gofly AI tool manifest",
		"type":                 "object",
		"required":             []string{"schemaVersion", "tool", "version", "description", "invocation", "docs", "examples", "verifyCommands", "output", "controlPlane", "llmGovernance", "featureLibrary", "commands"},
		"additionalProperties": false,
		"properties": map[string]any{
			"schemaVersion":  map[string]any{"type": "string", "const": aiToolManifestSchemaVersion},
			"tool":           map[string]any{"type": "string", "const": "gofly"},
			"version":        map[string]any{"type": "string"},
			"description":    map[string]any{"type": "string"},
			"invocation":     map[string]any{"type": "string"},
			"docs":           linkArraySchema,
			"examples":       linkArraySchema,
			"verifyCommands": stringArraySchema,
			"output": map[string]any{
				"type":                 "object",
				"required":             []string{"mode", "envelope", "errorFields"},
				"additionalProperties": false,
				"properties": map[string]any{
					"mode":        map[string]any{"type": "string"},
					"envelope":    stringArraySchema,
					"errorFields": stringArraySchema,
				},
			},
			"controlPlane":   map[string]any{"type": "object", "additionalProperties": true},
			"llmGovernance":  map[string]any{"type": "object", "additionalProperties": true},
			"featureLibrary": map[string]any{"type": "object", "additionalProperties": true},
			"commands": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"required":             []string{"name", "description", "usage", "inputSchema", "outputFormats", "sideEffects", "riskLevel", "supportsDryRun", "mutatesFilesystem"},
					"additionalProperties": false,
					"properties": map[string]any{
						"name":        map[string]any{"type": "string"},
						"aliases":     stringArraySchema,
						"description": map[string]any{"type": "string"},
						"usage":       map[string]any{"type": "string"},
						"inputSchema": map[string]any{
							"type":                 "object",
							"required":             []string{"type", "additionalProperties"},
							"additionalProperties": false,
							"properties": map[string]any{
								"type":                 map[string]any{"type": "string", "const": "object"},
								"properties":           map[string]any{"type": "object", "additionalProperties": propertySchema},
								"required":             stringArraySchema,
								"additionalProperties": map[string]any{"type": "boolean"},
							},
						},
						"outputContract":    outputContractSchema,
						"outputFormats":     stringArraySchema,
						"sideEffects":       stringArraySchema,
						"riskLevel":         map[string]any{"type": "string", "enum": []string{"read", "low", "medium", "high"}},
						"supportsDryRun":    map[string]any{"type": "boolean"},
						"mutatesFilesystem": map[string]any{"type": "boolean"},
						"examples":          stringArraySchema,
					},
				},
			},
		},
	}
}
