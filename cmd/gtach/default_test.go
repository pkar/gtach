//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryConfig(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	sockets := filepath.Join(root, "sockets")
	a, err := directoryConfig(dir, "/bin/custom shell", sockets)
	if err != nil {
		t.Fatal(err)
	}
	if a.Mode != "-A" || a.Escape != 28 || len(a.Command) != 2 || a.Command[0] != "/bin/custom shell" || a.Command[1] != "-i" {
		t.Fatalf("config: %+v", a)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	b, err := directoryConfig(alias, "", sockets)
	if err != nil {
		t.Fatal(err)
	}
	if a.Socket != b.Socket || b.Command[0] != "/bin/sh" {
		t.Fatalf("alias/fallback: %+v", b)
	}
	c, err := directoryConfig(root, "", sockets)
	if err != nil {
		t.Fatal(err)
	}
	if a.Socket == c.Socket {
		t.Fatal("different directories share a socket")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("project was modified: %v %v", entries, err)
	}
	info, err := os.Stat(sockets)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("socket directory permissions: %v %v", info, err)
	}
	long := filepath.Join(dir, strings.Repeat("a", 200))
	if err := os.Mkdir(long, 0700); err != nil {
		t.Fatal(err)
	}
	c, err = directoryConfig(long, "", sockets)
	if err != nil || len(filepath.Base(c.Socket)) != 64 {
		t.Fatalf("long path: %+v %v", c, err)
	}
	if len(filepath.Join(defaultSocketDir(), filepath.Base(c.Socket))) >= 104 {
		t.Fatal("automatic socket exceeds macOS limit")
	}
}

func TestDirectoryConfigRejectsUnsafePaths(t *testing.T) {
	for _, kind := range []string{"public", "symlink", "file", "owner"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			sockets := filepath.Join(root, "sockets")
			switch kind {
			case "public":
				if err := os.Mkdir(sockets, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(sockets, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(root, sockets); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(sockets, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "owner":
				if os.Geteuid() != 0 {
					t.Skip("changing owner requires root")
				}
				if err := os.Mkdir(sockets, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(sockets, 1, -1); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(sockets)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := directoryConfig(root, "", sockets); err == nil {
				t.Fatal("accepted unsafe socket directory")
			}
			after, err := os.Lstat(sockets)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("changed existing path")
			}
		})
	}
}
