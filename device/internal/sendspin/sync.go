package sendspin

// Staying in sync after the first sample.
//
// Landing the first sample at the right microsecond is the easy half. The
// speaker's own crystal then runs at its own rate — measured on this hardware
// at 47973 frames per second against 48000 nominal, −560ppm — so a stream
// that starts perfectly is 0.56ms out after a second, 34ms out after a
// minute, and a second and a half out over a long album side. The spec asks
// for ±1ms steady state. Nothing about that is achievable by scheduling
// alone; something has to CORRECT, continuously, for as long as the music
// plays.
//
// There are two ways to correct and this takes the cheaper one. Resampling at
// a slightly adjusted rate is what a desktop client does and it is
// inaudible — and it means a variable-rate resampler on the audio path of a
// device that is already spending ~18-20% of a core on the microphone and
// ~38% on the wake word. Dropping and duplicating whole samples costs
// nothing, and at the rates involved here it is inaudible for a different
// reason: 560ppm is one sample in 1786, and the correction is spread rather
// than applied in a lump.
//
// The whole of the policy lives here, pure, because it is the part that
// decides whether this sounds right — and because "it sounds slightly off" is
// the least debuggable report this project can receive. Everything it needs
// is a sample count.

// Plan is what to do with one chunk to bring playback back towards where the
// server says it should be.
type Plan struct {
	// HardResync abandons continuity: flush what is buffered and re-anchor
	// on this chunk. Audible — a gap or a repeat — and correct, because the
	// alternative at this size is minutes of gradual correction during which
	// the room is plainly out with the others.
	HardResync bool
	// Adjust is the sample count to add (positive: playback is running fast
	// and needs padding) or remove (negative: running slow). Bounded by the
	// policy, so a large error is worked off over many chunks rather than in
	// one lump.
	Adjust int
}

// CorrectionPolicy bounds how aggressively drift is worked off.
//
// The zero value is not usable; DefaultPolicy is. Written as a struct rather
// than as constants so a test can drive it to its edges without changing the
// numbers the device ships with.
type CorrectionPolicy struct {
	// HardResyncSamples: an error at least this large is not drift, it is a
	// stall, a seek, or a stream that started while the clock was still
	// settling. Correcting it gradually would take minutes.
	HardResyncSamples int
	// DeadbandSamples: below this, do nothing at all.
	//
	// This is the field that stops the correction being audible, and its
	// absence is the classic mistake. Without a deadband the corrector
	// nudges by a sample or two every chunk forever, alternating sign as it
	// crosses zero — a continuous micro-modulation of pitch, which is far
	// more noticeable than the static error it is chasing. Estimation noise
	// guarantees the crossing; it does not need real drift to happen.
	DeadbandSamples int
	// MaxAdjustPPM caps the correction RATE, in parts per million of the
	// chunk.
	//
	// IT HAS A FLOOR, NOT JUST A CEILING, and the floor is the one that is
	// easy to get wrong. The corrector must be able to move samples FASTER
	// than the crystal loses them — this speaker measures 47973 fps against
	// 48000, so 560ppm — or the error grows without bound however good the
	// clock estimate is, and the symptom is a room that walks steadily
	// further out exactly as if there were no corrector at all. The first
	// version of this shipped at 500ppm on the reasoning that staying well
	// inside the spec's allowance was the conservative choice; it was
	// conservative in the direction that does not work, and the test that
	// compares the two rates is what said so.
	//
	// The ceiling is the spec's ±0.5% (5000ppm) speed deviation over a
	// 150ms sliding average. That budget exists to absorb a device's own
	// imperfection, so the corrector takes a fraction of it.
	MaxAdjustPPM int
	// MinAdjust is the floor once a correction is happening at all: below
	// the deadband nothing happens, and above it at least this many samples
	// move, so a small persistent error is not left uncorrectable by a rate
	// cap that rounds it to zero.
	MinAdjust int
}

// DefaultPolicy is what the device ships with.
//
//   - 4800 samples (100ms) is a hard resync. Ten times the spec's floor, so
//     ordinary drift never reaches it, and small enough that a stall does not
//     leave the room audibly behind for long.
//   - 24 samples (0.5ms) of deadband. Half the spec's accuracy floor, so the
//     corrector goes quiet once inside the tolerance it is aiming for rather
//     than hunting around zero.
//   - 2000ppm of adjustment. Three and a half times the crystal's own
//     measured 560ppm error, so drift is not merely tracked but worked off,
//     and still less than half the spec's 5000ppm allowance.
var DefaultPolicy = CorrectionPolicy{
	HardResyncSamples: 4800,
	DeadbandSamples:   24,
	MaxAdjustPPM:      2000,
	MinAdjust:         1,
}

// MeasuredCrystalPPM is this speaker's own clock error, from
// /proc/asound/card0/pcm23p/sub0/status: 47973 frames per second against
// 48000 nominal. It is here rather than in a comment because it is the number
// MaxAdjustPPM has to beat, and a test compares the two.
const MeasuredCrystalPPM = 560

// Plan decides what to do with a chunk.
//
// errorSamples is where playback IS minus where it SHOULD BE, in samples:
// positive means the device is ahead (playing too early / running fast) and
// needs padding; negative means it is behind and samples must be dropped.
// chunkSamples is the chunk's length, which bounds how much can be done to
// it.
func (p CorrectionPolicy) Plan(errorSamples, chunkSamples int) Plan {
	if p.HardResyncSamples > 0 && abs(errorSamples) >= p.HardResyncSamples {
		return Plan{HardResync: true}
	}
	if abs(errorSamples) <= p.DeadbandSamples {
		return Plan{}
	}
	if chunkSamples <= 0 {
		return Plan{}
	}

	// The rate cap, as a sample count for this chunk.
	max := chunkSamples * p.MaxAdjustPPM / 1_000_000
	if max < p.MinAdjust {
		max = p.MinAdjust
	}
	// Never move more than the chunk holds: removing more samples than
	// arrived is not a correction, it is a dropped chunk.
	if max > chunkSamples {
		max = chunkSamples
	}

	adjust := errorSamples
	if adjust > max {
		adjust = max
	}
	if adjust < -max {
		adjust = -max
	}
	return Plan{Adjust: adjust}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ErrorSamples converts a timing error to a sample count at a given rate.
//
// Rounded rather than truncated: truncation biases every correction towards
// zero, which over a long stream is itself a drift — a corrector that is
// systematically 0.5 samples shy leaves an error it can never work off.
func ErrorSamples(errorMicros int64, sampleRate int) int {
	if sampleRate <= 0 {
		return 0
	}
	n := errorMicros * int64(sampleRate)
	if n >= 0 {
		return int((n + 500_000) / 1_000_000)
	}
	return int((n - 500_000) / 1_000_000)
}

// PlayAt converts a chunk's server timestamp into the local time its first
// sample must reach the speaker, accounting for this device's own
// write-to-ear latency.
//
// OutputDelayMs is subtracted because it is the time between handing a sample
// to ALSA and hearing it: to be HEARD at time T the sample must be WRITTEN at
// T − delay. Adding it instead is the sign error that puts a device
// consistently late by twice its own latency, and it produces a room that is
// out by a fixed, plausible-sounding amount — which reads as a bad clock
// estimate rather than as a sign.
func PlayAt(clock Snapshot, serverMicros int64, outputDelayMs int) (int64, bool) {
	local, ok := clock.ToLocal(serverMicros)
	if !ok {
		return 0, false
	}
	return local - int64(outputDelayMs)*1000, true
}

// ChunkAction is what to do with a chunk that has just been decoded.
type ChunkAction int

const (
	// ActionPlay: hand it to the speaker now.
	ActionPlay ChunkAction = iota
	// ActionHold: its moment has not arrived. The caller waits rather than
	// discarding — this is the normal state of a healthy stream, since the
	// server sends ahead deliberately.
	ActionHold
	// ActionDrop: its moment has passed by more than the buffer can hide.
	// Playing it anyway puts this room behind the others and every chunk
	// after it inherits the lateness.
	ActionDrop
)

// Classify decides what to do with a chunk whose play time is known.
//
// lateTolerance is how far past its moment a chunk may still be played. Zero
// is the wrong answer: chunks arrive in bursts and the moment one is examined
// is not the moment it plays, so a strict test drops audio that would have
// been perfectly on time. leadLimit is how far in the future a chunk may be
// and still be considered part of this stream at all — beyond it the
// timestamp is more likely to be a stale clock estimate than a real
// instruction, and holding on it would stall the stream for hours.
func Classify(playAtMicros, nowMicros int64, lateTolerance, leadLimit int64) ChunkAction {
	delta := playAtMicros - nowMicros
	switch {
	case delta > leadLimit:
		return ActionDrop
	case delta >= 0:
		return ActionHold
	case -delta <= lateTolerance:
		return ActionPlay
	default:
		return ActionDrop
	}
}
