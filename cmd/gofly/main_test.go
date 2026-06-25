package main

import (
	"os"
	"testing"
)

func TestMainVersionCommandReturnsWithoutExit(t *testing.T) {
	t.Setenv("GOFLY_PLUGIN_MODE", "")
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"gofly", "version"}

	main()
}
