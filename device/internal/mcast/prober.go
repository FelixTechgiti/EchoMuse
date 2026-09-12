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
	// Ask performs one probe. Injected so the cadence, the gate and the
	// wording are all host-testable without a network.
	Ask func() Reading
	// Active reports whether anything is advertising. Same gate the membership
	// watcher uses and for the same reason: a device with both endpoints off
	// has nothing to be invisible with, and probing it would put a question on
	// somebody's network on behalf of a feature they switched off.
	Active func() bool

	Tracker Tracker

	// Service is only used in the log line, so the reader knows what was asked.
	Service string
}

// Tick performs one probe and logs a transition if there was one. Returns how
// long to wait before the next probe, so a caller can follow the adaptive
// cadence without duplicating the rule.
func (p *Prober) Tick(now time.Time) time.Duration {
	if p.Ask == nil {
		return HealthyInterval
	}
	if p.Active != nil && !p.Active() {
		// Not a reading. Feeding this to the tracker as silence would report
		// every idle device as deaf, and — worse — the recovery line would then
		// date an outage that was somebody turning a switch back on.
		return HealthyInterval
	}
	if p.Service == "" {
		p.Service = ProbeService
	}
	r := p.Ask()
	ev, out := p.Tracker.Observe(now, r)
	switch ev {
	case EventDeaf:
		last, peers := p.Tracker.LastHeard()
		// "cannot" is what makes the log relay forward this as a warning, and
		// it has to: the device is perfectly reachable over unicast the whole
		// time, so nothing else in the system reports anything at all. Someone
		// reading their Home Assistant log is the only person who can see this.
		log.Printf("[mcast] this device cannot hear any other host on the "+
			"network — %d probe(s) for %s answered only by itself (%d reply/"+
			"replies). %s Unicast is unaffected, so it stays reachable and is "+
			"simply in no picker. Episode #%d.",
			p.Tracker.Misses, p.Service, r.Self,
			describeLastHeard(now, last, peers), p.Tracker.Episodes())
	case EventHeard:
		// The all-clear carries the duration, which is the whole point of the
		// pair — a line that only said "working again" would date the recovery
		// and lose the outage.
		log.Printf("[mcast] hears the network again: %d other host(s) answered "+
			"after %s of silence.", r.Peers, out.Round(time.Second))
	}
	return p.Tracker.Interval()
}

// describeLastHeard says when the link was last audible, or that it never has
// been. A device that has just started and has never heard anybody is a
// different situation from one that heard six hosts a minute ago, and the deaf
// line is read by somebody who has neither in front of them.
func describeLastHeard(now, last time.Time, peers int) string {
	if last.IsZero() {
		return "Nothing else has been heard since this firmware started."
	}
	return "Last heard " + now.Sub(last).Round(time.Second).String() +
		" ago, when " + strconv.Itoa(peers) + " host(s) answered."
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
