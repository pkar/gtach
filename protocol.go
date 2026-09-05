package gtach

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	inputMessage byte = iota + 1
	resizeMessage
	redrawMessage
	attachMessage
	maxPayload = 4096
)

// Redraw selects how an application is asked to refresh when a client attaches.
type Redraw byte

const (
	RedrawNone Redraw = iota
	RedrawCtrlL
	RedrawWinch
)

func readMessage(r io.Reader) (byte, []byte, error) {
	var h [3]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint16(h[1:]))
	if n > maxPayload {
		return 0, nil, fmt.Errorf("gtach: oversized message: %d", n)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return h[0], b, err
}

func writeMessage(w io.Writer, kind byte, b []byte) error {
	if len(b) > maxPayload {
		return fmt.Errorf("gtach: oversized message")
	}
	h := []byte{kind, byte(len(b) >> 8), byte(len(b))}
	if err := writeAll(w, h); err != nil {
		return err
	}
	return writeAll(w, b)
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
