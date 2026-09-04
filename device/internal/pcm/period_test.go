package pcm

import (
	"bytes"
	"errors"
	"os"
	"regexp"
	"strconv"
	"testing"
)

// collector records what actually reached the plane.
type collector struct {
	writes [][]byte
	fail   error
}

func (c *collector) write(b []byte) error {
	if c.fail != nil {
		return c.fail
	}
	c.writes = append(c.writes, append([]byte(nil), b...))
	return nil
}

func TestOnlyWholePeriodsReachThePlane(t *testing.T) {
	// The bug this exists to prevent. The ALSA loop takes one item off the
	// channel per iteration and hands it to the hardware as a period, and
	// the mixer returns the music buffer directly when nothing else plays —
	// so a short buffer becomes a short period. The audio does not stop; it
	// glitches, and the period accounting counts it as whole.
	c := &collector{}
	w := NewPeriodWriter(4096, c.write)

	// Sizes a real source produces: a resampled 2048-frame read is 2229
	// samples, and nothing about that is a multiple of 4096 bytes.
	for _, n := range []int{4458, 4458, 4458, 100, 1} {
		if err := w.Write(make([]byte, n)); err != nil {
			t.Fatal(err)
		}
	}
	for i, b := range c.writes {
		if len(b) != 4096 {
			t.Fatalf("write %d is %d bytes, want a whole 4096-byte period",
				i, len(b))
		}
	}
	if len(c.writes) == 0 {
		t.Fatal("nothing reached the plane")
	}
}

func TestNothingIsLostOrInvented(t *testing.T) {
	c := &collector{}
	w := NewPeriodWriter(64, c.write)

	var sent []byte
	for i := 0; i < 40; i++ {
		chunk := make([]byte, 37)
		for j := range chunk {
			chunk[j] = byte(i*37 + j)
		}
		sent = append(sent, chunk...)
		if err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	var got []byte
	for _, b := range c.writes {
		got = append(got, b...)
	}
	// Everything emitted so far must be a prefix of what was written, and
	// the remainder must account for the difference exactly.
	if !bytes.Equal(got, sent[:len(got)]) {
		t.Fatal("the emitted audio is not the audio that was written")
	}
	if len(got)+w.Pending() != len(sent) {
		t.Fatalf("%d emitted + %d pending != %d written",
			len(got), w.Pending(), len(sent))
	}
}

func TestAPartialPeriodIsHeldNotPadded(t *testing.T) {
	// Sources arrive in chunk sizes that have nothing to do with 42.7ms, so
	// padding each one to a boundary would insert silence tens of times a
	// second.
	c := &collector{}
	w := NewPeriodWriter(4096, c.write)
	if err := w.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != 0 {
		t.Fatalf("a 100-byte chunk produced %d writes", len(c.writes))
	}
	if w.Pending() != 100 {
		t.Fatalf("pending = %d, want 100", w.Pending())
	}
}

func TestTheTailIsPaddedAtFlush(t *testing.T) {
	// The last few milliseconds of a track are inaudible as a gap and
	// obvious as a click if the period is left half full.
	c := &collector{}
	w := NewPeriodWriter(64, c.write)
	w.Write(bytes.Repeat([]byte{0xAB}, 10))
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != 1 || len(c.writes[0]) != 64 {
		t.Fatalf("flush produced %d writes", len(c.writes))
	}
	if !bytes.Equal(c.writes[0][:10], bytes.Repeat([]byte{0xAB}, 10)) {
		t.Fatal("the tail audio was lost")
	}
	for _, b := range c.writes[0][10:] {
		if b != 0 {
			t.Fatal("the padding is not silence")
		}
	}
	if w.Pending() != 0 {
		t.Fatal("flush left a remainder")
	}
}

func TestFlushingNothingWritesNothing(t *testing.T) {
	// A stream that ended on a period boundary must not emit a spare period
	// of silence.
	c := &collector{}
	w := NewPeriodWriter(64, c.write)
	w.Write(make([]byte, 128))
	before := len(c.writes)
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != before {
		t.Fatal("flushing an empty remainder emitted a period")
	}
}

func TestResetDropsTheRemainderWithoutPlayingIt(t *testing.T) {
	// A flush or a seek: the buffered audio is exactly what should not be
	// played.
	c := &collector{}
	w := NewPeriodWriter(64, c.write)
	w.Write(make([]byte, 30))
	w.Reset()
	if w.Pending() != 0 {
		t.Fatal("Reset kept the remainder")
	}
	if len(c.writes) != 0 {
		t.Fatal("Reset played the remainder")
	}
}

func TestAFailedWriteDropsTheBufferRatherThanReplayingIt(t *testing.T) {
	// The plane is gone. Keeping the remainder would replay it on a later
	// stream, out of position.
	c := &collector{fail: errors.New("speaker dead")}
	w := NewPeriodWriter(64, c.write)
	if err := w.Write(make([]byte, 200)); err == nil {
		t.Fatal("a failed write was swallowed")
	}
	if w.Pending() != 0 {
		t.Fatalf("pending = %d after a failure", w.Pending())
	}
}

func TestTheBufferDoesNotCreepOverALongStream(t *testing.T) {
	c := &collector{}
	w := NewPeriodWriter(64, c.write)
	for i := 0; i < 20000; i++ {
		w.Write(make([]byte, 37))
	}
	if w.Pending() >= 64 {
		t.Fatalf("pending = %d, want less than one period", w.Pending())
	}
}

func TestZeroPeriodIsNotAnInfiniteLoop(t *testing.T) {
	c := &collector{}
	w := NewPeriodWriter(0, c.write)
	if err := w.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != 0 {
		t.Fatal("a zero period emitted something")
	}
}

// The plane's period size lives in the speaker binding, behind a cgo build
// tag this package cannot import. Two constants describing one number is the
// shape this tree has been bitten by before, so the source is read instead.
//
// The failure if they diverge is silent in cause: every push would be a
// partial period, the plane would never reach its prime gate, and the device
// would sit with a full buffer playing nothing.
func TestThePeriodMatchesTheSpeakersOwn(t *testing.T) {
	src, err := os.ReadFile("../bindings/speaker/pcm_speaker.go")
	if err != nil {
		t.Fatalf("cannot read the speaker binding: %v", err)
	}
	m := regexp.MustCompile(`(?m)^const monoPeriodBytes = periodSize \* 2`).Find(src)
	if m == nil {
		t.Fatal("monoPeriodBytes is no longer `periodSize * 2` in " +
			"pcm_speaker.go — this guard cannot see it, and it must be " +
			"rewritten rather than deleted")
	}
	ps := regexp.MustCompile(`(?m)^const periodSize = (\d+)`).FindSubmatch(src)
	if ps == nil {
		t.Fatal("periodSize is no longer a plain const in pcm_speaker.go")
	}
	n, err := strconv.Atoi(string(ps[1]))
	if err != nil {
		t.Fatal(err)
	}
	if want := n * 2; want != MusicPeriodBytes {
		t.Fatalf("the speaker's mono period is %d bytes and pcm uses %d — "+
			"every push would be a partial period and the plane would never "+
			"prime", want, MusicPeriodBytes)
	}
}
