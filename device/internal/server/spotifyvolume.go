package server

import "math"

// SpotifyVolumeMax is the top of librespot's scale. Its volume is a u16, and
// 65535 is unity — the value it reports when the slider is at the right-hand
// end.
const SpotifyVolumeMax = 65535

// spotifyDBFloor is where the mapping stops going down. Below it the codec
// index would be negative and the clamp would catch it, but only by accident
// of the numbers; naming the floor means the range is a decision rather than
// a side effect.
//
// -63.5 dB is the bottom of the control itself (index 0 at 0.5 dB a step from
// unity at 127), so nothing is given away by stopping here.
const spotifyDBFloor = -63.5

// LevelForSpotifyVolume maps librespot's reported volume onto this codec's
// index.
//
// # Why this is not a percentage
//
// librespot reports a LINEAR amplitude fraction — that is what
// `--volume-ctrl linear` means, and it is why this build asks for it rather
// than for the default `log`. The codec control is dB-LINEAR: 0.5 dB per step
// with unity at `volumeMax` (127).
//
// So the conversion is a real one, `20·log10(v/65535)`, and then the same
// offset `LevelForAirPlayDB` uses. Treating the u16 as a percentage of 0..127
// would crush the bottom two thirds of the slider into inaudibility — which is
// exactly the reason `volumeButtonFloor` exists for the physical buttons, and
// the same mistake AirPlay's mapping is written to avoid.
//
// # Why `--volume-ctrl log` would be worse, not simpler
//
// librespot's `log` curve exists so that a linear multiply on the SAMPLES
// sounds right. Feeding its output into a control that is already logarithmic
// applies the curve twice, and the slider then does almost nothing for its
// top half and everything in the last centimetre.
//
// # Zero is silence, and it is checked
//
// A slider dragged to the bottom means silence, and `20·log10(0)` is negative
// infinity rather than a number. Checked explicitly, so that a future change
// to the floor cannot quietly turn "off" into "very quiet" — the same reason
// AirPlay's -144 dB sentinel is checked rather than left to the clamp.
//
// # Not floored at volumeButtonFloor
//
// That floor keeps PHYSICAL presses from spending themselves crossing a third
// of the scale nobody can hear. Nothing about it applies to a slider somebody
// drags where they mean, and Home Assistant's volume 0.0 has to keep meaning
// silent. Same call as `LevelForAirPlayDB`.
func LevelForSpotifyVolume(v uint16) int {
	if v == 0 {
		return volumeMin
	}
	db := 20 * math.Log10(float64(v)/float64(SpotifyVolumeMax))
	if db < spotifyDBFloor {
		db = spotifyDBFloor
	}
	if db > 0 {
		db = 0
	}
	// 0.5 dB per step: two steps per dB, counting down from unity.
	level := int(math.Round(float64(volumeMax) + db*2))
	if level < volumeMin {
		return volumeMin
	}
	if level > volumeMax {
		return volumeMax
	}
	return level
}
