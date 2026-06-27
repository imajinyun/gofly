package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func dockerCommand(args []string) error {
	if printCommandHelp("docker", args) {
		return nil
	}
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("docker", flag.ContinueOnError)
	name := fs.String("name", "", "service name")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output Dockerfile path")
	goFile := fs.String("go", "", "main package or Go file to build")
	exe := fs.String("exe", "", "binary name")
	goVersion := fs.String("go-version", "1.26", "golang builder image version")
	version := fs.String("version", "", "golang builder image version")
	baseImage := fs.String("base", "gcr.io/distroless/static-debian12", "runtime base image")
	port := fs.String("port", "", "HTTP port metadata")
	tz := fs.String("tz", "", "container timezone metadata")
	templateSource := registerTemplateSourceFlags(fs, "", "", "")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	if *version != "" {
		*goVersion = *version
	}
	fillNameFromArgs(name, remaining)
	return generator.GenerateDockerfile(generator.DockerOptions{
		Name:        *name,
		Dir:         *dir,
		Output:      output.resolve(),
		GoFile:      *goFile,
		Exe:         *exe,
		GoVersion:   *goVersion,
		BaseImage:   *baseImage,
		Port:        *port,
		Timezone:    *tz,
		TemplateDir: *templateSource.Home,
		Remote:      *templateSource.Remote,
		Branch:      *templateSource.Branch,
	})
}
