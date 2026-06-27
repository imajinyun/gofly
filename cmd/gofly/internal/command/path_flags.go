package command

import "flag"

type outputPathFlags struct {
	Output *string
	Alias  *string
}

func registerOutputPathFlags(fs *flag.FlagSet, usage string) outputPathFlags {
	if usage == "" {
		usage = "output file"
	}
	return outputPathFlags{
		Output: fs.String("output", "", usage),
		Alias:  fs.String("o", "", usage),
	}
}

func (f outputPathFlags) resolve() string {
	if valueFromStringFlag(f.Output) != "" {
		return valueFromStringFlag(f.Output)
	}
	return valueFromStringFlag(f.Alias)
}
