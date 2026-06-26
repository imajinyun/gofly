package command

import "fmt"

func genCommand(args []string) error {
	if printCommandHelp("gen", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly gen handler|rpc|api|middleware|model|gateway`", errUsage)
	}
	switch args[0] {
	case "handler":
		return handlerCommand(append([]string{"gen"}, args[1:]...))
	case "rpc":
		return rpcGenCommand(args[1:])
	case "api", "rest":
		return apiGenCommand(args[1:])
	case "middleware":
		return apiMiddlewareCommand(args[1:])
	case "model":
		return modelGenCommand(args[1:])
	case "gateway":
		return gatewayGenCommand(args[1:])
	default:
		return fmt.Errorf("%w: expected `gofly gen handler|rpc|api|middleware|model|gateway`", errUsage)
	}
}

func aiCommand(args []string) error {
	if printCommandHelp("ai", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly ai manifest`", errUsage)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "manifest":
		return aiManifestCommand(rest)
	case "plan":
		return aiPlanCommand(rest)
	case "new":
		return aiNewCommand(rest)
	case "complete":
		return aiCompleteCommand(rest)
	case "stream":
		return aiStreamCommand(rest)
	case "doctor":
		return aiDoctorCommand(rest)
	case "control-plane":
		return aiControlPlaneCommand(rest)
	default:
		return fmt.Errorf("%w: expected `gofly ai manifest|control-plane|plan|new|complete|stream|doctor`", errUsage)
	}
}
