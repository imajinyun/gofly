package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

type modelMongoFlags struct {
	Type        *string
	TypeAlias   *string
	Dir         *string
	DirAlias    *string
	Package     *string
	Cache       *bool
	CacheAlias  *bool
	Prefix      *string
	PrefixAlias *string
	Easy        *bool
	EasyAlias   *bool
	Style       *string
}

func registerModelMongoFlags(fs *flag.FlagSet) modelMongoFlags {
	return modelMongoFlags{
		Type:        fs.String("type", "", "mongo model type name"),
		TypeAlias:   fs.String("t", "", "mongo model type name"),
		Dir:         fs.String("dir", ".", "output directory"),
		DirAlias:    fs.String("d", "", "output directory"),
		Package:     fs.String("package", "model", "generated Go package name"),
		Cache:       fs.Bool("cache", false, "generate cache helpers"),
		CacheAlias:  fs.Bool("c", false, "generate cache helpers"),
		Prefix:      fs.String("prefix", "", "model prefix to trim"),
		PrefixAlias: fs.String("p", "", "model prefix to trim"),
		Easy:        fs.Bool("easy", false, "use simplified mongo output"),
		EasyAlias:   fs.Bool("e", false, "use simplified mongo output"),
		Style:       fs.String("style", "go_zero", "model style"),
	}
}

func (flags modelMongoFlags) normalize() {
	if valueFromStringFlag(flags.Type) == "" {
		setStringFlag(flags.Type, valueFromStringFlag(flags.TypeAlias))
	}
	if valueFromStringFlag(flags.DirAlias) != "" {
		setStringFlag(flags.Dir, valueFromStringFlag(flags.DirAlias))
	}
	if valueFromStringFlag(flags.Prefix) == "" {
		setStringFlag(flags.Prefix, valueFromStringFlag(flags.PrefixAlias))
	}
	if valueFromBoolFlag(flags.CacheAlias) {
		setBoolFlag(flags.Cache, true)
	}
}

func modelMongoCommand(args []string) error {
	fs := flag.NewFlagSet("model mongo", flag.ContinueOnError)
	flags := registerModelMongoFlags(fs)
	registerGoctlModelTemplateFlags(fs)
	_ = flags.Easy
	_ = flags.EasyAlias
	_ = flags.Style
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	flags.normalize()
	fillNameFromArgs(flags.Type, remaining)
	return generator.GenerateMongoModel(generator.MongoModelOptions{
		Type:    *flags.Type,
		Dir:     *flags.Dir,
		Package: *flags.Package,
		Prefix:  *flags.Prefix,
		Cache:   *flags.Cache,
		Style:   *flags.Style,
	})
}
