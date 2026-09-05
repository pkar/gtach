//go:build linux || darwin

package gtach

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestReplayOnReattach(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	socket := socketPath(t)
	s, err := Start(ctx, Options{Socket: socket, Command: []string{"sh", "-c", "stty -echo; printf 'ready> '; while read -r x; do printf '<%s> ready> ' \"$x\"; done"}, WaitForClient: true, Replay: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := connect(t, socket, true)
	readUntil(t, a, "ready> ")
	a.Close()

	// Output produced with nobody attached must be retained too.
	push := connect(t, socket, false)
	if _, err := push.Write([]byte("detached\n")); err != nil {
		t.Fatal(err)
	}
	push.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		ready := strings.Contains(string(s.history.snapshot()), "<detached> ready> ")
		s.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached output not retained")
		}
		time.Sleep(time.Millisecond)
	}
	b := connect(t, socket, true)
	if got := readUntil(t, b, "<detached> ready> "); got != "ready> <detached> ready> " {
		t.Fatalf("replay: %q", got)
	}
	// No input was needed to recover the idle prompt. Repeated Attach must not
	// duplicate history, and subsequent live output must still arrive exactly once.
	if err := b.Attach(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("live\n")); err != nil {
		t.Fatal(err)
	}
	if got := readUntil(t, b, "<live> ready> "); got != "<live> ready> " {
		t.Fatalf("live output: %q", got)
	}
}

func TestReplayDefaultsOff(t *testing.T) {
	_, socket := startTest(t, "stty -echo; printf ready; while read -r x; do printf '<%s>' \"$x\"; done", true)
	a := connect(t, socket, true)
	readUntil(t, a, "ready")
	a.Close()
	b := connect(t, socket, true)
	b.Write([]byte("live\n"))
	if got := readUntil(t, b, "<live>"); got != "<live>" {
		t.Fatalf("unexpected replay: %q", got)
	}
}
