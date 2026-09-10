package server

import "testing"

// Both scales are dB, so the mapping is an offset. AirPlay's 0 dB is the
// codec's unity gain — the loudest this device is allowed to go, and the
// ceiling that exists because everything above it saturates inside the DAC.
func TestAirPlayZeroIsUnityGain(t *testing.T) {
	if got := LevelForAirPlayDB(0); got != volumeMax {
		t.Fatalf("0 dB -> %d, want %d (the codec's unity gain)", got, volumeMax)
	}
}

// One dB on the phone must be one dB on the speaker, or the slider stops
// meaning anything. 0.5 dB per step means two steps per dB.
func TestOneDBIsTwoSteps(t *testing.T) {
	for _, tc := range []struct {
		db   float64
		want int
	}{
		{0, 127}, {-6, 115}, {-15, 97}, {-30, 67},
	} {
		if got := LevelForAirPlayDB(tc.db); got != tc.want {
			t.Fatalf("%.0f dB -> %d, want %d", tc.db, got, tc.want)
		}
	}
}

// -144 is a SENTINEL, not a very quiet volume. It must reach actual silence:
// a phone that mutes and hears something is a bug report.
func TestMuteIsSilenceNotQuiet(t *testing.T) {
	if got := LevelForAirPlayDB(AirPlayDBMute); got != volumeMin {
		t.Fatalf("mute -> %d, want %d (silence)", got, volumeMin)
	}
	// And anything below the sentinel too, since a device that rounds or a
	// protocol that grows must not land on "quiet".
	if got := LevelForAirPlayDB(-200); got != volumeMin {
		t.Fatalf("-200 dB -> %d, want silence", got)
	}
}

// The button floor exists so PHYSICAL presses do not spend themselves
// crossing a third of the scale that sounds like silence. None of that
// applies to a slider somebody drags where they mean, and applying it would
// make the bottom of an AirPlay slider audible — the opposite of what
// dragging it to the bottom asks for.
func TestTheButtonFloorIsNotApplied(t *testing.T) {
	// A value below the floor must be reachable through this path.
	if got := LevelForAirPlayDB(-50); got >= volumeButtonFloor {
		t.Fatalf("-50 dB -> %d, which the button floor (%d) has clamped",
			got, volumeButtonFloor)
	}
	if got := LevelForAirPlayDB(-63.5); got != volumeMin {
		t.Fatalf("-63.5 dB is the bottom of the control, got %d", got)
	}
}

// AirPlay's own minimum is -30 dB, which lands comfortably above the button
// floor — so everything a phone can actually select is audible. Worth
// pinning: if the ceiling ever moved, this is the property that would break
// silently and leave a slider whose bottom half does nothing.
func TestEverythingAPhoneCanSelectIsAudible(t *testing.T) {
	if got := LevelForAirPlayDB(airPlayDBMin); got <= volumeButtonFloor {
		t.Fatalf("AirPlay's own minimum maps to %d, at or below the floor %d "+
			"— the bottom of the phone's slider would be inaudible",
			got, volumeButtonFloor)
	}
}

// Nothing may exceed the ceiling. It is not a preference: above unity the
// DAC saturates (THD 65% at index 153, measured), and a remote source must
// not be able to reach past a limit the physical buttons respect.
func TestNothingExceedsTheCeiling(t *testing.T) {
	for _, db := range []float64{0, 1, 12, 1000} {
		if got := LevelForAirPlayDB(db); got > volumeMax {
			t.Fatalf("%.0f dB -> %d, past the codec's unity ceiling %d",
				db, got, volumeMax)
		}
	}
}

// Monotonic, or a slider would move the wrong way somewhere in its travel.
func TestLouderIsNeverQuieter(t *testing.T) {
	prev := -1
	for db := -60.0; db <= 0.0; db += 0.25 {
		got := LevelForAirPlayDB(db)
		if got < prev {
			t.Fatalf("at %.2f dB the level fell from %d to %d", db, prev, got)
		}
		prev = got
	}
}
