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

// ServiceScaffoldOptions 是配置驱动的脚手架选项；包含模板扩展、feature、插件等。
// 与 Config 配合使用：ApplyOverlay(name, module, style, templateDir, features) 把 CLI 参数覆盖在配置之上。
type ServiceScaffoldOptions struct {
	Name                 string
	Module               string
	Dir                  string
	Style                string
	Profile              string
	TemplateDir          string
	TemplateRemote       string
	TemplateBranch       string
	StrictTemplateRemote bool
	Features             []string
	Plugins              []string // 可执行插件（或内部插件名），通过 PluginRunner 运行
	FrameworkPath        string
	ExtraFiles           map[string]string // 额外需要写入的文件，key 是相对路径
	SkipAPISpec          bool              // api new 时使用：是否跳过 .api 文件的生成
	Kind                 string            // "api" 或 "rpc"，决定是否额外写入 .api/.proto
}

const (
	ServiceStyleBasic      = "basic"
	ServiceStyleMinimal    = "minimal"
	ServiceStyleProduction = "production"
)

type APINewOptions struct {
	Name          string
	Module        string
	Dir           string
	Style         string
	SkipAPISpec   bool
	FrameworkPath string
}

type RPCNewOptions struct {
	Name          string
	Module        string
	Dir           string
	Profile       string
	FrameworkPath string
}

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

func GenerateAPINew(opts APINewOptions) error {
	if opts.Name == "" {
		return errors.New("name is required")
	}
	if opts.Module == "" {
		return errors.New("module is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", opts.Name)
	}
	style := opts.Style
	if style == "" {
		style = ServiceStyleBasic
	}
	if err := GenerateService(ServiceOptions{
		Name:          opts.Name,
		Module:        opts.Module,
		Dir:           opts.Dir,
		Style:         style,
		FrameworkPath: opts.FrameworkPath,
	}); err != nil {
		return err
	}
	if opts.SkipAPISpec {
		return nil
	}
	return writeRenderedFile(
		filepath.Join(opts.Dir, opts.Name+".api"),
		apiNewTemplate,
		map[string]string{"Name": opts.Name},
	)
}

func GenerateRPCNew(opts RPCNewOptions) error {
	if opts.Name == "" {
		return errors.New("name is required")
	}
	if opts.Module == "" {
		return errors.New("module is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", opts.Name)
	}
	if strings.TrimSpace(opts.Profile) != "" {
		if err := GenerateServiceScaffold(ServiceScaffoldOptions{
			Name:          opts.Name,
			Module:        opts.Module,
			Dir:           opts.Dir,
			Style:         ServiceStyleProduction,
			Profile:       opts.Profile,
			FrameworkPath: opts.FrameworkPath,
			Kind:          "rpc",
		}); err != nil {
			return err
		}
	} else {
		if err := GenerateService(ServiceOptions{
			Name:          opts.Name,
			Module:        opts.Module,
			Dir:           opts.Dir,
			Style:         ServiceStyleProduction,
			FrameworkPath: opts.FrameworkPath,
		}); err != nil {
			return err
		}
	}
	return writeRenderedFile(
		filepath.Join(opts.Dir, opts.Name+".proto"),
		strings.Replace(rpcNewTemplate, "package {{.Name}}.v1;", "package {{.Name}};", 1),
		map[string]string{"Name": lowerName(opts.Name)},
	)
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

// GenerateServiceScaffold 是配置驱动的脚手架入口，按 IR、renderer、filesystem sink 三层编排生成流程。
func GenerateServiceScaffold(opts ServiceScaffoldOptions) error {
	ir, err := buildServiceScaffoldIR(opts)
	if err != nil {
		return err
	}
	if err := cleanupLegacyServiceFilesForProfile(ir.Dir, ir.Profile); err != nil {
		return err
	}

	rendered := serviceScaffoldRenderer{}.Render(ir)
	sink := serviceFilesystemSink{Dir: ir.Dir, Stderr: os.Stderr}
	if err := sink.WriteRendered(rendered); err != nil {
		return err
	}
	if err := sink.RunPlugins(ir); err != nil {
		return err
	}

	return nil
}
