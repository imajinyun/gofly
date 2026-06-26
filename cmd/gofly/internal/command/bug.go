package command

import "flag"

type bugReport struct {
	Tool          string            `json:"tool"`
	Version       string            `json:"version"`
	Environment   map[string]string `json:"environment"`
	Checks        []toolCheck       `json:"checks"`
	SupportBundle supportBundleInfo `json:"supportBundle"`
	NextActions   []string          `json:"nextActions"`
}

type supportBundleInfo struct {
	Schema      string   `json:"schema"`
	Redaction   []string `json:"redaction"`
	Commands    []string `json:"commands"`
	Description string   `json:"description"`
}

func bugCommand(args []string) error {
	if printCommandHelp("bug", args) {
		return nil
	}
	fs := flag.NewFlagSet("bug", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print bug report as JSON")
	if _, err := parseInterspersedFlags(fs, args); err != nil {
		return err
	}
	report := bugReport{
		Tool:        "gofly",
		Version:     Version,
		Environment: envInfo(),
		Checks: []toolCheck{
			envToolCheck("go"),
			envToolCheck("protoc"),
			envToolCheck("git"),
		},
		SupportBundle: supportBundleInfo{
			Schema:      "gofly.support_bundle.v1",
			Redaction:   []string{"Authorization", "Cookie", "Set-Cookie", "GOFLY_LLM_*", "*TOKEN*", "*SECRET*", "*PASSWORD*"},
			Commands:    []string{"gofly doctor --json", "gofly env check --json", "gofly release check --json --strict", "gofly bug --json"},
			Description: "Attach this JSON with command output and generated-project failure logs after removing secrets.",
		},
		NextActions: []string{
			"attach this support bundle when opening an issue or asking for help",
			"run `gofly doctor --json` and fix failed checks before rerunning generators",
			"run `gofly release check --json --strict` before publishing release artifacts",
		},
	}
	if *jsonOutput {
		return printJSON(report)
	}
	cliOutputln("gofly bug report")
	cliOutputf("version: %s\n", Version)
	for _, key := range []string{"GOOS", "GOARCH", "GOVERSION", "GOFLY_VERSION"} {
		cliOutputf("%s=%s\n", key, report.Environment[key])
	}
	cliOutputln("tools:")
	for _, check := range report.Checks {
		cliOutputf("  %s\t%s\t%s\n", check.Name, check.Status, check.Path)
	}
	cliOutputln("Please include this report when filing an issue.")
	return nil
}
