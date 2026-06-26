package command

import (
	"flag"
	"fmt"
	"strings"
)

func aiProjectPlanValues(plan aiProjectPlan) (name, module, dir string) {
	fields := strings.Fields(plan.Command)
	if len(fields) > 3 && fields[0] == "gofly" && !strings.HasPrefix(fields[3], "-") {
		name = fields[3]
	}
	if v := templateInputValue(plan.Command, "--name"); v != "" {
		name = v
	}
	return name, templateInputValue(plan.Command, "--module"), templateInputValue(plan.Command, "--dir")
}

func aiProjectApplyArgs(plan aiProjectPlan) ([]string, error) {
	name, module, dir := aiProjectPlanValues(plan)
	fields := strings.Fields(plan.Template.Command)
	if len(fields) < 3 || fields[0] != "gofly" {
		return nil, fmt.Errorf("%w: unsupported scaffold command %q", errUsage, plan.Template.Command)
	}
	args := make([]string, 0, len(fields)-1)
	for _, field := range fields[1:] {
		switch field {
		case "<name>":
			args = append(args, name)
		case "<module>":
			args = append(args, module)
		case "<dir>":
			args = append(args, dir)
		default:
			args = append(args, field)
		}
	}
	args = stripCommandFlags(args, "--dry-run", "--plan", "--json")
	return args, nil
}

func stripCommandFlags(args []string, names ...string) []string {
	remove := make(map[string]struct{}, len(names))
	for _, name := range names {
		remove[name] = struct{}{}
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, hasInlineValue := splitFlagName(arg)
		if _, ok := remove[name]; ok {
			if !hasInlineValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

func splitFlagName(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") {
		return arg, false
	}
	name, _, ok := strings.Cut(arg, "=")
	if ok {
		return name, true
	}
	return arg, false
}

func templateInputValue(command, flagName string) string {
	fields := strings.Fields(command)
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == flagName && i+1 < len(fields) {
			return fields[i+1]
		}
		prefix := flagName + "="
		if strings.HasPrefix(field, prefix) {
			return strings.TrimPrefix(field, prefix)
		}
	}
	return ""
}

func flagWasProvided(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
