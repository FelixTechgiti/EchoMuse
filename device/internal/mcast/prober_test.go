package mcast

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/wilbowes/EchoMuse/internal/logrelay"
)

// capture swaps the process log destination for the length of fn. The wording
// is the deliverable here — this instrument's entire output is two log lines —
// so it is asserted rather than assumed.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	flags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(old)
		log.SetFlags(flags)
	}()
	fn()
	return buf.String()
}

func deafProber(ask func() Reading) *Prober {
	return &Prober{Ask: ask, Active: func() bool { return true }}
}

// THE contract that makes this instrument worth anything. The device is
// perfectly reachable over unicast throughout the fault, so nothing else in the
// system reports a thing; the only person who can see it is somebody reading
// their Home Assistant log, and the line gets there by matching the relay's
// failure markers. Wording this line without one of them leaves the measurement
// on a RAM-backed file on a device with no remote access.
func TestTheDeafLineReachesTheController(t *testing.T) {
	p := deafProber(func() Reading { return Reading{Self: 4} })
	now := time.Now()
	out := capture(t, func() {
		p.Tick(now)
		p.Tick(now.Add(time.Minute))
	})
	if out == "" {
		t.Fatal("two silent probes logged nothing")
	}
	level, fwd := logrelay.Classify(out)
	if !fwd || level != logrelay.LevelWarn {
		t.Fatalf("the deaf line is not relayed as a warning (level=%q fwd=%v):\n%s",
			level, fwd, out)
	}
}

// And the all-clear has to reach it too, or the log holds every onset and no
// recovery — which reports every outage as still running.
func TestTheRecoveryLineReachesTheController(t *testing.T) {
	peers := 0
	p := deafProber(func() Reading { return Reading{Peers: peers, Self: 4} })
	now := time.Now()
	p.Tick(now)
	p.Tick(now.Add(time.Minute))
	peers = 6
	out := capture(t, func() { p.Tick(now.Add(31 * time.Minute)) })
	level, fwd := logrelay.Classify(out)
	if !fwd {
		t.Fatalf("the recovery line is not relayed at all:\n%s", out)
	}
	if level != logrelay.LevelInfo {
		t.Errorf("the recovery line is relayed as %q; it is not a failure", level)
	}
	// The duration is the number #142 is open for. A line that only said
	// "working again" would date the recovery and lose the outage.
	if !strings.Contains(out, "31m0s") {
		t.Errorf("the recovery line does not carry the outage length:\n%s", out)
	}
}

// A device with both endpoints off has nothing to be invisible with, and a
// probe on its behalf puts a question on somebody's network for a feature they
// switched off. Worse, counting the result would report every idle device as
// deaf and then date an "outage" that was somebody flipping a switch back.
func TestNothingIsProbedWhileNothingIsAdvertised(t *testing.T) {
	asked := 0
	p := &Prober{
		Ask:    func() Reading { asked++; return Reading{} },
		Active: func() bool { return false },
	}
	now := time.Now()
	out := capture(t, func() {
		for i := 0; i < 10; i++ {
			p.Tick(now.Add(time.Duration(i) * time.Minute))
		}
	})
	if asked != 0 {
		t.Errorf("%d probes were sent with both endpoints off", asked)
	}
	if p.Tracker.Deaf() {
		t.Error("an idle device was recorded as deaf")
	}
	if out != "" {
		t.Errorf("an idle device logged:\n%s", out)
	}
}

// A deaf device must log once, not once per probe: the relay is rationed at six
// lines a minute and the control plane is the liveness channel. This is the
// same shape as #404 with a different payload.
func TestAnOutageLogsOnceHoweverLongItLasts(t *testing.T) {
	p := deafProber(func() Reading { return Reading{} })
	now := time.Now()
	out := capture(t, func() {
		for i := 0; i < 60; i++ {
			p.Tick(now.Add(time.Duration(i) * time.Minute))
		}
	})
	if n := strings.Count(out, "cannot hear"); n != 1 {
		t.Errorf("an hour of deafness produced %d warnings", n)
	}
}

// Tick returns the cadence so a caller cannot hold the healthy interval through
// an outage — which would date every recovery to the nearest five minutes.
func TestTickReportsTheCadenceTheStateCallsFor(t *testing.T) {
	p := deafProber(func() Reading { return Reading{} })
	now := time.Now()
	if d := p.Tick(now); d != HealthyInterval {
		t.Errorf("first silent probe asked for %s, want %s", d, HealthyInterval)
	}
	if d := p.Tick(now.Add(time.Minute)); d != SilentInterval {
		t.Errorf("a deaf device asked for %s, want %s", d, SilentInterval)
	}
}

// Somebody reading the warning has neither reading in front of them. "Never
// heard anybody since boot" and "heard six hosts a minute ago" are different
// situations and want different next steps.
func TestTheDeafLineSaysWhenTheLinkWasLastAudible(t *testing.T) {
	p := deafProber(func() Reading { return Reading{} })
	now := time.Now()
	fresh := capture(t, func() { p.Tick(now); p.Tick(now.Add(time.Minute)) })
	if !strings.Contains(fresh, "since this firmware started") {
		t.Errorf("a device that never heard anybody does not say so:\n%s", fresh)
	}

	peers := 6
	q := deafProber(func() Reading { return Reading{Peers: peers} })
	q.Tick(now)
	peers = 0
	seen := capture(t, func() {
		q.Tick(now.Add(time.Minute))
		q.Tick(now.Add(2 * time.Minute))
	})
	if !strings.Contains(seen, "Last heard") || !strings.Contains(seen, "6 host(s)") {
		t.Errorf("the deaf line drops what was heard before:\n%s", seen)
	}
}
