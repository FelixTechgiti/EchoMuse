package bootlog

import (
	"testing"
	"time"
)

// The whole point of the escalation is that a device which restarts normally
// writes NOTHING. Every ordinary reconnect measured on this fleet finishes
// well inside a minute; if this bar drops, every device in the field starts
// spending flash writes on its own healthy behaviour.
func TestQuietBelowTheFirstMilestone(t *testing.T) {
	var e Escalator
	for _, d := range []time.Duration{0, time.Second, 30 * time.Second, 59 * time.Second} {
		if e.Due(d) {
			t.Fatalf("reported at %s — a healthy restart must write nothing", d)
		}
	}
	if e.Reported() != 0 {
		t.Fatalf("Reported() = %d, want 0", e.Reported())
	}
}

// Called once per attempt, at whatever cadence the caller loops — so it must
// fire exactly once per milestone however often it is asked.
func TestOneLinePerMilestone(t *testing.T) {
	var e Escalator
	fired := 0
	// A browse round is ~15s; walk an hour of them.
	for elapsed := 0 * time.Second; elapsed <= time.Hour; elapsed += 15 * time.Second {
		if e.Due(elapsed) {
			fired++
		}
	}
	// 1, 5, 15, 30 minutes, then 30-minutely: the 60-minute mark.
	if fired != 5 {
		t.Fatalf("fired %d times in an hour, want 5 (1/5/15/30min + one repeat)", fired)
	}
	if e.Reported() != fired {
		t.Fatalf("Reported() = %d, want %d", e.Reported(), fired)
	}
}

// A caller that only manages to ask once, very late, must not then emit the
// whole backlog of missed milestones in one burst.
func TestOneLatecallIsOneLine(t *testing.T) {
	var e Escalator
	if !e.Due(4 * time.Hour) {
		t.Fatal("four hours is past every milestone and must report")
	}
	if e.Due(4 * time.Hour) {
		t.Fatal("the same instant reported twice")
	}
	if e.Reported() != 1 {
		t.Fatalf("Reported() = %d, want 1", e.Reported())
	}
}

// Past the milestones the cadence is measured from the LAST line, not from
// zero — otherwise a caller polling irregularly drifts off the schedule.
func TestRepeatIsMeasuredFromTheLastLine(t *testing.T) {
	e := Escalator{Milestones: []time.Duration{time.Minute}, Repeat: 10 * time.Minute}
	if !e.Due(time.Minute) {
		t.Fatal("first milestone missed")
	}
	// The caller was busy and next asks at 9m — still inside the repeat.
	if e.Due(9 * time.Minute) {
		t.Fatal("reported before the repeat interval had passed")
	}
	if !e.Due(11 * time.Minute) {
		t.Fatal("repeat did not fire")
	}
	// And the next one is 10 minutes after THAT line, not after 11m nominal.
	if e.Due(20 * time.Minute) {
		t.Fatal("repeat measured from the schedule rather than from the last line")
	}
	if !e.Due(21 * time.Minute) {
		t.Fatal("second repeat did not fire")
	}
}

// A fault that clears and returns is a second fault. Without the reset it
// would be reported on the cadence the first one had reached — so a device
// flapping all night would leave one line and then nothing.
func TestResetRestoresTheFullCadence(t *testing.T) {
	var e Escalator
	for elapsed := 0 * time.Second; elapsed <= 40*time.Minute; elapsed += 15 * time.Second {
		e.Due(elapsed)
	}
	if e.Reported() == 0 {
		t.Fatal("nothing reported in forty minutes")
	}
	e.Reset()
	if e.Reported() != 0 {
		t.Fatalf("Reported() = %d after Reset, want 0", e.Reported())
	}
	if e.Due(59 * time.Second) {
		t.Fatal("a reset escalator reported inside the first minute")
	}
	if !e.Due(time.Minute) {
		t.Fatal("a reset escalator did not report at the first milestone")
	}
}
