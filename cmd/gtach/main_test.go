//go:build linux || darwin

package main

import "testing"

func TestParse(t *testing.T) {
	for _, args := range [][]string{
		{"-a", "sock"}, {"-A", "sock"}, {"-c", "sock", "-e", "^a", "-z", "sh", "-l"},
		{"-n", "sock", "--", "sh", "-c", "echo ok"}, {"-N", "sock", "sh"}, {"-p", "sock"},
	} {
		if _, err := parse(args); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{}, {"-x", "sock"}, {"-c", "sock"}, {"-a", "sock", "sh"}, {"-a", "sock", "-r", "bad"},
		{"-a", "sock", "-e"}, {"-a", "sock", "-e", "too long"}, {"-n", "sock", "--bogus"},
	} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
	c, err := parse([]string{"-c", "sock", "-e", "^a", "-E", "-z", "-r", "winch", "sh", "-l"})
	if err != nil || c.Escape != 1 || !c.NoEscape || !c.NoSuspend || len(c.Command) != 2 {
		t.Fatalf("%+v: %v", c, err)
	}
}

func TestEscapeKey(t *testing.T) {
	for s, want := range map[string]byte{"^\\": 28, "^z": 26, "^?": 127, "^@": 0, "x": 'x'} {
		got, err := escapeKey(s)
		if err != nil || got != want {
			t.Fatalf("%q: %d %v", s, got, err)
		}
	}
}
