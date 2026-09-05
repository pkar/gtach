//go:build linux || darwin

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/pkar/gtach"
)

func defaultConfig() (config, error) {
	dir, err := os.Getwd()
	if err != nil {
		return config{}, err
	}
	return directoryConfig(dir, os.Getenv("SHELL"), defaultSocketDir())
}

func defaultSocketDir() string {
	// A short, stable path works across terminals, including macOS where TMPDIR
	// is often long. Never put sockets or other generated files in the project.
	return fmt.Sprintf("/tmp/gtach-%d", os.Geteuid())
}

func directoryConfig(dir, shell, socketDir string) (config, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return config{}, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return config{}, err
	}
	if err := os.Mkdir(socketDir, 0700); err != nil && !os.IsExist(err) {
		return config{}, err
	}
	// Refuse symlinks, other owners, and permissive directories. Do not chmod an
	// existing path: it could belong to somebody else in the shared /tmp namespace.
	info, err := os.Lstat(socketDir)
	if err != nil {
		return config{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || st.Uid != uint32(os.Geteuid()) {
		return config{}, fmt.Errorf("automatic socket directory %s must be a real directory owned by uid %d with mode 0700", socketDir, os.Geteuid())
	}
	if shell == "" {
		shell = "/bin/sh"
	}
	return config{
		Mode: "-A", Socket: filepath.Join(socketDir, fmt.Sprintf("%x", sha256.Sum256([]byte(dir)))),
		Command: []string{shell, "-i"}, Escape: 28, Redraw: gtach.RedrawCtrlL,
	}, nil
}
