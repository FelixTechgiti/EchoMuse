package speaker

import (
	"sync/atomic"
	"testing"
	"time"
)

// The bug: `stop media` was issued ONCE, before the wait. Android brings
// mediaserver back, and after an OTA restart it is fully up and takes the
// speaker — so by the time it has died and returned there was nothing left to
// ask it again. The open then blocked, and because main() initialises the
// speaker before mDNS, the control client, the buttons and the LEDs, the whole
// device went dark: no registration, no orange pulse, nothing but a power
// cycle. Measured on a device 2026-09-10 across two OTAs.
//
// Small durations throughout: the property under test is the CADENCE, and a
// test that waits ten real seconds is one nobody runs.

func TestAHolderThatLetsGoEndsTheWait(t *testing.T) {
	var reads int32
	held := func() (int, bool) {
		// Busy for the first two polls, then free — what mediaserver does at
		// a cold boot, where it released in 200ms on real hardware.
		return 667, atomic.AddInt32(&reads, 1) <= 2
	}
	if !waitFree(held, nil, time.Second, time.Millisecond, time.Hour) {
		t.Fatal("the wait timed out on a holder that let go")
	}
}

func TestTheHolderIsAskedAgainWhileItKeepsTheSpeaker(t *testing.T) {
	// The whole fix. One ask cannot survive a service that dies and comes
	// back inside the window.
	var asks int32
	held := func() (int, bool) { return 667, true }
	nudge := func() { atomic.AddInt32(&asks, 1) }

	if waitFree(held, nudge, 60*time.Millisecond, time.Millisecond, 10*time.Millisecond) {
		t.Fatal("reported a release from a holder that never let go")
	}
	if n := atomic.LoadInt32(&asks); n < 2 {
		t.Fatalf("asked the holder %d times across the window, want at least 2 "+
			"— one ask before the wait is the bug this exists to fix", n)
	}
}

func TestAReleaseStopsTheAsking(t *testing.T) {
	// Once the speaker is ours there is nothing to ask for, and `stop media`
	// costs a fork/exec — it must not run on past the release.
	var reads, asks int32
	held := func() (int, bool) {
		return 667, atomic.AddInt32(&reads, 1) <= 3
	}
	nudge := func() { atomic.AddInt32(&asks, 1) }

	if !waitFree(held, nudge, time.Second, time.Millisecond, 2*time.Millisecond) {
		t.Fatal("the wait timed out on a holder that let go")
	}
	before := atomic.LoadInt32(&asks)
	time.Sleep(20 * time.Millisecond)
	if after := atomic.LoadInt32(&asks); after != before {
		t.Fatalf("kept asking after the release: %d then %d", before, after)
	}
}

func TestNoNudgeIsSafe(t *testing.T) {
	// waitForFreePcm is called with a nudge today, and a nil one must not
	// panic: a future caller with nothing to ask is a contemplated state, not
	// a crash on the one path that strands the device.
	held := func() (int, bool) { return 667, true }
	if waitFree(held, nil, 10*time.Millisecond, time.Millisecond, time.Millisecond) {
		t.Fatal("reported a release from a holder that never let go")
	}
}
