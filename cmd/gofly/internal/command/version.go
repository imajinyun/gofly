package command

import (
	"flag"
	"fmt"
	"runtime"
	"strings"
)

// Version, Commit and BuiltAt are injected at build time via -ldflags.
// Example:
//
//	go build -ldflags "-X '.../command.Version=v1.0.0' \
//	                    -X '.../command.Commit=$(git rev-parse HEAD)' \
//	                    -X '.../command.BuiltAt=$(date -u +%FT%TZ)'"
var (
	Version = "v0.1.0"
	Commit  = "unknown"
	BuiltAt = "unknown"
)

type versionInfo struct {
	Tool      string `json:"tool"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"built_at"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

func versionCommand(args []string) error {
	if printCommandHelp("version", args) {
		return nil
	}
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print version metadata as JSON")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("%w: version does not accept positional arguments: %s", errUsage, strings.Join(remaining, " "))
	}
	info := currentVersionInfo()
	if *jsonOutput || outputMode() == outputJSON {
		return printJSONEnvelope("version", info)
	}
	cliOutputf("gofly %s\ncommit: %s\nbuilt:  %s\n", info.Version, info.Commit, info.BuiltAt)
	return nil
}

func currentVersionInfo() versionInfo {
	return versionInfo{
		Tool:      "gofly",
		Version:   Version,
		Commit:    Commit,
		BuiltAt:   BuiltAt,
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
}
