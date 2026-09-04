// Package pcm holds the sample conversions more than one audio source needs.
//
// Small on purpose. It exists because the downmix has a failure that is
// subtle, silent and easy to reintroduce — adding two int16 channels near
// full scale wraps to full-scale NEGATIVE, which is a far worse artefact than
// the clipping it looks like — and two producers of the music plane both
// needing that rule is exactly one copy too many.
package pcm

import "encoding/binary"

// MusicPeriodBytes is one ALSA period at the PumpMusic boundary: mono, 16-bit,
// so two bytes a frame. The speaker binding is behind a cgo build tag and
// cannot be imported here, so a test reads its source and fails if the two
// ever disagree.
const MusicPeriodBytes = 2048 * 2

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

// PeriodWriter feeds the music plane in whole ALSA periods.
//
// The plane is not a byte stream. The ALSA loop takes ONE item off the
// channel per iteration and hands it to the hardware as a period, and the
// mixer returns the music buffer directly when nothing else is playing — so a
// short buffer reaches the write as a short period. The audio does not stop;
// it glitches, the per-stream period accounting counts it as a whole one, and
// the underrun margin that instrumentation exists to measure is quietly
// wrong.
//
// Every producer of that plane therefore has to carry a remainder, and three
// of them do: Sendspin's runtime works it into its own alignment padding,
// while the two subprocess clients get it from here. It is not an obvious
// requirement from the outside — PumpMusic takes a []byte and returns an
// error, which reads like a stream — which is exactly why it is written down
// in one place rather than remembered in three.
type PeriodWriter struct {
	period int
	buf    []byte
	write  func([]byte) error
}

// NewPeriodWriter builds one. periodBytes must be the plane's own period size
// at the PumpMusic boundary: mono, 16-bit, so 2 bytes per frame.
func NewPeriodWriter(periodBytes int, write func([]byte) error) *PeriodWriter {
	return &PeriodWriter{period: periodBytes, write: write}
}

// Write buffers audio and emits every whole period it can.
//
// A partial period is HELD, not padded. Sources arrive in chunk sizes that
// have nothing to do with 42.7ms — a resampled 2048-frame read is 2229
// samples, and a FLAC frame is whatever the encoder chose — so padding each
// one to a boundary would insert silence tens of times a second.
func (w *PeriodWriter) Write(b []byte) error {
	if w.period <= 0 || len(b) == 0 {
		return nil
	}
	w.buf = append(w.buf, b...)
	for len(w.buf) >= w.period {
		if err := w.write(w.buf[:w.period]); err != nil {
			w.buf = w.buf[:0]
			return err
		}
		w.buf = w.buf[w.period:]
	}
	// Reclaim the head so the slice does not creep forward forever across a
	// long stream.
	if len(w.buf) == 0 {
		w.buf = w.buf[:0]
	} else if cap(w.buf) > 8*w.period {
		w.buf = append([]byte(nil), w.buf...)
	}
	return nil
}

// Flush pads whatever is left to a whole period and emits it.
//
// The tail IS padded, where a mid-stream partial is not: the last few
// milliseconds of a track are inaudible as a gap and obvious as a click if
// the period is left half full.
func (w *PeriodWriter) Flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	out := make([]byte, w.period)
	copy(out, w.buf)
	w.buf = w.buf[:0]
	return w.write(out)
}

// Reset drops the remainder without emitting it — for a flush or a seek,
// where the buffered audio is exactly what should not be played.
func (w *PeriodWriter) Reset() { w.buf = w.buf[:0] }

// Pending reports how many bytes are held back.
func (w *PeriodWriter) Pending() int { return len(w.buf) }
