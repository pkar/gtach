//go:build linux || darwin

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/pkar/gtach"
	"golang.org/x/sys/unix"
)

func TestCLI(t *testing.T) {
	dir, err := os.MkdirTemp("", "gtcli-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	binary := filepath.Join(dir, "gtach")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	socket := filepath.Join(dir, "s")
	t.Cleanup(func() {
		c, err := gtach.Dial(context.Background(), socket)
		if err == nil {
			c.Attach()
			c.Write([]byte("quit\n"))
			c.Close()
		}
	})

	var workingDir string
	var program = binary
	var extraEnv []string
	start := func(args ...string) (*exec.Cmd, *os.File) {
		t.Helper()
		cmd := exec.Command(program, args...)
		cmd.Dir = workingDir
		cmd.Env = append(os.Environ(), extraEnv...)
		f, err := pty.Start(cmd)
		if err != nil {
			t.Fatal(err)
		}
		fd, err := unix.FcntlInt(f.Fd(), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := unix.SetNonblock(fd, true); err != nil {
			t.Fatal(err)
		}
		f.Close()
		f = os.NewFile(uintptr(fd), "test-pty")
		t.Cleanup(func() {
			f.Close()
			if cmd.ProcessState == nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
		})
		return cmd, f
	}
	read := func(f *os.File, marker string) string {
		t.Helper()
		f.SetReadDeadline(time.Now().Add(5 * time.Second))
		var output strings.Builder
		b := make([]byte, 1024)
		for !strings.Contains(output.String(), marker) {
			n, err := f.Read(b)
			output.Write(b[:n])
			if err != nil {
				t.Fatalf("waiting for %q: %q: %v", marker, output.String(), err)
			}
		}
		return output.String()
	}
	wait := func(cmd *exec.Cmd) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Fatal("CLI did not exit")
		}
	}
	cmd, f := start("-c", socket, "-r", "none", "sh", "-c", "stty -echo; echo READY; while read -r x; do case \"$x\" in quit) exit;; *) echo \"GOT:$x\";; esac; done")
	read(f, "READY")
	f.Write([]byte("first\n"))
	read(f, "GOT:first")
	f.Write([]byte{28})
	wait(cmd)
	if _, err := os.Stat(socket); err != nil {
		t.Fatal("detach lost session:", err)
	}
	cmd, f = start("-A", socket, "-r", "none", "/must/not/run")
	f.Write([]byte("second\n"))
	read(f, "GOT:second")
	push := exec.Command(binary, "-p", socket)
	push.Stdin = strings.NewReader("pushed\n")
	if b, err := push.CombinedOutput(); err != nil {
		t.Fatalf("push: %v %s", err, b)
	}
	read(f, "GOT:pushed")
	f.Write([]byte("quit\n"))
	wait(cmd)

	for i := 0; i < 5; i++ {
		short, terminal := start("-c", filepath.Join(dir, "short"), "sh", "-c", "printf SHORT-OUTPUT")
		read(terminal, "SHORT-OUTPUT")
		wait(short)
	}

	detached := filepath.Join(dir, "detached")
	b, err := exec.Command(binary, "-n", detached, "sh", "-c", "read x; printf '%s' \"$x\" > \"$1\"", "sh", filepath.Join(dir, "result")).CombinedOutput()
	if err != nil {
		t.Fatalf("detached start: %v %s", err, b)
	}
	push = exec.Command(binary, "-p", detached)
	push.Stdin = strings.NewReader("detached-ok\n")
	if b, err := push.CombinedOutput(); err != nil {
		t.Fatalf("detached push: %v %s", err, b)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(dir, "result"))
		if string(b) == "detached-ok" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached command did not receive input")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b, err := exec.Command(binary, "-n", filepath.Join(dir, "fail"), "/no/such/command").CombinedOutput(); err == nil || !strings.Contains(string(b), "no such file") {
		t.Fatalf("exec error: %v %s", err, b)
	}
	// Bare invocation preserves shell state and keys sessions by the invoking
	// directory, even when the shell changes its own working directory.
	project := filepath.Join(dir, "project")
	other := filepath.Join(dir, "other")
	alias := filepath.Join(dir, "alias")
	for _, path := range []string{project, other} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		cfg, err := directoryConfig(path, "", defaultSocketDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			c, err := gtach.Dial(context.Background(), cfg.Socket)
			if err == nil {
				c.Attach()
				c.Write([]byte("exit\n"))
				c.Close()
			}
		})
	}
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	workingDir = project
	extraEnv = []string{"SHELL=/bin/sh", "PS1=GTACH-PROMPT>", "ENV=", "GTACH_TEST_STATE=", "GTACH_INHERIT_TEST=from caller"}
	cmd, f = start()
	read(f, "GTACH-PROMPT>")
	f.Write([]byte("printf 'ENV:%s\\n' \"$GTACH_INHERIT_TEST\"\n"))
	read(f, "ENV:from caller")
	for _, flag := range []string{"list", "--list"} {
		output, err := exec.Command(binary, flag).CombinedOutput()
		resolved, resolveErr := filepath.EvalSymlinks(project)
		if err != nil || resolveErr != nil || !strings.Contains(string(output), resolved) || !strings.Contains(string(output), "active") {
			t.Fatalf("list: %v %s", err, output)
		}
	}
	f.Write([]byte("GTACH_TEST_STATE=kept; cd /; printf 'STATE:%s\\n' \"$GTACH_TEST_STATE\"\n"))
	read(f, "STATE:kept")
	f.Write([]byte{28})
	wait(cmd)

	workingDir = other
	cmd, f = start()
	read(f, "GTACH-PROMPT>")
	f.Write([]byte("printf 'OTHER:%s\\n' \"${GTACH_TEST_STATE:-empty}\"\n"))
	read(f, "OTHER:empty")
	f.Write([]byte("exit\n"))
	wait(cmd)

	workingDir = alias
	extraEnv[0] = "SHELL=/no/such/shell" // Reattach must not start another shell.
	extraEnv[4] = "GTACH_INHERIT_TEST=changed caller"
	cmd, f = start()
	// Reattach must show prior output before the user types anything.
	read(f, "STATE:kept")
	f.Write([]byte("printf 'RESUMED:%s\\n' \"$GTACH_TEST_STATE\"\n"))
	read(f, "RESUMED:kept")
	f.Write([]byte("printf 'KEPT_ENV:%s\\n' \"$GTACH_INHERIT_TEST\"\n"))
	read(f, "KEPT_ENV:from caller")
	f.Write([]byte("exit\n"))
	wait(cmd)

	workingDir = project
	extraEnv[0] = "SHELL=" // Normal exit allows a fresh session, with /bin/sh fallback.
	cmd, f = start()
	read(f, "GTACH-PROMPT>")
	f.Write([]byte("printf 'FRESH:%s\\n' \"${GTACH_TEST_STATE:-empty}\"\n"))
	read(f, "FRESH:empty")
	f.Write([]byte("exit\n"))
	wait(cmd)
	entries, err := os.ReadDir(project)
	if err != nil || len(entries) != 0 {
		t.Fatalf("generated project files: %v %v", entries, err)
	}
	if b, err := exec.Command(binary).CombinedOutput(); err == nil || !strings.Contains(string(b), "requires a terminal") {
		t.Fatalf("bare non-TTY: %v %s", err, b)
	}
	if b, err := exec.Command(binary, "--help").CombinedOutput(); err != nil || !strings.Contains(string(b), "start or resume") {
		t.Fatalf("help: %v %s", err, b)
	}

	// Invoke from a real interactive Bash with a prompt configured in .bashrc.
	// PS1 is deliberately not exported; the inner shell must load its own rc.
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("PS1='PARENT-PROMPT> '; export FROM_RC=loaded\n"), 0600); err != nil {
		t.Fatal(err)
	}
	program = bash
	workingDir = project
	extraEnv = []string{"HOME=" + home, "ENV=", "BASH_ENV=", "TERM=xterm"}
	for _, setting := range []string{"unset SHELL", "export SHELL=/bin/sh"} {
		outer, terminal := start("--noprofile", "-i")
		read(terminal, "PARENT-PROMPT> ")
		terminal.Write([]byte(setting + "; export -n PS1; " + binary + "; printf 'OUTER-%s\\n' returned\n"))
		read(terminal, "[g] PARENT-PROMPT> ")
		terminal.Write([]byte("[ -n \"$BASH_VERSION\" ] && printf 'BASH-%s\\n' \"$FROM_RC\"\n"))
		read(terminal, "BASH-loaded")
		terminal.Write([]byte{28})
		read(terminal, "OUTER-returned")
		terminal.Write([]byte(binary + "; printf 'OUTER-%s\\n' returned\n"))
		replayed := read(terminal, "BASH-loaded") // Replay confirms the same Bash session resumed.
		terminal.Write([]byte("printf 'AFTER-%s\\n' reattach\n"))
		replayed += read(terminal, "AFTER-reattach")
		for _, clear := range []string{"\x1b[2J", "\x1b[3J", "\x1b[H\x1b[J"} {
			if strings.Contains(replayed, clear) {
				t.Fatalf("reattach cleared the screen: %q", replayed)
			}
		}
		terminal.Write([]byte("exit\n"))
		read(terminal, "OUTER-returned")
		terminal.Write([]byte("exit\n"))
		// Darwin waits for terminal output to drain when the session leader exits.
		// Keep reading the final echo/prompt while waiting, like a real terminal.
		drained := make(chan struct{})
		go func() { io.Copy(io.Discard, terminal); close(drained) }()
		wait(outer)
		terminal.Close()
		<-drained
	}

	foreground := exec.Command(binary, "-N", filepath.Join(dir, "foreground"), "sleep", "60")
	foreground.Stdout, foreground.Stderr = io.Discard, io.Discard
	if err := foreground.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if foreground.ProcessState == nil {
			foreground.Process.Kill()
			foreground.Wait()
		}
	}()
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "foreground")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("foreground not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	foreground.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- foreground.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		foreground.Process.Kill()
		<-done
		t.Fatal("foreground ignored SIGTERM")
	}
}
