package doctor

import (
	"go/build"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func CheckGoVersion() Check {
	v := runtime.Version()
	if strings.HasPrefix(v, "go1.24") || strings.HasPrefix(v, "go1.25") || strings.HasPrefix(v, "go1.26") {
		return Check{Name: "Go version", Status: "ok"}
	}
	if strings.HasPrefix(v, "go1.23") {
		return Check{Name: "Go version", Status: "warn", Message: "Go 1.23 detected; gofly recommends 1.24+", FixHint: "upgrade Go to 1.24 or later", NextActions: []string{"install Go 1.24 or later and rerun `gofly doctor --json`"}}
	}
	return Check{Name: "Go version", Status: "warn", Message: v + " may not support go.mod tool directives", FixHint: "upgrade Go to 1.24 or later", NextActions: []string{"install Go 1.24 or later and rerun `gofly doctor --json`"}}
}

func CheckGoModule() Check {
	if os.Getenv("GO111MODULE") == "off" {
		return Check{Name: "Go modules", Status: "fail", Message: "GO111MODULE=off", FixHint: "unset GO111MODULE or set it to on", NextActions: []string{"unset GO111MODULE or set GO111MODULE=on"}}
	}
	return Check{Name: "Go modules", Status: "ok"}
}

func CheckGOPATH() Check {
	gp := os.Getenv("GOPATH")
	if gp == "" {
		gp = build.Default.GOPATH
	}
	if gp == "" {
		return Check{Name: "GOPATH", Status: "warn", Message: "not set and default is empty", FixHint: "export GOPATH=$HOME/go", NextActions: []string{"export GOPATH=$HOME/go when using tools that still inspect GOPATH"}}
	}
	return Check{Name: "GOPATH", Status: "ok"}
}

func CheckTools() Check {
	missing := []string{}
	for _, tool := range []string{"go", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		return Check{Name: "Core tools", Status: "fail", Message: "missing: " + strings.Join(missing, ", "), FixHint: "install missing tools via system package manager", NextActions: []string{"install missing core tools and ensure they are available in PATH"}}
	}
	return Check{Name: "Core tools", Status: "ok"}
}

func CheckGit() Check {
	if _, err := exec.LookPath("git"); err != nil {
		return Check{Name: "Git", Status: "fail", Message: "not found in PATH", FixHint: "install git", NextActions: []string{"install git and rerun `gofly doctor --json`"}}
	}
	out, err := exec.Command("git", "version").Output()
	if err != nil {
		return Check{Name: "Git", Status: "warn", Message: "found but version check failed", NextActions: []string{"verify git is executable and rerun `gofly doctor --json`"}}
	}
	v := strings.TrimSpace(string(out))
	return Check{Name: "Git", Status: "ok", Message: v}
}

func CheckProtoc() Check {
	if _, err := exec.LookPath("protoc"); err != nil {
		return Check{Name: "protoc", Status: "warn", Message: "not found in PATH", FixHint: "install protoc (see https://grpc.io/docs/protoc-installation/)", NextActions: []string{"install protoc before running standard protobuf/gRPC generation"}}
	}
	out, err := exec.Command("protoc", "--version").Output()
	if err != nil {
		return Check{Name: "protoc", Status: "warn", Message: "found but version check failed", NextActions: []string{"verify protoc is executable and rerun `gofly doctor --json`"}}
	}
	return Check{Name: "protoc", Status: "ok", Message: strings.TrimSpace(string(out))}
}

func CheckWritePermission() Check {
	tmpDir := os.TempDir()
	f, err := os.CreateTemp(tmpDir, "gofly-doctor-*")
	if err != nil {
		return Check{Name: "Write permission", Status: "fail", Message: "cannot write to " + tmpDir, FixHint: "check TMPDIR / temp directory permissions", NextActions: []string{"fix TMPDIR permissions or set TMPDIR to a writable temporary directory"}}
	}
	if err := f.Close(); err != nil {
		return Check{Name: "Write permission", Status: "fail", Message: "cannot close temp file in " + tmpDir, FixHint: "check TMPDIR / temp directory permissions", NextActions: []string{"fix TMPDIR permissions or set TMPDIR to a writable temporary directory"}}
	}
	if err := os.Remove(f.Name()); err != nil {
		return Check{Name: "Write permission", Status: "warn", Message: "temp file cleanup failed: " + err.Error(), FixHint: "check TMPDIR / temp directory permissions", NextActions: []string{"clean stale temporary files and verify TMPDIR cleanup permissions"}}
	}
	return Check{Name: "Write permission", Status: "ok"}
}
