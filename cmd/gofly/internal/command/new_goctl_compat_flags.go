package command

import "flag"

type newGoctlCompatFlags struct {
	Idea             *bool
	Client           *bool
	ClientAlias      *bool
	NameFromFilename *bool
	GoOpt            *string
	GoGRPCOpt        *string
	GoGRPCOptAlias   *string
}

func registerNewAPICompatFlags(fs *flag.FlagSet) newGoctlCompatFlags {
	return newGoctlCompatFlags{
		Idea:             fs.Bool("idea", false, "open generated project in IDE"),
		Client:           fs.Bool("client", true, "generate client code"),
		ClientAlias:      fs.Bool("c", true, "generate client code"),
		NameFromFilename: fs.Bool("name-from-filename", false, "derive service name from filename"),
		GoOpt:            fs.String("go_opt", "", "extra protoc-gen-go option"),
	}
}

func registerNewRPCCompatFlags(fs *flag.FlagSet) newGoctlCompatFlags {
	flags := registerNewAPICompatFlags(fs)
	flags.GoGRPCOpt = fs.String("go-grpc_opt", "", "extra protoc-gen-go-grpc option")
	flags.GoGRPCOptAlias = fs.String("go_grpc_opt", "", "extra protoc-gen-go-grpc option")
	return flags
}
