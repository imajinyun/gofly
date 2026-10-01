//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Operator history production bounds. Keeping them as named constants gives the
// production check a single source of truth for the limits also described in
// the README and enforced by the generated config validation.
const (
	maxOperatorHistoryActions   = 1024
	maxOperatorHistoryBackups   = 3
	maxOperatorHistorySizeBytes = 1048576
	maxOperatorHistoryLineBytes = 65536

	minOperatorHistoryDebugReplayCooldown   = 100000000
	maxOperatorHistoryDebugReplayCooldown   = 60000000000
	minOperatorHistoryAuditValidateCooldown = 100000000
	maxOperatorHistoryAuditValidateCooldown = 60000000000
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: go run ./internal/config/production_check.go <config>")
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail("read config: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		fail("decode config json: %v", err)
	}
	history := object(object(object(object(root, "rpc"), "mux"), "log"), "otelCompatible")
	history = object(history, "operatorHistory")
	mustMax(history, "maxActions", maxOperatorHistoryActions)
	mustMax(history, "maxBackups", maxOperatorHistoryBackups)
	mustMax(history, "maxSizeBytes", maxOperatorHistorySizeBytes)
	mustMax(history, "maxLineBytes", maxOperatorHistoryLineBytes)
	mustRange(history, "debugReplayCooldown", minOperatorHistoryDebugReplayCooldown, maxOperatorHistoryDebugReplayCooldown)
	mustRange(history, "auditValidateCooldown", minOperatorHistoryAuditValidateCooldown, maxOperatorHistoryAuditValidateCooldown)
}

func object(in map[string]any, key string) map[string]any {
	next, _ := in[key].(map[string]any)
	if next == nil {
		fail("missing object %s", key)
	}
	return next
}

func mustMax(in map[string]any, key string, max int64) {
	raw, ok := in[key]
	if !ok {
		return
	}
	value, ok := raw.(float64)
	if !ok || value < 0 || value != float64(int64(value)) || int64(value) > max {
		fail("mux operator history %s exceeds %d", key, max)
	}
}

// mustRange enforces both bounds for values that carry a semantic minimum. A
// zero value means "use the built-in default" and is accepted, matching the
// generated config validation for cooldown fields.
func mustRange(in map[string]any, key string, min, max int64) {
	raw, ok := in[key]
	if !ok {
		return
	}
	value, ok := raw.(float64)
	if !ok || value < 0 || value != float64(int64(value)) {
		fail("mux operator history %s is invalid", key)
	}
	n := int64(value)
	if n == 0 {
		return
	}
	if n < min || n > max {
		fail("mux operator history %s must be between %d and %d", key, min, max)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "production-check failed: "+format+"\n", args...)
	os.Exit(1)
}
