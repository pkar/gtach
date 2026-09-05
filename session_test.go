//go:build linux || darwin

package gtach

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func socketPath(t *testing.T) string {
	t.Helper()
	// macOS Unix socket paths are short; testing.T.TempDir can exceed the limit.
	dir, err := os.MkdirTemp("", "gt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func startTest(t *testing.T, script string, wait bool) (*Session, string) {
	t.Helper()
	socket := socketPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	s, err := Start(ctx, Options{Socket: socket, Command: []string{"sh", "-c", script}, WaitForClient: wait})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, socket
}

func connect(t *testing.T, socket string, attach bool) *Client {
	t.Helper()
	c, err := Dial(context.Background(), socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if attach {
		if err := c.Attach(); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func readUntil(t *testing.T, c *Client, marker string) string {
	t.Helper()
	var result strings.Builder
	b := make([]byte, 4096)
	for !strings.Contains(result.String(), marker) {
		n, err := c.Read(b)
		result.Write(b[:n])
		if err != nil {
			t.Fatalf("read %q: %v (wanted %q)", result.String(), err, marker)
		}
	}
	return result.String()
}

func TestInitialOutputAndExit(t *testing.T) {
	s, socket := startTest(t, "printf 'initial-output'; exit 7", true)
	// Deliberately attach after the short-lived process has exited.
	time.Sleep(100 * time.Millisecond)
	c := connect(t, socket, true)
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "initial-output" {
		t.Fatalf("output %q", b)
	}
	var exit *exec.ExitError
	if !errors.As(s.Wait(), &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit: %v", s.Wait())
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains: %v", err)
	}
}

func TestDetachReattachPushAndResize(t *testing.T) {
	s, socket := startTest(t, "stty -echo; printf ready; while IFS= read -r line; do case \"$line\" in size) stty size;; quit) exit;; *) printf '<%s>\\n' \"$line\";; esac; done", true)
	c := connect(t, socket, true)
	readUntil(t, c, "ready")
	if _, err := c.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "<first>")
	c.Close()
	c = connect(t, socket, true)
	if err := c.Resize(43, 117); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("size\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "43 117")
	push := connect(t, socket, false)
	if _, err := push.Write([]byte("pushed\n")); err != nil {
		t.Fatal(err)
	}
	push.Close()
	readUntil(t, c, "<pushed>")
	if _, err := c.Write([]byte("quit\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestMultipleClients(t *testing.T) {
	s, socket := startTest(t, "stty -echo; echo ready; while read -r x; do echo \"<$x>\"; done", true)
	a := connect(t, socket, true)
	readUntil(t, a, "ready")
	b := connect(t, socket, true)
	// A round trip from b guarantees its Attach has been processed.
	b.Write([]byte("both\n"))
	readUntil(t, a, "<both>")
	readUntil(t, b, "<both>")
	a.Close()
	b.Write([]byte("survived\n"))
	readUntil(t, b, "<survived>")
	s.Close()
}

func TestCloseUnblocksPTY(t *testing.T) {
	for _, wait := range []bool{false, true} {
		s, _ := startTest(t, "sleep 60", wait)
		done := make(chan struct{})
		go func() { s.Close(); s.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Close blocked")
		}
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, err := Start(ctx, Options{Socket: socketPath(t), Command: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan struct{})
	go func() { s.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation blocked")
	}
}

func TestSocketSafety(t *testing.T) {
	socket := socketPath(t)
	opts := Options{Socket: socket, Command: []string{"sleep", "60"}}
	os.Chmod(filepath.Dir(socket), 0755)
	if s, err := Start(context.Background(), opts); err == nil {
		s.Close()
		t.Fatal("accepted public directory")
	}
	os.Chmod(filepath.Dir(socket), 0700)
	os.WriteFile(socket, []byte("keep"), 0600)
	if s, err := Start(context.Background(), opts); err == nil {
		s.Close()
		t.Fatal("overwrote file")
	}
	b, _ := os.ReadFile(socket)
	if string(b) != "keep" {
		t.Fatal("changed existing file")
	}
	os.Remove(socket)
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("socket mode: %v", info.Mode())
	}
	if other, err := Start(context.Background(), opts); err == nil {
		other.Close()
		t.Fatal("replaced active session")
	}
}

func TestStartFailures(t *testing.T) {
	socket := socketPath(t)
	if _, err := Start(context.Background(), Options{Socket: socket, Command: []string{"/nonexistent/gtach-command"}}); err == nil {
		t.Fatal("missing exec accepted")
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup socket leaked: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Start(ctx, Options{Socket: socket, Command: []string{"sh"}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestMalformedClientIsIsolated(t *testing.T) {
	_, socket := startTest(t, "stty -echo; echo ready; cat", true)
	good := connect(t, socket, true)
	readUntil(t, good, "ready")
	for _, packet := range [][]byte{{255, 0, 0}, {resizeMessage, 0, 1, 0}, {inputMessage, 255, 255}, {redrawMessage, 0, 1, 255}} {
		bad, err := net.Dial("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		bad.SetDeadline(time.Now().Add(time.Second))
		bad.Write(packet)
		var b [1]byte
		if _, err := bad.Read(b[:]); err == nil {
			t.Fatal("invalid client survived")
		} else if e, ok := err.(net.Error); ok && e.Timeout() {
			t.Fatal("invalid client was not closed")
		}
		bad.Close()
	}
	good.Write([]byte("alive\n"))
	readUntil(t, good, "alive")
}

func TestSlowClientDropped(t *testing.T) {
	s := &Session{clients: make(map[*peer]bool)}
	a, b := net.Pipe()
	defer b.Close()
	p := &peer{conn: a, output: make(chan []byte, 1), done: make(chan struct{})}
	s.clients[p] = true
	s.broadcast([]byte("one"))
	s.broadcast([]byte("two"))
	select {
	case <-p.done:
	default:
		t.Fatal("slow client not disconnected")
	}
}
