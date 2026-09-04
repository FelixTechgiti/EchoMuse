// Package resample converts 44.1kHz audio to the 48kHz the speaker runs at.
//
// It exists because AirPlay does. Classic AirPlay is 44,100 frames per second
// and this device's ALSA path is 48,000, so something has to convert, and the
// obvious cheap answers are all audibly wrong:
//
//   - DROPPING OR REPEATING SAMPLES to make the counts line up is what
//     "resampling" turns into when nobody writes a resampler. At 160/147 that
//     is a sample repeated roughly every nine, which is a 340Hz buzz laid
//     over the music.
//   - LINEAR INTERPOLATION is not much better on musical content: its
//     response falls away from the top octave and its images fold back as a
//     wash of high-frequency junk. It measures fine on a 1kHz tone and sounds
//     like a cheap radio on cymbals.
//
// So this is a proper polyphase FIR: upsample by 160, low-pass, downsample by
// 147, with only the taps that contribute to each output sample actually
// computed. The cost is TAPS multiply-accumulates per output sample — 16 at
// 48kHz is 768k MACs a second, which on a 1.3GHz A53 does not register beside
// the ~18-20% of a core the microphone already takes.
//
// Pure and dependency-free: it is arithmetic, and arithmetic that is wrong in
// a way you can only hear is exactly what belongs in a host test suite.
package resample

import "math"

// The 44.1kHz → 48kHz ratio, exactly: 48000/44100 = 160/147.
//
// Written as the reduced integers rather than as a float, because the whole
// polyphase structure depends on the ratio being rational. A float ratio
// would need a phase accumulator and would drift.
const (
	L = 160 // interpolation factor
	M = 147 // decimation factor
)

// taps is the filter length per phase, and it is a MEASUREMENT rather than a
// round number.
//
// The whole filter is L·taps long at the upsampled rate of 7.056MHz, and a
// Blackman window's transition band is about 5.5·fs/N wide. At 16 taps that
// is 15kHz of transition — which sounds like plenty of filter and is not: a
// corner that has to sit near 22kHz then has its passband ending around
// 14kHz, and the first version of this measured 5.3dB down at 20kHz for
// exactly that reason.
//
// 64 taps gives 3.8kHz of transition: flat to ~19kHz, stopband from ~23kHz.
//
// MEASURED rather than assumed, because the first version of this comment
// said the cost "does not register" and that was too generous. BenchmarkOneSecond
// converts one second of audio in 4.1ms on an x86 build host. This device is a
// 1.3GHz Cortex-A53 with no out-of-order execution, so a 10-20x factor is the
// honest range: roughly 40-80ms per second of audio, or 4-8% of ONE CORE.
//
// That is affordable and it is not free. It sits beside the ~18-20% the
// microphone takes and the ~38% of on-device wake word scoring, and it is
// only spent while AirPlay is actually playing. Sendspin needs no resampler
// at all — it asks the server for 48kHz — and Spotify hands the job to
// librespot in a process that can be killed. AirPlay is the one source that
// cannot, because classic AirPlay is 44.1kHz by definition.
const taps = 64

// historyMargin is the extra input samples kept beyond the filter length.
// M/L is just under 1, so two output samples can fall between two inputs and
// the second one's input position is a step behind the newest sample fed.
// Two is plenty; one would be exactly enough and leaves nothing for a reader
// to be wrong about.
const historyMargin = 2

// cutoffHz is where the low-pass sits, in Hz at the original 44.1kHz scale.
//
// It has to stop two different things: the interpolation images, which start
// at the SOURCE Nyquist of 22,050Hz, and the decimation aliases, which start
// at the output Nyquist of 24,000Hz. 22,050 is the binding one.
//
// 21,000 puts the transition band's upper edge just under it, leaving the
// passband flat to about 19kHz. That is beyond what a 44.1kHz source carries
// — encoders low-pass well below it — and beyond most people's hearing, and
// pushing the corner higher to chase the last kilohertz would put the images
// inside the transition instead.
const cutoffHz = 21000.0

// cutoff is that frequency as a fraction of the UPSAMPLED rate, which is what
// the sinc below is expressed in.
const cutoff = cutoffHz / (44100.0 * L)

// Resampler converts a stream of mono 16-bit samples from 44.1kHz to 48kHz.
//
// One per stream: it carries filter history, and a fresh instance mid-stream
// restarts from silence and clicks. Not safe for concurrent use — it is
// driven by one goroutine reading a pipe.
type Resampler struct {
	// h is the prototype filter, laid out so phase p's taps are
	// h[p], h[p+L], h[p+2L], … — which is what makes the inner loop a
	// stride rather than an index computation.
	h []float64
	// hist holds recent input samples, newest last. Longer than `taps` on
	// purpose: an output sample's input position lags the newest sample fed
	// by up to one step (M/L < 1, so two outputs can fall between two
	// inputs), and the filter then needs one sample further back than the
	// newest. The margin is what stops that reading off the end.
	hist []float64
	// n counts output samples produced, and is what the phase is derived
	// from. It is reduced modulo L so it cannot overflow on a long stream:
	// at 48kHz an int64 would take six million years, but a reader should
	// not have to work that out.
	n int
	// fed counts input samples taken, reduced alongside n so neither can
	// grow without bound.
	fed int
}

// New builds a resampler.
func New() *Resampler {
	return &Resampler{
		h:    buildFilter(),
		hist: make([]float64, taps+historyMargin),
	}
}

// buildFilter computes the windowed-sinc prototype.
//
// Blackman rather than Hamming: the extra stopband depth (−74dB against
// −41dB) is what keeps the interpolation images inaudible, and the wider
// transition it costs sits above 20kHz where nothing lives. Computed once per
// stream rather than baked in as a table, because a table of 2560 float64s in
// the binary is 20KB to save a few microseconds at stream start.
func buildFilter() []float64 {
	n := L * taps
	h := make([]float64, n)
	mid := float64(n-1) / 2
	var sum float64
	for i := 0; i < n; i++ {
		x := float64(i) - mid
		// sinc(2·cutoff·x), the ideal low-pass impulse response.
		var s float64
		if x == 0 {
			s = 2 * cutoff
		} else {
			s = math.Sin(2*math.Pi*cutoff*x) / (math.Pi * x)
		}
		// Blackman window.
		w := 0.42 -
			0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1)) +
			0.08*math.Cos(4*math.Pi*float64(i)/float64(n-1))
		h[i] = s * w
		sum += h[i]
	}
	// Normalise to unity DC gain THROUGH THE POLYPHASE STRUCTURE.
	//
	// Each output sample uses ONE phase, so what has to sum to 1 is a phase,
	// not the whole filter. Scaling the whole filter to sum to L leaves each
	// of its L phases summing to 1, which is the same thing said in one
	// operation. Normalising the whole filter to 1 instead makes every
	// output quiet by a factor of L — 44dB — which reads as "AirPlay is much
	// quieter than Spotify" rather than as a scaling bug.
	if sum != 0 {
		scale := float64(L) / sum
		for i := range h {
			h[i] *= scale
		}
	}
	return h
}

// Process converts a block of input samples and returns the output.
//
// Streaming: state is carried across calls, so `Process(a)` then `Process(b)`
// gives exactly what `Process(a+b)` would, and a test pins that.
func (r *Resampler) Process(in []float64) []float64 {
	if len(in) == 0 {
		return nil
	}
	out := make([]float64, 0, len(in)*L/M+2)
	for _, x := range in {
		copy(r.hist, r.hist[1:])
		r.hist[len(r.hist)-1] = x
		r.fed++

		// Output sample n sits at input time n·M/L, so it needs input
		// index floor(n·M/L) and uses phase (n·M) mod L. It can be produced
		// as soon as that input index has been fed.
		//
		// M/L is just under 1, so this emits one output per input most of
		// the time and two occasionally — which is exactly the 160-for-147
		// ratio, arriving one sample at a time.
		for {
			idx := r.n * M / L
			if idx >= r.fed {
				break
			}
			phase := (r.n * M) % L

			// hist[len-1] is input index fed-1, so the sample at `idx` is
			// `back` steps further into the history.
			back := (r.fed - 1) - idx
			var acc float64
			for k := 0; k < taps; k++ {
				j := len(r.hist) - 1 - back - k
				if j < 0 {
					// Older than the history: the stream has not produced
					// that sample yet (start-up), and zero is what it was.
					break
				}
				acc += r.h[phase+k*L] * r.hist[j]
			}
			out = append(out, acc)
			r.n++

			// Keep the counters bounded. L·M outputs is exactly one period
			// of both the phase and the index relationship — idx advances
			// by M·M over it — so subtracting leaves every future phase and
			// offset identical.
			if r.n >= L*M {
				r.n -= L * M
				r.fed -= M * M
			}
		}
	}
	return out
}

// Reset clears the filter history, for a new stream.
func (r *Resampler) Reset() {
	for i := range r.hist {
		r.hist[i] = 0
	}
	r.n, r.fed = 0, 0
}

// OutputLen is roughly how many samples `n` inputs produce. For sizing
// buffers, not for exact accounting.
func OutputLen(n int) int { return n * L / M }

// StreamConverter turns interleaved stereo 16-bit PCM at a source rate into
// mono 16-bit PCM at the device's 48kHz.
//
// It exists because THREE things needed the same five steps and two of them
// had already written it: unmarshal, downmix, resample, clamp, remarshal.
// AirPlay needs it because classic AirPlay is 44.1kHz by definition, and
// Spotify needs it because no released librespot — nor its `dev` branch — has
// a `--sample-rate` option at all. The resampling pull request was never
// merged, so the pipe backend emits 44,100 frames a second and that is that.
//
// One per stream. It carries filter history, and a fresh instance mid-stream
// restarts from silence and clicks.
type StreamConverter struct {
	rs *Resampler
}

// NewStreamConverter builds one for a source rate. At 48000 it skips the
// filter entirely rather than converting 48000 to 48000, which would cost
// 4-8% of a core to change nothing.
func NewStreamConverter(sourceRate int) *StreamConverter {
	if sourceRate == 48000 {
		return &StreamConverter{}
	}
	return &StreamConverter{rs: New()}
}

// Convert takes whole stereo frames and returns mono 16-bit at 48kHz.
//
// DOWNMIX FIRST, THEN RESAMPLE. Resampling two channels costs twice the
// filter for a result that is about to be summed anyway — the same audio for
// double the CPU, on the two sources that cannot hand the job to somebody
// else.
func (c *StreamConverter) Convert(stereo []byte, downmix func([]byte) []byte) []byte {
	mono := downmix(stereo)
	if c.rs == nil {
		return mono
	}
	in := make([]float64, len(mono)/2)
	for i := range in {
		in[i] = float64(int16(uint16(mono[i*2]) | uint16(mono[i*2+1])<<8))
	}
	out := c.rs.Process(in)
	res := make([]byte, len(out)*2)
	for i, v := range out {
		// Clamped, not wrapped. The filter overshoots slightly on a signal
		// already at full scale, and an int16 that wraps turns a peak into
		// full-scale opposite polarity — a crack rather than clipping. The
		// same rule the mixer follows.
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		s := uint16(int16(v))
		res[i*2], res[i*2+1] = byte(s), byte(s>>8)
	}
	return res
}

// Reset clears the filter for a new stream.
func (c *StreamConverter) Reset() {
	if c.rs != nil {
		c.rs.Reset()
	}
}
