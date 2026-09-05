//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pkar/gtach"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

var version = "dev"

const usage = `gtach: persistent PTY sessions
Usage: gtach
       gtach MODE SOCKET [OPTIONS] [--] [COMMAND [ARG...]]
       gtach list [SOCKET_DIR]
With no arguments, start or resume a shell in the current directory.
Press Ctrl-\ to detach without stopping the shell.
Explicit modes:
  -a  attach to an existing session
  -A  attach, or create and attach if absent
  -c  create and attach (fail if socket exists)
  -n  create detached
  -N  create detached, keep server in foreground
  -p  copy stdin verbatim to a session
Options:
  -e CHAR    detach key (default ^\; literal byte or ^X)
  -E         disable detach key
  -z         pass Ctrl-Z through instead of suspending the client
  -r METHOD  redraw: none, ctrl_l (default), winch
  --version  print version
  --list     list automatic sessions (alias for list; optional SOCKET_DIR)
For explicit modes, create the socket directory with mode 0700 first.
Bare gtach manages its own private socket directory.
`

type config struct {
	Mode, Socket        string
	Command             []string
	Escape              byte
	NoEscape, NoSuspend bool
	Redraw              gtach.Redraw
}

func parse(args []string) (config, error) {
	c := config{Escape: 28, Redraw: gtach.RedrawCtrlL}
	if len(args) < 2 {
		return c, errors.New("mode and socket are required")
	}
	c.Mode, c.Socket = args[0], args[1]
	switch c.Mode {
	case "-a", "-A", "-c", "-n", "-N", "-p":
	default:
		return c, fmt.Errorf("unknown mode %q", c.Mode)
	}
	args = args[2:]
	for len(args) > 0 {
		a := args[0]
		args = args[1:]
		switch a {
		case "--":
			c.Command = args
			return validate(c)
		case "-E":
			c.NoEscape = true
		case "-z":
			c.NoSuspend = true
		case "-e", "-r":
			if len(args) == 0 {
				return c, fmt.Errorf("%s needs a value", a)
			}
			value := args[0]
			args = args[1:]
			if a == "-e" {
				key, err := escapeKey(value)
				if err != nil {
					return c, err
				}
				c.Escape = key
			} else {
				switch value {
				case "none":
					c.Redraw = gtach.RedrawNone
				case "ctrl_l":
					c.Redraw = gtach.RedrawCtrlL
				case "winch":
					c.Redraw = gtach.RedrawWinch
				default:
					return c, fmt.Errorf("unknown redraw method %q", value)
				}
			}
		default:
			if strings.HasPrefix(a, "-") {
				return c, fmt.Errorf("unknown option %q", a)
			}
			c.Command = append([]string{a}, args...)
			return validate(c)
		}
	}
	return validate(c)
}

func validate(c config) (config, error) {
	if (c.Mode == "-c" || c.Mode == "-n" || c.Mode == "-N") && len(c.Command) == 0 {
		return c, errors.New("command is required")
	}
	if (c.Mode == "-a" || c.Mode == "-p") && len(c.Command) != 0 {
		return c, errors.New("command is not allowed in this mode")
	}
	return c, nil
}

func escapeKey(s string) (byte, error) {
	if len(s) == 1 {
		return s[0], nil
	}
	if len(s) == 2 && s[0] == '^' {
		b := s[1]
		if b >= 'a' && b <= 'z' {
			b -= 32
		}
		if b == '?' {
			return 127, nil
		}
		if b >= '@' && b <= '_' {
			return b & 31, nil
		}
	}
	return 0, fmt.Errorf("invalid detach key %q", s)
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--internal-server" {
		os.Exit(server())
	}
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println(version)
		return
	}
	if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Print(usage)
		return
	}
	if len(os.Args) >= 2 && (os.Args[1] == "list" || os.Args[1] == "--list") {
		dir := defaultSocketDir()
		var err error
		if len(os.Args) > 3 {
			err = errors.New("usage: gtach list [SOCKET_DIR]")
		} else {
			if len(os.Args) == 3 {
				dir = os.Args[2]
			}
			err = listSessions(os.Stdout, dir)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "gtach:", err)
			os.Exit(1)
		}
		return
	}
	var c config
	var err error
	if len(os.Args) == 1 {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			err = errors.New("attach requires a terminal on stdin (use --help for usage)")
		} else {
			c, err = defaultConfig()
		}
	} else {
		c, err = parse(os.Args[1:])
	}
	if err == nil {
		err = run(c)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gtach:", err)
		os.Exit(1)
	}
}

func options(c config) gtach.Options {
	rows, cols := uint16(24), uint16(80)
	if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
		rows, cols = uint16(h), uint16(w)
	}
	return gtach.Options{Socket: c.Socket, Command: c.Command, Env: os.Environ(), Rows: rows, Cols: cols, WaitForClient: c.Mode == "-c" || c.Mode == "-A", Replay: true}
}

func run(c config) error {
	socket, err := filepath.Abs(c.Socket)
	if err != nil {
		return err
	}
	c.Socket = socket
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if c.Mode == "-N" {
		s, err := gtach.Start(ctx, options(c))
		if err != nil {
			return err
		}
		return s.Wait()
	}
	var client *gtach.Client
	if c.Mode == "-a" || c.Mode == "-A" || c.Mode == "-p" {
		client, err = gtach.Dial(ctx, c.Socket)
		if err != nil && (c.Mode != "-A" || !errors.Is(err, os.ErrNotExist)) {
			return err
		}
	}
	if c.Mode == "-p" {
		defer client.Close()
		_, err := io.Copy(client, os.Stdin)
		return err
	}
	if c.Mode != "-n" && !term.IsTerminal(int(os.Stdin.Fd())) {
		if client != nil {
			client.Close()
		}
		return errors.New("attach requires a terminal on stdin (use -p to push input)")
	}
	if client == nil {
		if len(c.Command) == 0 {
			return errors.New("session does not exist and no command was supplied")
		}
		if err := launch(options(c)); err != nil {
			// Another -A may have created the session while we were starting.
			if c.Mode != "-A" {
				return err
			}
			client, _ = gtach.Dial(ctx, c.Socket)
			if client == nil {
				return err
			}
		}
		if c.Mode == "-n" {
			return nil
		}
		if client == nil {
			client, err = gtach.Dial(ctx, c.Socket)
			if err != nil {
				return err
			}
		}
	}
	defer client.Close()
	return attach(ctx, client, c)
}

// Re-exec rather than fork Go's multithreaded runtime. Configuration and startup
// acknowledgement use inherited pipes, never environment variables or temp files.
func launch(opts gtach.Options) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	input, send, err := os.Pipe()
	if err != nil {
		return err
	}
	defer input.Close()
	defer send.Close()
	receive, ready, err := os.Pipe()
	if err != nil {
		return err
	}
	defer receive.Close()
	defer ready.Close()
	cmd := exec.Command(executable, "--internal-server")
	cmd.ExtraFiles = []*os.File{input, ready}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	input.Close()
	ready.Close()
	success := false
	defer func() {
		if !success {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		} else {
			_ = cmd.Process.Release()
		}
	}()
	_ = send.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(send).Encode(opts); err != nil {
		return err
	}
	send.Close()
	_ = receive.SetReadDeadline(time.Now().Add(10 * time.Second))
	var status string
	if err := json.NewDecoder(receive).Decode(&status); err != nil {
		return fmt.Errorf("server startup: %w", err)
	}
	if status != "" {
		return errors.New(status)
	}
	success = true
	return nil
}

func server() int {
	input, ready := os.NewFile(3, "config"), os.NewFile(4, "ready")
	var opts gtach.Options
	err := json.NewDecoder(io.LimitReader(input, 1024*1024)).Decode(&opts)
	input.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stop()
	var session *gtach.Session
	if err == nil {
		session, err = gtach.Start(ctx, opts)
	}
	status := ""
	if err != nil {
		status = err.Error()
	}
	if reportErr := json.NewEncoder(ready).Encode(status); reportErr != nil && session != nil {
		session.Close()
		return 1
	}
	ready.Close()
	if err != nil {
		return 1
	}
	if session.Wait() != nil {
		return 1
	}
	return 0
}

func attach(ctx context.Context, c *gtach.Client, cfg config) error {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, state)
	resize := func() error {
		w, h, err := term.GetSize(fd)
		if err != nil {
			return err
		}
		if w <= 0 || h <= 0 {
			return nil
		}
		return c.Resize(uint16(h), uint16(w))
	}
	if err := resize(); err != nil {
		return err
	}
	if err := c.Attach(); err != nil {
		return err
	}
	// A short-lived command can finish immediately after Attach. Drain its
	// output even if the optional redraw races with the server closing.
	_ = c.Redraw(cfg.Redraw)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	output := make(chan error, 1)
	go func() { _, err := io.Copy(os.Stdout, c); output <- err }()
	b := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-output:
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		case <-winch:
			if err := resize(); err != nil {
				return err
			}
		default:
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 100)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		n, err = unix.Read(fd, b)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		start := 0
		for i, key := range b[:n] {
			detach := !cfg.NoEscape && key == cfg.Escape
			suspend := !cfg.NoSuspend && key == 26
			if !detach && !suspend {
				continue
			}
			if _, err := c.Write(b[start:i]); err != nil {
				return err
			}
			if detach {
				return nil
			}
			if err := term.Restore(fd, state); err != nil {
				return err
			}
			// SIGSTOP cannot be swallowed by Go's signal handling or orphaned groups.
			if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
				return err
			}
			if _, err := term.MakeRaw(fd); err != nil {
				return err
			}
			if err := resize(); err != nil {
				return err
			}
			if err := c.Redraw(cfg.Redraw); err != nil {
				return err
			}
			start = i + 1
		}
		if _, err := c.Write(b[start:n]); err != nil {
			return err
		}
	}
}
