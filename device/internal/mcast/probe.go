package mcast

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// The second way to become invisible, and the one the membership watcher above
// cannot see.
//
// # The measurement this exists to repeat
//
// Taken 2026-09-12 with `device/tools/mdnsprobe` running ON the device, at the
// same minute as a controller-side scan (#142):
//
//	on the device:    _spotify-connect._tcp.local -> 328 bytes from 192.168.178.140
//	                  (itself, four answers, complete and correct — nothing else)
//	from controller:  "Spotify Connect was not seen from this device, while
//	                   6 other host(s) did answer."
//
// **The device does not hear the six hosts the controller hears.** Its own
// responder answers its own query, which is local delivery rather than the
// air. One mechanism explains the whole symptom: the controller's browse is a
// multicast QUERY, it never arrives, so nothing answers and the device is in
// no picker — while every reading taken on the device says the endpoints are
// healthy, because they are.
//
// And the membership was PRESENT for that reading. `Watcher` would have looked,
// found 224.0.0.251 on wlan0, and correctly done nothing. Anyone reading the
// watcher as "the mDNS fix" is wrong about half the outages.
//
// # Why this only measures
//
// The mechanism is a layer below anything this project controls — the leads are
// the AP's IGMP snooping, multicast-to-unicast conversion, and the DTIM path,
// and the device is on 5GHz where all three are most likely. Every remedy
// available here is a guess: a forced leave/rejoin, an endpoint restart, a band
// change. `wifi.Describe` is the precedent in this tree — it rides the
// `no controller` lines and nothing acts on it, because the repair for a zombie
// association is to drop the WiFi of a device whose only management path is
// that WiFi. Instrument first.
//
// What is missing is not another theory, it is DURATION and FREQUENCY with
// nobody present. That is what decides between the remaining leads, and it is
// what this writes down.
//
// # Why it never binds 5353
//
// `mdnsprobe` binds an ephemeral port and sets the unicast-response (QU) bit,
// and the comment at the top of it says why: whether two responders on one host
// interfere is part of the question being asked, so the instrument must not
// become a third. The same applies with more force in-process — librespot and
// shairport-sync are children of this program, and a parent that took 5353
// first could stop either from binding it at all. A detector that breaks mDNS
// to measure mDNS is worse than no detector.

const (
	// ProbeService is the broadest question mDNS has: every responder on the
	// link answers a service enumeration. The point is "is ANYBODY out there",
	// so asking about a specific service would measure that service's
	// popularity as well as this device's hearing.
	ProbeService = "_services._dns-sd._udp.local"

	// MDNSPort and the group are where the question goes. The reply comes back
	// to our own ephemeral port because of the QU bit.
	MDNSPort = 5353

	// ProbeWait is how long to collect replies. mDNS responders are required to
	// delay a shared-record answer by 20-120ms to spread the burst, and a busy
	// link keeps trickling for a while after that; a second is long past the
	// spec's window and still short enough to sit inside a ticker.
	ProbeWait = 1 * time.Second

	// HealthyInterval / SilentInterval are the adaptive cadence.
	//
	// A probe asks every host on the link to answer, so this is not free to
	// anybody else on that network — 1440 of them a day is a poor neighbour.
	// Five minutes while healthy is one query per 300s against responders that
	// announce more often than that unprompted.
	//
	// The moment it goes quiet the question changes: how LONG was it deaf is
	// the number #142 needs, and that is measured from the recovery, not from
	// the onset. So the silent cadence is the fast one. It costs nothing on
	// anybody else's network either, because a device that hears nothing is by
	// definition not being heard.
	HealthyInterval = 5 * time.Minute
	SilentInterval  = 60 * time.Second

	// ProbeMisses is how many consecutive silent probes make a device deaf. One
	// lost query is ordinary on WiFi and multicast is unacknowledged by design;
	// hearing something, by contrast, is unambiguous and clears immediately.
	ProbeMisses = 2
)

// Reading is one probe's result.
type Reading struct {
	// Peers is how many DISTINCT hosts answered that are not this device.
	// The whole measurement lives in the word "not": the fault reading above
	// had a complete, correct answer in it — from itself.
	Peers int
	// Self is how many replies came from one of this device's own addresses.
	// Reported because Self>0 with Peers==0 is the exact signature, and it
	// separates "the responders are dead" from "nothing on the link is heard".
	Self int
	// Err is set when the probe could not be carried out at all. Failure to
	// look is not evidence of absence — the same rule the membership watcher
	// and the wake-word reconcile follow — so a reading with an error never
	// counts as silence.
	Err error
}

// Heard reports whether this probe reached anybody else.
func (r Reading) Heard() bool { return r.Err == nil && r.Peers > 0 }

// Event is a transition worth a log line. Nothing is logged in between: a
// healthy device writes one line when it goes deaf and one when it comes back,
// and a device that has never been deaf writes nothing at all.
type Event int

const (
	EventNone Event = iota
	// EventDeaf — the device stopped hearing the link.
	EventDeaf
	// EventHeard — it started again. Carries how long it was out, which is the
	// number the issue is open for.
	EventHeard
)

// Tracker turns a series of readings into those two transitions. Pure, so the
// whole decision is host-testable and only the socket is not.
type Tracker struct {
	// Misses is how many consecutive silent readings declare deafness.
	Misses int

	misses    int
	deaf      bool
	silentAt  time.Time // when the first of the current run of silent probes was taken
	lastPeers int       // how many were heard the last time anything was
	lastHeard time.Time
	episodes  int
}

// Observe folds one reading in and reports a transition, if there was one.
//
// The duration reported with EventHeard is measured from the FIRST silent
// probe, not from the one that crossed the miss threshold. The device was
// already deaf then; the threshold only governs when we are willing to say so.
func (t *Tracker) Observe(now time.Time, r Reading) (Event, time.Duration) {
	if t.Misses <= 0 {
		t.Misses = ProbeMisses
	}
	if r.Err != nil {
		return EventNone, 0
	}
	if r.Peers > 0 {
		t.misses = 0
		t.lastPeers = r.Peers
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

// Episodes is how many times this device has gone deaf since it started. The
// frequency half of what #142 asks for; the duration half rides EventHeard.
func (t *Tracker) Episodes() int { return t.episodes }

// LastHeard is when anything other than this device was last heard, and how
// many answered then. Zero time means never — which on a device that has just
// started is not a fault, and is why the deaf line says "probed" rather than
// "lost".
func (t *Tracker) LastHeard() (time.Time, int) { return t.lastHeard, t.lastPeers }

// Interval is the cadence the current state calls for.
func (t *Tracker) Interval() time.Duration {
	if t.deaf {
		return SilentInterval
	}
	return HealthyInterval
}

// Query builds the mDNS question. Exported and pure because the one thing that
// can silently ruin this instrument is a malformed packet: every responder
// would ignore it, every probe would read zero, and the device would be
// reported deaf for ever while hearing perfectly.
//
// The QU bit (0x8000 on the class) asks for a unicast reply to our source port.
// It is what lets this bind an ephemeral port instead of 5353 — see the package
// comment — and it is the same choice mdnsprobe made for the same reason.
func Query(name string) ([]byte, error) {
	b := []byte{
		0, 0, // ID 0: mDNS matches on the question, not on an ID
		0, 0, // flags: a query, not truncated, no recursion
		0, 1, // one question
		0, 0, 0, 0, 0, 0,
	}
	for _, label := range strings.Split(strings.Trim(name, "."), ".") {
		if label == "" || len(label) > 63 {
			return nil, fmt.Errorf("mcast: %q is not a usable mDNS name", name)
		}
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0)       // root label
	b = append(b, 0, 12)   // QTYPE PTR
	b = append(b, 0x80, 1) // QCLASS IN, with the unicast-response bit
	return b, nil
}

// IsResponse reports whether a datagram is an mDNS response rather than
// something else that happened to arrive on our ephemeral port.
//
// Deliberately the whole of the parsing. What is being counted is that a packet
// from another host ARRIVED; its contents would say what that host runs, which
// is a different question and one nothing here asks. A full DNS parser would be
// a second place for this to fail silently.
func IsResponse(b []byte) bool {
	return len(b) >= 12 && b[2]&0x80 != 0
}

// SelfAddrs returns this host's own unicast addresses, for telling our
// responder's reply apart from the network's.
func SelfAddrs() map[string]bool {
	out := map[string]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			out[n.IP.String()] = true
		}
	}
	return out
}

// Ask sends one query and counts who answers. The socket half, kept as small as
// the decision above is large.
func Ask(service string, wait time.Duration, self map[string]bool) Reading {
	q, err := Query(service)
	if err != nil {
		return Reading{Err: err}
	}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		return Reading{Err: err}
	}
	defer c.Close()

	dst := &net.UDPAddr{IP: net.ParseIP(Group), Port: MDNSPort}
	if _, err := c.WriteToUDP(q, dst); err != nil {
		return Reading{Err: err}
	}
	if err := c.SetReadDeadline(time.Now().Add(wait)); err != nil {
		return Reading{Err: err}
	}

	peers := map[string]bool{}
	var mine int
	buf := make([]byte, 2048)
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			// A deadline is the ordinary end of a probe, not a failure to look:
			// zero replies IS the reading. Anything else means the socket went
			// away under us, and that must not be counted as silence.
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				break
			}
			return Reading{Err: err}
		}
		if !IsResponse(buf[:n]) {
			continue
		}
		ip := from.IP.String()
		if self[ip] {
			mine++
			continue
		}
		peers[ip] = true
	}
	return Reading{Peers: len(peers), Self: mine}
}
