package speaker

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wilbowes/EchoMuse/internal/bootlog"
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

// A device whose speaker never opens is a device that registers, answers its
// buttons, lights its ring and plays nothing — and until this, every line
// saying why went to /tmp, which the power cycle used to recover from it
// wipes. The record is on /data and has to survive that.
func TestSilentSpeakerIsRecordedOnData(t *testing.T) {
	var lines []string
	restore := stubSpeakerLog(t, &lines,
		[]time.Duration{20 * time.Millisecond, 60 * time.Millisecond},
		40*time.Millisecond)
	defer restore()

	stop := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		close(stop)
	}()
	if retryOpen(func() error { return errStillHeld }, stop, time.Millisecond, time.Millisecond) {
		t.Fatal("retryOpen claimed success")
	}

	if len(lines) == 0 {
		t.Fatal("a speaker that never opened left no record on /data")
	}
	if !strings.Contains(lines[0], "no speaker for") {
		t.Fatalf("first line does not say what is wrong: %q", lines[0])
	}
	if !strings.Contains(lines[0], errStillHeld.Error()) {
		t.Fatalf("first line does not carry the reason the open failed: %q", lines[0])
	}
	// Escalating, not per-attempt: this loop ran hundreds of times.
	if len(lines) > 6 {
		t.Fatalf("%d lines for one fault — the escalation is not holding, and "+
			"this is flash that cannot be replaced", len(lines))
	}
}

// The ordinary case — mediaserver letting go a second or two after an OTA —
// must write nothing at all. It is every device, every update.
func TestARecoverableOpenIsSilent(t *testing.T) {
	var lines []string
	restore := stubSpeakerLog(t, &lines, bootlog.DefaultMilestones, bootlog.DefaultRepeat)
	defer restore()

	attempts := 0
	open := func() error {
		attempts++
		if attempts < 4 {
			return errStillHeld
		}
		return nil
	}
	if !retryOpen(open, make(chan struct{}), time.Millisecond, time.Millisecond) {
		t.Fatal("retryOpen gave up on a recoverable open")
	}
	if len(lines) != 0 {
		t.Fatalf("an ordinary restart wrote %d lines to /data: %v", len(lines), lines)
	}
}

// An all-clear is only worth a flash write if somebody was told about the
// fault it clears.
func TestRecoveryIsRecordedOnlyAfterAReport(t *testing.T) {
	var lines []string
	restore := stubSpeakerLog(t, &lines,
		[]time.Duration{20 * time.Millisecond}, time.Hour)
	defer restore()

	start := time.Now()
	open := func() error {
		if time.Since(start) < 50*time.Millisecond {
			return errStillHeld
		}
		return nil
	}
	if !retryOpen(open, make(chan struct{}), time.Millisecond, time.Millisecond) {
		t.Fatal("retryOpen gave up")
	}
	if len(lines) != 2 {
		t.Fatalf("want one fault line and one recovery line, got %v", lines)
	}
	if !strings.Contains(lines[1], "speaker opened after") {
		t.Fatalf("recovery not recorded: %q", lines[1])
	}
}

var errStillHeld = errors.New("cannot open pcm23p: device or resource busy")

func stubSpeakerLog(t *testing.T, into *[]string,
	marks []time.Duration, repeat time.Duration) func() {
	t.Helper()
	oldLog, oldMarks, oldRepeat := speakerLog, speakerLogMilestones, speakerLogRepeat
	speakerLog = func(format string, args ...any) {
		*into = append(*into, fmt.Sprintf(format, args...))
	}
	speakerLogMilestones, speakerLogRepeat = marks, repeat
	return func() {
		speakerLog, speakerLogMilestones, speakerLogRepeat = oldLog, oldMarks, oldRepeat
	}
}
