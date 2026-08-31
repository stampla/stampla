package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBuiltBinary proves the one thing the in-process tests cannot: that
// this file wires the interface to the process — its arguments, its
// streams, its exit status, and the version the release stamps in.
//
// It is the only test here that builds anything, so it is the only one
// -short skips.
func TestBuiltBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("building the binary is slow")
	}
	exe := filepath.Join(t.TempDir(), "stampla")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	build := exec.Command("go", "build", "-ldflags=-X main.version=9.9.9-test", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the command: %v\n%s", err, out)
	}

	stdout, err := exec.Command(exe, "version").Output()
	if err != nil {
		t.Fatalf("stampla version: %v", err)
	}
	// The release wiring: goreleaser sets main.version at the tag, and a
	// binary that ignored it would report every release as "dev".
	if !strings.HasPrefix(string(stdout), "stampla 9.9.9-test\n") {
		t.Errorf("stampla version = %q, want the stamped version", stdout)
	}

	// And the exit code reaches the process, which is what every script
	// around this tool is written against.
	err = exec.Command(exe, "no-such-verb").Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("stampla no-such-verb = %v, want a usage failure", err)
	}
	if exit.ExitCode() != 64 {
		t.Errorf("stampla no-such-verb exited %d, want 64", exit.ExitCode())
	}
}
