package server

import "testing"

func TestSpotifyFullVolumeIsUnityGain(t *testing.T) {
	if got := LevelForSpotifyVolume(SpotifyVolumeMax); got != volumeMax {
		t.Errorf("full slider = %d, want %d (the codec's unity gain)", got, volumeMax)
	}
}

// Zero is silence, not "very quiet". Checked explicitly so a change to the
// floor cannot turn a slider dragged to the bottom into audible sound.
func TestSpotifyZeroIsSilence(t *testing.T) {
	if got := LevelForSpotifyVolume(0); got != volumeMin {
		t.Errorf("zero = %d, want %d", got, volumeMin)
	}
}

// The whole point of the conversion. Half the AMPLITUDE is -6 dB, which on a
// 0.5 dB-per-step control is twelve steps down from unity — not half the
// index. Treating the u16 as a percentage would give 63 here and make the
// bottom two thirds of the slider inaudible.
func TestHalfAmplitudeIsMinusSixDBAndNotHalfTheIndex(t *testing.T) {
	got := LevelForSpotifyVolume(SpotifyVolumeMax / 2)
	if want := volumeMax - 12; got != want {
		t.Errorf("half amplitude = %d, want %d (-6dB at two steps per dB)", got, want)
	}
	if got == volumeMax/2 {
		t.Error("the u16 was treated as a percentage of the index range")
	}
}

// A quarter of the amplitude is -12 dB, i.e. another twelve steps. Two points
// on the curve pin that it is logarithmic rather than merely offset.
func TestQuarterAmplitudeIsMinusTwelveDB(t *testing.T) {
	got := LevelForSpotifyVolume(SpotifyVolumeMax / 4)
	if want := volumeMax - 24; got != want {
		t.Errorf("quarter amplitude = %d, want %d", got, want)
	}
}

// Monotonic, and never outside the control's range. A slider that jumped
// backwards somewhere in the middle would be the kind of fault nobody reports
// precisely because it only shows up at one position.
func TestMappingIsMonotonicAndInRange(t *testing.T) {
	prev := -1
	for v := 0; v <= SpotifyVolumeMax; v += 97 {
		got := LevelForSpotifyVolume(uint16(v))
		if got < volumeMin || got > volumeMax {
			t.Fatalf("volume %d mapped to %d, outside %d..%d",
				v, got, volumeMin, volumeMax)
		}
		if got < prev {
			t.Fatalf("volume %d mapped to %d, below the previous %d", v, got, prev)
		}
		prev = got
	}
}

// The bottom of the slider must stay reachable as silence-adjacent rather than
// wrapping or clamping to something audible.
func TestVeryQuietStaysVeryQuiet(t *testing.T) {
	if got := LevelForSpotifyVolume(1); got > 5 {
		t.Errorf("the quietest non-zero volume mapped to %d, which is audible", got)
	}
}

// Deliberately NOT floored at volumeButtonFloor: that floor is about physical
// presses, and Home Assistant's volume 0.0 has to keep meaning silent. Same
// decision as LevelForAirPlayDB, pinned so neither drifts.
func TestSpotifyIsNotFlooredAtTheButtonFloor(t *testing.T) {
	if got := LevelForSpotifyVolume(100); got >= volumeButtonFloor {
		t.Errorf("a very low slider mapped to %d, at or above the button floor "+
			"%d — that floor is for buttons, not for a slider somebody drags",
			got, volumeButtonFloor)
	}
}
