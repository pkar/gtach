package gtach

// ReplayLimit is the maximum number of recent PTY bytes retained when Replay is
// enabled. Replay is raw output, not a terminal screen snapshot.
const ReplayLimit = 1024 * 1024

// outputHistory is a bounded ring, guarded by Session.mu. Snapshots own their
// storage so subsequent output cannot mutate a client's queued replay.
type outputHistory struct {
	data []byte
	next int
}

func (h *outputHistory) append(b []byte) {
	if len(b) >= ReplayLimit {
		h.data = append(h.data[:0], b[len(b)-ReplayLimit:]...)
		h.next = 0
		return
	}
	if len(h.data) < ReplayLimit {
		n := min(len(b), ReplayLimit-len(h.data))
		h.data = append(h.data, b[:n]...)
		b = b[n:]
	}
	if len(b) == 0 {
		return
	}
	n := copy(h.data[h.next:], b)
	copy(h.data, b[n:])
	h.next = (h.next + len(b)) % ReplayLimit
}

func (h *outputHistory) snapshot() []byte {
	b := make([]byte, 0, len(h.data))
	b = append(b, h.data[h.next:]...)
	return append(b, h.data[:h.next]...)
}
