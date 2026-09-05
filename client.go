package gtach

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
)

// Client is a connection to a session. Read returns raw PTY output. Write sends
// input without interpreting control characters. One reader and multiple writers
// may operate concurrently. Close detaches without stopping the command.
type Client struct {
	conn net.Conn
	mu   sync.Mutex
}

// Dial connects without subscribing to output, suitable for pushing input.
// Call Attach to subscribe to output and release a WaitForClient session.
func Dial(ctx context.Context, socket string) (*Client, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Read(b []byte) (int, error) { return c.conn.Read(b) }
func (c *Client) Close() error               { return c.conn.Close() }

func (c *Client) send(kind byte, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return writeMessage(c.conn, kind, b)
}

// Attach subscribes to future output; there is no scrollback or terminal emulator.
func (c *Client) Attach() error { return c.send(attachMessage, nil) }

func (c *Client) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for len(b) > 0 {
		n := min(len(b), maxPayload)
		if err := writeMessage(c.conn, inputMessage, b[:n]); err != nil {
			return total, err
		}
		total += n
		b = b[n:]
	}
	return total, nil
}

// Resize updates the shared PTY. The most recent client's size wins.
func (c *Client) Resize(rows, cols uint16) error {
	if rows == 0 || cols == 0 {
		return fmt.Errorf("gtach: terminal dimensions must be nonzero")
	}
	var b [4]byte
	binary.BigEndian.PutUint16(b[:2], rows)
	binary.BigEndian.PutUint16(b[2:], cols)
	return c.send(resizeMessage, b[:])
}

func (c *Client) Redraw(method Redraw) error {
	if method > RedrawWinch {
		return fmt.Errorf("gtach: invalid redraw method")
	}
	return c.send(redrawMessage, []byte{byte(method)})
}
