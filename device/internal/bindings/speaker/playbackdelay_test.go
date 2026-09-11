package speaker

import "testing"

func TestPlaybackFramesAddsTheRingToTheHardware(t *testing.T) {
	// Four periods waiting is ~171ms the DMA pointer cannot see.
	got := PlaybackFrames(1024, 4)
	want := int64(1024 + 4*periodSize)
	if got != want {
		t.Fatalf("PlaybackFrames(1024, 4) = %d, want %d", got, want)
	}
}

func TestAnEmptyRingIsJustTheHardware(t *testing.T) {
	if got := PlaybackFrames(777, 0); got != 777 {
		t.Fatalf("PlaybackFrames(777, 0) = %d, want 777", got)
	}
}

// A negative from either source is a bad read, not a negative delay. Treating
// it as one would move the corrector's target the WRONG WAY, which is worse
// than ignoring the sample.
func TestNegativesAreFloored(t *testing.T) {
	if got := PlaybackFrames(-5, -2); got != 0 {
		t.Fatalf("PlaybackFrames(-5, -2) = %d, want 0", got)
	}
}

// The bias this exists to remove, stated as a number: at the local prime
// depth the ring alone is over 150ms, which is an order of magnitude more
// than the ±1ms the sendspin corrector is trying to hold.
func TestTheRingAloneDwarfsTheCorrectorsDeadband(t *testing.T) {
	ms := PlaybackFrames(0, localPrimePeriods) * 1000 / sampleRate
	if ms < 150 {
		t.Fatalf("local prime ring = %dms, expected it to be the dominant term", ms)
	}
}

func TestQueuedPeriodsCountsWhatIsWaiting(t *testing.T) {
	s := newAudioStream(8, make(chan struct{}))
	if got := s.queuedPeriods(); got != 0 {
		t.Fatalf("fresh stream queued %d periods, want 0", got)
	}
	for i := 0; i < 3; i++ {
		if ok, err := s.pump(make([]byte, periodSize*4), periodSize*2); !ok || err != nil {
			t.Fatalf("pump %d: ok=%v err=%v", i, ok, err)
		}
	}
	if got := s.queuedPeriods(); got != 3 {
		t.Fatalf("queued %d periods after three pumps, want 3", got)
	}
	if s.take() == nil {
		t.Fatal("take returned nothing")
	}
	if got := s.queuedPeriods(); got != 2 {
		t.Fatalf("queued %d periods after a take, want 2", got)
	}
}
