package logrelay

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// maxPerWindow and window bound what reaches the control plane.
//
// Six lines a minute is enough to see a fault develop and is nothing beside
// the ~12 keepalive and RTT exchanges in the same minute. The bound exists
// because the failure this relay reports is usually a LOOP — a supervisor
// restarting a subprocess every minute, a pump refusing every period — so the
// interesting case is exactly the one that would otherwise flood.
const maxPerWindow = 6
const window = time.Minute

// queueDepth is how many lines may be waiting for the sender goroutine.
// Small: a backlog means the control plane is already struggling, which is
// when this must be quietest.
const queueDepth = 16

// Relay is an io.Writer that passes everything through to the real log
// destination and forwards a selection to the controller.
//
// **The forward is ASYNCHRONOUS and must stay so.** Sending takes `connMu` on
// the control client, and this Write can be reached from anywhere — including
// code already holding that mutex, since writeJSON logs its own failures. A
// direct call would deadlock the control plane the first time a send failed.
// So Write only enqueues, never blocks, and a goroutine does the sending, the
// same rule shadow.Scorer.Push follows for the mic goroutine.
type Relay struct {
	out  io.Writer
	ch   chan entry
	quit chan struct{}

	mu           sync.Mutex
	windowStart  time.Time
	sentInWindow int
	dropped      int

	now func() time.Time
}

type entry struct {
	level string
	line  string
}

// New wraps out and starts the sender. send is called from one goroutine and
// may block; nothing else waits on it.
func New(out io.Writer, send func(level, message string)) *Relay {
	r := &Relay{
		out:  out,
		ch:   make(chan entry, queueDepth),
		quit: make(chan struct{}),
		now:  time.Now,
	}
	go r.run(send)
	return r
}

// Stop ends the sender. Writes keep working; they simply stop forwarding.
func (r *Relay) Stop() { close(r.quit) }

func (r *Relay) run(send func(level, message string)) {
	for {
		select {
		case <-r.quit:
			return
		case e := <-r.ch:
			send(e.level, e.line)
		}
	}
}

// Write passes the bytes through and forwards what Classify selects.
//
// The pass-through happens FIRST and its result is what is returned: this is
// the process's log destination, and a relay that could swallow a line would
// be worse than no relay. Nothing below the pass-through can fail the write.
func (r *Relay) Write(p []byte) (int, error) {
	n, err := r.out.Write(p)
	line := string(bytes.TrimRight(p, "\n"))
	level, ok := Classify(line)
	if !ok {
		return n, err
	}
	if suffix, allowed := r.admit(); allowed {
		select {
		case r.ch <- entry{level: level, line: line + suffix}:
		default:
			// The sender is behind, which means the control plane is busy —
			// exactly when this should be quiet. Counted, not blocked.
			r.mu.Lock()
			r.dropped++
			r.mu.Unlock()
		}
	}
	return n, err
}

// admit applies the rate limit and returns the text describing what was
// dropped since the last line that got through.
//
// The count rides the NEXT line that is allowed rather than a message of its
// own: a separate "N suppressed" line would itself be traffic on the channel
// this is rationing, and it would arrive with nothing to attach it to.
func (r *Relay) admit() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.windowStart.IsZero() || now.Sub(r.windowStart) >= window {
		r.windowStart = now
		r.sentInWindow = 0
	}
	if r.sentInWindow >= maxPerWindow {
		r.dropped++
		return "", false
	}
	r.sentInWindow++
	if r.dropped == 0 {
		return "", true
	}
	d := r.dropped
	r.dropped = 0
	return " (+" + itoa(d) + " more not relayed)", true
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
