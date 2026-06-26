package command

func rpcCommandHelp(command string) (commandHelp, bool) {
	switch command {
	case "rpc gen":
		return commandHelp{Name: "rpc gen", Short: "Generate gofly/gRPC service code from a protobuf file.", Usage: "gofly rpc gen --src <service.proto> --out <dir> [flags]", Flags: []string{"--src, --file <file>           protobuf source file", "--out, --dir <dir>             output directory", "--package <pkg>                generated package name", "--profile <profile>            generation profile: gofly-ai|gozero-compatible|kitex-compatible", "--transport grpc|gofly|both    transport targets", "--with-middleware              generate middleware/interceptor chain helpers", "--with-recovery                generate recovery option helpers", "--with-validator               generate validator and biz error helpers", "--standard                     also run standard protoc plugins", "--timeout <duration>           maximum protoc execution time with --standard", "--style go_zero                scaffold style option", "--home, --remote, --branch     template source options", "--json                         emit generation result as JSON"}, Examples: []string{"gofly rpc gen -src greeter.proto -out . -style go_zero", "gofly rpc gen greeter.proto --out rpc --transport gofly --profile kitex-compatible --json"}}, true
	case "rpc idl":
		return commandHelp{Name: "rpc idl", Short: "Inspect proto or thrift IDL metadata.", Usage: "gofly rpc idl --file <service.proto|service.thrift> [--format text|json]", Flags: []string{"--file, --src <file>       proto or thrift IDL file", "--format text|json        output format"}, Examples: []string{"gofly rpc idl greeter.proto --format json", "gofly rpc idl --file greeter.thrift"}}, true
	case "rpc thrift":
		return commandHelp{Name: "rpc thrift", Short: "Convert a thrift IDL to a proto compatibility skeleton.", Usage: "gofly rpc thrift --file <service.thrift> --out <dir>", Flags: []string{"--file, --src <file>       thrift IDL file", "--out, --dir <dir>         output directory"}, Examples: []string{"gofly rpc thrift greeter.thrift --out proto"}}, true
	case "rpc client":
		return commandHelp{Name: "rpc client", Short: "Generate gofly RPC client wrapper code from proto or thrift IDL.", Usage: "gofly rpc client --file <service.proto|service.thrift> --out <dir> [--package <pkg>]", Flags: []string{"--file, --src <file>       proto or thrift IDL file", "--out, --dir <dir>         output directory", "--package <pkg>            generated package name"}, Examples: []string{"gofly rpc client greeter.proto --out internal/rpcclient --package rpcclient"}}, true
	case "rpc server":
		return commandHelp{Name: "rpc server", Short: "Generate gofly RPC server implementation stubs from proto or thrift IDL.", Usage: "gofly rpc server --file <service.proto|service.thrift> --out <dir> [--package <pkg>]", Flags: []string{"--file, --src <file>       proto or thrift IDL file", "--out, --dir <dir>         output directory", "--package <pkg>            generated package name"}, Examples: []string{"gofly rpc server greeter.proto --out internal/rpcimpl --package rpcimpl"}}, true
	case "rpc middleware":
		return commandHelp{Name: "rpc middleware", Short: "Generate a gRPC unary middleware skeleton.", Usage: "gofly rpc middleware <name> --dir <service-dir>", Flags: []string{"--name <name>       middleware name", "--dir <dir>         service root directory"}, Examples: []string{"gofly rpc middleware auth --dir ."}}, true
	case "rpc lint":
		return commandHelp{Name: "rpc lint", Short: "Lint proto or thrift IDL for service/method contract completeness.", Usage: "gofly rpc lint --file <service.proto|service.thrift>", Flags: []string{"--file, --src <file>       proto or thrift IDL file"}, Examples: []string{"gofly rpc lint greeter.proto"}}, true
	case "rpc deps":
		return commandHelp{Name: "rpc deps", Short: "List proto imports or thrift includes.", Usage: "gofly rpc deps --file <service.proto|service.thrift> [--format text|json]", Flags: []string{"--file, --src <file>       proto or thrift IDL file", "--format text|json        output format"}, Examples: []string{"gofly rpc deps greeter.proto", "gofly rpc deps greeter.thrift --format json"}}, true
	case "gen rpc":
		return commandHelp{Name: "gen rpc", Short: "Generate gofly/gRPC service code from a protobuf file.", Usage: "gofly gen rpc --src <service.proto> --out <dir> [flags]", Flags: []string{"--src, --file <file>           protobuf source file", "--out, --dir <dir>             output directory", "--package <pkg>                generated package name", "--profile <profile>            generation profile: gofly-ai|gozero-compatible|kitex-compatible", "--transport grpc|gofly|both    transport targets", "--with-middleware              generate middleware/interceptor chain helpers", "--with-recovery                generate recovery option helpers", "--with-validator               generate validator and biz error helpers", "--standard                     also run standard protoc plugins", "--timeout <duration>           maximum protoc execution time with --standard", "--style go_zero                scaffold style option", "--home, --remote, --branch     template source options", "--json                         emit generation result as JSON"}, Examples: []string{"gofly gen rpc -src greeter.proto -out . -style go_zero", "gofly gen rpc greeter.proto --out rpc --transport gofly --profile kitex-compatible --json"}}, true
	case "rpc protoc":
		return commandHelp{Name: "rpc protoc", Short: "Run standard protoc Go plugins.", Usage: "gofly rpc protoc <service.proto> [--I <paths>] [--go_out <dir>] [--go-grpc_out <dir>]", Flags: []string{"--src, --file <file>    protobuf source file", "--dir <dir>             output directory", "--I, --proto_path       include paths", "--go_out <dir>          protoc-gen-go output", "--go-grpc_out <dir>     protoc-gen-go-grpc output", "--zrpc_out <dir>        service output directory", "--extra <args>          extra protoc arguments", "--timeout <duration>    maximum protoc execution time"}, Examples: []string{"gofly rpc protoc greeter.proto -I . --go_out . --go-grpc_out ."}}, true
	case "rpc check":
		return commandHelp{Name: "rpc check", Short: "Validate protobuf syntax and generator support.", Usage: "gofly rpc check --src <service.proto>", Flags: []string{"--src, --file <file>  protobuf source file"}, Examples: []string{"gofly rpc check -src greeter.proto"}}, true
	case "rpc breaking":
		return commandHelp{Name: "rpc breaking", Short: "Detect breaking changes between two protobuf files.", Usage: "gofly rpc breaking --base <old.proto> --target <new.proto>", Flags: []string{"--base <file>      old protobuf file", "--target <file>    new protobuf file"}, Examples: []string{"gofly rpc breaking old.proto new.proto"}}, true
	case "rpc descriptor":
		return commandHelp{Name: "rpc descriptor", Short: "Compare runtime RPC descriptors for compatibility.", Usage: "gofly rpc descriptor --base <old-descriptor.json|url> --target <new-descriptor.json|url> [--format text|json]", Flags: []string{"--base <file|url>       old rpc.Descriptor json, descriptor URL, or admin base URL", "--target <file|url>     new rpc.Descriptor json, descriptor URL, or admin base URL", "--url <admin-url>       alias source for a remote admin descriptor endpoint", "--service <name>        service name when URL points at /admin or /rpc/admin/descriptors", "--token <token>         bearer token for descriptor URL sources", "--format text|json      output format"}, Examples: []string{"gofly rpc descriptor old.json new.json", "gofly rpc descriptor --url http://127.0.0.1:9090/admin --service greeter --target next.json", "gofly rpc descriptor --base http://127.0.0.1:8081/rpc/admin/descriptors/greeter --target next.json --token secret", "gofly rpc descriptor --base old.json --target new.json --format json"}}, true
	case "rpc plugin":
		return commandHelp{Name: "rpc plugin", Short: "Run a gofly RPC plugin.", Usage: "gofly rpc plugin <plugin> --file <service.proto> [--dir <dir>]", Flags: []string{"--file, --src <file>    protobuf source file", "--plugin <plugin>      plugin executable", "--dir <dir>            working/output directory"}, Examples: []string{"gofly rpc plugin ./my-plugin --file greeter.proto --dir ."}}, true
	case "rpc template":
		return commandHelp{Name: "rpc template", Short: "Generate starter proto templates or manage local/remote templates.", Usage: "gofly rpc template [-o <file>] [--name <name>] [--home <dir>] [--remote <repo|dir>] [--branch <branch>]", Flags: []string{"-o, --output <file>  write starter .proto template", "--name <name>       service name used by template", "--home <dir>        template directory for starter template", "--remote <repo|dir> remote git repository or local template directory", "--branch <branch>   remote git branch", "init|list|clean|update|revert manage template directory"}, Examples: []string{"gofly rpc template -o greeter.proto --name Greeter", "gofly rpc template -o greeter.proto --remote ./company-templates", "gofly rpc template init --home .gofly/templates"}}, true
	case "rpc new", "new rpc":
		return commandHelp{Name: command, Short: "Create an RPC service scaffold.", Usage: "gofly " + command + " <name> --module <module> [flags]", Flags: []string{"--name <name>                  RPC service name", "--module <module>              Go module path", "--dir <dir>                    output directory", "--style minimal|basic|production", "--profile <profile>            generation profile: gofly-ai|gozero-compatible|kitex-compatible", "--home, --remote, --branch     template source options", "--client, --go_opt, --go-grpc_opt accepted scaffold options", "--json                         emit scaffold result as JSON"}, Examples: []string{"gofly new rpc greeter --module example.com/greeter --style go_zero", "gofly rpc new greeter --module example.com/greeter --dir greeter --profile kitex-compatible --json"}}, true
	case "rpc":
		return commandHelp{
			Name:  "rpc",
			Short: "Generate, check and scaffold RPC services from proto or thrift IDL files.",
			Usage: "gofly rpc <command> [arguments]",
			Commands: []helpCommand{
				{Name: "new", Short: "create an RPC service scaffold"},
				{Name: "idl", Short: "inspect proto/thrift IDL metadata"},
				{Name: "thrift", Short: "convert thrift IDL to proto skeleton"},
				{Name: "client", Short: "generate RPC client wrapper"},
				{Name: "server", Short: "generate RPC server stubs"},
				{Name: "middleware", Short: "generate gRPC middleware skeleton"},
				{Name: "lint", Short: "lint IDL contract completeness"},
				{Name: "deps", Short: "list IDL imports/includes"},
				{Name: "gen", Short: "generate gofly/grpc code from proto"},
				{Name: "protoc", Short: "run standard protoc Go plugins"},
				{Name: "check", Short: "validate proto and generator support"},
				{Name: "breaking", Short: "compare proto/API compatibility"},
				{Name: "descriptor", Short: "compare runtime descriptor compatibility"},
				{Name: "plugin", Short: "run RPC plugin"},
				{Name: "template", Short: "generate proto template or manage templates"},
			},
			Flags: []string{"-h, --help  show help for rpc", "-o <file>   generate a starter .proto template"},
			Examples: []string{
				"gofly rpc gen -src greeter.proto -out . -style go_zero",
				"gofly rpc protoc greeter.proto -zrpc_out . -go_opt paths=source_relative",
				"gofly rpc -o greeter.proto",
			},
		}, true
	default:
		return commandHelp{}, false
	}
}
