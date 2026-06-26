package command

func printAIDoctorReport(r aiDoctorReport) {
	cliOutputf("gofly ai doctor %s\n", r.Version)
	cliOutputln()

	cliOutputf("Providers:\n")
	for _, p := range r.Providers {
		printAIDoctorItem(p, "  ")
	}

	cliOutputf("\nEnvironment:\n")
	for _, e := range r.EnvVars {
		printAIDoctorItem(e, "  ")
	}

	cliOutputf("\nSecrets:\n")
	for _, s := range r.Secrets {
		printAIDoctorItem(s, "  ")
	}

	cliOutputf("\nFailover:\n")
	printAIDoctorItem(r.Failover, "  ")

	cliOutputf("\nConfig:\n")
	printAIDoctorItem(r.Config, "  ")

	cliOutputf("\nCache:\n")
	printAIDoctorItem(r.Cache, "  ")

	cliOutputf("\nTelemetry:\n")
	printAIDoctorItem(r.Telemetry, "  ")

	cliOutputf("\nCost:\n")
	printAIDoctorItem(r.Cost, "  ")

	cliOutputf("\n%s\n", r.Summary)
}

func printAIDoctorItem(item aiDoctorItem, indent string) {
	switch item.Status {
	case "ok":
		cliOutputf("%s\033[92m[OK]\033[0m   %s", indent, item.Name)
	case "warn":
		cliOutputf("%s\033[93m[WARN]\033[0m %s: %s", indent, item.Name, item.Message)
	case "fail":
		cliOutputf("%s\033[91m[FAIL]\033[0m %s: %s", indent, item.Name, item.Message)
	default:
		cliOutputf("%s\033[90m[INFO]\033[0m %s", indent, item.Name)
	}
	if item.Message != "" && (item.Status == "ok" || item.Status == "info") {
		cliOutputf(": %s", item.Message)
	}
	cliOutputln()
	for _, next := range item.NextActions {
		cliOutputf("%s       next: %s\n", indent, next)
	}
}
