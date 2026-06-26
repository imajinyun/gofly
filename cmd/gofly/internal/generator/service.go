package generator

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	data := map[string]string{
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
