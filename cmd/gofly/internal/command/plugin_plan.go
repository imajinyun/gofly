package command

import "strings"

func pluginRunPlan(commandName, dir, name, module, remote, goPlugin string, plugins []string) cliPlan {
	inputs := map[string]string{
		"command": commandName,
		"dir":     dir,
		"name":    name,
		"module":  module,
	}
	if remote != "" {
		inputs["remote"] = remote
	}
	if goPlugin != "" {
		inputs["goPlugin"] = goPlugin
	}
	if len(plugins) > 0 {
		inputs["plugins"] = strings.Join(plugins, ",")
	}
	return cliPlan{
		Command:           "plugin run",
		DryRun:            true,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions: []cliPlanAction{
			{Operation: "resolve-plugins", Target: strings.Join(plugins, ","), Description: "resolve configured plugin inputs", RiskLevel: "medium"},
			{Operation: "execute-plugins", Target: strings.Join(plugins, ","), Description: "execute plugins with the requested plugin command", RiskLevel: "high"},
			{Operation: "apply-plugin-output", Target: dir, Description: "write files and apply patches returned by plugins", RiskLevel: "high"},
		},
		Warnings:    []string{"dry-run does not download remote plugins, execute plugin binaries, write files, or apply patches"},
		NextActions: []string{"rerun without --dry-run/--plan to apply these actions"},
	}
}
