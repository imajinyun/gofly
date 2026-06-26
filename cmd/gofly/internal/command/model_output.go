package command

import (
	"path/filepath"
	"sort"
)

func printModelGenerated(dir string) {
	modelDir := filepath.Join(dir, "model")
	displayDir := modelDir
	if absDir, err := filepath.Abs(modelDir); err == nil {
		displayDir = absDir
	}
	cliOutputf("model generated: %s\n", displayDir)
	files := generatedModelFiles(modelDir)
	if len(files) == 0 {
		return
	}
	cliOutputln("model files:")
	for _, file := range files {
		displayFile := file
		if absFile, err := filepath.Abs(file); err == nil {
			displayFile = absFile
		}
		cliOutputf("  %s\n", displayFile)
	}
}

func generatedModelFiles(modelDir string) []string {
	patterns := []string{
		filepath.Join(modelDir, "entity", "*.go"),
		filepath.Join(modelDir, "repo", "*.go"),
	}
	files := make([]string, 0)
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		files = append(files, matches...)
	}
	sort.Strings(files)
	return files
}
