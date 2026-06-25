package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestMainVersionCommandReturnsWithoutExit(t *testing.T) {
	t.Setenv("GOFLY_PLUGIN_MODE", "")
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"gofly", "version"}

	main()
}

func TestRunMainSTDIOExitContract(t *testing.T) {
	t.Setenv("GOFLY_PLUGIN_MODE", "")

	t.Run("text usage error writes stderr and exits 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"example", "run", "nonexistent"}, strings.NewReader(""), &stdout, &stderr)
		if code != 2 {
			t.Fatalf("runMain text usage exit = %d, want 2", code)
		}
		if stdout.Len() != 0 {
			t.Fatalf("runMain text usage stdout = %q, want empty", stdout.String())
		}
		if !strings.Contains(stderr.String(), "unknown example") {
			t.Fatalf("runMain text usage stderr = %q, want actionable error", stderr.String())
		}
	})

	t.Run("global JSON usage error writes only stdout envelope", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"--output", "json", "example", "nonexistent"}, strings.NewReader(""), &stdout, &stderr)
		if code != 2 {
			t.Fatalf("runMain JSON usage exit = %d, want 2", code)
		}
		if stderr.Len() != 0 {
			t.Fatalf("runMain JSON usage stderr = %q, want empty", stderr.String())
		}
		if strings.Count(stdout.String(), `"ok"`) != 1 {
			t.Fatalf("runMain JSON usage stdout emitted duplicate envelopes:\n%s", stdout.String())
		}
		var envelope struct {
			OK    bool `json:"ok"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("runMain JSON usage stdout is not valid envelope: %v\n%s", err, stdout.String())
		}
		if envelope.OK || envelope.Error.Code != "USAGE_ERROR" || envelope.Error.Message == "" {
			t.Fatalf("runMain JSON usage envelope = %+v", envelope)
		}
	})
}
