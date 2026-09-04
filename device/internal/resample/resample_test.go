package resample

import (
	"math"
	"testing"
)

// sine generates n samples of a tone at freq Hz, sampled at 44100.
func sine(n int, freq, amp float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/44100)
	}
	return out
}

// rms of a slice, skipping the first `skip` samples so the filter's own
// start-up transient is not measured as signal.
func rms(x []float64, skip int) float64 {
	if len(x) <= skip {
		return 0
	}
	var s float64
	for _, v := range x[skip:] {
		s += v * v
	}
	return math.Sqrt(s / float64(len(x)-skip))
}

// goertzel measures the amplitude at one frequency in a 48kHz block. Used
// instead of an FFT because one bin is all these tests need and it keeps the
// package dependency-free.
func goertzel(x []float64, freq float64) float64 {
	if len(x) == 0 {
		return 0
	}
	w := 2 * math.Pi * freq / 48000
	c := 2 * math.Cos(w)
	var s1, s2 float64
	for _, v := range x {
		s0 := v + c*s1 - s2
		s2, s1 = s1, s0
	}
	re := s1 - s2*math.Cos(w)
	im := s2 * math.Sin(w)
	return 2 * math.Hypot(re, im) / float64(len(x))
}

func TestTheOutputIsLongerByExactlyTheRatio(t *testing.T) {
	// 160/147. A resampler that is even slightly off produces a stream that
	// drifts against the speaker's clock, which is a gap or an overrun every
	// few minutes rather than an audible artefact.
	r := New()
	const in = 44100 // one second
	out := r.Process(make([]float64, in))
	want := in * L / M
	if d := len(out) - want; d < -2 || d > 2 {
		t.Fatalf("%d samples in gave %d out, want ~%d", in, len(out), want)
	}
}

func TestAToneComesOutAtTheSameFrequency(t *testing.T) {
	// The whole point. A 1kHz tone at 44.1k must be a 1kHz tone at 48k — not
	// 1088Hz, which is what happens when the samples are passed through
	// unconverted and the speaker plays them at its own rate.
	r := New()
	out := r.Process(sine(44100, 1000, 0.5))
	at1k := goertzel(out[2000:], 1000)
	at1088 := goertzel(out[2000:], 1088.4)
	if at1k < 0.4 {
		t.Fatalf("1kHz came out at amplitude %.3f, want ~0.5", at1k)
	}
	if at1088 > at1k/10 {
		t.Fatalf("energy at 1088Hz (%.4f) against 1kHz (%.4f) — the rate was "+
			"not converted", at1088, at1k)
	}
}

func TestTheLevelIsPreserved(t *testing.T) {
	// The polyphase gain trap: each output sample uses ONE phase, so the
	// gain that matters is L times one phase's sum, not the whole filter's.
	// Normalising the whole filter instead is quiet by ~44dB, which reads as
	// "AirPlay is much quieter than Spotify" rather than as a scaling bug.
	r := New()
	out := r.Process(sine(44100, 1000, 0.5))
	got := rms(out, 2000)
	want := 0.5 / math.Sqrt2
	if math.Abs(got-want)/want > 0.05 {
		t.Fatalf("output RMS %.4f, want %.4f (%.1f dB out)",
			got, want, 20*math.Log10(got/want))
	}
}

func TestDcPassesAtUnity(t *testing.T) {
	r := New()
	in := make([]float64, 20000)
	for i := range in {
		in[i] = 0.25
	}
	out := r.Process(in)
	// Skip the ramp-in; the steady state is what unity gain means.
	for i := 5000; i < len(out); i++ {
		if math.Abs(out[i]-0.25) > 0.002 {
			t.Fatalf("DC 0.25 came out as %.5f at sample %d", out[i], i)
		}
	}
}

func TestThePassbandIsFlatWhereMusicLives(t *testing.T) {
	// The first version of this filter was 16 taps, which gives 15kHz of
	// transition band — plenty of filter by the look of it, and 5.3dB down
	// at 20kHz in practice. These are the frequencies that have to come
	// through at level.
	for _, f := range []float64{100, 1000, 5000, 10000, 15000} {
		r := New()
		out := r.Process(sine(44100, f, 0.5))[4000:]
		got := goertzel(out, f)
		if db := 20 * math.Log10(got/0.5); db < -1 || db > 1 {
			t.Fatalf("%.0fHz came out %.2fdB off unity", f, db)
		}
	}
}

func TestTheImagesAreGoneRatherThanQuieter(t *testing.T) {
	// Upsampling by 160 puts images of a 15kHz tone at 44100±15000 and
	// beyond; the one that would fold back into the audible band lands at
	// 18,900Hz after the 48kHz decimation. Without the filter that is a
	// loud, unrelated tone sitting on top of the music — not a subtle
	// artefact.
	r := New()
	out := r.Process(sine(44100, 15000, 0.5))[4000:]
	signal := goertzel(out, 15000)
	image := goertzel(out, 18900)
	if signal < 0.4 {
		t.Fatalf("the 15kHz signal came out at %.3f", signal)
	}
	if db := 20 * math.Log10(image/signal); db > -60 {
		t.Fatalf("image at 18.9kHz is only %.1fdB below the signal "+
			"(%.5f against %.3f)", db, image, signal)
	}
}

func TestNothingSurvivesAboveTheSourcesNyquist(t *testing.T) {
	// Anything the filter lets through above 22,050Hz is an image, because
	// the source cannot contain it.
	r := New()
	out := r.Process(sine(44100, 8000, 0.5))[4000:]
	signal := goertzel(out, 8000)
	for _, f := range []float64{23000, 23500} {
		if got := goertzel(out, f); got > signal/1000 {
			t.Fatalf("%.0fHz carries %.5f against a signal of %.3f", f, got, signal)
		}
	}
}

func TestStreamingMatchesOneShot(t *testing.T) {
	// The state is carried across calls, and it has to be: a fresh instance
	// mid-stream restarts from silence and clicks. This is what proves the
	// history is carried rather than merely present.
	in := sine(20000, 440, 0.4)

	whole := New().Process(in)

	piecewise := New()
	var pieces []float64
	for i := 0; i < len(in); i += 137 { // a deliberately awkward block size
		end := i + 137
		if end > len(in) {
			end = len(in)
		}
		pieces = append(pieces, piecewise.Process(in[i:end])...)
	}

	if len(pieces) != len(whole) {
		t.Fatalf("streamed %d samples, one-shot %d", len(pieces), len(whole))
	}
	for i := range whole {
		if math.Abs(whole[i]-pieces[i]) > 1e-12 {
			t.Fatalf("sample %d: one-shot %.12f, streamed %.12f",
				i, whole[i], pieces[i])
		}
	}
}

func TestResetClearsTheTail(t *testing.T) {
	r := New()
	r.Process(sine(5000, 1000, 0.9))
	r.Reset()
	out := r.Process(make([]float64, 2000))
	for i, v := range out {
		if math.Abs(v) > 1e-9 {
			t.Fatalf("silence after Reset produced %.9f at sample %d", v, i)
		}
	}
}

func TestTheCounterStaysBoundedOverALongStream(t *testing.T) {
	// L*M output samples is one full period of both the phase and the index
	// relationship, so subtracting it leaves every future phase identical. A
	// counter that grew instead would be correct and would still need a
	// reader to work out that six million years is fine.
	r := New()
	for i := 0; i < 50; i++ {
		r.Process(make([]float64, 4410))
	}
	if r.n >= L*M {
		t.Fatalf("output counter reached %d, past the %d period", r.n, L*M)
	}
}

func TestAnEmptyBlockIsNotAFault(t *testing.T) {
	r := New()
	if out := r.Process(nil); out != nil {
		t.Fatalf("nil input produced %d samples", len(out))
	}
	if out := r.Process([]float64{}); out != nil {
		t.Fatalf("an empty block produced %d samples", len(out))
	}
}

func TestOutputLenIsCloseEnoughToSizeABuffer(t *testing.T) {
	r := New()
	for _, n := range []int{147, 1470, 44100} {
		got := len(r.Process(make([]float64, n)))
		want := OutputLen(n)
		if d := got - want; d < -2 || d > 2 {
			t.Fatalf("%d in: produced %d, OutputLen says %d", n, got, want)
		}
		r.Reset()
	}
}

func TestTheRatioIsTheRealOne(t *testing.T) {
	// 48000/44100 reduces to 160/147. A ratio written as a float would need
	// a phase accumulator and would drift; these are what make the polyphase
	// structure exact.
	if L*44100 != M*48000 {
		t.Fatalf("L/M = %d/%d does not convert 44100 to 48000", L, M)
	}
}
