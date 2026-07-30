// Package shadow scores the wake stream on the device WITHOUT acting on it, so
// on-device detection can be compared against the controller's on the same
// audio before anything depends on it.
//
// The one hard rule: a shadow feature must never be able to damage the real
// audio path. Inference costs ~31ms per 80ms frame on an Echo Dot, and the mic
// goroutine reads 160ms ALSA batches into a ring only 160ms deep — running two
// frames of inference inline would spend 62ms of that budget and risk the
// capture stalls that lose whole batches. So Push only hands the samples to a
// buffered channel and returns; a separate goroutine does the work, and when it
// falls behind, frames are DROPPED AND COUNTED rather than blocking the caller.
// A shadow run that drops frames tells you something useful. One that stutters
// the microphone tells you nothing and costs a working device.
//
// Scoring is otherwise identical to the controller's: the same models, the same
// streaming buffers, the same threshold. Nothing here triggers a turn, touches
// the LEDs, or sends audio — it counts, and it reports crossings.
package shadow

import (
	"io"
	"sync"
	"time"

	"github.com/wilbowes/EchoMuse/internal/wakeword"
)

// queueFrames is how much audio may be in flight to the scorer goroutine.
// 8 frames is 640ms: enough to ride out a scheduling hiccup or a slow frame,
// short enough that a persistently overloaded device drops promptly instead of
// scoring audio from a second ago and reporting a crossing that has no
// plausible relationship to what was said.
const queueFrames = 8

// DefaultRefractory suppresses repeat crossings after one fires. Wake scores
// stay above threshold for several consecutive frames, so without this a single
// utterance emits a handful of crossings — noise in the logs, and a correlation
// window that matches whichever one happens to be nearest.
const DefaultRefractory = 1500 * time.Millisecond

// Stats is a snapshot of what the scorer has seen. Counters are cumulative
// since the last Drain.
type Stats struct {
	// Frames scored, and Drops that never reached the scorer because it was
	// behind. Drops are the health metric: a nonzero rate means the device
	// cannot keep up and the comparison is running on a subset of the audio.
	Frames uint64
	Drops  uint64
	// NotReady counts frames pushed before the detector had 16 embeddings
	// (~1.28s after a reset). Expected to be small and nonzero.
	NotReady uint64
	// Crossings is how many times the score reached the threshold, after the
	// refractory period is applied.
	Crossings uint64
	// MaxScore is the highest score seen. This is the number that says whether
	// a device is nearly detecting or nowhere close.
	MaxScore float32
	// Threshold is the crossing threshold in force at the end of the window.
	// Reported because the controller cannot judge a non-crossing without it:
	// during playback the controller lowers its own bar to the barge-in
	// threshold, and a device scoring against the normal one was never asked
	// the same question. Without this the comparison records every barge-in as
	// an on-device miss.
	Threshold float32
	// Errors counts inference failures, with the most recent message. An
	// inference error is not fatal here: the scorer keeps going, because
	// losing shadow data is always preferable to losing the audio path.
	Errors  uint64
	LastErr string
}

// Scorer runs a Detector over pushed audio on its own goroutine.
type Scorer struct {
	det       *wakeword.Detector
	threshold float32
	// bargeThreshold is used instead of threshold while the speaker is
	// streaming, mirroring the controller's own effective threshold. Zero
	// disables the behaviour, so a controller that never sends one leaves the
	// scorer exactly as it was.
	bargeThreshold float32
	speakerActive  func() bool
	refract        time.Duration
	onCross        func(score float32, at time.Time)

	ch   chan []int16
	done chan struct{}
	stop sync.Once

	// closer releases the inference engine, when one owns resources (the ORT
	// sessions). Nil for an injected Inferer, e.g. in tests.
	closer io.Closer
	// info describes the loaded engine for logging: runtime version, model,
	// whether XNNPACK attached. Set once at construction, never mutated.
	info string

	mu        sync.Mutex
	stats     Stats
	ready     bool
	lastCross time.Time
	// resetReq is consumed by the scorer goroutine rather than acted on by
	// the caller, because Detector is not safe for concurrent use and the
	// caller is a different goroutine.
	resetReq bool
}

// NewScorer starts a scorer. onCross is called from the scorer goroutine when
// the score reaches threshold, so it must not block — the controller-bound
// send it wraps is buffered for that reason.
func NewScorer(inf wakeword.Inferer, threshold float32, onCross func(score float32, at time.Time)) *Scorer {
	s := &Scorer{
		det:       wakeword.New(inf),
		threshold: threshold,
		refract:   DefaultRefractory,
		onCross:   onCross,
		ch:        make(chan []int16, queueFrames),
		done:      make(chan struct{}),
	}
	go s.run()
	return s
}

// SetThreshold updates the crossing threshold, which the controller can change
// at any time via a config push.
func (s *Scorer) SetThreshold(t float32) {
	s.mu.Lock()
	s.threshold = t
	s.mu.Unlock()
}

// SetBargeThreshold sets the threshold used while the speaker is streaming, and
// the predicate for "is it streaming". Pass 0 (or a nil predicate) to score
// against the normal threshold at all times.
//
// This mirrors the controller: echo at the mic is ~25dB louder than the person,
// so speech-over-TTS scores are depressed and the controller drops its bar to
// bargeInThreshold during playback. A device that did not do the same would
// disagree with the controller on every barge-in — and did, which is how this
// was found.
func (s *Scorer) SetBargeThreshold(t float32, active func() bool) {
	s.mu.Lock()
	s.bargeThreshold = t
	s.speakerActive = active
	s.mu.Unlock()
}

// effectiveThreshold picks the bar currently in force. Caller holds mu.
func (s *Scorer) effectiveThresholdLocked() float32 {
	if s.bargeThreshold > 0 && s.speakerActive != nil && s.speakerActive() &&
		s.bargeThreshold < s.threshold {
		return s.bargeThreshold
	}
	return s.threshold
}

// Push queues one chunk of 16kHz mono PCM. It never blocks and never fails:
// if the scorer is behind, the frame is dropped and counted.
//
// The samples are COPIED, because the mic pipeline reuses its buffers between
// periods. One 2.5KB allocation per 80ms is immaterial next to the inference
// it feeds, and the alternative — a buffer pool shared across goroutines — is
// a lifetime bug waiting to happen on the audio path.
func (s *Scorer) Push(samples []int16) {
	cp := make([]int16, len(samples))
	copy(cp, samples)
	s.enqueue(cp)
}

// PushBytes is Push for little-endian S16 bytes, which is what the mic
// pipeline carries. An odd trailing byte cannot happen (frames are whole
// samples) and is dropped rather than silently shifting every sample after it.
func (s *Scorer) PushBytes(b []byte) {
	n := len(b) / 2
	if n == 0 {
		return
	}
	cp := make([]int16, n)
	for i := 0; i < n; i++ {
		cp[i] = int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)
	}
	s.enqueue(cp)
}

// enqueue is the only path into the scorer, and the only place a drop can
// happen — deliberately one place, so "never block the audio path" is a
// property of one function rather than a convention.
func (s *Scorer) enqueue(cp []int16) {
	select {
	case s.ch <- cp:
	default:
		s.mu.Lock()
		s.stats.Drops++
		s.mu.Unlock()
	}
}

// Ready reports whether the detector has accumulated enough embeddings to
// score (~1.28s of audio). Exposed through the mutex rather than by reading the
// Detector, which belongs to the scorer goroutine and is not safe to touch from
// anywhere else — worth having so a log line can distinguish "no crossings
// because the room was quiet" from "no crossings because it never warmed up".
func (s *Scorer) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// Reset asks the scorer to clear its streaming state, for when the mic stream
// restarts — audio from before a gap must not contribute to a score after it.
// Applied by the scorer goroutine, since Detector is single-goroutine.
func (s *Scorer) Reset() {
	s.mu.Lock()
	s.resetReq = true
	s.mu.Unlock()
}

// Drain returns the accumulated stats and zeroes the counters, so the caller
// can attach them to a periodic report. MaxScore resets too: it is the maximum
// within a reporting window, not since boot, which is what makes a series of
// them readable.
func (s *Scorer) Drain() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.stats
	s.stats = Stats{}
	return out
}

// Info describes the loaded inference engine, for a log line at startup.
func (s *Scorer) Info() string { return s.info }

// Close stops the scorer goroutine and releases the inference engine. Safe to
// call more than once: it runs both on a config change and at shutdown, and
// those can overlap.
func (s *Scorer) Close() {
	s.stop.Do(func() {
		close(s.ch)
		<-s.done
		// After the goroutine has exited, so no inference is in flight.
		if s.closer != nil {
			_ = s.closer.Close()
		}
	})
}

func (s *Scorer) run() {
	defer close(s.done)
	for pcm := range s.ch {
		s.mu.Lock()
		reset := s.resetReq
		s.resetReq = false
		threshold := s.effectiveThresholdLocked()
		s.stats.Threshold = threshold
		s.mu.Unlock()

		if reset {
			s.det.Reset()
			s.mu.Lock()
			s.ready = false
			s.mu.Unlock()
		}

		if _, err := s.det.Push(pcm); err != nil {
			s.recordErr(err)
			continue
		}
		if !s.det.Ready() {
			s.mu.Lock()
			s.stats.Frames++
			s.stats.NotReady++
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		s.ready = true
		s.mu.Unlock()
		score, err := s.det.Score()
		if err != nil {
			s.recordErr(err)
			continue
		}

		now := time.Now()
		s.mu.Lock()
		s.stats.Frames++
		if score > s.stats.MaxScore {
			s.stats.MaxScore = score
		}
		crossed := score >= threshold && now.Sub(s.lastCross) >= s.refract
		if crossed {
			s.stats.Crossings++
			s.lastCross = now
		}
		s.mu.Unlock()

		if crossed && s.onCross != nil {
			s.onCross(score, now)
		}
	}
}

func (s *Scorer) recordErr(err error) {
	s.mu.Lock()
	s.stats.Frames++
	s.stats.Errors++
	s.stats.LastErr = err.Error()
	s.mu.Unlock()
}
