package generator

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

type GenerationProfile string

const (
	ProfileGoflyAI          GenerationProfile = "gofly-ai"
	ProfileGoZeroCompatible GenerationProfile = "gozero-compatible"
	ProfileKitexCompatible  GenerationProfile = "kitex-compatible"
)

type serviceScaffoldArtifact struct {
	Path string
	Kind string
}

type serviceScaffoldRuntimeFeature struct {
	Name        string
	Description string
}

type serviceScaffoldIR struct {
	Name            string
	Module          string
	Dir             string
	Style           string
	Kind            string
	Profile         GenerationProfile
	Data            map[string]string
	Files           map[string]string
	Artifacts       []serviceScaffoldArtifact
	RuntimeFeatures []serviceScaffoldRuntimeFeature
	Plugins         []string
}

func buildServiceScaffoldIR(opts ServiceScaffoldOptions) (serviceScaffoldIR, error) {
	if opts.Name == "" {
		return serviceScaffoldIR{}, errors.New("name is required")
	}
	if opts.Module == "" {
		return serviceScaffoldIR{}, errors.New("module is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", opts.Name)
	}
	style, err := normalizeServiceStyle(opts.Style)
	if err != nil {
		return serviceScaffoldIR{}, err
	}
	profile, err := normalizeGenerationProfile(opts.Profile)
	if err != nil {
		return serviceScaffoldIR{}, err
	}
	if err := ValidateFeatureNames(opts.Features); err != nil {
		return serviceScaffoldIR{}, err
	}

	data := serviceScaffoldData(opts)
	files := serviceFilesForProfile(style, opts.Name, profile)
	mergeServiceScaffoldExtras(files, opts)

	files, err = applyServiceTemplateSource(files, opts)
	if err != nil {
		return serviceScaffoldIR{}, err
	}

	if len(opts.Features) > 0 {
		scope := ExtensionScope{
			Name:   opts.Name,
			Module: opts.Module,
			Style:  style,
			Dir:    opts.Dir,
			Data:   data,
		}
		files, data, err = ApplyFeatureNames(opts.Features, scope, files, data)
		if err != nil {
			return serviceScaffoldIR{}, err
		}
	}

	return serviceScaffoldIR{
		Name:            opts.Name,
		Module:          opts.Module,
		Dir:             opts.Dir,
		Style:           style,
		Kind:            opts.Kind,
		Profile:         profile,
		Data:            data,
		Files:           files,
		Artifacts:       serviceScaffoldArtifacts(files),
		RuntimeFeatures: serviceScaffoldRuntimeFeatures(profile, opts.Kind),
		Plugins:         normalizedServicePlugins(opts.Plugins),
	}, nil
}

func normalizeGenerationProfile(profile string) (GenerationProfile, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		return ProfileGoflyAI, nil
	}
	switch GenerationProfile(profile) {
	case ProfileGoflyAI, ProfileGoZeroCompatible, ProfileKitexCompatible:
		return GenerationProfile(profile), nil
	default:
		return "", errors.New("unknown generation profile " + profile)
	}
}

func serviceScaffoldArtifacts(files map[string]string) []serviceScaffoldArtifact {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	artifacts := make([]serviceScaffoldArtifact, 0, len(paths))
	for _, path := range paths {
		artifacts = append(artifacts, serviceScaffoldArtifact{Path: path, Kind: serviceScaffoldArtifactKind(path)})
	}
	return artifacts
}

func serviceScaffoldArtifactKind(path string) string {
	switch {
	case strings.HasSuffix(path, ".go"):
		return "go-source"
	case strings.HasSuffix(path, ".api"):
		return "api-contract"
	case strings.HasSuffix(path, ".proto"):
		return "proto-contract"
	case path == "Dockerfile" || strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml"):
		return "deployment"
	default:
		return "project-file"
	}
}

func serviceScaffoldRuntimeFeatures(profile GenerationProfile, kind string) []serviceScaffoldRuntimeFeature {
	features := []serviceScaffoldRuntimeFeature{
		{Name: "deterministic-files", Description: "scaffold files render in stable lexical order"},
		{Name: "safe-paths", Description: "generated paths are constrained to the output directory"},
		{Name: "control-plane-snapshot", Description: "generated config exposes runtime policy and scaffold contract snapshots"},
	}
	switch profile {
	case ProfileGoZeroCompatible:
		features = append(features, serviceScaffoldRuntimeFeature{Name: "goctl-layout", Description: "REST and model scaffolds can evolve toward goctl-compatible layering"})
	case ProfileKitexCompatible:
		features = append(features, serviceScaffoldRuntimeFeature{Name: "idl-runtime-contract", Description: "RPC scaffolds can expose IDL/runtime contracts for Kitex-style governance"})
	case ProfileGoflyAI:
		features = append(features, serviceScaffoldRuntimeFeature{Name: "ai-governance", Description: "scaffolds expose manifest-friendly verification metadata"})
	}
	if strings.EqualFold(kind, "api") {
		features = append(features, serviceScaffoldRuntimeFeature{Name: "rest-contract", Description: "API scaffolds include REST contract artifacts"})
	}
	if strings.EqualFold(kind, "rpc") {
		features = append(features, serviceScaffoldRuntimeFeature{Name: "rpc-contract", Description: "RPC scaffolds include proto contract artifacts"})
	}
	return features
}

func serviceScaffoldData(opts ServiceScaffoldOptions) map[string]string {
	return map[string]string{
		"Name":             opts.Name,
		"Module":           opts.Module,
		"ReplaceBlock":     frameworkReplaceBlock(opts.FrameworkPath),
		"GoFile":           "./cmd/" + opts.Name,
		"Exe":              opts.Name,
		"GoVersion":        "1.26",
		"BaseImage":        "gcr.io/distroless/static-debian12",
		"Namespace":        "default",
		"Image":            opts.Name + ":latest",
		"Port":             "8080",
		"RPCPort":          "8081",
		"Replicas":         "2",
		"Host":             opts.Name + ".example.com",
		"Path":             "/",
		"Data":             kubeConfigData(nil),
		"RevisionHistory":  "",
		"ImagePullSecrets": "",
		"ServiceAccount":   "",
		"ImagePullPolicy":  "",
		"Resources":        kubeResources("100m", "128Mi", "500m", "512Mi"),
		"ServiceType":      "",
		"NodePort":         "",
		"Autoscale":        kubeAutoscale(opts.Name, "default", "2", "6"),
	}
}

func mergeServiceScaffoldExtras(files map[string]string, opts ServiceScaffoldOptions) {
	if strings.EqualFold(opts.Kind, "api") && !opts.SkipAPISpec {
		files[opts.Name+".api"] = apiNewTemplate
	}
	if strings.EqualFold(opts.Kind, "rpc") {
		files[opts.Name+".proto"] = rpcNewTemplate
	}
	for path, content := range opts.ExtraFiles {
		files[path] = content
	}
}

func applyServiceTemplateSource(files map[string]string, opts ServiceScaffoldOptions) (map[string]string, error) {
	templateDir := opts.TemplateDir
	if opts.TemplateRemote != "" && templateDir == "" {
		templateDir = filepath.Join(opts.Dir, ".gofly", "templates")
	}
	if templateDir != "" || opts.TemplateRemote != "" {
		var err error
		templateDir, err = ResolveTemplateSource(
			templateDir,
			opts.TemplateRemote,
			opts.TemplateBranch,
			opts.StrictTemplateRemote,
		)
		if err != nil {
			return nil, err
		}
	}
	if templateDir == "" {
		return files, nil
	}
	return ApplyTemplateExtension(templateDir, files)
}

func normalizedServicePlugins(plugins []string) []string {
	if len(plugins) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		plugin = strings.TrimSpace(plugin)
		if plugin == "" {
			continue
		}
		normalized = append(normalized, plugin)
	}
	return normalized
}
