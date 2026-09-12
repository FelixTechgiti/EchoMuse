package logrelay

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// The gap this closes: the firmware logged
// `[airplay] shairport-sync exited: exit status 1` every minute for two hours
// and only the device's owner could see it, because /tmp is RAM-backed and the
// only lines ever relayed were the [mem] summaries. Two wrong diagnoses and
// five shell sessions came out of that on 2026-09-10.
//
// The two ways this can fail are opposite and both silent: too narrow and the
// next fault is invisible again; too broad and the relay degrades the liveness
// channel it reports over, which is #404 with a different payload.

func TestTheLineThatCostTwoDiagnosesIsRelayed(t *testing.T) {
	level, ok := Classify("2026/09/10 14:44:29 [airplay] shairport-sync exited: exit status 1")
	if !ok || level != LevelWarn {
		t.Fatalf("Classify = %q, %v — the whole point of this package", level, ok)
	}
}

func TestOutcomesAreMatchedNotComponents(t *testing.T) {
	// A subsystem written next year should be relayed the day it fails,
	// without anyone remembering to add it to a list.
	for _, line := range []string{
		"[newthing] could not open /dev/whatever: permission denied",
		"[sendspin] connection failed",
		"[wifi] timed out waiting for association",
		"[speaker] open attempt 3 failed: playback device still held",
		"panic: runtime error",
	} {
		if level, ok := Classify(line); !ok || level != LevelWarn {
			t.Fatalf("not relayed: %q", line)
		}
	}
}

func TestTheSpeakerLifecycleIsRelayedSoItsABSENCECanBeRead(t *testing.T) {
	// "PcmSpeaker initialised" never appearing is the tell for a device whose
	// PCM Android will not release. An absence is only legible if the presence
	// is normally there to compare against.
	if level, ok := Classify("PcmSpeaker initialised — silence stream running"); !ok || level != LevelInfo {
		t.Fatalf("Classify = %q, %v", level, ok)
	}
}

func TestTheNoisyTelemetryIsNotRelayed(t *testing.T) {
	// [mem] has its own relay and is 89% of the device_logs table; [aec] and
	// [mic] clock run ~1/s during playback. Any of them here would drown the
	// lines this exists for and load the liveness channel.
	for _, line := range []string{
		"[mem] heap_sys=7.4MB rss=26MB pause_total=22ms",
		"[aec] att=14.2dB far: rms=4000 mean=0 peak=12000",
		"[mic] clock: stalls=0 skew=-14.8s (capture fast)",
	} {
		if _, ok := Classify(line); ok {
			t.Fatalf("relayed noise: %q", line)
		}
	}
}

func TestAnOrdinaryLineIsNotRelayed(t *testing.T) {
	if _, ok := Classify("Volume set to 96/127"); ok {
		t.Fatal("relayed an ordinary line")
	}
}

// ─── the writer ──────────────────────────────────────────────────────────────

type recorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *recorder) add(level, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, level+"|"+msg)
}
func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

func waitFor(t *testing.T, rec *recorder, n int) []string {
	t.Helper()
	for i := 0; i < 200; i++ {
		if got := rec.all(); len(got) >= n {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	return rec.all()
}

func TestEverythingStillReachesTheRealLog(t *testing.T) {
	// This is the process's log destination. A relay that could swallow a
	// line would be worse than no relay at all.
	var out bytes.Buffer
	rec := &recorder{}
	r := New(&out, rec.add)
	defer r.Stop()

	for _, l := range []string{"ordinary line\n", "[mem] noise\n", "it failed\n"} {
		if _, err := r.Write([]byte(l)); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"ordinary line", "[mem] noise", "it failed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("the real log lost %q:\n%s", want, out.String())
		}
	}
}

func TestTheRateLimitHoldsAndTheDroppedCountRidesTheNextLine(t *testing.T) {
	// The failure this relays is usually a LOOP, so the interesting case is
	// exactly the one that would flood. A separate "N suppressed" message
	// would itself be traffic on the channel being rationed.
	var out bytes.Buffer
	rec := &recorder{}
	r := New(&out, rec.add)
	defer r.Stop()

	base := time.Now()
	r.now = func() time.Time { return base }

	for i := 0; i < maxPerWindow+5; i++ {
		r.Write([]byte("it failed again\n"))
	}
	got := waitFor(t, rec, maxPerWindow)
	if len(got) != maxPerWindow {
		t.Fatalf("relayed %d lines in one window, cap is %d", len(got), maxPerWindow)
	}

	// Next window: the first line through carries what was dropped.
	r.now = func() time.Time { return base.Add(window + time.Second) }
	r.Write([]byte("it failed again\n"))
	got = waitFor(t, rec, maxPerWindow+1)
	last := got[len(got)-1]
	if !strings.Contains(last, "+5 more not relayed") {
		t.Fatalf("the dropped count did not ride the next line: %q", last)
	}
}

func TestWriteNeverBlocksWhenTheSenderIsStuck(t *testing.T) {
	// Write can be reached from code already holding the control plane's
	// mutex, because writeJSON logs its own failures. Blocking here would
	// deadlock the connection the first time a send failed.
	var out bytes.Buffer
	stuck := make(chan struct{})
	r := New(&out, func(string, string) { <-stuck })
	defer func() { close(stuck); r.Stop() }()

	done := make(chan struct{})
	go func() {
		for i := 0; i < queueDepth*4; i++ {
			r.Write([]byte("it failed\n"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on a stuck sender")
	}
}

// Go says "deadline exceeded", not "timeout", and that gap cost a real
// silence: `[sendspin] session ended: context deadline exceeded` repeated every
// two minutes for hours on a live device and reached the controller not once
// (measured 2026-09-12). A list of outcome words that cannot hear the standard
// library's own phrasing has a hole in exactly the place Go programs use.
func TestGoesDeadlineWordingIsAFailure(t *testing.T) {
	for _, line := range []string{
		"[sendspin] session ended: context deadline exceeded",
		"[ota] read tcp 10.0.0.2:443: i/o deadline exceeded",
	} {
		level, forward := Classify(line)
		if !forward || level != LevelWarn {
			t.Errorf("%q is not relayed as a warning (level=%q forward=%v)",
				line, level, forward)
		}
	}
}

// And the rest of the policy is unchanged: a cancellation is an ordinary
// shutdown, not a fault, so widening to "context" wholesale would put every
// clean stop on the liveness channel.
func TestACancellationIsStillNotAFailure(t *testing.T) {
	if _, forward := Classify("[sendspin] session ended: context canceled"); forward {
		t.Error("a cancelled context was relayed as a failure")
	}
}
