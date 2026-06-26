package command

import (
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/rpc"
)

func formatRPCDescriptorCompatibilityText(report rpc.DescriptorCompatibilityReport) string {
	if len(report.Changes) == 0 {
		return "No breaking changes\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Descriptor compatibility: %d breaking, %d warning(s), %d change(s)\n", report.Breaking, report.Warnings, len(report.Changes))
	for _, change := range report.Changes {
		fmt.Fprintf(&b, "[%s] %s %s: %s\n", change.Severity, change.Category, change.Subject, change.Description)
	}
	return b.String()
}
