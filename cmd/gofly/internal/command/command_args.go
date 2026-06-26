package command

import (
	"flag"
	"io"
	"strings"
)

// mergeLists 合并两组字符串，保持顺序并去重。
func mergeLists(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range b {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func fillNameFromArgs(name *string, args []string) {
	if name == nil || *name != "" || len(args) == 0 {
		return
	}
	*name = args[0]
}

func parseInterspersedFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if arg == "" || arg == "-" || arg[0] != '-' {
			positionals = append(positionals, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		flag := fs.Lookup(flagName(arg))
		if flag == nil || isBoolFlag(flag) {
			continue
		}
		if i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positionals, nil
}

func flagName(arg string) string {
	name := strings.TrimLeft(arg, "-")
	if before, _, ok := strings.Cut(name, "="); ok {
		return before
	}
	return name
}

func isBoolFlag(flag *flag.Flag) bool {
	type boolFlag interface {
		IsBoolFlag() bool
	}
	value, ok := flag.Value.(boolFlag)
	return ok && value.IsBoolFlag()
}

func splitLeadingName(args []string) (string, []string) {
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		return "", args
	}
	return args[0], args[1:]
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func joinCSV(values ...string) string {
	parts := []string{}
	for _, value := range values {
		parts = append(parts, splitCSV(value)...)
	}
	return strings.Join(parts, ",")
}
