package bootlog

import "time"

// DefaultMilestones is how long a fault may go unremarked, and DefaultRepeat
// how often it is remarked on after that.
//
// ESCALATING rather than periodic, because the file being written to is eMMC
// that cannot be replaced and every fault this records is open-ended: a device
// that never finds a controller, or never gets its speaker back, would
// otherwise spend a flash write every interval for as long as it stays that
// way — which is precisely when the fault lasts longest.
//
// One minute is past every ordinary restart. The successful reconnect measured
// on 2026-09-10 took 1m57s from a device that had to boot, and an in-place
// restart is quicker, so a healthy device writes nothing at all. Half-hourly
// after that is enough to show a fault is still open across a night without
// the file becoming the fault.
var DefaultMilestones = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
}

// DefaultRepeat is the interval once the milestones are spent.
const DefaultRepeat = 30 * time.Minute

// An Escalator decides when something that has been going wrong for a while is
// worth a line on /data.
//
// Pure, and separate from every loop it advises, so the cadence is tested
// without a network, a device, or waiting half an hour. What it gets wrong is
// silent either way: too eager and it wears the flash of every device that
// ever restarts, too shy and the fault it exists for leaves no trace again.
//
// The zero value is usable and is what every caller wants.
type Escalator struct {
	// Milestones and Repeat override the defaults. Leave both zero.
	Milestones []time.Duration
	Repeat     time.Duration

	// done is the position in the schedule; lines is how many were actually
	// written. They differ whenever a caller was away past several
	// milestones, and keeping them apart is what lets Reported() answer the
	// question it is asked — "was anybody told" — rather than "how far along
	// the schedule are we", which no caller wants.
	done  int
	lines int
	last  time.Duration
}

// Due reports whether elapsed has passed the next milestone, advancing when it
// has. Call it once per attempt, with how long the condition has held.
func (e *Escalator) Due(elapsed time.Duration) bool {
	marks := e.Milestones
	if marks == nil {
		marks = DefaultMilestones
	}
	repeat := e.Repeat
	if repeat <= 0 {
		repeat = DefaultRepeat
	}

	var next time.Duration
	if e.done < len(marks) {
		next = marks[e.done]
	} else {
		next = e.last + repeat
	}
	if elapsed < next {
		return false
	}
	// Every milestone already behind us is SPENT, not queued. A caller can be
	// away for a long time — a browse round is bounded but a blocking ALSA
	// open is not — and advancing one milestone per call would then pay out
	// the whole backlog as a burst of flash writes on the next few attempts,
	// which is exactly the cost the escalation exists to avoid. One line says
	// what a burst of four would.
	e.done++
	for e.done < len(marks) && marks[e.done] <= elapsed {
		e.done++
	}
	e.lines++
	e.last = elapsed
	return true
}

// Reported is how many lines this run of the fault has produced, which is what
// tells a recovery whether it is worth remarking on: a fault nobody heard
// about needs no all-clear.
func (e *Escalator) Reported() int { return e.lines }

// Reset returns the escalator to its zero state, for a condition that has
// cleared and may come back. Without it a device that reconnects and drops
// again would report the second outage on the cadence the first one reached —
// so a fault that recurs all night would be recorded once and then be as
// invisible as it was before any of this existed.
func (e *Escalator) Reset() {
	e.done = 0
	e.lines = 0
	e.last = 0
}
