package sendspin

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The runtime hands the music plane whole PERIODS, and the period size lives
// in the speaker binding — behind a cgo build tag, so this package cannot
// import it to find out. Two constants describing one number is the shape
// this tree has been bitten by before, so the source is read instead.
//
// The failure if they diverge is not subtle in effect and is completely
// silent in cause: every push would be a partial period, the plane would
// never reach its prime gate, and the device would sit with a full buffer
// playing nothing.
func TestThePeriodMatchesTheSpeakersOwn(t *testing.T) {
	src, err := os.ReadFile("../bindings/speaker/pcm_speaker.go")
	if err != nil {
		t.Fatalf("cannot read the speaker binding: %v", err)
	}
	m := regexp.MustCompile(`(?m)^const periodSize = (\d+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("periodSize is no longer declared as a plain const in " +
			"pcm_speaker.go — this guard cannot see it, and it must be " +
			"rewritten rather than deleted")
	}
	got, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	if got != periodFrames {
		t.Fatalf("the speaker's period is %d frames and sendspin uses %d — "+
			"every push would be a partial period and the plane would never "+
			"prime", got, periodFrames)
	}
}

// The same for the buffer depth: buffer_capacity is derived from it and the
// server paces against it, so a change on one side and not the other has the
// server either starving us or overrunning the buffer.
func TestTheAdvertisedBufferMatchesTheSpeakersOwn(t *testing.T) {
	src, err := os.ReadFile("../bindings/speaker/pcm_speaker.go")
	if err != nil {
		t.Fatalf("cannot read the speaker binding: %v", err)
	}
	if !regexp.MustCompile(`MusicBufferSeconds = float64\(audioChanDepth\) \* float64\(periodSize\) / 48000`).Match(src) {
		t.Fatal("MusicBufferSeconds is no longer derived from audioChanDepth " +
			"and periodSize — a hardcoded figure there is a second copy of " +
			"the buffer depth, and buffer_capacity is built on it")
	}
}
