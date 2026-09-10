package speaker

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// The bug retryOpen exists for: the ALSA open used to happen inside
// NewPcmSpeaker, and main() initialises the speaker BEFORE mDNS, the control
// client, the buttons and the LEDs. So a device that lost the race with
// Android's mediaserver — which it does whenever there is a plug in the jack
// and the process was restarted rather than cold-booted — went completely
// dark and needed a power cycle. Measured twice on 2026-09-10.
//
// Two properties are worth pinning, and neither is about ALSA:
//   - a held device is RETRIED rather than given up on, because the holder
//     does eventually let go and nothing waits on the answer any more;
//   - a stop is honoured BETWEEN attempts, or Close would sit out a full
//     retry interval on every shutdown.
//
// Millisecond delays throughout: the property is the loop, not the wait.

func TestAHeldDeviceIsRetriedUntilItOpens(t *testing.T) {
	var attempts int32
	open := func() error {
		if atomic.AddInt32(&attempts, 1) < 3 {
			return errors.New("held")
		}
		return nil
	}
	if !retryOpen(open, make(chan struct{}), time.Millisecond, time.Millisecond) {
		t.Fatal("gave up on a device that let go on the third attempt")
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestAnOpenThatSucceedsFirstTimeCostsNoDelay(t *testing.T) {
	start := time.Now()
	if !retryOpen(func() error { return nil }, make(chan struct{}), time.Hour, time.Hour) {
		t.Fatal("reported failure on an open that succeeded")
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("waited %s before returning on the happy path", el)
	}
}

// Close closes stopCh, and it must not then wait out a retry interval — the
// process is on its way down and the speaker is the last thing holding it.
func TestAStopEndsTheRetryLoopWithoutWaiting(t *testing.T) {
	stop := make(chan struct{})
	var attempts int32
	open := func() error {
		if atomic.AddInt32(&attempts, 1) == 1 {
			close(stop)
		}
		return errors.New("held")
	}
	start := time.Now()
	if retryOpen(open, stop, time.Hour, time.Hour) {
		t.Fatal("reported success after a stop")
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("waited %s for a retry interval after a stop", el)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d after a stop, want 1", got)
	}
}

// A stop that arrives while nothing is in flight must be seen before the next
// open is attempted, not only after it.
func TestAStopBeforeTheFirstAttemptOpensNothing(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	var attempts int32
	open := func() error {
		atomic.AddInt32(&attempts, 1)
		return nil
	}
	if retryOpen(open, stop, time.Millisecond, time.Millisecond) {
		t.Fatal("opened after a stop")
	}
	if got := atomic.LoadInt32(&attempts); got != 0 {
		t.Fatalf("attempts = %d, want 0", got)
	}
}

// ─── the backoff, and the nudge budget ───────────────────────────────────────
//
// Both exist because "the open runs once per process start" stopped being true
// the day the open began retrying, and the comments that rested on it did not
// notice. With a plug in the jack, Android's mediaserver holds the speaker for
// as long as the plug is there: a flat three-second retry then spends a
// fork/exec pair and a codec probe every three seconds for the life of the
// process, and an unbounded nudge kills an Android system service roughly
// every 2.6 seconds, for ever, on a board sharing 512MB with Android.

func TestTheRetryDelayDoublesToACeiling(t *testing.T) {
	got := []time.Duration{}
	d := time.Second
	for i := 0; i < 8; i++ {
		got = append(got, d)
		d = nextDelay(d, 8*time.Second)
	}
	want := []time.Duration{1, 2, 4, 8, 8, 8, 8, 8}
	for i, w := range want {
		if got[i] != w*time.Second {
			t.Fatalf("delay %d = %v, want %v", i, got[i], w*time.Second)
		}
	}
}

func TestTheCeilingIsNeverExceededEvenFromAboveIt(t *testing.T) {
	// A delay already past the ceiling must come back to it rather than
	// doubling away from it — the ceiling is what makes the unrecoverable
	// case a heartbeat instead of a busy loop.
	if got := nextDelay(time.Hour, time.Minute); got != time.Minute {
		t.Fatalf("nextDelay = %v, want the ceiling", got)
	}
}

func TestARecoverableHolderIsNotDelayedByTheBackoff(t *testing.T) {
	// mediaserver after an OTA lets go within seconds, so it is over before
	// the delay has grown at all. The backoff must cost that case nothing.
	var attempts int32
	open := func() error {
		if atomic.AddInt32(&attempts, 1) < 2 {
			return errors.New("held")
		}
		return nil
	}
	start := time.Now()
	if !retryOpen(open, make(chan struct{}), time.Millisecond, time.Hour) {
		t.Fatal("gave up on a holder that let go on the second attempt")
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("took %s for one short retry", el)
	}
}

func TestTheHolderIsAskedAFiniteNumberOfTimes(t *testing.T) {
	// A holder that never lets go must not be asked for ever. The budget is
	// what the original ten-second window already spent, so the case this was
	// built for is untouched.
	var asks int32
	held := func() (int, bool) { return 667, true } // never releases
	nudge := func() { atomic.AddInt32(&asks, 1) }
	if waitFree(held, nudge, 200*time.Millisecond, time.Millisecond, 0) {
		t.Fatal("reported a release from a holder that never lets go")
	}
	if got := atomic.LoadInt32(&asks); got > maxNudges {
		t.Fatalf("asked %d times, budget is %d", got, maxNudges)
	}
	if got := atomic.LoadInt32(&asks); got != maxNudges {
		t.Fatalf("asked %d times, want the full budget %d before giving up",
			got, maxNudges)
	}
}
