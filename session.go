//go:build linux || darwin

// Package gtach runs commands in persistent PTY sessions accessed over Unix
// sockets. It is a pure-Go implementation of dtach's core idea, not its protocol.
package gtach

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Options configures a session. Socket must be in an existing, user-owned
// directory with mode 0700. Existing paths are never removed or overwritten.
type Options struct {
	Socket  string
	Command []string
	// Env nil inherits the current environment; Dir empty uses the current directory.
	Env        []string
	Dir        string
	Rows, Cols uint16
	// WaitForClient preserves initial output until the first Attach. Cancel the
	// context or call Close if no client will attach.
	WaitForClient bool
	// Replay retains the last ReplayLimit bytes of output, including output while
	// detached, and sends them once on each connection's first Attach. Off by default.
	Replay bool
}

// Session owns a command, its PTY, and its socket. It remains alive when clients
// disconnect. The hosting process must remain alive too.
type Session struct {
	cmd       *exec.Cmd
	terminal  *os.File
	listener  *net.UnixListener
	mu        sync.Mutex
	clients   map[*peer]bool
	closing   bool
	first     chan struct{}
	firstOnce sync.Once
	stop      chan struct{}
	stopOnce  sync.Once
	done      chan struct{}
	err       error
	workers   sync.WaitGroup
	inputMu   sync.Mutex
	exited    atomic.Bool
	history   *outputHistory
}

type peer struct {
	conn   net.Conn
	output chan []byte
	done   chan struct{}
	once   sync.Once
}

func (p *peer) close() { p.once.Do(func() { close(p.done); p.conn.Close() }) }

// Start binds the socket and starts the command before returning. Cancellation
// terminates the command's process group and closes all client connections.
func Start(ctx context.Context, opts Options) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(opts.Command) == 0 {
		return nil, fmt.Errorf("gtach: command is required")
	}
	if opts.Socket == "" {
		return nil, fmt.Errorf("gtach: socket is required")
	}
	dir, err := os.Stat(filepath.Dir(opts.Socket))
	if err != nil {
		return nil, err
	}
	st, ok := dir.Sys().(*syscall.Stat_t)
	if !ok || !dir.IsDir() || dir.Mode().Perm() != 0700 || st.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("gtach: socket directory must be owned by uid %d with mode 0700", os.Geteuid())
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: opts.Socket, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(opts.Socket, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	cmd := exec.Command(opts.Command[0], opts.Command[1:]...)
	cmd.Env, cmd.Dir = opts.Env, opts.Dir
	if opts.Rows == 0 {
		opts.Rows = 24
	}
	if opts.Cols == 0 {
		opts.Cols = 80
	}
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: opts.Rows, Cols: opts.Cols})
	if err != nil {
		listener.Close()
		return nil, err
	}
	// Rewrap a nonblocking duplicate so Close interrupts pending PTY I/O.
	fd, err := unix.FcntlInt(terminal.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err == nil {
		err = unix.SetNonblock(fd, true)
		if err != nil {
			unix.Close(fd)
		}
	}
	if err != nil {
		_ = cmd.Process.Kill()
		terminal.Close()
		_ = cmd.Wait()
		listener.Close()
		return nil, err
	}
	terminal.Close()
	terminal = os.NewFile(uintptr(fd), "pty")
	s := &Session{cmd: cmd, terminal: terminal, listener: listener,
		clients: make(map[*peer]bool), first: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	if opts.Replay {
		s.history = &outputHistory{}
	}
	if !opts.WaitForClient {
		s.firstOnce.Do(func() { close(s.first) })
	}
	s.workers.Add(1)
	go s.accept()
	go s.run(ctx)
	return s, nil
}

// Wait waits for cleanup and returns the command's exit error. It may be called
// repeatedly or concurrently. Output is drained for up to one second after exit.
func (s *Session) Wait() error { <-s.done; return s.err }

// Close terminates the session and waits for cleanup. It is safe to call twice.
func (s *Session) Close() error { s.shutdown(); return s.Wait() }

func (s *Session) shutdown() {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.listener.Close()
		// The PTY child is a session/process-group leader.
		if !s.exited.Load() {
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		}
		s.terminal.Close()
		s.mu.Lock()
		s.closing = true
		for p := range s.clients {
			p.close()
		}
		s.mu.Unlock()
	})
}

func (s *Session) run(ctx context.Context) {
	exited := make(chan error, 1)
	go func() { err := s.cmd.Wait(); s.exited.Store(true); exited <- err }()
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		select {
		case <-s.first:
		case <-s.stop:
			return
		}
		b := make([]byte, 4096)
		for {
			n, err := s.terminal.Read(b)
			if n > 0 {
				s.broadcast(append([]byte(nil), b[:n]...))
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case s.err = <-exited:
		// A short-lived command may exit before its initial client connects.
		select {
		case <-s.first:
		case <-ctx.Done():
		case <-s.stop:
		}
		select {
		case <-outputDone:
		case <-time.After(time.Second):
		case <-ctx.Done():
		case <-s.stop:
		}
		// Let writers drain their bounded queues before closing the connections.
		s.mu.Lock()
		for p := range s.clients {
			close(p.output)
		}
		s.closing = true
		s.mu.Unlock()
		s.listener.Close()
		drained := make(chan struct{})
		go func() { s.workers.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-time.After(time.Second):
		case <-ctx.Done():
		case <-s.stop:
		}
	case <-ctx.Done():
		s.shutdown()
		s.err = <-exited
	case <-s.stop:
		s.err = <-exited
	}
	s.shutdown()
	<-outputDone
	s.workers.Wait()
	close(s.done)
}

func (s *Session) broadcast(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return
	}
	if s.history != nil {
		s.history.append(b)
	}
	for p, attached := range s.clients {
		if attached {
			select {
			case p.output <- b:
			default:
				p.close()
			}
		}
	}
}

func (s *Session) accept() {
	defer s.workers.Done()
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			return
		}
		p := &peer{conn: conn, output: make(chan []byte, 64), done: make(chan struct{})}
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			p.close()
			return
		}
		s.clients[p] = false
		s.workers.Add(2)
		s.mu.Unlock()
		go func() {
			defer s.workers.Done()
			defer p.close()
			for {
				select {
				case b, ok := <-p.output:
					if !ok {
						return
					}
					_ = p.conn.SetWriteDeadline(time.Now().Add(time.Second))
					if writeAll(p.conn, b) != nil {
						return
					}
				case <-p.done:
					return
				}
			}
		}()
		go s.handle(p)
	}
}

func (s *Session) control(fn func(int) error) error {
	raw, err := s.terminal.SyscallConn()
	if err != nil {
		return err
	}
	var result error
	if err := raw.Control(func(fd uintptr) { result = fn(int(fd)) }); err != nil {
		return err
	}
	return result
}

func (s *Session) handle(p *peer) {
	defer s.workers.Done()
	defer func() { p.close(); s.mu.Lock(); delete(s.clients, p); s.mu.Unlock() }()
	for {
		kind, b, err := readMessage(p.conn)
		if err != nil {
			return
		}
		switch kind {
		case attachMessage:
			if len(b) != 0 {
				return
			}
			s.mu.Lock()
			if s.closing {
				s.mu.Unlock()
				return
			}
			if !s.clients[p] {
				// Queue replay and subscribe under the broadcast lock so live output
				// follows the snapshot without a gap or duplicate bytes.
				if s.history != nil && len(s.history.data) > 0 {
					p.output <- s.history.snapshot() // An unattached peer has an empty queue.
				}
				s.clients[p] = true
			}
			s.mu.Unlock()
			s.firstOnce.Do(func() { close(s.first) })
		case inputMessage:
			s.inputMu.Lock()
			err = writeAll(s.terminal, b)
			s.inputMu.Unlock()
		case resizeMessage:
			if len(b) != 4 {
				return
			}
			rows, cols := binary.BigEndian.Uint16(b[:2]), binary.BigEndian.Uint16(b[2:])
			if rows == 0 || cols == 0 {
				return
			}
			err = s.control(func(fd int) error {
				return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
			})
		case redrawMessage:
			if len(b) != 1 {
				return
			}
			switch Redraw(b[0]) {
			case RedrawNone:
			case RedrawCtrlL:
				var state *unix.Termios
				err = s.control(func(fd int) error { var e error; state, e = terminalState(fd); return e })
				if err == nil && state.Lflag&(unix.ICANON|unix.ECHO) == 0 {
					s.inputMu.Lock()
					err = writeAll(s.terminal, []byte{12})
					s.inputMu.Unlock()
				}
			case RedrawWinch:
				var group int
				err = s.control(func(fd int) error { var e error; group, e = unix.IoctlGetInt(fd, unix.TIOCGPGRP); return e })
				if err == nil && group > 0 {
					err = syscall.Kill(-group, syscall.SIGWINCH)
				}
			default:
				return
			}
		default:
			return
		}
		if err != nil && !errors.Is(err, syscall.ESRCH) {
			return
		}
	}
}
