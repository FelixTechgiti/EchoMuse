package airplay

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wilbowes/EchoMuse/internal/resample"
)

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

func fakeShairport(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shairport-sync")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAMissingBinaryIsReportedNotIgnored(t *testing.T) {
	c := New(Options{Binary: "/nonexistent/shairport-sync"}, &fakeSink{}, &fakePlane{})
	ok, err := c.Available()
	if ok || !errors.Is(err, ErrNoBinary) {
		t.Fatalf("Available() = %v, %v", ok, err)
	}
	if err := c.Start(); !errors.Is(err, ErrNoBinary) {
		t.Fatalf("Start() = %v, want ErrNoBinary", err)
	}
	if c.Running() {
		t.Fatal("the supervisor started with no binary")
	}
	if !strings.Contains(err.Error(), "/nonexistent/shairport-sync") {
		t.Fatalf("the error does not name the path: %v", err)
	}
}

func TestReportDistinguishesTheFaults(t *testing.T) {
	rep := Report()
	if rep["binary"] != BinaryPath {
		t.Fatalf("report does not name the path: %v", rep)
	}
	if rep["ok"] == true {
		t.Skip("shairport-sync is unexpectedly present here")
	}
	if rep["reason"] != "not_installed" {
		t.Fatalf("reason = %v, want not_installed", rep["reason"])
	}
}

func TestThePlaneIsClaimedOnFirstAudioNotAtStart(t *testing.T) {
	// shairport-sync runs continuously so it can appear in the AirPlay list,
	// and it is silent until somebody selects it. Claiming at start would
	// take the plane from Home Assistant for a receiver nobody is playing to.
	bin := fakeShairport(t, "sleep 3\n")
	plane := &fakePlane{}
	c := New(Options{Binary: bin}, &fakeSink{}, plane)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	time.Sleep(400 * time.Millisecond)
	if plane.claimCount() != 0 {
		t.Fatalf("claimed the plane %d times with no audio", plane.claimCount())
	}
}

func TestAudioReachesThePlaneResampled(t *testing.T) {
	// 44100 stereo in, 48000 mono out: the byte count should be close to
	// half (mono) times 160/147 (rate).
	const inFrames = readFrames * 4
	bin := fakeShairport(t, "dd if=/dev/zero bs="+
		itoa(inFrames*stereoBytesPerFrame)+" count=1 2>/dev/null\nsleep 1\n")
	sink := &fakeSink{}
	c := New(Options{Binary: bin}, sink, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	got := waitForStableBytes(t, sink) / 2 // mono 16-bit samples
	want := inFrames * 160 / 147
	if got == 0 {
		t.Fatal("no audio reached the music plane")
	}
	if math.Abs(float64(got-want))/float64(want) > 0.05 {
		t.Fatalf("produced %d samples from %d input frames, want ~%d "+
			"(mono at 48kHz)", got, inFrames, want)
	}
}

func TestAt48kHzNothingIsResampled(t *testing.T) {
	// AirPlay 2 delivers 48kHz. Running it through the resampler anyway
	// would cost 4-8% of a core to convert 48000 to 48000.
	const inFrames = readFrames * 2
	bin := fakeShairport(t, "dd if=/dev/zero bs="+
		itoa(inFrames*stereoBytesPerFrame)+" count=1 2>/dev/null\nsleep 1\n")
	sink := &fakeSink{}
	c := New(Options{Binary: bin, SourceRate: 48000}, sink, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	if got, want := waitForStableBytes(t, sink)/2, inFrames; got != want {
		t.Fatalf("produced %d samples from %d frames at 48kHz, want exactly %d",
			got, inFrames, want)
	}
}

func TestTheDownmixHappensBeforeTheResample(t *testing.T) {
	// Resampling two channels costs twice the filter for a result that is
	// about to be summed anyway — the same audio for double the CPU, on the
	// one source that cannot hand the job to a subprocess.
	//
	// Checked by conversion rather than by inspection: a stereo pair that
	// averages to a constant must come out as that constant.
	c := New(Options{Binary: "/bin/true", SourceRate: 48000}, &fakeSink{}, &fakePlane{})
	in := make([]byte, 4*8)
	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint16(in[i*4:], uint16(int16(1000)))
		binary.LittleEndian.PutUint16(in[i*4+2:], uint16(int16(3000)))
	}
	out := c.convert(nil, in)
	if len(out) != 16 {
		t.Fatalf("8 stereo frames gave %d mono bytes, want 16", len(out))
	}
	for i := 0; i < 8; i++ {
		if v := int16(binary.LittleEndian.Uint16(out[i*2:])); v != 2000 {
			t.Fatalf("sample %d = %d, want the average 2000", i, v)
		}
	}
}

func TestTheResampledOutputIsClampedNotWrapped(t *testing.T) {
	// The filter can overshoot on a signal already at full scale, and an
	// int16 that wraps turns a peak into full-scale opposite polarity — a
	// crack rather than clipping.
	c := New(Options{Binary: "/bin/true"}, &fakeSink{}, &fakePlane{})
	rs := resample.New()
	in := make([]byte, 4*4096)
	for i := 0; i < 4096; i++ {
		binary.LittleEndian.PutUint16(in[i*4:], uint16(int16(32767)))
		binary.LittleEndian.PutUint16(in[i*4+2:], uint16(int16(32767)))
	}
	out := c.convert(rs, in)
	// Skip the filter's ramp-in: the history starts at zero, so the first
	// few hundred outputs are the step response and legitimately swing
	// either side of zero. What a WRAP looks like is different and
	// unmistakable — a value near -32768 where the signal is at +32767.
	const rampBytes = 2 * 4 * 64 // a few filter lengths
	if len(out) <= rampBytes {
		t.Fatalf("only %d bytes out — not past the ramp", len(out))
	}
	for i := rampBytes; i+1 < len(out); i += 2 {
		v := int16(binary.LittleEndian.Uint16(out[i:]))
		if v < 30000 {
			t.Fatalf("a full-scale positive signal produced %d at byte %d — "+
				"a wrap looks like this and clipping does not", v, i)
		}
	}
}

func TestAudioIsDroppedWhileHomeAssistantHoldsThePlane(t *testing.T) {
	bin := fakeShairport(t, "dd if=/dev/zero bs=8192 count=4 2>/dev/null\nsleep 1\n")
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
	// Killing it removes the Echo from every AirPlay list on the network,
	// which is the right outcome: a receiver that is listed, selected and
	// silent is worse than one that is not listed.
	bin := fakeShairport(t, "sleep 30\n")
	c := New(Options{Binary: bin}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	proc := waitForProc(t, c)
	done := make(chan struct{})
	go func() { proc.Wait(); close(done) }()
	c.Stop()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the process outlived Stop")
	}
}

func TestARenameRestartsTheReceiver(t *testing.T) {
	bin := fakeShairport(t, "sleep 30\n")
	c := New(Options{Binary: bin, Name: "Old"}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	proc := waitForProc(t, c)

	done := make(chan struct{})
	go func() { proc.Wait(); close(done) }()
	c.SetName("New")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a rename did not restart the receiver")
	}
	if c.name() != "New" || !c.Running() {
		t.Fatalf("name=%q running=%v", c.name(), c.Running())
	}
}

func TestRenamingToTheSameNameDoesNothing(t *testing.T) {
	bin := fakeShairport(t, "sleep 30\n")
	c := New(Options{Binary: bin, Name: "Lounge"}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	before := waitForProc(t, c)
	c.SetName("Lounge")
	c.SetName("")
	time.Sleep(200 * time.Millisecond)
	c.mu.Lock()
	after := c.proc
	c.mu.Unlock()
	if before != after {
		t.Fatal("a no-op rename restarted the receiver")
	}
}

func TestLeavingDoesNotStopTheSupervisor(t *testing.T) {
	bin := fakeShairport(t, "sleep 5\n")
	c := New(Options{Binary: bin}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	waitForProc(t, c)
	c.Leave("preempted")
	if !c.Running() {
		t.Fatal("a preemption stopped the supervisor — the Echo would never " +
			"come back as an AirPlay target")
	}
}

func TestTheCommandLineKeepsShairportOffAlsa(t *testing.T) {
	// The device already owns the speaker, and two things opening it is the
	// #80 failure: a blocking open with no timeout, eighteen minutes of a
	// stranded device.
	c := New(Options{Binary: "/bin/true", Name: "Lounge"}, &fakeSink{}, &fakePlane{})
	got := strings.Join(c.args(), " ")
	if !strings.Contains(got, "-o stdout") {
		t.Fatalf("not on stdout: %s", got)
	}
	if strings.Contains(got, "alsa") {
		t.Fatalf("ALSA reached the command line: %s", got)
	}
	if !strings.Contains(got, "-a Lounge") {
		t.Fatalf("the name is missing: %s", got)
	}
}

func TestClassicIsTheDefaultRate(t *testing.T) {
	// Classic AirPlay is 44.1kHz by definition, and it is what the build
	// recipe targets first. Defaulting to 48000 would play every classic
	// stream 8.8% fast, which reads as a broken receiver rather than as a
	// rate.
	c := New(Options{Binary: "/bin/true"}, &fakeSink{}, &fakePlane{})
	if c.opts.SourceRate != 44100 {
		t.Fatalf("default source rate = %d, want 44100", c.opts.SourceRate)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

func waitForProc(t *testing.T, c *Client) *os.Process {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		p := c.proc
		c.mu.Unlock()
		if p != nil {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the process never started")
	return nil
}

// waitForStableBytes waits until the sink has taken audio and stopped
// growing. Checking as soon as the first push lands reads a partial stream —
// the pump feeds one buffer per read and there are several.
func waitForStableBytes(t *testing.T, sink *fakeSink) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	last, stable := -1, 0
	for time.Now().Before(deadline) {
		n := sink.bytes()
		if n > 0 && n == last {
			stable++
			if stable >= 5 {
				return n
			}
		} else {
			stable = 0
		}
		last = n
		time.Sleep(20 * time.Millisecond)
	}
	if last <= 0 {
		t.Fatal("no audio reached the music plane")
	}
	return last
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
