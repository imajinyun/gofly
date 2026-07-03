package command

import (
	"flag"

	commanddoctor "github.com/imajinyun/gofly/cmd/gofly/internal/command/doctor"
)

type doctorCheck = commanddoctor.Check
type doctorReport = commanddoctor.Report

func doctorCommand(args []string) error {
	return commanddoctor.Command(args, doctorHooks())
}

func doctorHooks() commanddoctor.Hooks {
	return commanddoctor.Hooks{
		PrintHelp:     printCommandHelp,
		ParseFlags:    parseInterspersedFlags,
		PrintJSON:     printJSON,
		PrintTextf:    cliOutputf,
		PrintTextln:   cliOutputln,
		Version:       Version,
		AppendMissing: appendMissingStrings,
	}
}

func runDoctor() doctorReport {
	return commanddoctor.Run(Version, appendMissingStrings)
}

func printDoctorReport(report doctorReport) {
	commanddoctor.PrintReport(report, cliOutputf, cliOutputln)
}

func doctorNextActions(checks []doctorCheck, fails int, warns int) []string {
	return commanddoctor.NextActions(checks, fails, warns, appendMissingStrings)
}

func checkGoVersion() doctorCheck {
	return commanddoctor.CheckGoVersion()
}

func checkGoModule() doctorCheck {
	return commanddoctor.CheckGoModule()
}

func checkGOPATH() doctorCheck {
	return commanddoctor.CheckGOPATH()
}

func checkTools() doctorCheck {
	return commanddoctor.CheckTools()
}

func checkGit() doctorCheck {
	return commanddoctor.CheckGit()
}

func checkProtoc() doctorCheck {
	return commanddoctor.CheckProtoc()
}

func checkWritePermission() doctorCheck {
	return commanddoctor.CheckWritePermission()
}

func parseDoctorFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	return parseInterspersedFlags(fs, args)
}
