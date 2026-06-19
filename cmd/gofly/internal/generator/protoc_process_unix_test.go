//go:build unix

package generator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGenerateStandardProtoTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(testProto), 0o644); err != nil {
		t.Fatal(err)
	}
	childPIDPath := filepath.Join(dir, "child.pid")
	readyPath := filepath.Join(dir, "child.ready")
	fakeProtoc := filepath.Join(dir, "protoc")
	script := fmt.Sprintf("#!/bin/sh\nsleep 10 &\necho $! > %q\ntouch %q\nwait\n", childPIDPath, readyPath)
	if err := os.WriteFile(fakeProtoc, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		errCh <- GenerateStandardProto(ctx, ProtocOptions{
			ProtoFile: protoPath,
			GoOut:     dir,
			GoGRPCOut: dir,
			Protoc:    fakeProtoc,
		})
	}()

	waitForFileOrError(t, readyPath, errCh, 10*time.Second)
	cancel()
	data, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}

	err = <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GenerateStandardProto cancel err = %v, want context canceled", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", data, err)
	}
	waitForProcessExit(t, pid, 3*time.Second)
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitForFileOrError(t *testing.T, path string, errCh <-chan error, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-errCh:
			t.Fatalf("GenerateStandardProto returned before creating %s: %v", path, err)
		case <-tick.C:
			if _, err := os.Stat(path); err == nil {
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", path)
		}
	}
}

func waitForProcessExit(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("protoc child process %d survived timeout cancellation", pid)
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
