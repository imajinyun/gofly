// Package command implements the gofly CLI: code generation, scaffolding,
// governance, service discovery, deployment and developer tooling.
package command

import (
	"flag"
	"strings"
)

func warnNoopFlag(command, flagName, reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = "accepted for compatibility"
	}
	errorf("[gofly] %s: --%s is currently a compatibility no-op (%s)\n", command, flagName, reason)
}

func flagProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			provided = true
		}
	})
	return provided
}
