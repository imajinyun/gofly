package command

import (
	"sort"
	"strings"
)

type cliPlan struct {
	Command           string            `json:"command"`
	DryRun            bool              `json:"dryRun"`
	MutatesFilesystem bool              `json:"mutatesFilesystem"`
	Inputs            map[string]string `json:"inputs,omitempty"`
	Actions           []cliPlanAction   `json:"actions"`
	GeneratedFiles    int               `json:"generatedFiles"`
	PluginEffects     []cliPluginEffect `json:"pluginEffects,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	NextActions       []string          `json:"nextActions,omitempty"`
}

type cliPlanAction struct {
	Operation   string `json:"operation"`
	Target      string `json:"target"`
	Description string `json:"description"`
	RiskLevel   string `json:"riskLevel"`
}

type cliPluginEffect struct {
	Name     string `json:"name"`
	Executed bool   `json:"executed"`
	Files    int    `json:"files"`
	Patches  int    `json:"patches"`
	Note     string `json:"note,omitempty"`
}

func encodeStringMap(in map[string]string) string {
	if len(in) == 0 {
		return ""
	}
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+in[key])
	}
	return strings.Join(parts, ",")
}
