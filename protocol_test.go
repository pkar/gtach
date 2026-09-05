package gtach

import (
	"bytes"
	"io"
	"testing"
)

func TestProtocol(t *testing.T) {
	for _, size := range []int{0, 1, maxPayload} {
		var b bytes.Buffer
		want := bytes.Repeat([]byte{0xff}, size)
		if err := writeMessage(&b, inputMessage, want); err != nil {
			t.Fatal(err)
		}
		kind, got, err := readMessage(&b)
		if err != nil || kind != inputMessage || !bytes.Equal(got, want) {
			t.Fatalf("round trip: %d %v", kind, err)
		}
	}
	if err := writeMessage(io.Discard, inputMessage, make([]byte, maxPayload+1)); err == nil {
		t.Fatal("oversize accepted")
	}
	for _, b := range [][]byte{{1}, {1, 0, 2, 0}, {1, 255, 255}} {
		if _, _, err := readMessage(bytes.NewReader(b)); err == nil {
			t.Fatal("malformed packet accepted")
		}
	}
}

func FuzzReadMessage(f *testing.F) {
	f.Add([]byte{1, 0, 1, 65})
	f.Add([]byte{255, 255, 255})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, p, err := readMessage(bytes.NewReader(b))
		if err == nil && len(p) > maxPayload {
			t.Fatal("limit exceeded")
		}
	})
}
