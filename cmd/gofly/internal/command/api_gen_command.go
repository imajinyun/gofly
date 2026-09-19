package command

import (
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiGenCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api gen", flag.ContinueOnError)
	file := registerAPIFileFlags(fs, "api file")
	dir := fs.String("dir", ".", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	rpcPkg := fs.String("rpc-package", "", "RPC generated package import path for gateway generation")
	profile := fs.String("profile", "", "generation profile: gofly-ai, gozero-compatible, or kitex-compatible")
	profileAlias := fs.String("generation-profile", "", "alias for --profile")
	pluginArg := fs.String("plugin", "", "additional plugin executable (comma-separated) to run after generation")
	test := fs.Bool("test", false, "generate test files")
	typeGroup := fs.Bool("type-group", false, "group generated types")
	jsonOut := registerCLIJSONOutputFlag(fs, "emit generation result as JSON")
	style := registerGoctlTemplateFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	apiFile := file.resolve(leadingFile, remaining)
	resolvedProfile, err := resolveAPIGenerationProfile(fs, *profile, *profileAlias, *style)
	if err != nil {
		return err
	}
	if err := generator.GenerateRESTFromAPI(generator.APIOptions{APIFile: apiFile, Dir: *dir, Package: *pkg, RPCPackage: *rpcPkg, Profile: resolvedProfile, Test: *test, TypeGroup: *typeGroup}); err != nil {
		return err
	}
	if err := runPostPlugins(*pluginArg, generator.PluginRequest{
		Command: "api",
		Input:   map[string]string{"api": apiFile, "package": *pkg},
		Dir:     *dir,
	}); err != nil {
		return err
	}
	if *jsonOut || outputMode() == outputJSON {
		inputs := map[string]string{"api": apiFile, "dir": *dir}
		if *pkg != "" {
			inputs["package"] = *pkg
		}
		if *rpcPkg != "" {
			inputs["rpcPackage"] = *rpcPkg
		}
		if resolvedProfile != "" {
			inputs["profile"] = resolvedProfile
		}
		if *test {
			inputs["test"] = "true"
		}
		if *typeGroup {
			inputs["typeGroup"] = "true"
		}
		addAPIStaleReportInputs(inputs, *dir)
		return printJSONEnvelope("api.gen", buildIDLGeneratePlan("api gen", inputs, splitCSV(*pluginArg)))
	}
	return nil
}

func resolveAPIGenerationProfile(fs *flag.FlagSet, profile, profileAlias, style string) (string, error) {
	profileProvided := flagProvided(fs, "profile")
	aliasProvided := flagProvided(fs, "generation-profile")
	styleProvided := flagProvided(fs, "style")

	resolvedProfile := strings.TrimSpace(profile)
	if aliasProvided {
		resolvedAlias, err := generator.NormalizeGenerationProfile(profileAlias)
		if err != nil {
			return "", err
		}
		if profileProvided {
			resolvedPrimary, err := generator.NormalizeGenerationProfile(profile)
			if err != nil {
				return "", err
			}
			if resolvedPrimary != resolvedAlias {
				return "", fmt.Errorf("%w: --profile %q conflicts with --generation-profile %q", errUsage, profile, profileAlias)
			}
		}
		resolvedProfile = string(resolvedAlias)
	}

	if !styleProvided {
		return resolvedProfile, nil
	}
	if !isGoZeroAPIStyle(style) {
		return "", fmt.Errorf("%w: unsupported API style %q", errUsage, style)
	}
	if resolvedProfile == "" {
		return string(generator.ProfileGoZeroCompatible), nil
	}
	normalized, err := generator.NormalizeGenerationProfile(resolvedProfile)
	if err != nil {
		return "", err
	}
	if normalized != generator.ProfileGoZeroCompatible {
		return "", fmt.Errorf("%w: --style %q requires --profile %q, got %q", errUsage, style, generator.ProfileGoZeroCompatible, resolvedProfile)
	}
	return string(normalized), nil
}

func isGoZeroAPIStyle(style string) bool {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "go_zero", "gozero", "go-zero":
		return true
	default:
		return false
	}
}

type apiStaleReportSummary struct {
	StaleHandlers []string `json:"staleHandlers"`
	StaleLogics   []string `json:"staleLogics"`
}

func addAPIStaleReportInputs(inputs map[string]string, dir string) {
	path := filepath.Join(dir, ".gofly", "stale-api-files.json")
	data, err := generator.ReadFileUnderRoot(dir, filepath.Join(".gofly", "stale-api-files.json"), "stale api report")
	if err != nil {
		return
	}
	var report apiStaleReportSummary
	if err := json.Unmarshal(data, &report); err != nil {
		return
	}
	inputs["staleReportPath"] = path
	inputs["staleHandlers"] = strconv.Itoa(len(report.StaleHandlers))
	inputs["staleLogics"] = strconv.Itoa(len(report.StaleLogics))
}
