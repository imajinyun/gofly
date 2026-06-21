package command

var rpcCommands = newCommandRegistry(
	commandSpec{Name: "idl", Aliases: []string{"inspect"}, Run: rpcIDLCommand},
	commandSpec{Name: "thrift", Aliases: []string{"thrift2proto"}, Run: rpcThriftCommand},
	commandSpec{Name: "client", Run: rpcClientCommand},
	commandSpec{Name: "server", Run: rpcServerCommand},
	commandSpec{Name: "middleware", Run: rpcMiddlewareCommand},
	commandSpec{Name: "lint", Run: rpcLintCommand},
	commandSpec{Name: "deps", Run: rpcDepsCommand},
	commandSpec{Name: "check", Run: rpcCheckCommand},
	commandSpec{Name: "doc", Aliases: []string{"docs", "swagger", "openapi"}, Run: rpcDocCommand},
	commandSpec{Name: "breaking", Aliases: []string{"break"}, Run: rpcBreakingCommand},
	commandSpec{Name: "descriptor", Run: rpcDescriptorCommand},
	commandSpec{Name: "gen", Run: rpcGenCommand},
	commandSpec{Name: "protoc", Run: rpcProtocCommand},
	commandSpec{Name: "new", Run: rpcNewCommand},
	commandSpec{Name: "plugin", Run: rpcPluginCommand},
	commandSpec{Name: "template", Aliases: []string{"tpl"}, Run: rpcTemplateDispatchCommand},
)

var apiCommands = newCommandRegistry(
	commandSpec{Name: "check", Aliases: []string{"validate"}, Run: apiCheckCommand},
	commandSpec{Name: "breaking", Aliases: []string{"break"}, Run: apiBreakingCommand},
	commandSpec{Name: "gen", Aliases: []string{"go"}, Run: apiGenCommand},
	commandSpec{Name: "types", Run: apiTypesCommand},
	commandSpec{Name: "route", Aliases: []string{"routes"}, Run: apiRouteCommand},
	commandSpec{Name: "import", Run: apiImportCommand},
	commandSpec{Name: "diff", Run: apiDiffCommand},
	commandSpec{Name: "plugin", Run: apiPluginCommandRunner},
	commandSpec{Name: "middleware", Run: apiMiddlewareCommand},
	commandSpec{Name: "format", Aliases: []string{"fmt"}, Run: apiFormatCommand},
	commandSpec{Name: "doc", Run: func(args []string) error { return apiDocCommand("doc", args) }},
	commandSpec{Name: "docs", Run: func(args []string) error { return apiDocCommand("docs", args) }},
	commandSpec{Name: "swagger", Run: func(args []string) error { return apiDocCommand("swagger", args) }},
	commandSpec{Name: "client", Run: func(args []string) error { return apiClientCommand("client", args) }},
	commandSpec{Name: "ts", Aliases: []string{"typescript"}, Run: func(args []string) error { return apiClientCommand("ts", args) }},
	commandSpec{Name: "js", Aliases: []string{"javascript"}, Run: func(args []string) error { return apiClientCommand("js", args) }},
	commandSpec{Name: "dart", Run: func(args []string) error { return apiClientCommand("dart", args) }},
	commandSpec{Name: "java", Run: func(args []string) error { return apiClientCommand("java", args) }},
	commandSpec{Name: "kotlin", Aliases: []string{"kt"}, Run: func(args []string) error { return apiClientCommand("kotlin", args) }},
	commandSpec{Name: "new", Run: apiNewCommand},
)

var modelCommands = newCommandRegistry(
	commandSpec{Name: "gen", Run: modelGenCommand},
	commandSpec{Name: "mysql", Run: modelMySQLCommand},
	commandSpec{Name: "pg", Aliases: []string{"postgres", "postgresql"}, Run: modelPostgresCommand},
	commandSpec{Name: "mongo", Run: modelMongoCommand},
)

func rpcTemplateDispatchCommand(args []string) error {
	if len(args) > 0 && isTemplateSubcommand(args[0]) {
		return templateCommand(args)
	}
	return rpcTemplateCommand(args)
}
