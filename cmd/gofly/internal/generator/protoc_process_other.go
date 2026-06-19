//go:build !unix

package generator

import "os/exec"

func configureProtocCommand(cmd *exec.Cmd) {}
