package release

func printReleaseCheckText(hooks Hooks, report releaseCheckReport) {
	hooks.PrintTextf("gofly release check — %s\n\n", report.Summary)
	for _, c := range report.Checks {
		mark := "✓"
		if c.Status == "fail" {
			mark = "✗"
		} else if c.Status == "skip" {
			mark = "-"
		}
		hooks.PrintTextf("  %s %-20s %s", mark, c.Name, c.Status)
		if c.Detail != "" {
			hooks.PrintTextf(" — %s", c.Detail)
		}
		if c.Blocker {
			hooks.PrintText(" [BLOCKER]")
		}
		hooks.PrintTextln()
	}
	if len(report.Blocking) > 0 {
		hooks.PrintTextln("\nBlocking:")
		for _, b := range report.Blocking {
			hooks.PrintTextf("  • %s\n", b)
		}
	}
}
