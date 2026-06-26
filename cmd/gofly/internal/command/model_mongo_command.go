package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func modelMongoCommand(args []string) error {
	fs := flag.NewFlagSet("model mongo", flag.ContinueOnError)
	typeName := fs.String("type", "", "mongo model type name")
	t := fs.String("t", "", "mongo model type name")
	dir := fs.String("dir", ".", "output directory")
	d := fs.String("d", "", "output directory")
	pkg := fs.String("package", "model", "generated Go package name")
	cache := fs.Bool("cache", false, "generate cache helpers")
	c := fs.Bool("c", false, "generate cache helpers")
	prefix := fs.String("prefix", "", "model prefix to trim")
	p := fs.String("p", "", "model prefix to trim")
	easy := fs.Bool("easy", false, "use simplified mongo output")
	e := fs.Bool("e", false, "use simplified mongo output")
	style := fs.String("style", "go_zero", "model style")
	registerGoctlModelTemplateFlags(fs)
	_ = easy
	_ = e
	_ = style
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *typeName == "" {
		*typeName = *t
	}
	if *d != "" {
		*dir = *d
	}
	if *prefix == "" {
		*prefix = *p
	}
	if *c {
		*cache = true
	}
	fillNameFromArgs(typeName, remaining)
	return generator.GenerateMongoModel(generator.MongoModelOptions{Type: *typeName, Dir: *dir, Package: *pkg, Prefix: *prefix, Cache: *cache, Style: *style})
}
