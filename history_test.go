package gtach

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestReplayLimit(t *testing.T) {
	const oneMiB = 1024 * 1024
	if ReplayLimit != oneMiB {
		t.Fatalf("replay limit = %d, want %d", ReplayLimit, oneMiB)
	}
	var h outputHistory
	// Fill via normal PTY-sized reads, then wrap and discard only the oldest bytes.
	chunk := bytes.Repeat([]byte("a"), 4096)
	for i := 0; i < oneMiB/len(chunk); i++ {
		h.append(chunk)
	}
	h.append([]byte("tail"))
	want := append(bytes.Repeat([]byte("a"), oneMiB-4), []byte("tail")...)
	if got := h.snapshot(); !bytes.Equal(got, want) {
		t.Fatalf("incorrect 1 MiB replay tail (length %d)", len(got))
	}
}

func TestOutputHistory(t *testing.T) {
	var h outputHistory
	var want []byte
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		b := make([]byte, rng.Intn(ReplayLimit*2))
		rng.Read(b)
		before := h.snapshot()
		saved := append([]byte(nil), before...)
		h.append(b)
		if !bytes.Equal(before, saved) {
			t.Fatal("mutated queued snapshot")
		}
		want = append(want, b...)
		if len(want) > ReplayLimit {
			want = want[len(want)-ReplayLimit:]
		}
		if got := h.snapshot(); !bytes.Equal(got, want) {
			t.Fatalf("iteration %d: history differs", i)
		}
		if len(h.data) > ReplayLimit {
			t.Fatal("history exceeds limit")
		}
	}
}
