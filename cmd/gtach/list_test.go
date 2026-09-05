//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"io"
	"time"

	"github.com/pkar/gtach"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListSessions(t *testing.T) {
	dir, err := os.MkdirTemp("", "gt-list-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	live := filepath.Join(dir, "live")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: live, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := saveSessionDirectory(live, "/project/with\nnewline"); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(dir, "dead")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: dead, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()
	if err := saveSessionDirectory(filepath.Join(dir, "finished"), "/finished"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := listSessions(&out, dir); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "active") || !strings.Contains(text, "stale") || !strings.Contains(text, `/project/with\nnewline`) || strings.Contains(text, "finished") {
		t.Fatalf("listing: %s", text)
	}
	if strings.Count(text, "\n") != 3 {
		t.Fatalf("unescaped control character: %q", text)
	}
	if _, err := os.Stat(dead); err != nil {
		t.Fatal("listing removed stale socket")
	}
	info, err := os.Stat(live + ".json")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("metadata permissions: %v %v", info, err)
	}
}

func TestListEmpty(t *testing.T) {
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		var out bytes.Buffer
		if err := listSessions(&out, dir); err != nil {
			t.Fatal(err)
		}
		if out.String() != "No sessions\n" {
			t.Fatal(out.String())
		}
	}
}

func TestListDoesNotAttach(t *testing.T) {
	dir, err := os.MkdirTemp("", "gt-list-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	socket := filepath.Join(dir, "waiting")
	s, err := gtach.Start(ctx, gtach.Options{Socket: socket, Command: []string{"sh", "-c", "printf ready"}, WaitForClient: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := listSessions(io.Discard, dir); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("list released waiting server: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	client, err := gtach.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Attach(); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(client)
	if err != nil || string(b) != "ready" {
		t.Fatalf("initial output lost: %q %v", b, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOptionsInheritEnvironment(t *testing.T) {
	t.Setenv("GTACH_INHERIT_TEST", "spaces = and punctuation!")
	opts := options(config{})
	found := false
	for _, entry := range opts.Env {
		if entry == "GTACH_INHERIT_TEST=spaces = and punctuation!" {
			found = true
		}
	}
	if !found {
		t.Fatal("caller environment not captured")
	}
}
