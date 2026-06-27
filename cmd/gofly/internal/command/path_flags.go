package command

import "flag"

type outputPathFlags struct {
	Output *string
	Alias  *string
}

type apiFileFlags struct {
	File *string
	API  *string
}

type idlFileFlags struct {
	File *string
	Src  *string
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

func registerAPIFileFlags(fs *flag.FlagSet, usage string) apiFileFlags {
	if usage == "" {
		usage = "api file"
	}
	return apiFileFlags{
		File: fs.String("file", "", usage),
		API:  fs.String("api", "", usage),
	}
}

func registerIDLFileFlags(fs *flag.FlagSet, usage string) idlFileFlags {
	if usage == "" {
		usage = "proto or thrift idl file"
	}
	return idlFileFlags{
		File: fs.String("file", "", usage),
		Src:  fs.String("src", "", usage),
	}
}

func (f outputPathFlags) resolve() string {
	if valueFromStringFlag(f.Output) != "" {
		return valueFromStringFlag(f.Output)
	}
	return valueFromStringFlag(f.Alias)
}

func (f apiFileFlags) resolve(leading string, remaining []string) string {
	if valueFromStringFlag(f.File) != "" {
		return valueFromStringFlag(f.File)
	}
	if valueFromStringFlag(f.API) != "" {
		return valueFromStringFlag(f.API)
	}
	if leading != "" {
		return leading
	}
	return firstRemainingArg(remaining)
}

func (f idlFileFlags) resolve(leading string, remaining []string) string {
	if valueFromStringFlag(f.File) != "" {
		return valueFromStringFlag(f.File)
	}
	if valueFromStringFlag(f.Src) != "" {
		return valueFromStringFlag(f.Src)
	}
	if leading != "" {
		return leading
	}
	return firstRemainingArg(remaining)
}

func firstRemainingArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
