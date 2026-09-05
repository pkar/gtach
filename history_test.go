package gtach

import (
	"bytes"
	"math/rand"
	"testing"
)

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
