package command

func printReleaseCheckText(report releaseCheckReport) {
	cliOutputf("gofly release check — %s\n\n", report.Summary)
	for _, c := range report.Checks {
		mark := "✓"
		if c.Status == "fail" {
			mark = "✗"
		} else if c.Status == "skip" {
			mark = "-"
		}
		cliOutputf("  %s %-20s %s", mark, c.Name, c.Status)
		if c.Detail != "" {
			cliOutputf(" — %s", c.Detail)
		}
		if c.Blocker {
			cliOutput(" [BLOCKER]")
		}
		cliOutputln()
	}
	if len(report.Blocking) > 0 {
		cliOutputln("\nBlocking:")
		for _, b := range report.Blocking {
			cliOutputf("  • %s\n", b)
		}
	}
}
