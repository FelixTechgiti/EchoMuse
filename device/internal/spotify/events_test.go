package spotify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEventReadsTheScriptsOutput(t *testing.T) {
	// Exactly what eventScript writes: the event, a space, the volume or
	// nothing. Anything else here means the two halves have drifted.
	e, ok := ParseEvent("volume_changed 32768")
	if !ok || e.Kind != "volume_changed" || !e.HasVol || e.Volume != 32768 {
		t.Fatalf("volume_changed parsed as %+v (ok=%v)", e, ok)
	}
	e, ok = ParseEvent("paused ")
	if !ok || e.Kind != "paused" || e.HasVol {
		t.Fatalf("paused parsed as %+v (ok=%v)", e, ok)
	}
}

// librespot has around twenty events and adds more between releases. A parser
// that rejected the ones it did not know would turn an upgrade into a silent
// loss of the ones it did.
func TestUnknownEventsParseAndDoNothing(t *testing.T) {
	e, ok := ParseEvent("shuffle_changed")
	if !ok {
		t.Fatal("an unknown event must still parse")
	}
	if e.EndsPlayback() {
		t.Fatal("an unknown event must not flush the music plane")
	}
}

func TestOnlyRealStopsEndPlayback(t *testing.T) {
	for _, k := range []string{"paused", "stopped", "session_disconnected"} {
		if e, _ := ParseEvent(k); !e.EndsPlayback() {
			t.Errorf("%q should end playback", k)
		}
	}
	// end_of_track fires at EVERY track boundary in normal playback.
	// Flushing there would cut the tail off every song to start the next
	// one — a latency fix turned into a gap.
	for _, k := range []string{"end_of_track", "playing", "loading", "preloading",
		"track_changed", "volume_changed", "seeked", "position_correction"} {
		if e, _ := ParseEvent(k); e.EndsPlayback() {
			t.Errorf("%q must NOT flush the music plane", k)
		}
	}
}

func TestEmptyLinesAreIgnored(t *testing.T) {
	for _, l := range []string{"", "   ", "\t"} {
		if _, ok := ParseEvent(l); ok {
			t.Errorf("%q should not parse as an event", l)
		}
	}
}

// A volume that is not a number, or does not fit a u16, leaves HasVol false
// rather than reporting a wrong level. Setting the speaker from a misparse is
// worse than not setting it.
func TestUnparseableVolumeIsNotReported(t *testing.T) {
	for _, v := range []string{"abc", "-1", "99999", "1.5", ""} {
		e, ok := ParseEvent("volume_changed " + v)
		if !ok {
			t.Fatalf("volume_changed %q should still parse as an event", v)
		}
		if e.HasVol {
			t.Errorf("volume %q was accepted as %d", v, e.Volume)
		}
	}
}

// SOURCE GUARD over the script. librespot runs it with the environment set
// and nothing else; the firmware parses what it writes. The two are a
// contract that no test would otherwise exercise together, and the failure
// would be silent — events simply stop arriving and a pause goes back to
// taking six seconds.
func TestEventScriptWritesWhatParseEventReads(t *testing.T) {
	sc := eventScript("/tmp/pipe")
	for _, want := range []string{
		"$PLAYER_EVENT",    // the event name, first field
		"${VOLUME:-}",      // and the volume, empty rather than unset
		">> /tmp/pipe",     // appended, because two events can overlap
		`printf '%s %s\n'`, // one line, space-separated — what ParseEvent splits
	} {
		if !strings.Contains(sc, want) {
			t.Errorf("the event script no longer contains %q:\n%s", want, sc)
		}
	}
	// `>` would be meaningless on a FIFO and reads as if it truncated.
	if strings.Contains(sc, "> /tmp/pipe") && !strings.Contains(sc, ">> /tmp/pipe") {
		t.Error("the script truncates rather than appends")
	}
}

func TestWritePlumbingCreatesAFifoAndAScript(t *testing.T) {
	dir := t.TempDir()
	onevent, pipe := writeEventPlumbing(dir)
	if onevent == "" {
		t.Fatal("plumbing reported failure on a writable directory")
	}
	// librespot's run_program splits on whitespace and execs argv[0], so the
	// value has to be `<program> <script>` and the program has to exist on
	// the device. There is no shell.
	f := strings.Fields(onevent)
	if len(f) != 2 || f[0] != "/system/bin/sh" {
		t.Fatalf("--onevent is %q; librespot execs the first field, so it must "+
			"name a program and the script must be its argument", onevent)
	}
	if _, err := os.Stat(f[1]); err != nil {
		t.Fatalf("the script named in --onevent does not exist: %v", err)
	}
	fi, err := os.Stat(pipe)
	if err != nil {
		t.Fatalf("no pipe: %v", err)
	}
	if fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("%s is not a FIFO (mode %v)", pipe, fi.Mode())
	}
	// No leftover .part: a half-written script is one librespot would run.
	if _, err := os.Stat(filepath.Join(dir, eventScriptName+".part")); err == nil {
		t.Error("the temp file was left behind")
	}
}

// The endpoint restarts often and the path is fixed, so a second start must
// reuse the pipe rather than fail on it.
func TestPlumbingIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if a, _ := writeEventPlumbing(dir); a == "" {
		t.Fatal("first call failed")
	}
	if b, _ := writeEventPlumbing(dir); b == "" {
		t.Fatal("second call failed — an existing FIFO must be reused")
	}
}

// A regular file at the pipe's path is what an older firmware would have left.
// Replacing it is safe; failing on it would disable events for ever on exactly
// the devices that upgraded.
func TestARegularFileInTheWayIsReplaced(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, eventPipeName)
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if a, _ := writeEventPlumbing(dir); a == "" {
		t.Fatal("a stale regular file blocked the plumbing")
	}
	fi, err := os.Stat(stale)
	if err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("the stale file was not replaced by a FIFO: %v %v", fi, err)
	}
}

func TestNoDirectoryMeansNoPlumbing(t *testing.T) {
	if a, _ := writeEventPlumbing(""); a != "" {
		t.Error("plumbing reported success with no directory")
	}
}

// The reader has to see what the script writes, through a real FIFO. This is
// the one place the two halves meet.
func TestReaderDeliversWhatTheScriptWould(t *testing.T) {
	dir := t.TempDir()
	_, pipe := writeEventPlumbing(dir)
	if pipe == "" {
		t.Fatal("no pipe")
	}
	got := make(chan Event, 4)
	stop := make(chan struct{})
	go readEventPipe(pipe, stop, func(e Event) { got <- e })

	// O_WRONLY would block here if the reader were not holding the pipe open
	// O_RDWR — which is precisely the property being relied on, so opening
	// this way is part of the test rather than a convenience.
	w, err := os.OpenFile(pipe, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("a writer could not open the pipe: %v", err)
	}
	defer w.Close()
	if _, err := w.WriteString("volume_changed 4096\npaused \n"); err != nil {
		t.Fatal(err)
	}

	first := <-got
	if first.Kind != "volume_changed" || first.Volume != 4096 {
		t.Errorf("first event was %+v", first)
	}
	second := <-got
	if !second.EndsPlayback() {
		t.Errorf("second event %+v should end playback", second)
	}
	close(stop)
}
