package generator

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/rpc"
)

type ServiceOptions struct {
	Name          string
	Module        string
	Dir           string
	Style         string
	FrameworkPath string
}

const (
	ServiceStyleBasic      = "basic"
	ServiceStyleMinimal    = "minimal"
	ServiceStyleProduction = "production"
)

func GenerateService(opts ServiceOptions) error {
	if opts.Name == "" {
		return errors.New("name is required")
	}
	if opts.Module == "" {
		return errors.New("module is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", opts.Name)
	}
	style, err := normalizeServiceStyle(opts.Style)
	if err != nil {
		return err
	}
	data := withGeneratedResilienceTemplateData(applyOperatorHistoryTemplateData(map[string]string{
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
		"FeatureImports":   "",
		"MuxOTelSinkName":  "",
		"Autoscale":        kubeAutoscale(opts.Name, "default", "2", "6"),
	}), opts.Name)
	if err := cleanupLegacyServiceFiles(opts.Dir); err != nil {
		return err
	}
	ir := serviceScaffoldIR{Dir: opts.Dir, Data: data, Files: serviceFiles(style, opts.Name)}
	rendered := serviceScaffoldRenderer{}.Render(ir)
	return serviceFilesystemSink{Dir: opts.Dir}.WriteRendered(rendered)
}

func frameworkReplaceBlock(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH"))
	}
	if path == "" {
		return ""
	}
	return "\nreplace github.com/imajinyun/gofly => " + path + "\n"
}

// applyOperatorHistoryTemplateData injects the operator-history-derived template
// values (cooldown bounds and recommended production limits) sourced from the
// core rpc package, so generated docs and the production check share one source
// of truth. It returns the same map for convenient chaining.
func applyOperatorHistoryTemplateData(data map[string]string) map[string]string {
	limits := rpc.RPCMuxDiagnosisOperatorHistoryLimits()
	recommended := rpc.RPCMuxDiagnosisOperatorHistoryRecommendedLimits()
	data["DebugReplayCooldownMin"] = limits.MinDebugReplayCooldown.String()
	data["DebugReplayCooldownMax"] = limits.MaxDebugReplayCooldown.String()
	data["DebugReplayCooldownMinNanos"] = strconv.FormatInt(int64(limits.MinDebugReplayCooldown), 10)
	data["DebugReplayCooldownMaxNanos"] = strconv.FormatInt(int64(limits.MaxDebugReplayCooldown), 10)
	data["AuditValidateCooldownMin"] = limits.MinAuditValidateCooldown.String()
	data["AuditValidateCooldownMax"] = limits.MaxAuditValidateCooldown.String()
	data["AuditValidateCooldownMinNanos"] = strconv.FormatInt(int64(limits.MinAuditValidateCooldown), 10)
	data["AuditValidateCooldownMaxNanos"] = strconv.FormatInt(int64(limits.MaxAuditValidateCooldown), 10)
	data["RecommendedMaxActions"] = strconv.Itoa(recommended.MaxActions)
	data["RecommendedMaxBackups"] = strconv.Itoa(recommended.MaxBackups)
	data["RecommendedMaxSizeBytes"] = strconv.FormatInt(recommended.MaxSizeBytes, 10)
	data["RecommendedMaxLineBytes"] = strconv.FormatInt(recommended.MaxLineBytes, 10)
	return data
}
