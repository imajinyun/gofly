package command

import (
	"fmt"
	"os"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// runPostPlugins runs one or more comma-separated post-generation plugins.
func runPostPlugins(pluginArg string, req generator.PluginRequest) error {
	pluginArg = strings.TrimSpace(pluginArg)
	if pluginArg == "" {
		return nil
	}
	req = enrichPluginRequestIDL(req)
	runner := generator.NewPluginRunner()
	for _, p := range splitCSV(pluginArg) {
		if strings.TrimSpace(p) == "" {
			continue
		}
		resp, err := runner.Run(p, req)
		if err != nil {
			return fmt.Errorf("run plugin %s: %w", p, err)
		}
		if resp.Message != "" {
			errorf("[gofly] plugin %s: %s\n", p, resp.Message)
		}
		if _, err := resp.WriteFiles(req.Dir); err != nil {
			return fmt.Errorf("write plugin %s files: %w", p, err)
		}
		if err := resp.ApplyPatches(req.Dir); err != nil {
			return fmt.Errorf("apply plugin %s patches: %w", p, err)
		}
	}
	return nil
}

func enrichPluginRequestIDL(req generator.PluginRequest) generator.PluginRequest {
	if len(req.IDL) > 0 || req.Input == nil {
		return req
	}
	for key, format := range map[string]string{"api": "api", "proto": "proto", "thrift": "thrift", "openapi": "openapi"} {
		path := strings.TrimSpace(req.Input[key])
		if path == "" {
			continue
		}
		// #nosec G304 -- post-plugin IDL enrichment reads explicit generator input files already supplied by the CLI.
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		req.IDL = data
		req.IDLFormat = format
		return req
	}
	return req
}

// looksLikeShellScript detects plain CLI plugins by extension or shebang.
func looksLikeShellScript(path string) bool {
	if path == "" {
		return false
	}
	if strings.HasSuffix(strings.ToLower(path), ".sh") {
		return true
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return false
	}
	// #nosec G304 -- plugin compatibility probing reads the explicit plugin path supplied by the operator.
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 2)
	if n, _ := f.Read(buf); n < 2 {
		return false
	}
	return buf[0] == '#' && buf[1] == '!'
}
