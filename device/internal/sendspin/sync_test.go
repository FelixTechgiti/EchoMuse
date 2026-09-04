package sendspin

import "testing"

func TestNothingHappensInsideTheDeadband(t *testing.T) {
	// The field that stops the correction being audible. Without it the
	// corrector nudges every chunk forever, alternating sign as estimation
	// noise carries the error across zero — a continuous micro-modulation of
	// pitch, far more noticeable than the static error it chases.
	p := DefaultPolicy
	for _, err := range []int{0, 1, -1, 24, -24} {
		if got := p.Plan(err, 4800); got != (Plan{}) {
			t.Fatalf("error %d produced %+v inside the deadband", err, got)
		}
	}
}

func TestJustOutsideTheDeadbandSomethingHappens(t *testing.T) {
	p := DefaultPolicy
	got := p.Plan(25, 4800)
	if got.HardResync || got.Adjust <= 0 {
		t.Fatalf("error 25 produced %+v, want a small positive adjustment", got)
	}
	got = p.Plan(-25, 4800)
	if got.HardResync || got.Adjust >= 0 {
		t.Fatalf("error -25 produced %+v, want a small negative adjustment", got)
	}
}

func TestTheSignIsWhatItSaysItIs(t *testing.T) {
	// Positive error = the device is AHEAD and needs padding. Getting this
	// backwards doubles the drift instead of cancelling it, and the symptom
	// — a room that walks steadily further out — looks identical to no
	// correction at all.
	p := DefaultPolicy
	if p.Plan(1000, 4800).Adjust <= 0 {
		t.Fatal("a device running ahead was not padded")
	}
	if p.Plan(-1000, 4800).Adjust >= 0 {
		t.Fatal("a device running behind did not drop samples")
	}
}

func TestALargeErrorIsAHardResyncRatherThanMinutesOfCreeping(t *testing.T) {
	p := DefaultPolicy
	for _, err := range []int{4800, -4800, 48000, -100000} {
		got := p.Plan(err, 4800)
		if !got.HardResync {
			t.Fatalf("error %d produced %+v, want a hard resync", err, got)
		}
		if got.Adjust != 0 {
			t.Fatalf("a hard resync also asked for an adjustment: %+v", got)
		}
	}
}

func TestTheCorrectionRateIsBoundedAtBothEnds(t *testing.T) {
	// The ceiling is the spec's ±0.5% (5000ppm) over a 150ms average, and
	// that budget exists to absorb the device's own imperfection rather than
	// to be spent by its corrector. The FLOOR is the one that is easy to get
	// wrong, and it has been: a corrector capped below the crystal's own
	// 560ppm error can never catch up, and the symptom is a room that walks
	// steadily further out exactly as if nothing were correcting at all.
	p := DefaultPolicy
	if p.MaxAdjustPPM <= MeasuredCrystalPPM {
		t.Fatalf("correction rate %dppm cannot outrun the crystal's %dppm",
			p.MaxAdjustPPM, MeasuredCrystalPPM)
	}
	if p.MaxAdjustPPM > 2500 {
		t.Fatalf("correction rate %dppm spends more than half the spec's "+
			"5000ppm allowance", p.MaxAdjustPPM)
	}

	// And the cap is actually applied: an error just under the resync
	// threshold must not be corrected in one lump.
	const chunk = 4800 // 100ms
	got := p.Plan(p.HardResyncSamples-1, chunk)
	if got.HardResync {
		t.Fatal("test setup: expected an adjustment, not a resync")
	}
	if got.Adjust <= 0 {
		t.Fatalf("a large error produced no correction: %+v", got)
	}
	if ppm := got.Adjust * 1_000_000 / chunk; ppm > p.MaxAdjustPPM {
		t.Fatalf("correction rate %dppm exceeded the %dppm cap", ppm, p.MaxAdjustPPM)
	}
}

func TestACorrectionNeverRemovesMoreThanTheChunkHolds(t *testing.T) {
	// Removing more samples than arrived is not a correction, it is a
	// dropped chunk — and the caller would be handed a negative length.
	p := CorrectionPolicy{HardResyncSamples: 1 << 30, DeadbandSamples: 0,
		MaxAdjustPPM: 1_000_000, MinAdjust: 1}
	for _, chunk := range []int{1, 10, 720, 7200} {
		got := p.Plan(-1_000_000, chunk)
		if -got.Adjust > chunk {
			t.Fatalf("chunk %d: asked to drop %d samples", chunk, -got.Adjust)
		}
		got = p.Plan(1_000_000, chunk)
		if got.Adjust > chunk {
			t.Fatalf("chunk %d: asked to pad by %d samples", chunk, got.Adjust)
		}
	}
}

func TestASmallPersistentErrorIsStillCorrectable(t *testing.T) {
	// A rate cap that rounds to zero on short chunks would leave an error
	// the corrector can see and can never work off — the deadband is where
	// "small enough to ignore" is decided, not the arithmetic.
	p := DefaultPolicy
	const shortChunk = 720 // 15ms, the shortest the spec allows
	got := p.Plan(p.DeadbandSamples+1, shortChunk)
	if got.Adjust == 0 {
		t.Fatalf("a correctable error rounded to nothing on a short chunk: %+v", got)
	}
}

func TestTheCorrectorOutrunsTheCrystalInSamplesPerSecond(t *testing.T) {
	// The same requirement expressed as the thing that actually happens: how
	// many samples the corrector can move per second of audio, against how
	// many the crystal loses in that second. This is the form that caught
	// the first version, which was capped at 500ppm and moved 20 samples a
	// second against the 26 it needed.
	const chunk = 4800 // 100ms
	perChunk := DefaultPolicy.Plan(DefaultPolicy.HardResyncSamples-1, chunk).Adjust
	perSecond := perChunk * (SampleRate / chunk)
	lost := MeasuredCrystalPPM * SampleRate / 1_000_000
	if perSecond <= lost {
		t.Fatalf("corrector moves %d samples/s, the crystal loses %d",
			perSecond, lost)
	}
}

func TestErrorSamplesRoundsRatherThanTruncating(t *testing.T) {
	// Truncation biases every correction towards zero, which over a long
	// stream is itself a drift: a corrector systematically half a sample shy
	// leaves an error it can never work off.
	if got := ErrorSamples(20, 48000); got != 1 { // 20µs ≈ 0.96 samples
		t.Fatalf("ErrorSamples(20µs) = %d, want 1", got)
	}
	if got := ErrorSamples(-20, 48000); got != -1 {
		t.Fatalf("ErrorSamples(-20µs) = %d, want -1", got)
	}
	if got := ErrorSamples(1_000_000, 48000); got != 48000 {
		t.Fatalf("ErrorSamples(1s) = %d, want 48000", got)
	}
	if got := ErrorSamples(0, 48000); got != 0 {
		t.Fatalf("ErrorSamples(0) = %d", got)
	}
	if got := ErrorSamples(1000, 0); got != 0 {
		t.Fatalf("a zero sample rate produced %d", got)
	}
}

func TestOutputDelayIsSubtractedNotAdded(t *testing.T) {
	// To be HEARD at T a sample must be WRITTEN at T − delay. The sign error
	// puts a device late by twice its own latency, which reads as a bad
	// clock estimate rather than as a sign — a fixed, plausible-sounding
	// amount out.
	clock := Snapshot{Ready: true, Offset: 0, UseDrift: false}
	got, ok := PlayAt(clock, 10_000_000, 50)
	if !ok {
		t.Fatal("PlayAt refused a ready clock")
	}
	if got != 10_000_000-50_000 {
		t.Fatalf("PlayAt = %d, want %d", got, 10_000_000-50_000)
	}
}

func TestPlayAtRefusesAnUnsyncedClock(t *testing.T) {
	if _, ok := PlayAt(Snapshot{}, 1, 0); ok {
		t.Fatal("scheduled a chunk against a clock that is not synced")
	}
}

func TestAChunkInTheFutureIsHeldNotDropped(t *testing.T) {
	// The normal state of a healthy stream: the server sends ahead
	// deliberately, so most chunks are examined before their moment.
	if got := Classify(1_000_500, 1_000_000, 10_000, 30_000_000); got != ActionHold {
		t.Fatalf("a chunk 500µs out was %v, want hold", got)
	}
}

func TestAChunkSlightlyLateIsStillPlayed(t *testing.T) {
	// Zero tolerance is the wrong answer: chunks arrive in bursts and the
	// moment one is examined is not the moment it plays, so a strict test
	// drops audio that would have been perfectly on time.
	if got := Classify(999_000, 1_000_000, 10_000, 30_000_000); got != ActionPlay {
		t.Fatalf("a chunk 1ms late was %v, want play", got)
	}
}

func TestAChunkFarPastItsMomentIsDropped(t *testing.T) {
	// Playing it anyway puts this room behind the others, and every chunk
	// after it inherits the lateness.
	if got := Classify(900_000, 1_000_000, 10_000, 30_000_000); got != ActionDrop {
		t.Fatalf("a chunk 100ms late was %v, want drop", got)
	}
}

func TestAnAbsurdlyDistantChunkIsDroppedRatherThanWaitedOn(t *testing.T) {
	// Beyond the lead limit the timestamp is more likely to be a stale clock
	// estimate than a real instruction, and holding on it stalls the stream
	// for hours.
	if got := Classify(1_000_000_000, 1_000_000, 10_000, 30_000_000); got != ActionDrop {
		t.Fatalf("a chunk 1000s out was %v, want drop", got)
	}
}

func TestExactlyOnTimeIsHeldRatherThanDropped(t *testing.T) {
	// The boundary. Being one microsecond early is not a reason to discard
	// audio.
	if got := Classify(1_000_000, 1_000_000, 10_000, 30_000_000); got != ActionHold {
		t.Fatalf("a chunk due exactly now was %v, want hold", got)
	}
}
