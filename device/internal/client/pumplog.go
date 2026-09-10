package client

import (
	"sync"
	"time"
)

// Rate limiting for the pump error log.
//
// A pump error was a rare event until 2026-09-10, when the speaker stopped
// gating main(): a device whose PCM Android will not release now runs happily
// and REFUSES every period with `speaker not open yet`. That is the intended
// trade — a silent speaker instead of a dark device — but the refusal is per
// PERIOD, so a source that keeps feeding produces ~23 log lines a second for
// as long as it plays.
//
// `/tmp` is RAM-backed on this device, and the same log is the only account of
// why anything failed. Filling it costs memory on a board sharing 512MB with
// Android AND evicts the lines somebody would need — the identical hazard the
// ambient-light driver's darkness message posed, where a 1Hz poll reached
// 609KB of one repeated line and left no room for the evidence.
//
// So the log says it once and then says it again at a cadence a person can
// read. Not dropped entirely: a device that is reachable and silent has to be
// diagnosable, and this line is the whole diagnosis.
type errThrottle struct {
	mu     sync.Mutex
	last   time.Time
	missed int
}

// pumpLogInterval is how often a repeating pump error is allowed through.
// Seconds rather than minutes because this is a live fault somebody is
// watching a log for, and one line per 5s is legible where 23 per second is
// not.
const pumpLogInterval = 5 * time.Second

// allow reports whether this occurrence should be logged, and how many were
// suppressed since the last one that was. The count is what keeps a throttled
// line honest: "and 114 more" is a rate, where a bare repeat could be a single
// straggler.
func (t *errThrottle) allow(now time.Time, every time.Duration) (bool, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.last.IsZero() && now.Sub(t.last) < every {
		t.missed++
		return false, 0
	}
	missed := t.missed
	t.missed = 0
	t.last = now
	return true, missed
}

// andMore renders the suppressed count, or nothing when none were.
func andMore(missed int) string {
	if missed == 0 {
		return ""
	}
	return " (and " + itoa(missed) + " more suppressed)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
