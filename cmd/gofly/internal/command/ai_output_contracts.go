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
