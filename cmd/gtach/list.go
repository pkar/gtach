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
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"
)

// Store only the directory label, never the shell's environment. Atomic rename
// avoids partial metadata when two terminals start the same directory session.
func saveSessionDirectory(socket, dir string) error {
	f, err := os.CreateTemp(filepath.Dir(socket), ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(dir)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), socket+".json")
}

func sessionDirectory(socket string) string {
	path := socket + ".json"
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return "-"
	}
	f, err := os.Open(path)
	if err != nil {
		return "-"
	}
	defer f.Close()
	var dir string
	if json.NewDecoder(io.LimitReader(f, 64*1024)).Decode(&dir) != nil || dir == "" {
		return "-"
	}
	return dir
}

func listSessions(w io.Writer, dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		_, err = fmt.Fprintln(w, "No sessions")
		return err
	}
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	count := 0
	for _, entry := range entries {
		if entry.Type()&os.ModeSocket == 0 {
			continue
		}
		socket := filepath.Join(dir, entry.Name())
		// Probe without Attach: listing must not replay, send input, or release a
		// server waiting for its first interactive client.
		conn, err := (&net.Dialer{Timeout: 250 * time.Millisecond}).DialContext(context.Background(), "unix", socket)
		status := "active"
		if err == nil {
			conn.Close()
		} else if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
			status = "stale"
		} else {
			status = "unreachable"
		}
		if count == 0 {
			if _, err := fmt.Fprintln(table, "STATUS\tDIRECTORY\tSOCKET"); err != nil {
				return err
			}
		}
		// Quote paths so control characters in filenames cannot affect the terminal.
		if _, err := fmt.Fprintf(table, "%s\t%q\t%q\n", status, sessionDirectory(socket), socket); err != nil {
			return err
		}
		count++
	}
	if count == 0 {
		_, err := fmt.Fprintln(w, "No sessions")
		return err
	}
	return table.Flush()
}
