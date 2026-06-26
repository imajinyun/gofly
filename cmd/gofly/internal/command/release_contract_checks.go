package command

import (
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func releaseAPIBreakingCheck(base, target string) (releaseCheckItem, []string, []string) {
	apiReport, err := generator.DetectAPIChanges(generator.APIBreakingOptions{Base: base, Target: target})
	item := releaseCheckItem{Name: "api-breaking", Status: "pass"}
	var blockers, warnings []string
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		blockers = append(blockers, "api breaking check error: "+err.Error())
	} else if apiReport.HasBreaking() {
		item.Status = "fail"
		item.Detail = fmt.Sprintf("%d breaking change(s) detected", apiReport.Breaking)
		item.Blocker = true
		blockers = append(blockers, fmt.Sprintf("API breaking: %d change(s)", apiReport.Breaking))
	} else if !apiReport.IsEmpty() {
		item.Status = "pass"
		item.Detail = fmt.Sprintf("%d warning(s), no breaking", apiReport.Warnings)
		if apiReport.Warnings > 0 {
			warnings = append(warnings, fmt.Sprintf("API warnings: %d", apiReport.Warnings))
		}
	} else {
		item.Detail = "no changes"
	}
	return item, blockers, warnings
}

func releaseRPCBreakingCheck(base, target string) (releaseCheckItem, []string, []string) {
	rpcReport, err := generator.DetectProtoDescriptorChanges(generator.ProtoBreakingOptions{Base: base, Target: target})
	item := releaseCheckItem{Name: "rpc-breaking", Status: "pass"}
	var blockers, warnings []string
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		blockers = append(blockers, "rpc breaking check error: "+err.Error())
	} else if rpcReport.HasBreaking() {
		item.Status = "fail"
		item.Detail = fmt.Sprintf("%d breaking change(s) detected", rpcReport.Breaking)
		item.Blocker = true
		blockers = append(blockers, fmt.Sprintf("RPC breaking: %d change(s)", rpcReport.Breaking))
	} else if len(rpcReport.Changes) > 0 {
		item.Status = "pass"
		item.Detail = fmt.Sprintf("%d warning(s), no breaking", rpcReport.Warnings)
		if rpcReport.Warnings > 0 {
			warnings = append(warnings, fmt.Sprintf("RPC warnings: %d", rpcReport.Warnings))
		}
	} else {
		item.Detail = "no changes"
	}
	return item, blockers, warnings
}
