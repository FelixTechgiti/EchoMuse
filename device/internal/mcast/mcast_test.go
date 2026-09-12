package mcast

import (
	"errors"
	"testing"
	"time"
)

// Read off the device 2026-09-12, WHILE IT WAS INVISIBLE. wlan0 holds only
// 224.0.0.1, which every interface joins; 224.0.0.251 is absent.
const procBroken = `Idx	Device    : Count Querier	Group    Users Timer	Reporter
1	lo        :     1      V3
				010000E0     1 0:00000000		0
9	wlan0     :     1      V3
				010000E0     1 0:00000000		0
10	p2p0      :     1      V3
				010000E0     1 0:00000000		0
`

// The same device seconds after both endpoints were restarted, when the
// controller's scan went from "not being heard" to "Every enabled endpoint is
// visible". Two users: librespot and shairport-sync.
const procHealthy = `Idx	Device    : Count Querier	Group    Users Timer	Reporter
1	lo        :     1      V3
				010000E0     1 0:00000000		0
9	wlan0     :     2      V3
				FB0000E0     2 0:00000000		0
				010000E0     1 0:00000000		0
10	p2p0      :     1      V3
				010000E0     1 0:00000000		0
`

func TestJoinedReadsTheRealCapturesBothWays(t *testing.T) {
	if Joined(procBroken, "wlan0", Group) {
		t.Error("the capture taken while the device was invisible must not " +
			"read as joined — that is the whole fault")
	}
	if !Joined(procHealthy, "wlan0", Group) {
		t.Error("the capture taken while the device was visible must read as joined")
	}
}

// The group belongs to an INTERFACE, and the file lists several. Matching
// anywhere in the file would read p2p0's or lo's membership as wlan0's.
func TestJoinedIsPerInterface(t *testing.T) {
	proc := `Idx	Device    : Count Querier	Group    Users Timer	Reporter
1	lo        :     2      V3
				FB0000E0     1 0:00000000		0
				010000E0     1 0:00000000		0
9	wlan0     :     1      V3
				010000E0     1 0:00000000		0
`
	if Joined(proc, "wlan0", Group) {
		t.Error("lo's membership was counted as wlan0's")
	}
	if !Joined(proc, "lo", Group) {
		t.Error("lo really is a member here")
	}
}

// The byte order is the trap: 224.0.0.251 prints as FB0000E0 on this device,
// not as E00000FB. Pinned so a refactor of encodeGroup cannot silently invert
// it — the failure would be a watcher that thinks the group is never present
// and restarts the endpoints for ever.
func TestGroupIsEncodedLittleEndian(t *testing.T) {
	if got := encodeGroup("224.0.0.251"); got != "FB0000E0" {
		t.Errorf("encodeGroup(224.0.0.251) = %q, want FB0000E0", got)
	}
	if got := encodeGroup("224.0.0.1"); got != "010000E0" {
		t.Errorf("encodeGroup(224.0.0.1) = %q, want 010000E0", got)
	}
}

// A group that is not an IPv4 address must match NOTHING rather than match
// everything: a zero value would read as "not joined" on every tick.
func TestBadGroupNeverMatches(t *testing.T) {
	for _, g := range []string{"", "nonsense", "::1", "224.0.0"} {
		if encodeGroup(g) != "" {
			t.Errorf("encodeGroup(%q) should be empty", g)
		}
		if Joined(procHealthy, "wlan0", g) {
			t.Errorf("Joined with group %q must be false", g)
		}
	}
}

func TestJoinedHandlesEmptyAndHeaderOnly(t *testing.T) {
	if Joined("", "wlan0", Group) {
		t.Error("empty proc must not read as joined")
	}
	header := "Idx\tDevice    : Count Querier\tGroup    Users Timer\tReporter\n"
	if Joined(header, "wlan0", Group) {
		t.Error("a file with no interfaces must not read as joined")
	}
}

// ---- watcher ----

type fakeWatch struct {
	proc     string
	err      error
	active   bool
	rejoined int
}

func newWatcher(f *fakeWatch) *Watcher {
	return &Watcher{
		Read:   func() (string, error) { return f.proc, f.err },
		Active: func() bool { return f.active },
		Rejoin: func() { f.rejoined++ },
		Misses: 2,
	}
}

func TestWatcherNeedsConsecutiveMissesBeforeActing(t *testing.T) {
	f := &fakeWatch{proc: procBroken, active: true}
	w := newWatcher(f)
	now := time.Now()

	if w.Tick(now) {
		t.Fatal("acted on the first miss — a re-association is briefly absent " +
			"and is exactly when a restart helps least")
	}
	if f.rejoined != 0 {
		t.Fatal("rejoined on the first miss")
	}
	if !w.Tick(now.Add(time.Minute)) {
		t.Fatal("did not act on the second consecutive miss")
	}
	if f.rejoined != 1 {
		t.Fatalf("rejoined %d times, want 1", f.rejoined)
	}
}

// A healthy read between two misses resets the count. Without this a device
// that loses the group for one tick every hour would eventually accumulate
// enough misses to fire on an interface that is fine.
func TestHealthyReadResetsTheMissCount(t *testing.T) {
	f := &fakeWatch{proc: procBroken, active: true}
	w := newWatcher(f)
	now := time.Now()

	w.Tick(now)
	f.proc = procHealthy
	w.Tick(now.Add(time.Minute))
	f.proc = procBroken
	if w.Tick(now.Add(2 * time.Minute)) {
		t.Fatal("the healthy read in between should have reset the counter")
	}
	if f.rejoined != 0 {
		t.Fatalf("rejoined %d times, want 0", f.rejoined)
	}
}

// The rule that keeps this from becoming its own fault: a rejoin that does
// not take must not restart two subprocesses every interval for ever.
func TestWatcherBacksOffBetweenRepairs(t *testing.T) {
	f := &fakeWatch{proc: procBroken, active: true}
	w := newWatcher(f)
	w.Backoff = 2 * time.Minute
	now := time.Now()

	w.Tick(now)
	if !w.Tick(now) {
		t.Fatal("expected the first repair")
	}
	// Still broken, and the window has not passed.
	w.Tick(now.Add(10 * time.Second))
	if w.Tick(now.Add(20 * time.Second)) {
		t.Fatal("repaired again inside the backoff window")
	}
	if f.rejoined != 1 {
		t.Fatalf("rejoined %d times inside the window, want 1", f.rejoined)
	}
	// Past the window ONE tick is enough: the misses accumulated while the
	// window suppressed the repair are not thrown away, so a device that has
	// been broken throughout is repaired as soon as it is allowed to be,
	// rather than waiting out the miss threshold all over again.
	if !w.Tick(now.Add(3 * time.Minute)) {
		t.Fatal("expected a second repair once the window had passed")
	}
	if f.rejoined != 2 {
		t.Fatalf("rejoined %d times, want 2", f.rejoined)
	}
	if w.delay != 4*time.Minute {
		t.Errorf("backoff = %s, want it doubled to 4m", w.delay)
	}
}

func TestBackoffIsCapped(t *testing.T) {
	f := &fakeWatch{proc: procBroken, active: true}
	w := newWatcher(f)
	w.Backoff = time.Minute
	w.MaxDelay = 4 * time.Minute
	now := time.Now()
	for i := 0; i < 20; i++ {
		now = now.Add(time.Hour)
		w.Tick(now)
		w.Tick(now)
	}
	if w.delay > 4*time.Minute {
		t.Errorf("backoff grew to %s, past the %s cap", w.delay, w.MaxDelay)
	}
}

// Nothing is a member when nothing is running, so absence is correct and
// acting on it would restart endpoints the user switched off.
func TestInactiveEndpointsAreNeverRestarted(t *testing.T) {
	f := &fakeWatch{proc: procBroken, active: false}
	w := newWatcher(f)
	now := time.Now()
	for i := 0; i < 5; i++ {
		if w.Tick(now.Add(time.Duration(i) * time.Minute)) {
			t.Fatal("restarted an endpoint that is not running")
		}
	}
	if f.rejoined != 0 {
		t.Fatalf("rejoined %d times, want 0", f.rejoined)
	}
}

// Misses counted while the endpoints were off must not fire the moment one is
// switched on — the group is legitimately absent until it joins.
func TestMissesDoNotCarryAcrossAnInactivePeriod(t *testing.T) {
	f := &fakeWatch{proc: procBroken, active: true}
	w := newWatcher(f)
	now := time.Now()
	w.Tick(now) // one miss
	f.active = false
	w.Tick(now.Add(time.Minute))
	f.active = true
	if w.Tick(now.Add(2 * time.Minute)) {
		t.Fatal("a miss from before the endpoints were switched off was carried over")
	}
}

// Failure to LOOK is not evidence of absence.
func TestUnreadableProcIsNotAMiss(t *testing.T) {
	f := &fakeWatch{err: errors.New("no such file"), active: true}
	w := newWatcher(f)
	now := time.Now()
	for i := 0; i < 5; i++ {
		if w.Tick(now.Add(time.Duration(i) * time.Minute)) {
			t.Fatal("acted on a read error")
		}
	}
	if f.rejoined != 0 {
		t.Fatalf("rejoined %d times, want 0", f.rejoined)
	}
}
