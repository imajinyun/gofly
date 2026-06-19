package generator

import (
	"fmt"
	"go/format"
	"io"
	"path/filepath"
)

type serviceFilesystemSink struct {
	Dir    string
	Stderr io.Writer
}

func (s serviceFilesystemSink) WriteRendered(files []scaffoldRenderedFile) error {
	for _, file := range files {
		content := []byte(file.Content)
		if filepath.Ext(file.Path) == ".go" {
			formatted, err := format.Source(content)
			if err != nil {
				return fmt.Errorf("format %s: %w", file.Path, err)
			}
			content = formatted
		}
		if err := writeGeneratedFileUnder(s.Dir, file.Path, content); err != nil {
			return err
		}
	}
	return nil
}

func (s serviceFilesystemSink) RunPlugins(ir serviceScaffoldIR) error {
	if len(ir.Plugins) == 0 {
		return nil
	}
	runner := NewPluginRunner()
	for _, plugin := range ir.Plugins {
		req := PluginRequest{
			Command: "service",
			Service: ir.Name,
			Module:  ir.Module,
			Style:   ir.Style,
			Dir:     ir.Dir,
			Input: map[string]string{
				"kind": ir.Kind,
			},
		}
		resp, err := runner.Run(plugin, req)
		if err != nil {
			return fmt.Errorf("run plugin %s: %w", plugin, err)
		}
		if resp.Message != "" && s.Stderr != nil {
			fmt.Fprintf(s.Stderr, "[gofly] plugin %s: %s\n", plugin, resp.Message)
		}
		if _, err := resp.WriteFiles(s.Dir); err != nil {
			return fmt.Errorf("write plugin %s files: %w", plugin, err)
		}
		if err := resp.ApplyPatches(s.Dir); err != nil {
			return fmt.Errorf("apply plugin %s patches: %w", plugin, err)
		}
	}
	return nil
}
