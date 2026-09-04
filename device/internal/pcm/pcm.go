// Package pcm holds the sample conversions more than one audio source needs.
//
// Small on purpose. It exists because the downmix has a failure that is
// subtle, silent and easy to reintroduce — adding two int16 channels near
// full scale wraps to full-scale NEGATIVE, which is a far worse artefact than
// the clipping it looks like — and two producers of the music plane both
// needing that rule is exactly one copy too many.
package pcm

import "encoding/binary"

// DownmixStereo averages interleaved stereo 16-bit little-endian samples into
// mono.
//
// Averaged in int32 and halved, never added in int16. The same rule the ALSA
// mixer already follows for the voice/music sum, and it is here rather than
// at each call site because "it sounds distorted on loud passages" is a
// report nobody traces back to an integer width.
//
// A trailing partial frame is ignored rather than padded: it means the caller
// handed over a short read, and inventing a sample is worse than dropping
// 20µs of audio.
func DownmixStereo(data []byte) []byte {
	frames := len(data) / 4
	out := make([]byte, frames*2)
	for i := 0; i < frames; i++ {
		l := int32(int16(binary.LittleEndian.Uint16(data[i*4:])))
		r := int32(int16(binary.LittleEndian.Uint16(data[i*4+2:])))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16((l+r)/2)))
	}
	return out
}
