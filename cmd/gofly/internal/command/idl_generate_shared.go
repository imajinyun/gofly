package command

import (
	"flag"
	"strings"
	"time"
)

const defaultProtocTimeout = 2 * time.Minute

func buildIDLGeneratePlan(command string, inputs map[string]string, plugins []string) cliPlan {
	dir := inputs["dir"]
	actions := []cliPlanAction{{Operation: "write-files", Target: dir, Description: "generate code under the output directory", RiskLevel: "medium"}}
	cleanPlugins := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		plugin = strings.TrimSpace(plugin)
		if plugin != "" {
			cleanPlugins = append(cleanPlugins, plugin)
		}
	}
	if len(cleanPlugins) > 0 {
		inputs["plugins"] = strings.Join(cleanPlugins, ",")
		actions = append(actions, cliPlanAction{Operation: "run-plugins", Target: strings.Join(cleanPlugins, ","), Description: "execute post-generation plugins and apply returned files or patches", RiskLevel: "high"})
	}
	return cliPlan{
		Command:           command,
		DryRun:            false,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions:           actions,
		GeneratedFiles:    countGeneratedGoProjectFiles(dir),
		NextActions:       []string{"review generated diff", "go test ./..."},
	}
}

func registerGoctlTemplateFlags(fs *flag.FlagSet) {
	fs.String("style", "go_zero", "scaffold style option")
	fs.String("home", "", "template home directory")
	fs.String("remote", "", "remote template repository")
	fs.String("branch", "", "remote template branch")
	fs.Bool("multiple", false, "generate multiple service packages")
}
