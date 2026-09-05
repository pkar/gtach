package gtach

import "golang.org/x/sys/unix"

func terminalState(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TIOCGETA)
}
