package main

import (
	"context"
	"testing"
)

func TestCacheLocalReport(t *testing.T) {
	report, err := buildReport(context.Background())
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if report.Schema != "gofly.cache_local.v1" {
		t.Fatalf("schema = %q, want gofly.cache_local.v1", report.Schema)
	}
	for _, capability := range []string{
		"typed-local-cache",
		"loader-fill",
		"negative-cache",
		"bloom-protection",
		"tiered-l1-l2-cache",
		"cache-disabled-mode",
		"stats-and-prometheus",
	} {
		if !contains(report.Capabilities, capability) {
			t.Fatalf("capabilities = %#v, missing %s", report.Capabilities, capability)
		}
	}
	if report.Local.FirstLoad != "profile:u:42" || report.Local.SecondLoad != "profile:u:42" {
		t.Fatalf("local loads = %q/%q, want cached profile", report.Local.FirstLoad, report.Local.SecondLoad)
	}
	if report.Local.LoaderCalls != 1 || report.Local.Stats.Loads != 1 || report.Local.Stats.Hits != 1 || !report.Local.PrometheusOK {
		t.Fatalf("local summary = %#v, want one load, one hit and prometheus output", report.Local)
	}
	if report.Negative.Error != "cache entry not found" || report.Negative.LoaderCalls != 1 || report.Negative.Stats.Negatives != 1 {
		t.Fatalf("negative summary = %#v, want cached not-found result", report.Negative)
	}
	if report.Bloom.GhostError != "cache entry not found" || report.Bloom.AllowedValue != "value:allowed" || report.Bloom.LoaderCalls != 1 || report.Bloom.Stats.BloomRejects != 1 {
		t.Fatalf("bloom summary = %#v, want ghost rejected and allowed key loaded", report.Bloom)
	}
	if report.Tiered.FirstLoad.Name != "Ada" || report.Tiered.SecondLoad.Name != "Ada" || report.Tiered.AfterL1Clear.Name != "Ada" {
		t.Fatalf("tiered profile values = %#v, want Ada from each tier path", report.Tiered)
	}
	if report.Tiered.LoaderCalls != 1 || !report.Tiered.NamespacedRemote || report.Tiered.L2Stats.Sets != 1 || report.Tiered.L2Stats.Hits != 1 {
		t.Fatalf("tiered summary = %#v, want one loader call and L2 backfill hit", report.Tiered)
	}
	if report.Disabled.LoaderCalls != 2 || report.Disabled.Stats.Disabled != true || report.Disabled.Stats.Entries != 0 {
		t.Fatalf("disabled summary = %#v, want pass-through cache", report.Disabled)
	}
	if len(report.Disabled.Values) != 2 || report.Disabled.Values[0] == report.Disabled.Values[1] {
		t.Fatalf("disabled values = %#v, want two loader-produced values", report.Disabled.Values)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
