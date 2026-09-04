package resample

import "testing"

// The cost of the filter, kept as a benchmark rather than as a claim in a
// comment. The first version of that comment said the cost "does not
// register", which was too generous by about an order of magnitude.
//
// On an x86 build host this converts a second of audio in ~4ms. The device is
// a 1.3GHz Cortex-A53 with no out-of-order execution, so the honest
// extrapolation is 10-20x: 4-8% of one core while AirPlay is playing.
//
// Run it before changing `taps`: doubling the filter doubles this.
func BenchmarkOneSecondOfAudio(b *testing.B) {
	r := New()
	in := make([]float64, 44100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Process(in)
	}
}
