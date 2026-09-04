package spotify

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSink is the music plane.
type fakeSink struct {
	mu     sync.Mutex
	pushed []byte
	ends   int
}

func (s *fakeSink) PumpMusic(d []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushed = append(s.pushed, d...)
	return nil
}
func (s *fakeSink) EndMusicStream() { s.mu.Lock(); s.ends++; s.mu.Unlock() }
func (s *fakeSink) FlushMusic()     {}
func (s *fakeSink) bytes() int      { s.mu.Lock(); defer s.mu.Unlock(); return len(s.pushed) }
func (s *fakeSink) samples() []int16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int16, len(s.pushed)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(s.pushed[i*2:]))
	}
	return out
}

type fakePlane struct {
	mu     sync.Mutex
	held   bool
	refuse bool
	claims int
	frees  int
}

func (p *fakePlane) Claim() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.claims++
	if p.refuse {
		return false
	}
	p.held = true
	return true
}
func (p *fakePlane) Release()        { p.mu.Lock(); p.held = false; p.frees++; p.mu.Unlock() }
func (p *fakePlane) MayWrite() bool  { p.mu.Lock(); defer p.mu.Unlock(); return p.held }
func (p *fakePlane) claimCount() int { p.mu.Lock(); defer p.mu.Unlock(); return p.claims }

// fakeLibrespot writes a script that stands in for the real binary. A script
// rather than a mock because the whole of this package is process management:
// what is worth testing is that a real process is started, its stdout is
// read, and it is actually killed.
func fakeLibrespot(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "librespot")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAMissingBinaryIsReportedNotIgnored(t *testing.T) {
	// A toggle that saves, reports success and plays nothing is the failure
	// this codebase names most often, and "the binary was never pushed" is
	// otherwise indistinguishable from "Spotify is broken".
	c := New(Options{Binary: "/nonexistent/librespot"}, &fakeSink{}, &fakePlane{})
	ok, err := c.Available()
	if ok {
		t.Fatal("a missing binary reported available")
	}
	if !errors.Is(err, ErrNoBinary) {
		t.Fatalf("err = %v, want ErrNoBinary", err)
	}
	if err := c.Start(); !errors.Is(err, ErrNoBinary) {
		t.Fatalf("Start() = %v, want ErrNoBinary", err)
	}
	if c.Running() {
		t.Fatal("the supervisor started with no binary")
	}
	// And the error names the path, so somebody reading a log knows where
	// to put the file.
	if !strings.Contains(err.Error(), "/nonexistent/librespot") {
		t.Fatalf("the error does not name the path: %v", err)
	}
}

func TestANonExecutableBinaryIsReportedSeparately(t *testing.T) {
	// A different fault with a different fix: the file arrived and the
	// chmod did not.
	dir := t.TempDir()
	path := filepath.Join(dir, "librespot")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{Binary: path}, &fakeSink{}, &fakePlane{})
	ok, err := c.Available()
	if ok {
		t.Fatal("a non-executable file reported available")
	}
	if !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("err = %v, want it to name the mode", err)
	}
}

func TestAudioFromTheProcessReachesTheMusicPlane(t *testing.T) {
	// 4096 stereo frames of a constant, so the downmix is checkable.
	bin := fakeLibrespot(t, `
i=0
while [ $i -lt 64 ]; do
  printf '\x10\x27\x10\x27' 
  i=$((i+1))
done | dd bs=1 count=256 2>/dev/null
# pad out to more than one read chunk
dd if=/dev/zero bs=8192 count=2 2>/dev/null
`)
	sink := &fakeSink{}
	plane := &fakePlane{}
	c := New(Options{Binary: bin, Name: "Test"}, sink, plane)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for sink.bytes() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if sink.bytes() == 0 {
		t.Fatal("no audio reached the music plane")
	}
	// Stereo in, mono out: half the bytes.
	if sink.bytes()%2 != 0 {
		t.Fatalf("pushed %d bytes — not whole mono samples", sink.bytes())
	}
}

func TestThePlaneIsClaimedOnFirstAudioNotAtStart(t *testing.T) {
	// librespot runs continuously so it can appear in the app, and it is
	// silent until somebody selects it. Claiming at start would take the
	// plane from Home Assistant for a speaker nobody is playing to.
	bin := fakeLibrespot(t, "sleep 3\n")
	sink := &fakeSink{}
	plane := &fakePlane{}
	c := New(Options{Binary: bin}, sink, plane)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	time.Sleep(400 * time.Millisecond)
	if plane.claimCount() != 0 {
		t.Fatalf("claimed the plane %d times with no audio playing",
			plane.claimCount())
	}
}

func TestAudioIsDroppedWhileHomeAssistantHoldsThePlane(t *testing.T) {
	// The session carries on rather than being killed: the user's phone
	// shows the Echo playing, which is wrong, and the alternative is ending
	// a session they may want back in ten seconds. Bounded by HA releasing
	// the plane.
	bin := fakeLibrespot(t, "dd if=/dev/zero bs=8192 count=4 2>/dev/null\nsleep 1\n")
	sink := &fakeSink{}
	plane := &fakePlane{refuse: true}
	c := New(Options{Binary: bin}, sink, plane)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	time.Sleep(600 * time.Millisecond)
	if sink.bytes() != 0 {
		t.Fatalf("wrote %d bytes without owning the plane", sink.bytes())
	}
	if plane.claimCount() == 0 {
		t.Fatal("never tried to claim the plane")
	}
}

func TestStopKillsTheProcess(t *testing.T) {
	// Killing it is what makes the Echo DISAPPEAR from the Spotify app, and
	// that is the right outcome rather than a side effect: a speaker that is
	// listed, selected and silent is worse than one that is not listed.
	bin := fakeLibrespot(t, "sleep 30\n")
	c := New(Options{Binary: bin}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait for the process to exist.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		p := c.proc
		c.mu.Unlock()
		if p != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.mu.Lock()
	proc := c.proc
	c.mu.Unlock()
	if proc == nil {
		t.Fatal("the process never started")
	}

	done := make(chan struct{})
	go func() { proc.Wait(); close(done) }()
	c.Stop()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the process outlived Stop")
	}
	if c.Running() {
		t.Fatal("still running after Stop")
	}
}

func TestStartAndStopAreIdempotent(t *testing.T) {
	// The controller re-sends the whole config on every reconnect.
	bin := fakeLibrespot(t, "sleep 5\n")
	c := New(Options{Binary: bin}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("the second Start errored: %v", err)
	}
	c.Stop()
	c.Stop()
	if c.Running() {
		t.Fatal("still running")
	}
}

func TestLeavingDoesNotStopTheSupervisor(t *testing.T) {
	// The Echo comes back as a Spotify target once the plane is free. It
	// does NOT resume what was playing: reappearing as a speaker is not the
	// same as taking over playback somebody moved elsewhere.
	bin := fakeLibrespot(t, "sleep 5\n")
	c := New(Options{Binary: bin}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	time.Sleep(300 * time.Millisecond)

	c.Leave("preempted")
	if !c.Running() {
		t.Fatal("a preemption stopped the supervisor — the Echo would never " +
			"come back as a Spotify target")
	}
}

func TestLeavingWithNoSessionIsSafe(t *testing.T) {
	// The arbiter holds this callback and can call it before anything has
	// started, or between restarts.
	c := New(Options{Binary: "/nonexistent"}, &fakeSink{}, &fakePlane{})
	c.Leave("preempted")
}

func TestThePlaneIsReleasedWhenTheProcessExits(t *testing.T) {
	bin := fakeLibrespot(t, "dd if=/dev/zero bs=8192 count=1 2>/dev/null\n")
	sink := &fakeSink{}
	plane := &fakePlane{}
	c := New(Options{Binary: bin}, sink, plane)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		plane.mu.Lock()
		frees := plane.frees
		plane.mu.Unlock()
		if frees > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the plane was never released after the process exited")
}

// ── The command line ──────────────────────────────────────────────────────

func TestTheCommandLineCarriesTheDecisions(t *testing.T) {
	c := New(Options{Binary: "/bin/true", Name: "Lounge", Bitrate: 320,
		CacheDir: "/tmp/cache"}, &fakeSink{}, &fakePlane{})
	got := strings.Join(c.args(), " ")
	for _, want := range []string{
		// PCM to stdout. No ALSA in librespot at all: the device already
		// owns the speaker, and two things opening it is the #80 failure —
		// a blocking open with no timeout, eighteen minutes of a stranded
		// device.
		"--backend pipe",
		// librespot does the resampling, in a process that can be killed,
		// rather than a resampler on the voice assistant's audio path.
		"--sample-rate 48000",
		// Named rather than left to the default, so a librespot that
		// changes its default does not silently send 32-bit floats into a
		// 16-bit mixer.
		"--format S16",
		// The eMMC is 8GB and shared with Android; a cache of decoded audio
		// is the fastest way to fill it.
		"--disable-audio-cache",
		"--name Lounge",
		"--bitrate 320",
		"--cache /tmp/cache",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in: %s", want, got)
		}
	}
}

func TestDiscoveryIsNotDisabled(t *testing.T) {
	// Zeroconf discovery is how the speaker appears in the app without a
	// login. Passing --disable-discovery would leave a device that can only
	// be reached by someone who already typed a password into it.
	c := New(Options{Binary: "/bin/true"}, &fakeSink{}, &fakePlane{})
	for _, a := range c.args() {
		if a == "--disable-discovery" {
			t.Fatal("discovery was disabled — the Echo would not appear in " +
				"the Spotify app at all")
		}
	}
}

func TestTheCredentialCacheIsKeptEvenThoughTheAudioCacheIsNot(t *testing.T) {
	// They are different caches with opposite trade-offs: the credential
	// blob is what stops the user re-authorising after every reboot, and it
	// is bytes rather than megabytes.
	c := New(Options{Binary: "/bin/true"}, &fakeSink{}, &fakePlane{})
	got := strings.Join(c.args(), " ")
	if !strings.Contains(got, "--cache ") {
		t.Fatal("no credential cache — the user would re-authorise every reboot")
	}
	if !strings.Contains(got, "--disable-audio-cache") {
		t.Fatal("the audio cache was left on")
	}
}

func TestDefaultsAreFilledIn(t *testing.T) {
	c := New(Options{}, &fakeSink{}, &fakePlane{})
	if c.opts.Binary != BinaryPath {
		t.Fatalf("binary = %q", c.opts.Binary)
	}
	if c.opts.CacheDir != CacheDir {
		t.Fatalf("cache = %q", c.opts.CacheDir)
	}
	if c.opts.Bitrate != 160 {
		t.Fatalf("bitrate = %d, want Spotify's own default", c.opts.Bitrate)
	}
	if c.name() == "" {
		t.Fatal("no name — the speaker would appear unnamed in the app")
	}
}

func TestTheRestartBackoffIsBoundedAtBothEnds(t *testing.T) {
	// librespot exits on its own for ordinary reasons — a session moved to
	// another device, a network blip, Spotify logging it out — so restarting
	// is the normal case and has to be quiet.
	if restartMin < time.Second {
		t.Fatalf("restart floor %s would respawn in a tight loop", restartMin)
	}
	if restartMax > 5*time.Minute {
		t.Fatalf("restart ceiling %s is longer than anyone waits before "+
			"deciding it is broken", restartMax)
	}
	if restartMax < restartMin {
		t.Fatal("the ceiling is below the floor")
	}
}

// ── Report ────────────────────────────────────────────────────────────────

func TestReportAlwaysNamesThePath(t *testing.T) {
	// It rides the register message and reaches a support bundle. "ok:
	// false" alone says nothing about where the file should have gone.
	rep := Report()
	if rep["binary"] != BinaryPath {
		t.Fatalf("report = %v, want it to name %s", rep, BinaryPath)
	}
	if _, ok := rep["ok"]; !ok {
		t.Fatalf("report has no verdict: %v", rep)
	}
}

func TestReportDistinguishesAbsentFromUnusable(t *testing.T) {
	// Three faults with three different fixes: push the binary, it landed as
	// a directory, the chmod did not run. Collapsing them to "off" is how
	// #90 got stuck twice.
	if rep := Report(); rep["ok"] == true {
		// On a build machine the path does not exist, which is the case
		// this asserts.
		t.Skip("librespot is unexpectedly present here")
	} else if rep["reason"] != "not_installed" {
		t.Fatalf("reason = %v, want not_installed", rep["reason"])
	}
}

func TestARenameRestartsARunningSession(t *testing.T) {
	// The name is a command-line argument and librespot reads it once.
	// Without the restart, renaming the device in Home Assistant would save,
	// report success, and change nothing until the next reboot.
	bin := fakeLibrespot(t, "sleep 30\n")
	c := New(Options{Binary: bin, Name: "Old"}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	deadline := time.Now().Add(3 * time.Second)
	var first *os.Process
	for time.Now().Before(deadline) {
		c.mu.Lock()
		first = c.proc
		c.mu.Unlock()
		if first != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if first == nil {
		t.Fatal("the process never started")
	}

	done := make(chan struct{})
	go func() { first.Wait(); close(done) }()
	c.SetName("New")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a rename did not restart the session")
	}
	if c.name() != "New" {
		t.Fatalf("name = %q", c.name())
	}
	if !c.Running() {
		t.Fatal("a rename stopped the supervisor")
	}
}

func TestRenamingToTheSameNameDoesNothing(t *testing.T) {
	// The controller re-sends the whole config on every reconnect. Without
	// the guard, every reconnect would kill the session.
	bin := fakeLibrespot(t, "sleep 30\n")
	c := New(Options{Binary: bin, Name: "Lounge"}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		p := c.proc
		c.mu.Unlock()
		if p != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.mu.Lock()
	before := c.proc
	c.mu.Unlock()

	c.SetName("Lounge")
	c.SetName("") // an unset name must not blank the real one either

	time.Sleep(200 * time.Millisecond)
	c.mu.Lock()
	after := c.proc
	c.mu.Unlock()
	if before != after {
		t.Fatal("a no-op rename restarted the session")
	}
	if c.name() != "Lounge" {
		t.Fatalf("name = %q — an empty rename overwrote it", c.name())
	}
}
