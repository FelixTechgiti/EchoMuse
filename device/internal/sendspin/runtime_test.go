package sendspin

import (
	"encoding/binary"
	"sync"
	"testing"
	"time"
)

// fakeSink is the music plane. Everything the runtime does reaches the
// speaker through this, which is what makes the scheduler testable at all —
// the real one is behind a cgo build tag, and a scheduler exercisable only on
// hardware is one exercised by listening.
type fakeSink struct {
	mu       sync.Mutex
	pushed   [][]byte
	ended    int
	flushed  int
	delay    int64
	running  bool
	pumpFail error
}

func (s *fakeSink) PumpMusic(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pumpFail != nil {
		return s.pumpFail
	}
	s.pushed = append(s.pushed, append([]byte(nil), data...))
	return nil
}
func (s *fakeSink) EndMusicStream() { s.mu.Lock(); s.ended++; s.mu.Unlock() }
func (s *fakeSink) FlushMusic()     { s.mu.Lock(); s.flushed++; s.mu.Unlock() }
func (s *fakeSink) PlaybackDelay() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delay, s.running
}
func (s *fakeSink) frames() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.pushed {
		n += len(p) / bytesPerFrame
	}
	return n
}
func (s *fakeSink) setPos(delay int64, running bool) {
	s.mu.Lock()
	s.delay, s.running = delay, running
	s.mu.Unlock()
}

// fakePlane is the arbitration.
type fakePlane struct {
	mu      sync.Mutex
	held    bool
	refused bool
	claims  int
	frees   int
}

func (p *fakePlane) Claim() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.claims++
	if p.refused {
		return false
	}
	p.held = true
	return true
}
func (p *fakePlane) Release()       { p.mu.Lock(); p.held = false; p.frees++; p.mu.Unlock() }
func (p *fakePlane) MayWrite() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.held }

// pcmFrames builds n frames of mono 16-bit PCM with a recognisable ramp.
func pcmFrames(n int) []byte {
	out := make([]byte, n*bytesPerFrame)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(i)))
	}
	return out
}

// runtimeAt wires a runtime with a fixed clock and a synced snapshot whose
// server time equals local time, so a chunk timestamp IS the moment to play.
func runtimeAt(sink MusicSink, plane PlaneOwner, nowMicros *int64) *Runtime {
	r := NewRuntime(sink, plane, func() Snapshot {
		return Snapshot{Ready: true, Offset: 0, UseDrift: false}
	}, 0)
	r.now = func() time.Time { return time.UnixMicro(*nowMicros) }
	return r
}

func pcmStream() StreamStart {
	return StreamStart{Codec: CodecPCM, SampleRate: 48000, Channels: 1, BitDepth: 16}
}

func TestAudioIsDroppedWhileSomeoneElseHoldsThePlane(t *testing.T) {
	// The normal state while Home Assistant is playing. Writing anyway puts
	// two songs on one speaker, which is what the arbiter exists to prevent.
	sink := &fakeSink{}
	plane := &fakePlane{refused: true}
	now := int64(1_000_000)
	r := runtimeAt(sink, plane, &now)

	r.OnStreamStart(pcmStream())
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(4800)})

	if sink.frames() != 0 {
		t.Fatalf("wrote %d frames without owning the plane", sink.frames())
	}
	if plane.claims != 1 {
		t.Fatalf("claims = %d, want exactly one attempt", plane.claims)
	}
}

func TestARefusedClaimIsNotAnError(t *testing.T) {
	// The connection stays up and the session ends properly when the
	// arbiter says so. Fighting for the plane here is the failure.
	sink := &fakeSink{}
	plane := &fakePlane{refused: true}
	now := int64(1_000_000)
	r := runtimeAt(sink, plane, &now)
	r.OnStreamStart(pcmStream())
	r.OnStreamEnd() // must not panic, must still release
	if plane.frees != 1 {
		t.Fatalf("frees = %d", plane.frees)
	}
}

func TestAudioReachesTheSinkInWholePeriods(t *testing.T) {
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(1_000_000)
	r := runtimeAt(sink, plane, &now)
	sink.setPos(0, true)

	r.OnStreamStart(pcmStream())
	// One chunk of exactly two periods, timestamped for right now, so no
	// alignment padding is added.
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})

	sink.mu.Lock()
	pushed := len(sink.pushed)
	sizes := make([]int, len(sink.pushed))
	for i, p := range sink.pushed {
		sizes[i] = len(p)
	}
	sink.mu.Unlock()

	if pushed != 2 {
		t.Fatalf("pushed %d periods, want 2 (sizes %v)", pushed, sizes)
	}
	for i, n := range sizes {
		if n != periodFrames*bytesPerFrame {
			t.Fatalf("period %d is %d bytes, want %d", i, n, periodFrames*bytesPerFrame)
		}
	}
}

func TestAPartialPeriodIsHeldRatherThanPadded(t *testing.T) {
	// Chunks are 15–150ms and a period is 42.7ms — neither divides the
	// other, so padding each chunk to a period boundary would insert
	// silence 23 times a second.
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(1_000_000)
	r := runtimeAt(sink, plane, &now)
	// Hardware stopped, so nothing is corrected and the frame counts are
	// exactly what went in — this test is about the period boundary, not
	// about the corrector.
	sink.setPos(0, false)

	r.OnStreamStart(pcmStream())
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(periodFrames / 2)})
	if sink.frames() != 0 {
		t.Fatalf("a half period was pushed: %d frames", sink.frames())
	}
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(periodFrames / 2)})
	if sink.frames() != periodFrames {
		t.Fatalf("frames = %d, want one whole period", sink.frames())
	}
}

func TestTheFirstSampleIsDelayedBySilenceToHitItsTimestamp(t *testing.T) {
	// The only lever available at the start: nothing here controls when the
	// prime gate releases, so without padding the first sample plays at
	// whatever moment the buffer happened to fill. The plane plays what it
	// is given in order, so N frames of silence delay the audio by N frames.
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)
	sink.setPos(0, true)

	r.OnStreamStart(pcmStream())
	// Due 100ms from now: 4800 frames of silence should go in front.
	r.OnAudio(AudioChunk{Timestamp: now + 100_000, Data: pcmFrames(4 * periodFrames)})

	if got := sink.frames(); got < 4800 {
		t.Fatalf("pushed %d frames, want at least the 4800 of padding", got)
	}
	sink.mu.Lock()
	first := sink.pushed[0]
	sink.mu.Unlock()
	for i := 0; i < len(first); i++ {
		if first[i] != 0 {
			t.Fatalf("the first period is not silence (byte %d = %d)", i, first[i])
		}
	}
}

func TestAlreadyQueuedAudioCountsTowardsTheAlignment(t *testing.T) {
	// The buffer holds up to 5.46s. Padding as though it were empty would
	// delay the first sample by the buffer's contents on top of the gap it
	// was meant to close.
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)
	// 100ms already queued, and the chunk is due in 100ms: nothing to pad.
	sink.setPos(4800, true)

	r.OnStreamStart(pcmStream())
	r.OnAudio(AudioChunk{Timestamp: now + 100_000, Data: pcmFrames(2 * periodFrames)})

	if got := sink.frames(); got != 2*periodFrames {
		t.Fatalf("pushed %d frames, want exactly the audio (%d) with no padding",
			got, 2*periodFrames)
	}
}

func TestALateChunkIsNotPaddedIntoTheFuture(t *testing.T) {
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)
	sink.setPos(0, true)

	r.OnStreamStart(pcmStream())
	// 10ms late — inside the tolerance, so it plays, and there is nothing
	// to pad.
	r.OnAudio(AudioChunk{Timestamp: now - 10_000, Data: pcmFrames(2 * periodFrames)})
	if got := sink.frames(); got != 2*periodFrames {
		t.Fatalf("pushed %d frames, want %d", got, 2*periodFrames)
	}
}

func TestAChunkFarOutOfPositionIsDroppedAndReAnchors(t *testing.T) {
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)
	sink.setPos(0, true)

	r.OnStreamStart(pcmStream())
	r.OnAudio(AudioChunk{Timestamp: now - 5_000_000, Data: pcmFrames(4800)})
	if sink.frames() != 0 {
		t.Fatalf("a chunk 5s late was played: %d frames", sink.frames())
	}
	r.mu.Lock()
	anchored := r.anchored
	r.mu.Unlock()
	if anchored {
		t.Fatal("a dropped chunk left the alignment in place")
	}
}

func TestNothingIsWrittenBeforeTheClockIsSynced(t *testing.T) {
	// Playing at an unknown time is exactly what the spec forbids reporting
	// available for.
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(1_000_000)
	r := NewRuntime(sink, plane, func() Snapshot { return Snapshot{} }, 0)
	r.now = func() time.Time { return time.UnixMicro(now) }

	r.OnStreamStart(pcmStream())
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(4 * periodFrames)})
	if sink.frames() != 0 {
		t.Fatalf("wrote %d frames against an unsynced clock", sink.frames())
	}
}

func TestAClearFlushesAndReAnchorsButDoesNotEndTheStream(t *testing.T) {
	// A seek. Treating it as an end leaves the device quiet for the rest of
	// the track.
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)
	sink.setPos(0, true)

	r.OnStreamStart(pcmStream())
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})
	r.OnStreamClear()

	sink.mu.Lock()
	flushed, ended := sink.flushed, sink.ended
	sink.mu.Unlock()
	if flushed != 1 {
		t.Fatalf("flushes = %d, want 1", flushed)
	}
	if ended != 0 {
		t.Fatalf("a clear ended the stream (%d)", ended)
	}
	if !plane.MayWrite() {
		t.Fatal("a clear gave up the music plane")
	}

	// And audio still flows afterwards.
	before := sink.frames()
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})
	if sink.frames() <= before {
		t.Fatal("the stream did not survive the clear")
	}
}

func TestTheStreamEndReleasesThePlane(t *testing.T) {
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)

	r.OnStreamStart(pcmStream())
	r.OnStreamEnd()

	sink.mu.Lock()
	ended := sink.ended
	sink.mu.Unlock()
	if ended != 1 {
		t.Fatalf("EndMusicStream calls = %d", ended)
	}
	if plane.MayWrite() {
		t.Fatal("the plane was not released")
	}
}

func TestAudioAfterTheStreamEndsIsDropped(t *testing.T) {
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)
	sink.setPos(0, true)

	r.OnStreamStart(pcmStream())
	r.OnStreamEnd()
	before := sink.frames()
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})
	if sink.frames() != before {
		t.Fatal("audio was played after the stream ended")
	}
}

// ── The correction ────────────────────────────────────────────────────────

func TestDriftIsCorrectedFromTheHardwareNotFromOurOwnCount(t *testing.T) {
	// The point of reading `delay` at all. Our own bookkeeping is perfect by
	// construction and cannot see the hardware consuming 47973 frames a
	// second rather than 48000.
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)
	sink.setPos(0, true)

	r.OnStreamStart(pcmStream())
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})

	// 20ms more is queued than there should be, so the next sample would
	// land 20ms LATE. The correction is to remove samples and catch up —
	// and the opposite sign here doubles the drift instead of cancelling
	// it, with a symptom identical to no correction at all.
	sink.setPos(960, true)
	before := sink.frames()
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})
	added := sink.frames() - before

	if added == 0 {
		t.Fatal("nothing was pushed")
	}
	if added >= 2*periodFrames {
		t.Fatalf("added %d frames with 20ms of queue error — no correction applied",
			added)
	}
}

func TestNoCorrectionInsideTheDeadband(t *testing.T) {
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)
	sink.setPos(0, true)

	r.OnStreamStart(pcmStream())
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})
	before := sink.frames()

	// Ten samples of error: well inside the 24-sample deadband.
	sink.setPos(10, true)
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})
	if got := sink.frames() - before; got != 2*periodFrames {
		t.Fatalf("added %d frames inside the deadband, want %d", got, 2*periodFrames)
	}
}

func TestNothingIsCorrectedWhileTheHardwareIsNotRunning(t *testing.T) {
	// Before the prime gate releases there is nothing to measure against,
	// and a delay of 0 read as real says the buffer is empty.
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)
	sink.setPos(0, false)

	r.OnStreamStart(pcmStream())
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})
	before := sink.frames()
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})
	if got := sink.frames() - before; got != 2*periodFrames {
		t.Fatalf("added %d frames with the hardware stopped, want %d",
			got, 2*periodFrames)
	}
}

func TestAHugeErrorFlushesRatherThanCreeping(t *testing.T) {
	sink := &fakeSink{}
	plane := &fakePlane{}
	now := int64(10_000_000)
	r := runtimeAt(sink, plane, &now)
	sink.setPos(0, true)

	r.OnStreamStart(pcmStream())
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})

	sink.setPos(48000, true) // a second out
	r.OnAudio(AudioChunk{Timestamp: now, Data: pcmFrames(2 * periodFrames)})

	sink.mu.Lock()
	flushed := sink.flushed
	sink.mu.Unlock()
	if flushed == 0 {
		t.Fatal("a second of error did not trigger a hard resync")
	}
}

// ── adjustFrames ──────────────────────────────────────────────────────────

func TestDroppingRemovesExactlyTheFramesAsked(t *testing.T) {
	in := pcmFrames(1000)
	out := adjustFrames(in, -7)
	if got := len(out) / bytesPerFrame; got != 993 {
		t.Fatalf("dropped to %d frames, want 993", got)
	}
}

func TestDuplicatingAddsExactlyTheFramesAsked(t *testing.T) {
	in := pcmFrames(1000)
	out := adjustFrames(in, 7)
	if got := len(out) / bytesPerFrame; got != 1007 {
		t.Fatalf("padded to %d frames, want 1007", got)
	}
}

func TestTheCorrectionIsSpreadNotBunched(t *testing.T) {
	// Removing eight consecutive frames is a 0.17ms discontinuity — a
	// click. Removing eight spread across 100ms is eight single-sample
	// repeats nobody can hear, and that is the whole reason this is cheaper
	// than resampling.
	in := pcmFrames(4800)
	out := adjustFrames(in, -8)

	// The ramp makes a gap visible: consecutive removals would leave one
	// jump of 8, spread ones leave eight jumps of 2.
	jumps := 0
	worst := 0
	prev := int(int16(binary.LittleEndian.Uint16(out[0:])))
	for i := 1; i < len(out)/bytesPerFrame; i++ {
		v := int(int16(binary.LittleEndian.Uint16(out[i*2:])))
		if d := v - prev; d != 1 {
			jumps++
			if d > worst {
				worst = d
			}
		}
		prev = v
	}
	if jumps != 8 {
		t.Fatalf("%d discontinuities, want 8 (one per dropped frame)", jumps)
	}
	if worst > 2 {
		t.Fatalf("worst gap is %d frames — the correction was bunched", worst)
	}
}

func TestAdjustingByNothingReturnsTheInput(t *testing.T) {
	in := pcmFrames(100)
	if out := adjustFrames(in, 0); len(out) != len(in) {
		t.Fatalf("a zero adjustment changed the length")
	}
}

func TestAnAdjustmentLargerThanTheChunkIsClamped(t *testing.T) {
	in := pcmFrames(10)
	if out := adjustFrames(in, 100); len(out)/bytesPerFrame > 20 {
		t.Fatalf("padded 10 frames to %d", len(out)/bytesPerFrame)
	}
	if out := adjustFrames(in, -100); len(out) != 0 {
		t.Fatalf("dropping more than the chunk left %d bytes", len(out))
	}
}

// ── Decoding ──────────────────────────────────────────────────────────────

func TestMonoPcmPassesThroughUntouched(t *testing.T) {
	in := pcmFrames(100)
	out, err := decodeChunk(pcmStream(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("mono PCM was rewritten: %d bytes in, %d out", len(in), len(out))
	}
}

func TestStereoIsAveragedInWiderArithmeticNotAddedInInt16(t *testing.T) {
	// Two channels near full scale sum past 32767 and wrap to full-scale
	// negative, which is a far worse artefact than the clipping it looks
	// like. Same rule the mixer already follows.
	in := make([]byte, 4)
	binary.LittleEndian.PutUint16(in[0:], uint16(int16(32000)))
	binary.LittleEndian.PutUint16(in[2:], uint16(int16(32000)))

	f := pcmStream()
	f.Channels = 2
	out, err := decodeChunk(f, in)
	if err != nil {
		t.Fatal(err)
	}
	got := int16(binary.LittleEndian.Uint16(out))
	if got != 32000 {
		t.Fatalf("downmix = %d, want 32000 (a wrap would be strongly negative)", got)
	}
}

func TestFlacIsRefusedRatherThanPlayedAsNoise(t *testing.T) {
	// Advertised behind PCM precisely so this cannot be reached. Reaching
	// it means the ordering rule in SupportedFormats was broken, and the
	// difference between an error here and no check at all is the
	// difference between a log line and noise from the speaker.
	f := pcmStream()
	f.Codec = CodecFLAC
	if _, err := decodeChunk(f, pcmFrames(100)); err == nil {
		t.Fatal("FLAC decoded without a decoder")
	}
}

func TestAnUnadvertisedBitDepthIsRefused(t *testing.T) {
	f := pcmStream()
	f.BitDepth = 24
	if _, err := decodeChunk(f, pcmFrames(100)); err == nil {
		t.Fatal("24-bit PCM was accepted")
	}
}
