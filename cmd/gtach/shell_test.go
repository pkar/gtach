//go:build linux || darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelectShell(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	if got := selectShell(bash, "/bin/sh"); got != bash {
		t.Fatalf("selected %q, want current shell %q", got, bash)
	}
	if got := selectShell(bash, ""); got != bash {
		t.Fatalf("unexported SHELL: %q", got)
	}
	for _, parent := range []string{"", "/no/such/bash", "/usr/bin/node", "/usr/local/bin/gtach"} {
		if got := selectShell(parent, "/custom/shell"); got != "/custom/shell" {
			t.Fatalf("fallback for %q: %q", parent, got)
		}
		if got := selectShell(parent, ""); got != "/bin/sh" {
			t.Fatalf("default for %q: %q", parent, got)
		}
	}
}

func TestParentExecutable(t *testing.T) {
	// Test the OS lookup with a known PID, independent of which process runs tests.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got := parentExecutable(os.Getpid())
	want, err := filepath.EvalSymlinks(self)
	if err != nil {
		t.Fatal(err)
	}
	got, err = filepath.EvalSymlinks(got)
	if err != nil || got != want {
		t.Fatalf("process executable: %q, want %q: %v", got, want, err)
	}
}
