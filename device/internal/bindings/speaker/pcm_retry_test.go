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
	if !retryOpen(open, make(chan struct{}), time.Millisecond) {
		t.Fatal("gave up on a device that let go on the third attempt")
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestAnOpenThatSucceedsFirstTimeCostsNoDelay(t *testing.T) {
	start := time.Now()
	if !retryOpen(func() error { return nil }, make(chan struct{}), time.Hour) {
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
	if retryOpen(open, stop, time.Hour) {
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
	if retryOpen(open, stop, time.Millisecond) {
		t.Fatal("opened after a stop")
	}
	if got := atomic.LoadInt32(&attempts); got != 0 {
		t.Fatalf("attempts = %d, want 0", got)
	}
}
