package pcm

import (
	"encoding/binary"
	"testing"
)

func stereo(pairs ...[2]int16) []byte {
	out := make([]byte, len(pairs)*4)
	for i, p := range pairs {
		binary.LittleEndian.PutUint16(out[i*4:], uint16(p[0]))
		binary.LittleEndian.PutUint16(out[i*4+2:], uint16(p[1]))
	}
	return out
}

func mono(t *testing.T, b []byte) []int16 {
	t.Helper()
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return out
}

func TestTwoLoudChannelsDoNotWrap(t *testing.T) {
	// The whole reason this is a shared function. Adding 32000 + 32000 in
	// int16 gives -1536: not a clipped peak but a full-scale sign flip,
	// which is audible as a crack on every loud passage rather than as
	// distortion.
	got := mono(t, DownmixStereo(stereo([2]int16{32000, 32000})))
	if got[0] != 32000 {
		t.Fatalf("downmix = %d, want 32000", got[0])
	}
	got = mono(t, DownmixStereo(stereo([2]int16{-32000, -32000})))
	if got[0] != -32000 {
		t.Fatalf("downmix = %d, want -32000", got[0])
	}
}

func TestFullScaleSurvives(t *testing.T) {
	got := mono(t, DownmixStereo(stereo([2]int16{32767, 32767}, [2]int16{-32768, -32768})))
	if got[0] != 32767 || got[1] != -32768 {
		t.Fatalf("downmix = %v, want [32767 -32768]", got)
	}
}

func TestOpposedChannelsCancel(t *testing.T) {
	got := mono(t, DownmixStereo(stereo([2]int16{20000, -20000})))
	if got[0] != 0 {
		t.Fatalf("downmix = %d, want 0", got[0])
	}
}

func TestTheAverageIsTheAverage(t *testing.T) {
	got := mono(t, DownmixStereo(stereo(
		[2]int16{100, 300}, [2]int16{0, 0}, [2]int16{-1000, 1000})))
	for i, want := range []int16{200, 0, 0} {
		if got[i] != want {
			t.Fatalf("frame %d = %d, want %d", i, got[i], want)
		}
	}
}

func TestAPartialFrameIsDroppedNotInvented(t *testing.T) {
	// A short read from a pipe. Padding it would put a sample in the stream
	// that nobody sent; dropping it costs 20µs.
	in := append(stereo([2]int16{500, 500}), 0x01, 0x02)
	got := mono(t, DownmixStereo(in))
	if len(got) != 1 || got[0] != 500 {
		t.Fatalf("got %v, want one sample of 500", got)
	}
}

func TestEmptyInputIsEmptyOutput(t *testing.T) {
	if got := DownmixStereo(nil); len(got) != 0 {
		t.Fatalf("nil produced %d bytes", len(got))
	}
	if got := DownmixStereo([]byte{1, 2}); len(got) != 0 {
		t.Fatalf("a lone half-frame produced %d bytes", len(got))
	}
}
