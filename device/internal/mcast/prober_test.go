package mcast

import (
	"bytes"
	"errors"
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
	old, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() { log.SetOutput(old); log.SetFlags(flags) }()
	fn()
	return buf.String()
}

// counting returns a prober whose counter advances by step each sample.
func counting(step *int64) *Prober {
	var n int64
	return &Prober{
		Active: func() bool { return true },
		Sample: func() Reading { n += *step; return Reading{Packets: n, Found: true} },
	}
}

// THE regression. On hardware 2026-09-12 the old active probe reported this
// device deaf while its own 5353 rule had accepted 93,704 packets — it was
// measuring the firewall, which drops the unicast replies it asked for. A
// moving counter means traffic is arriving, and nothing may call that deaf.
func TestAMovingCounterIsNeverDeaf(t *testing.T) {
	step := int64(300) // ~a packet a second across a five-minute window
	p := counting(&step)
	now := time.Now()
	out := capture(t, func() {
		for i := 0; i < 40; i++ {
			p.Tick(now.Add(time.Duration(i) * HealthyInterval))
		}
	})
	if p.Tracker.Deaf() {
		t.Error("a device receiving 300 packets per window was reported deaf")
	}
	if out != "" {
		t.Errorf("a healthy device logged:\n%s", out)
	}
}

func TestAStuckCounterIsDeafAfterTheMissWindow(t *testing.T) {
	var tr Tracker
	now := time.Now()
	base := Reading{Packets: 93704, Found: true}
	tr.Observe(now, base) // baseline
	if ev, _ := tr.Observe(now.Add(5*time.Minute), base); ev != EventNone {
		t.Fatal("one silent window already declared deafness")
	}
	ev, out := tr.Observe(now.Add(10*time.Minute), base)
	if ev != EventDeaf {
		t.Fatalf("two silent windows produced %v", ev)
	}
	if out != 5*time.Minute {
		t.Errorf("the outage measured %s, want 5m — dated from the FIRST silent "+
			"sample, not from the one that crossed the threshold", out)
	}
}

// The one way a counter lies. Every config push and every firewall repair
// deletes and re-inserts the rule, which zeroes it. Reading that as silence
// would report a fault every time somebody saved a setting.
func TestARuleReinsertionIsNotSilence(t *testing.T) {
	var tr Tracker
	now := time.Now()
	tr.Observe(now, Reading{Packets: 93704, Found: true})
	for i := 1; i <= 6; i++ {
		// Counter restarts from zero and climbs again.
		ev, _ := tr.Observe(now.Add(time.Duration(i)*time.Minute),
			Reading{Packets: int64(i * 40), Found: true})
		if ev != EventNone {
			t.Fatalf("a re-inserted rule produced %v at step %d", ev, i)
		}
	}
	if tr.Deaf() {
		t.Error("a rule re-insertion was counted as an outage")
	}
}

// Failure to look is not evidence of absence, and a MISSING rule is not a zero
// reading — it means the firewall is not in the state we believe.
func TestAnUnreadableOrAbsentRuleIsNotSilence(t *testing.T) {
	for _, r := range []Reading{
		{Err: errors.New("no iptables on this device")},
		{Packets: 0, Found: false},
	} {
		var tr Tracker
		now := time.Now()
		tr.Observe(now, Reading{Packets: 10, Found: true})
		for i := 1; i <= 10; i++ {
			if ev, _ := tr.Observe(now.Add(time.Duration(i)*time.Minute), r); ev != EventNone {
				t.Fatalf("%+v produced %v", r, ev)
			}
		}
		if tr.Deaf() {
			t.Errorf("%+v was counted as an outage", r)
		}
	}
}

// THE contract that makes this instrument worth anything: the device is
// reachable over unicast throughout, so the only person who can see the fault
// is somebody reading their Home Assistant log, and the line gets there by
// matching the relay's failure markers.
func TestTheDeafLineReachesTheController(t *testing.T) {
	var step int64
	p := counting(&step) // never advances
	now := time.Now()
	out := capture(t, func() {
		p.Tick(now)
		p.Tick(now.Add(5 * time.Minute))
		p.Tick(now.Add(10 * time.Minute))
	})
	if out == "" {
		t.Fatal("a stuck counter logged nothing")
	}
	level, fwd := logrelay.Classify(out)
	if !fwd || level != logrelay.LevelWarn {
		t.Fatalf("the deaf line is not relayed as a warning (level=%q fwd=%v):\n%s",
			level, fwd, out)
	}
}

// And the all-clear too, or the log holds every onset and no recovery — which
// reads as every outage still running.
func TestTheRecoveryLineReachesTheControllerWithTheDuration(t *testing.T) {
	var step int64
	p := counting(&step)
	now := time.Now()
	p.Tick(now)
	p.Tick(now.Add(5 * time.Minute))
	p.Tick(now.Add(10 * time.Minute)) // deaf here
	step = 412
	out := capture(t, func() { p.Tick(now.Add(35 * time.Minute)) })
	level, fwd := logrelay.Classify(out)
	if !fwd {
		t.Fatalf("the recovery line is not relayed at all:\n%s", out)
	}
	if level != logrelay.LevelInfo {
		t.Errorf("the recovery line is relayed as %q; it is not a failure", level)
	}
	if !strings.Contains(out, "30m0s") {
		t.Errorf("the recovery line does not carry the outage length:\n%s", out)
	}
	if !strings.Contains(out, "412") {
		t.Errorf("the recovery line does not say how much arrived:\n%s", out)
	}
}

// Hearing and being heard are two directions, and the line must not infer one
// from the other. Measured on hardware: the device logged the deaf line while
// the controller's scan answered "Every enabled endpoint is visible".
func TestTheDeafLineDoesNotClaimTheDeviceIsInvisible(t *testing.T) {
	var step int64
	p := counting(&step)
	now := time.Now()
	out := capture(t, func() {
		p.Tick(now)
		p.Tick(now.Add(5 * time.Minute))
		p.Tick(now.Add(10 * time.Minute))
	})
	for _, claim := range []string{"in no picker", "not visible", "invisible"} {
		if strings.Contains(strings.ToLower(out), claim) {
			t.Errorf("the deaf line asserts %q, which it does not measure:\n%s", claim, out)
		}
	}
	if !strings.Contains(out, "scan") {
		t.Errorf("the deaf line does not send the reader to the scan that "+
			"answers visibility:\n%s", out)
	}
}

// A device with both endpoints off has nothing to be invisible with, and
// counting the result would report every idle device as deaf.
func TestNothingIsSampledWhileNothingIsAdvertised(t *testing.T) {
	sampled := 0
	p := &Prober{
		Sample: func() Reading { sampled++; return Reading{Found: true} },
		Active: func() bool { return false },
	}
	now := time.Now()
	out := capture(t, func() {
		for i := 0; i < 10; i++ {
			p.Tick(now.Add(time.Duration(i) * time.Minute))
		}
	})
	if sampled != 0 {
		t.Errorf("%d samples taken with both endpoints off", sampled)
	}
	if p.Tracker.Deaf() || out != "" {
		t.Errorf("an idle device was recorded as deaf or logged:\n%s", out)
	}
}

// A deaf device logs once, not once per window: the relay is rationed at six
// lines a minute on the liveness channel. Same shape as #404.
func TestAnOutageLogsOnceHoweverLongItLasts(t *testing.T) {
	var step int64
	p := counting(&step)
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
// an outage, which would date every recovery to the nearest five minutes.
func TestTickReportsTheCadenceTheStateCallsFor(t *testing.T) {
	var step int64
	p := counting(&step)
	now := time.Now()
	if d := p.Tick(now); d != HealthyInterval {
		t.Errorf("the baseline sample asked for %s, want %s", d, HealthyInterval)
	}
	p.Tick(now.Add(5 * time.Minute))
	if d := p.Tick(now.Add(10 * time.Minute)); d != SilentInterval {
		t.Errorf("a deaf device asked for %s, want %s", d, SilentInterval)
	}
}

func TestEpisodesCountsEachOutageSeparately(t *testing.T) {
	var tr Tracker
	now := time.Now()
	var n int64 = 1000
	step := func(adv int64) {
		now = now.Add(time.Minute)
		n += adv
		tr.Observe(now, Reading{Packets: n, Found: true})
	}
	step(0) // baseline
	for i := 0; i < 2; i++ {
		step(0)
		step(0)
		step(50)
	}
	if tr.Episodes() != 2 {
		t.Errorf("%d episodes for two outages", tr.Episodes())
	}
}

// The cadence is what an outage's resolution depends on.
func TestTheSilentCadenceIsTheFastOne(t *testing.T) {
	if SilentInterval >= HealthyInterval {
		t.Fatalf("silent=%s is not faster than healthy=%s", SilentInterval, HealthyInterval)
	}
}
