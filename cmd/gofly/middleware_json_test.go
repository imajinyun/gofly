package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunMainMiddlewareJSONErrors(t *testing.T) {
	t.Setenv("GOFLY_PLUGIN_MODE", "")
	for _, test := range []struct {
		name string
		args []string
		code int
	}{
		{"unknown preset", []string{"--preset", "unknown", "--json"}, 1},
		{"missing dependency", []string{"--preset", "auth", "--dir", t.TempDir(), "--json"}, 1},
		{"mixed modes", []string{"Custom", "--preset", "auth", "--json"}, 2},
		{"flag before json", []string{"--invalid", "--json"}, 2},
		{"flag after json", []string{"--json", "--invalid"}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := runMain(append([]string{"api", "middleware"}, test.args...), strings.NewReader(""), &out, &errOut)
			if code != test.code || errOut.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q stdout=%s", code, errOut.String(), &out)
			}
			var envelope struct {
				OK      bool   `json:"ok"`
				Command string `json:"command"`
				Error   struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatalf("invalid JSON: %v: %s", err, &out)
			}
			if envelope.OK || envelope.Command != "api.middleware" || envelope.Error.Message == "" {
				t.Fatalf("envelope=%+v", envelope)
			}
		})
	}
	for _, args := range [][]string{{"--json", "--json=false", "--preset", "unknown"}, {"--name", "--json", "--preset", "unknown"}} {
		var out, errOut bytes.Buffer
		if code := runMain(append([]string{"api", "middleware"}, args...), strings.NewReader(""), &out, &errOut); code == 0 || out.Len() != 0 || errOut.Len() == 0 {
			t.Fatalf("args=%v stdout=%s stderr=%s exit=%d", args, &out, &errOut, code)
		}
	}
}
