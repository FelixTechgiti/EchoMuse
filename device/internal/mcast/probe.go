package mcast

import "time"

// The second way to become invisible, and the one the membership watcher above
// cannot see: the membership present, both endpoints healthy, and nothing from
// the link arriving at all (#142).
//
// # The instrument this replaces, and why it was wrong
//
// The first version SENT an mDNS query from an ephemeral port with the
// unicast-response (QU) bit set and counted who answered. That reasoning was
// about not disturbing the responders — it never binds 5353 — and it missed the
// platform this runs on.
//
// **On FireOS the replies cannot arrive.** They come from foreign unicast
// addresses to a port no firewall rule names; `-m state --state ESTABLISHED`
// does not match them, because the query went to 224.0.0.251 and the answer
// comes from 192.168.178.x, which is a different flow to conntrack; and the
// chain policy is DROP. So the probe measured the firewall and reported zero
// however healthy the network was.
//
// Measured on hardware 2026-09-12: the warning was in the log while that
// device's own `udp dpt:5353` rule had accepted **93,704 packets**. The device
// was never deaf. `device/tools/mdnsprobe` has the same design and the same
// blind spot, which is why the reading that opened #142 said "heard only
// itself" — that was an artefact, not a network fault.
//
// The lesson is in this repository already, one section along: "Advertised is
// not reachable — FireOS drops every inbound port." Every plane this project
// has is dialled BY the device, so nothing had ever needed an inbound rule, and
// an instrument that quietly needed one was written anyway.
//
// # What it does now
//
// It reads the packet counter on the firewall's own mDNS rule. That cannot be
// fooled by the firewall because it IS the firewall: a rule that accepted a
// packet counted it. It costs one exec, sends nothing, and asks nothing of
// anybody else's network — where the old probe asked every host on the link to
// answer, every cycle, for ever.
//
// # Deaf is not the same as unheard
//
// Announcements go out unprompted, so a responder that hears nothing still
// advertises, and a device can be deaf and listed at once — measured the same
// day, when this warning and the controller's "every enabled endpoint is
// visible" were both true in the same minute. This package measures one
// direction and says so; visibility is `em_mdnsscan`'s question.

const (
	// MDNSPort is the rule whose counter is read.
	MDNSPort = "5353"

	// Iface is where mDNS arrives on this hardware. The rule is per-interface,
	// so this has to agree with what `internal/netfilter` inserts.
	Iface = "wlan0"

	// HealthyInterval / SilentInterval are the adaptive cadence. Reading a
	// counter is one exec and puts nothing on the network, so the only cost is
	// the wake-up; five minutes while healthy, a minute once it looks quiet, so
	// that an outage is dated from its recovery to the minute.
	HealthyInterval = 5 * time.Minute
	SilentInterval  = 60 * time.Second

	// ProbeMisses is how many consecutive windows with no new packet make a
	// device deaf. Two, so a single quiet window on a sleepy network is not an
	// alarm — mDNS chatter on an ordinary LAN runs about a packet a second, so
	// two five-minute windows of absolute silence is a real fault.
	ProbeMisses = 2
)

// Reading is one sample of the counter.
type Reading struct {
	// Packets is the rule's lifetime total. Only its CHANGE is meaningful.
	Packets int64
	// Found says the rule was in the listing at all. A missing rule is not a
	// zero reading: it means the firewall is not in the state we believe, and
	// the sample says nothing about the network.
	Found bool
	// Err is set when the listing could not be read. Failure to look is not
	// evidence of absence — the rule the membership watcher and the wake-word
	// reconcile both follow.
	Err error
}

// Usable reports whether this sample can be compared against another.
func (r Reading) Usable() bool { return r.Err == nil && r.Found }

// Event is a transition worth a log line. Nothing is logged in between: a
// healthy device writes one line when it goes deaf and one when it comes back,
// and a device that has never been deaf writes nothing at all.
type Event int

const (
	EventNone Event = iota
	// EventDeaf — no mDNS packet arrived for the whole of the miss window.
	EventDeaf
	// EventHeard — packets are arriving again. Carries how long the gap was,
	// which is the number #142 is open for.
	EventHeard
)

// Tracker turns a series of counter samples into those two transitions. Pure,
// so the whole decision is host-testable and only the exec is not.
type Tracker struct {
	// Misses is how many consecutive silent windows declare deafness.
	Misses int

	have      bool  // a comparable baseline exists
	last      int64 // the counter as of the previous usable sample
	misses    int
	deaf      bool
	silentAt  time.Time // when the first silent window of this run was sampled
	lastHeard time.Time
	lastDelta int64
	episodes  int
	resets    int
}

// Observe folds one sample in and reports a transition, if there was one.
//
// The duration reported with EventHeard is measured from the FIRST silent
// sample, not from the one that crossed the miss threshold: the device was
// already quiet then, and the threshold only governs when we are willing to
// say so.
func (t *Tracker) Observe(now time.Time, r Reading) (Event, time.Duration) {
	if t.Misses <= 0 {
		t.Misses = ProbeMisses
	}
	if !r.Usable() {
		return EventNone, 0
	}
	if !t.have {
		t.have, t.last = true, r.Packets
		return EventNone, 0
	}

	// A counter that went DOWN means the rule was re-inserted, which this
	// firmware does on every config push and every repair — `internal/netfilter`
	// deletes and re-inserts rather than trusting `-C`. That is not silence, and
	// reading it as silence would report a fault every time somebody saved a
	// setting. Re-baseline and wait for the next window.
	if r.Packets < t.last {
		t.last = r.Packets
		t.resets++
		return EventNone, 0
	}

	if r.Packets > t.last {
		t.lastDelta = r.Packets - t.last
		t.last = r.Packets
		t.misses = 0
		t.lastHeard = now
		if t.deaf {
			t.deaf = false
			out := now.Sub(t.silentAt)
			t.silentAt = time.Time{}
			return EventHeard, out
		}
		return EventNone, 0
	}

	if t.misses == 0 {
		t.silentAt = now
	}
	t.misses++
	if t.deaf || t.misses < t.Misses {
		return EventNone, 0
	}
	t.deaf = true
	t.episodes++
	return EventDeaf, now.Sub(t.silentAt)
}

// Deaf reports the current state, which is what chooses the cadence.
func (t *Tracker) Deaf() bool { return t.deaf }

// Episodes is how many times this device has gone deaf since it started — the
// frequency half of what #142 asks for; the duration half rides EventHeard.
func (t *Tracker) Episodes() int { return t.episodes }

// LastHeard is when mDNS last arrived, and how many packets that window
// carried. Zero time means the counter has not moved since this firmware
// started, which on a device that has just booted is not a fault.
func (t *Tracker) LastHeard() (time.Time, int64) { return t.lastHeard, t.lastDelta }

// Interval is the cadence the current state calls for.
func (t *Tracker) Interval() time.Duration {
	if t.deaf {
		return SilentInterval
	}
	return HealthyInterval
}
