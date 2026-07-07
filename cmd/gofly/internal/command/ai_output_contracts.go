package command

func aiProjectPlanOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json, --output json or --format json is used; deterministic text plan otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		EventFields: []string{
			"prompt", "projectType", "template", "features", "command", "riskLevel", "mutatesFilesystem", "dryRun", "verify", "warnings", "nextActions",
		},
		Semantics: map[string]string{
			"command":            "ai.plan",
			"determinism":        "uses deterministic local template matching and does not call an external LLM provider",
			"filesystemMutation": "never writes files; mutatesFilesystem is always false and dryRun is always true",
		},
	}
}

func aiProjectApplyOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json, --output json or --format json is used; deterministic text plan/apply summary otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		EventFields: []string{
			"plan", "applied", "outputDir", "executedCommand", "generatedFeatures", "dependencies", "configHints", "featureVerify", "verify", "verifyRan", "verifyPassed", "verification", "warnings", "nextActions", "mutatesFilesystem",
		},
		Semantics: map[string]string{
			"command":            "ai.new",
			"dryRunDefault":      "prints the selected scaffold plan without writing files unless --apply is set or --dry-run=false is explicitly used",
			"filesystemMutation": "writes scaffold files only under the validated --dir boundary when apply mode is enabled",
			"verification":       "--verify runs allowlisted local commands under --dir and reports every command result",
		},
	}
}

func aiCompleteOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:        "single JSON envelope for normal completion; newline-delimited JSON envelopes when --stream is set with JSON output",
		Envelope:    []string{"ok", "command", "version", "data", "error"},
		EventFields: []string{"provider", "model", "text", "usage", "budget", "governance"},
		Semantics: map[string]string{
			"stream": "when --stream is set, use the ai stream output contract with command ai.complete",
		},
	}
}

func aiLLMTelemetryFields() []string {
	return []string{"operation", "provider", "model", "status", "error_class", "retryable", "provider_status_code", "provider_status_class", "stream_events", "cache_status", "input_tokens", "output_tokens", "total_tokens"}
}

func aiStreamOutputContract(command string) *aiOutputContract {
	return &aiOutputContract{
		Mode:        "newline-delimited JSON; each line is one JSON envelope and is independently parseable",
		Envelope:    []string{"ok", "command", "version", "data", "error"},
		EventFields: []string{"provider", "model", "index", "delta", "done", "usage", "budget", "governance"},
		Semantics: map[string]string{
			"command": command,
			"delta":   "incremental text chunk; may be empty for usage or done events",
			"done":    "true only on stream termination events emitted by the provider/governance layer",
			"usage":   "token usage snapshot when the provider emits usage; omitted or zero-valued otherwise",
			"error":   "stream errors are emitted as a final error envelope in JSON stream mode before command failure when possible",
		},
	}
}

func aiDoctorOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json or --output json is used; human-readable diagnostic report otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		EventFields: []string{
			"version", "providers", "envVars", "secrets", "failover", "config", "cache", "telemetry", "cost", "summary",
		},
		Semantics: map[string]string{
			"command": "ai.doctor",
			"secrets": "reports secret presence and remediation without printing secret values",
			"status":  "diagnostic item status is one of ok, warn, fail or info; severity is present for actionable warnings/failures",
		},
	}
}

func gatewayProfileValidateOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json, --output json or --format json is used; text summary otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error"},
		EventFields: []string{
			"ok", "compatible", "errors", "changes", "current", "candidate",
		},
		Semantics: map[string]string{
			"command":       "gateway.profile.validate",
			"contract":      "compares a candidate descriptor-method transcode profile against current config without mutating runtime state",
			"breakingDiff":  "removed mappings or target changes are reported as breaking changes",
			"ciIntegration": "suitable for generated project and release gates before accepting profile mapping changes",
		},
	}
}

func gatewayAggregationValidateOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json, --output json or --format json is used; markdown summary table when --format markdown is used; SARIF 2.1.0 when --format sarif is used; text summary otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error"},
		EventFields: []string{
			"ok", "compatible", "errors", "changes", "current", "candidate",
		},
		Semantics: map[string]string{
			"command":       "gateway.aggregation.validate",
			"contract":      "compares a candidate BFF aggregation step and response-shape contract against current gateway config or old/new OpenAPI x-gofly-aggregation contracts without mutating runtime state",
			"breakingDiff":  "removed steps, fallback loss, path changes, shape mode changes, and target changes are reported as breaking changes",
			"ciIntegration": "suitable for generated project and release gates before accepting BFF aggregation contract changes",
		},
	}
}

func releaseCheckOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json, --output json or --format json is used; text summary otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error"},
		EventFields: []string{
			"version", "summary", "checks", "blocking", "warnings", "recommended_semver", "evidence",
		},
		Semantics: map[string]string{
			"command":                     "release.check",
			"checks":                      "checks is a list of named release gates with status, blocker, detail, and optional evidence",
			"evidenceFilter":              "--evidence returns only the named check in data.checks and keeps that check's evidence machine-readable",
			"aggregationEvidenceFamilies": "gateway-aggregation-contract evidence contains aggregation-json-diff and aggregation-openapi-diff families",
			"rpcMuxAdapterEvidenceFamily": "rpc-mux-adapter-evidence exposes BenchmarkRPCExperimentalMuxAdapterOpenSendReceiveClose report-only baseline/current samples and promotion decision",
			"ciIntegration":               "suitable for release and CI gates; failed blockers are also repeated in error.details.blocking",
		},
	}
}
