package mcast

import (
	"log"
	"strconv"
	"time"
)

// Prober runs the reachability probe on the adaptive cadence and writes the two
// transitions to the log. It is the instrument described in the package
// comment; nothing here repairs anything.
type Prober struct {
	// Sample reads the firewall's mDNS counter once. Injected so the cadence,
	// the gate and the wording are all host-testable without a firewall.
	Sample func() Reading
	// Active reports whether anything is advertising. Same gate the membership
	// watcher uses and for the same reason: a device with both endpoints off
	// has nothing to be invisible with, and probing it would put a question on
	// somebody's network on behalf of a feature they switched off.
	Active func() bool

	Tracker Tracker
}

// Tick performs one probe and logs a transition if there was one. Returns how
// long to wait before the next probe, so a caller can follow the adaptive
// cadence without duplicating the rule.
func (p *Prober) Tick(now time.Time) time.Duration {
	if p.Sample == nil {
		return HealthyInterval
	}
	if p.Active != nil && !p.Active() {
		// Not a reading. Feeding this to the tracker as silence would report
		// every idle device as deaf, and — worse — the recovery line would then
		// date an outage that was somebody turning a switch back on.
		return HealthyInterval
	}
	r := p.Sample()
	ev, out := p.Tracker.Observe(now, r)
	switch ev {
	case EventDeaf:
		last, delta := p.Tracker.LastHeard()
		// "cannot" is what makes the log relay forward this as a warning, and
		// it has to: the device is perfectly reachable over unicast the whole
		// time, so nothing else in the system reports anything at all.
		//
		// It says what was MEASURED and stops there. An earlier version ended
		// "so it is simply in no picker", which is an inference and was FALSE
		// on hardware: the device reported this while the controller's scan
		// answered "Every enabled endpoint is visible on the network".
		// Announcements go out unprompted and need no query to have arrived.
		log.Printf("[mcast] this device cannot hear the network — not one mDNS "+
			"packet has reached %s:%s in %s (%d consecutive windows). %s "+
			"Unicast is unaffected. Whether it is still VISIBLE is a separate "+
			"question: its own announcements may still be getting out, so read "+
			"the controller's network scan rather than assuming this one. "+
			"Episode #%d.",
			Iface, MDNSPort, out.Round(time.Second), p.Tracker.Misses,
			describeLastHeard(now, last, delta), p.Tracker.Episodes())
	case EventHeard:
		// The all-clear carries the duration, which is the whole point of the
		// pair — a line that only said "working again" would date the recovery
		// and lose the outage.
		_, delta := p.Tracker.LastHeard()
		log.Printf("[mcast] hears the network again: %d mDNS packet(s) arrived "+
			"after %s of silence.", delta, out.Round(time.Second))
	}
	return p.Tracker.Interval()
}

// describeLastHeard says when mDNS last arrived, or that it never has since
// boot. A device that has just started with a counter that has not moved is a
// different situation from one that was busy a minute ago, and the deaf line is
// read by somebody who has neither in front of them.
func describeLastHeard(now, last time.Time, delta int64) string {
	if last.IsZero() {
		return "The counter has not moved since this firmware started."
	}
	return "Last traffic " + now.Sub(last).Round(time.Second).String() +
		" ago, " + strconv.FormatInt(delta, 10) + " packet(s) in that window."
}

// Run probes until done is closed, on whatever cadence the state calls for.
// A timer rather than a ticker, because the interval changes with the state and
// a ticker would keep the healthy cadence through an outage.
//
// A nil done channel is never ready, which is how the firmware runs it: there
// is nothing to stop this short of the process exiting.
func (p *Prober) Run(done <-chan struct{}) {
	// The first probe waits a cadence rather than firing at startup: the radio
	// has just associated, the endpoints are still being started by the first
	// config push, and a probe into that reports a fault that is really a boot.
	delay := SilentInterval
	t := time.NewTimer(delay)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-t.C:
			t.Reset(p.Tick(now))
		}
	}
}
