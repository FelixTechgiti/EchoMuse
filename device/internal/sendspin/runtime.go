package sendspin

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/wilbowes/EchoMuse/internal/pcm"
)

// Turning scheduled chunks into sound.
//
// The device's music plane is a FIFO with a prime gate: periods go in, the
// ALSA loop takes them at the hardware's own rate, and PumpMusic blocks once
// the buffer is full — so the feed is rate-limited to playback for free and
// nothing here has to pace it. What this has to do instead is get the audio
// to come out at the moment the server named, and keep it there.
//
// Alignment is by SILENCE at the start and by sample-level correction
// afterwards, and both halves are needed:
//
//   - At the start nothing controls when the prime gate releases, so the
//     first real sample would play at whatever moment the buffer happened to
//     fill. Padding with silence moves it: the plane plays what it is given
//     in order, so N frames of silence in front of the audio delays it by
//     exactly N frames.
//   - Afterwards the hardware runs at its own rate, and `delay` (appl_ptr −
//     hw_ptr) is what makes that visible. It grows when we push faster than
//     the hardware consumes, which is exactly the drift signal, and it is a
//     real DMA position rather than our own bookkeeping.
//
// Everything reaches the speaker through a MusicSink, so the whole runtime is
// testable on the host — the real sink is behind a cgo build tag and a
// scheduler that can only be exercised on hardware is one that gets exercised
// by listening.

// MusicSink is the device's music plane, as much of it as this needs.
type MusicSink interface {
	// PumpMusic queues one period of mono 16-bit PCM. Blocks when the
	// buffer is full, which is what paces the feed.
	PumpMusic(data []byte) error
	// EndMusicStream marks the stream complete so a drain is not counted as
	// an underrun.
	EndMusicStream()
	// FlushMusic discards what is queued — a seek, or leaving the group.
	FlushMusic()
	// PlaybackDelay reports frames queued and not yet played, and whether
	// the hardware is actually running. Not valid until it is: before the
	// prime gate releases there is nothing to measure against.
	PlaybackDelay() (frames int64, running bool)
}

// PlaneOwner is the arbitration this runtime has to respect. Kept as an
// interface rather than importing internal/musicplane so this package stays
// free of everything but the protocol — and so a test can drive the
// preemption path without building an arbiter.
type PlaneOwner interface {
	Claim() bool
	Release()
	MayWrite() bool
}

// periodFrames is the device's ALSA period, in frames. Chunks are 15–150ms
// and periods are 42.7ms, so neither divides the other and the runtime holds
// a remainder between chunks rather than padding each one — padding per chunk
// would insert silence 23 times a second.
const periodFrames = 2048

// bytesPerFrame for the wire format this device asks for: mono, 16-bit.
const bytesPerFrame = 2

// Runtime implements Handler: it is what a Conn hands its chunks to.
//
// Asserted rather than assumed: Handler has eight methods and a rename on
// either side produces a Conn that compiles and calls nothing.
var _ Handler = (*Runtime)(nil)

// Runtime implements Handler: it is what a Conn hands its chunks to.
type Runtime struct {
	sink   MusicSink
	plane  PlaneOwner
	clock  func() Snapshot
	now    func() time.Time
	policy CorrectionPolicy

	// outputDelayMs is this device's write-to-ear latency, subtracted when
	// converting a server timestamp into a moment to write at.
	outputDelayMs int

	mu sync.Mutex
	// format of the stream in flight, nil when none is.
	format *StreamStart
	// flacDec is built at stream start for a FLAC stream and nil otherwise.
	// It holds the STREAMINFO the server sent out of band, which the chunks
	// do not carry.
	flacDec *flacDecoder
	// pending holds bytes not yet forming a whole period.
	pending []byte
	// anchored is set once the first real sample has been placed. Until
	// then the runtime is padding silence to hit the first timestamp.
	anchored bool
	// pushedFrames counts what has gone into the sink this stream,
	// including padding. Only for logging: the correction reads the
	// hardware, never this.
	pushedFrames int64
	// lastLog rate-limits the correction log line, which would otherwise
	// print 23 times a second.
	lastLog time.Time
}

// NewRuntime wires one.
func NewRuntime(sink MusicSink, plane PlaneOwner, clock func() Snapshot,
	outputDelayMs int) *Runtime {
	return &Runtime{
		sink:          sink,
		plane:         plane,
		clock:         clock,
		now:           time.Now,
		policy:        DefaultPolicy,
		outputDelayMs: outputDelayMs,
	}
}

// OnActivate records what the server turned on. A player role the server did
// not activate means no audio is coming, which is worth a log line rather
// than silence — "it connected and nothing happened" is otherwise
// indistinguishable from a paused group.
func (r *Runtime) OnActivate(a ServerActivate) {
	if len(a.ActiveRoles) == 0 {
		log.Printf("[sendspin] activated: %v (no roles named — assuming player)",
			a.Activities)
		return
	}
	if !AppliesToPlayer(a.ActiveRoles) {
		log.Printf("[sendspin] the server activated %v and NOT the player role — "+
			"no audio will arrive on this connection", a.ActiveRoles)
		return
	}
	log.Printf("[sendspin] activated: %v, roles %v", a.Activities, a.ActiveRoles)
}

// OnSynced is the first moment a server timestamp can be converted at all.
func (r *Runtime) OnSynced() { log.Println("[sendspin] clock synced") }

// OnCommand is a no-op by design: this client claims no supported_commands,
// because volume and mute here belong to the controller and the button. A
// command arriving anyway is logged rather than dropped — it means the
// server believes something about this client that is not true.
func (r *Runtime) OnCommand(c PlayerCommand) {
	log.Printf("[sendspin] ignoring a player command we never claimed to "+
		"support: %+v", c)
}

// OnGroupUpdate is informational for now.
func (r *Runtime) OnGroupUpdate(g GroupUpdate) {
	log.Printf("[sendspin] group %q: %s", g.GroupName, g.PlaybackState)
}

// OnStreamStart claims the music plane and resets the alignment.
//
// The claim can FAIL — Home Assistant may hold the plane — and a failure is
// not an error to retry: the connection stays up, the chunks are dropped, and
// the session ends properly the moment the arbiter says so. Fighting for the
// plane here would put two songs on one speaker, which is what the arbiter
// exists to prevent.
func (r *Runtime) OnStreamStart(s StreamStart) {
	if !r.plane.Claim() {
		log.Printf("[sendspin] stream started but Home Assistant holds the " +
			"music plane — dropping audio until it does not")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f := s
	r.format = &f
	r.flacDec = nil
	r.pending = r.pending[:0]
	r.anchored = false
	r.pushedFrames = 0
	log.Printf("[sendspin] stream: %s %dHz %dch %dbit",
		s.Codec, s.SampleRate, s.Channels, s.BitDepth)

	if s.Codec == CodecFLAC {
		// Built HERE rather than lazily on the first chunk, so a malformed
		// or missing header is one log line at stream start instead of the
		// same failure 23 times a second — and so the stream is refused
		// before any audio is placed against a timestamp.
		dec, err := newFLACDecoder(s)
		if err != nil {
			log.Printf("[sendspin] %v — dropping this stream", err)
			r.format = nil
			return
		}
		r.flacDec = dec
	}
}

// OnStreamClear is a SEEK, not a stop. What is buffered is discarded and the
// alignment is dropped — the next chunk carries a new timestamp and the
// silence padding has to be recomputed against it — but the stream itself
// continues, and treating this as an end would leave the device quiet for the
// rest of the track.
func (r *Runtime) OnStreamClear() {
	r.sink.FlushMusic()
	r.mu.Lock()
	r.pending = r.pending[:0]
	r.anchored = false
	r.pushedFrames = 0
	r.mu.Unlock()
	log.Println("[sendspin] stream cleared — re-anchoring on the next chunk")
}

// OnStreamEnd closes the stream and gives the plane back. Nothing rejoins:
// the plane is simply free.
func (r *Runtime) OnStreamEnd() {
	r.mu.Lock()
	r.format = nil
	r.flacDec = nil
	tail := len(r.pending)
	r.pending = r.pending[:0]
	r.mu.Unlock()

	if tail > 0 {
		// A partial period at the end is padded rather than dropped: the
		// last few milliseconds of a track are inaudible as a gap and
		// obvious as a click if the period is left half full.
		pad := make([]byte, periodFrames*bytesPerFrame)
		if err := r.sink.PumpMusic(pad); err != nil {
			log.Printf("[sendspin] final period: %v", err)
		}
	}
	r.sink.EndMusicStream()
	r.plane.Release()
	log.Println("[sendspin] stream ended")
}

// OnAudio is the hot path: one chunk, 15–150ms of it, ~23 times a second.
func (r *Runtime) OnAudio(c AudioChunk) {
	if !r.plane.MayWrite() {
		// Someone else owns the plane. Dropped silently — this is the
		// normal state while Home Assistant is playing, and a log line per
		// chunk would be 23 a second.
		return
	}

	r.mu.Lock()
	format := r.format
	dec := r.flacDec
	r.mu.Unlock()
	if format == nil {
		return
	}

	var pcm []byte
	var err error
	if dec != nil {
		pcm, err = dec.decode(c.Data)
	} else {
		pcm, err = decodeChunk(*format, c.Data)
	}
	if err != nil {
		log.Printf("[sendspin] decode: %v", err)
		return
	}
	if len(pcm) == 0 {
		return
	}

	playAt, ok := PlayAt(r.clock(), c.Timestamp, r.outputDelayMs)
	if !ok {
		// Not synced. Dropping is right: playing at an unknown time is
		// what the spec forbids reporting available for.
		return
	}

	now := r.now().UnixMicro()
	switch Classify(playAt, now, int64(lateToleranceMs)*1000, int64(leadLimitS)*1_000_000) {
	case ActionDrop:
		log.Printf("[sendspin] dropping a chunk %dms out of position",
			(playAt-now)/1000)
		r.mu.Lock()
		r.anchored = false
		r.mu.Unlock()
		return
	case ActionHold, ActionPlay:
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.anchored {
		r.alignLocked(playAt, now, format.SampleRate)
		r.anchored = true
	} else {
		pcm = r.correctLocked(pcm, playAt, now, format.SampleRate)
	}

	r.pending = append(r.pending, pcm...)
	r.drainLocked()
}

// lateToleranceMs is how far past its moment a chunk may still be played.
// Chunks arrive in bursts and the moment one is examined is not the moment it
// plays, so a strict test discards audio that would have been on time.
const lateToleranceMs = 50

// leadLimitS bounds how far ahead a timestamp may be and still be treated as
// an instruction. Beyond it, it is more likely a stale clock estimate, and
// holding on it would stall the stream for hours.
const leadLimitS = 30

// alignLocked places the first real sample by padding with silence.
//
// The music plane plays what it is given, in order, so N frames of silence in
// front of the audio delays it by exactly N frames. That is the only lever
// available at the start: nothing here controls when the prime gate releases,
// so without padding the first sample plays at whatever moment the buffer
// happened to fill.
func (r *Runtime) alignLocked(playAt, now int64, rate int) {
	delay, running := r.sink.PlaybackDelay()
	if !running {
		// Nothing is playing yet, so the audio starts when the prime gate
		// releases and there is nothing to pad against. Anchoring here and
		// letting the steady-state correction take over is deliberate: the
		// alternative is guessing at the gate's timing, and the corrector
		// measures it rather than guessing.
		delay = 0
	}
	// When the next sample pushed would play, if nothing were added.
	wouldPlayAt := now + delay*1_000_000/int64(rate)
	ahead := playAt - wouldPlayAt
	if ahead <= 0 {
		// Already late. Nothing to pad; the corrector works it off, or
		// declares a hard resync if it is far enough out.
		return
	}
	pad := ErrorSamples(ahead, rate)
	// Never pad beyond the buffer: the plane holds ~5.46s and pushing more
	// silence than that blocks the read loop for the difference.
	if maxPad := rate * 5; pad > maxPad {
		pad = maxPad
	}
	if pad <= 0 {
		return
	}
	r.pending = append(r.pending, make([]byte, pad*bytesPerFrame)...)
	r.pushedFrames += int64(pad)
	log.Printf("[sendspin] aligning: %d frames (%.1fms) of silence before the "+
		"first sample", pad, float64(pad)*1000/float64(rate))
}

// correctLocked applies the drift correction to one chunk.
//
// The error comes from the HARDWARE — `delay` is appl_ptr minus hw_ptr, a
// real DMA position — not from counting what has been pushed. Our own count
// is perfect by construction and cannot see the hardware consuming 47973
// frames a second rather than 48000, which is the whole of the drift.
func (r *Runtime) correctLocked(pcm []byte, playAt, now int64, rate int) []byte {
	delay, running := r.sink.PlaybackDelay()
	if !running {
		return pcm // nothing to measure against yet
	}
	// Where the next sample pushed will actually play: now, plus everything
	// already queued, plus what is waiting in `pending`.
	//
	// ADDING `pending` IS WHAT MAKES THIS SIGNAL SMOOTH, and without it the
	// corrector would chase a sawtooth. `delay` jumps by a whole period
	// (2048 frames, 42.7ms) every time a period is pushed, which is far
	// outside the 24-sample deadband — but the same push removes exactly
	// those frames from `pending`, so the sum does not move. Consumption is
	// smooth on this hardware for a separate reason: hw_ptr is a real DMA
	// position advancing in 112–144 frame steps every ~2.4ms, not software
	// bookkeeping that would step a whole period at a time.
	//
	// So no smoothing is applied here, deliberately. An EMA over a signal
	// that is already clean would only add lag to the one measurement the
	// correction depends on, and the deadband already absorbs the jitter
	// that remains (a procfs read and a scheduling delay).
	queued := delay + int64(len(r.pending)/bytesPerFrame)
	wouldPlayAt := now + queued*1_000_000/int64(rate)
	// THE SIGN. CorrectionPolicy takes a POSITION error — where playback is
	// minus where it should be — so positive means the audio is coming out
	// EARLY and needs padding to hold it back. Here the natural quantity is
	// the other way round: wouldPlayAt is when the next sample lands, so
	// wouldPlayAt > playAt means late. Subtracting in this order converts
	// one into the other, and getting it backwards doubles the drift instead
	// of cancelling it — with a symptom, a room walking steadily further
	// out, identical to no correction at all.
	errMicros := playAt - wouldPlayAt
	errSamples := ErrorSamples(errMicros, rate)

	frames := len(pcm) / bytesPerFrame
	plan := r.policy.Plan(errSamples, frames)

	if plan.HardResync {
		log.Printf("[sendspin] hard resync: %dms out of position", -errMicros/1000)
		r.sink.FlushMusic()
		r.pending = r.pending[:0]
		r.anchored = false
		return pcm
	}
	if plan.Adjust == 0 {
		return pcm
	}
	if time.Since(r.lastLog) > 30*time.Second {
		r.lastLog = time.Now()
		log.Printf("[sendspin] drift: %dµs (%d samples), correcting by %d",
			errMicros, errSamples, plan.Adjust)
	}
	return adjustFrames(pcm, plan.Adjust)
}

// adjustFrames removes or duplicates whole frames, spread evenly across the
// chunk.
//
// SPREAD, not taken from one place. Removing eight consecutive frames is a
// 0.17ms discontinuity — a click; removing eight frames spread across 100ms
// is eight single-sample repeats nobody can hear. The whole reason this is
// cheaper than resampling is that the correction is small enough to hide, and
// bunching it up spends that advantage.
func adjustFrames(pcm []byte, adjust int) []byte {
	frames := len(pcm) / bytesPerFrame
	if frames == 0 || adjust == 0 {
		return pcm
	}
	if adjust > 0 {
		// Duplicate: the audio is coming out early and needs holding back.
		if adjust > frames {
			adjust = frames
		}
		step := frames / adjust
		// Offset by half a step so no adjustment lands on a chunk boundary.
		// Two chunks joined at a repeated or missing frame put the whole
		// correction at the seam, which is the one place a discontinuity is
		// most likely to be audible.
		off := step / 2
		out := make([]byte, 0, len(pcm)+adjust*bytesPerFrame)
		done := 0
		for i := 0; i < frames; i++ {
			f := pcm[i*bytesPerFrame : (i+1)*bytesPerFrame]
			out = append(out, f...)
			if done < adjust && i >= off && (i-off)%step == 0 {
				out = append(out, f...)
				done++
			}
		}
		return out
	}

	drop := -adjust
	if drop >= frames {
		return pcm[:0]
	}
	step := frames / drop
	off := step / 2
	out := make([]byte, 0, len(pcm)-drop*bytesPerFrame)
	done := 0
	for i := 0; i < frames; i++ {
		if done < drop && i >= off && (i-off)%step == 0 {
			done++
			continue
		}
		out = append(out, pcm[i*bytesPerFrame:(i+1)*bytesPerFrame]...)
	}
	return out
}

// drainLocked hands whole periods to the sink and keeps the remainder.
//
// A remainder is kept rather than padded because chunks are 15–150ms and a
// period is 42.7ms — neither divides the other, so padding each chunk to a
// period boundary would insert silence 23 times a second.
func (r *Runtime) drainLocked() {
	const periodBytes = periodFrames * bytesPerFrame
	for len(r.pending) >= periodBytes {
		period := r.pending[:periodBytes]
		if err := r.sink.PumpMusic(period); err != nil {
			log.Printf("[sendspin] PumpMusic: %v", err)
			r.pending = r.pending[:0]
			return
		}
		r.pushedFrames += periodFrames
		r.pending = r.pending[periodBytes:]
	}
	// Reclaim the head of the slice so `pending` does not grow without
	// bound across a long stream.
	if len(r.pending) == 0 {
		r.pending = r.pending[:0]
	} else if cap(r.pending) > 8*periodBytes {
		r.pending = append([]byte(nil), r.pending...)
	}
}

// decodeChunk turns a chunk's payload into mono 16-bit PCM at the device's
// rate.
func decodeChunk(f StreamStart, data []byte) ([]byte, error) {
	switch f.Codec {
	case CodecPCM:
		if f.BitDepth != 16 {
			return nil, fmt.Errorf("sendspin: %d-bit PCM is not advertised", f.BitDepth)
		}
		if f.Channels == 1 {
			return data, nil
		}
		if f.Channels == 2 {
			return pcm.DownmixStereo(data), nil
		}
		return nil, fmt.Errorf("sendspin: %d channels", f.Channels)
	case CodecFLAC:
		// Handled by the stream's own decoder — it holds the STREAMINFO the
		// server sent out of band, which a per-chunk function cannot.
		return nil, fmt.Errorf("sendspin: FLAC needs the stream decoder")
	}
	return nil, fmt.Errorf("sendspin: codec %q", f.Codec)
}
