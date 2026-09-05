//go:build linux || darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

func currentShell() string {
	return selectShell(parentExecutable(os.Getppid()), os.Getenv("SHELL"))
}

func selectShell(parent, fallback string) string {
	// Launch only known shells, never an arbitrary parent such as an editor,
	// terminal wrapper, or gtach itself. SHELL is often a login-shell preference
	// or an unexported variable, so prefer the shell actually invoking us.
	switch filepath.Base(parent) {
	case "sh", "bash", "dash", "ash", "zsh", "ksh", "ksh93", "mksh", "fish", "csh", "tcsh", "nu":
		if path, err := exec.LookPath(parent); err == nil {
			return path
		}
	}
	if fallback != "" {
		return fallback
	}
	return "/bin/sh"
}
