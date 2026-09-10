package server

import "math"

// AirPlayDBMute is the value AirPlay sends for mute. Not a quiet volume: it
// is a sentinel, and 30 dB below the bottom of the real range.
const AirPlayDBMute = -144.0

// AirPlay's own slider spans these, by definition of the protocol.
const (
	airPlayDBMin = -30.0
	airPlayDBMax = 0.0
)

// LevelForAirPlayDB maps an AirPlay volume onto this codec's index.
//
// # Both scales are already dB, so this is an OFFSET and not a rescale
//
// tinymix ctl 61 is 0.5 dB per step with unity at `volumeMax` (127), so
// `index = 127 + dB*2` puts AirPlay's 0 dB at the codec's unity gain and
// walks down in step with it. A phone showing half volume then produces
// half volume in the same dB sense the hardware uses.
//
// The tempting alternative — treating the slider as a percentage of 0..127 —
// is wrong twice over. The control is dB-LINEAR, so a linear percentage
// crushes everything below about a third of the slider into inaudibility
// (which is the whole reason `volumeButtonFloor` exists); and it would throw
// away the fact that AirPlay already tells us decibels.
//
// # Mute lands on SILENCE, not on the floor
//
// AirPlay sends -144 dB for mute, which is a sentinel rather than a very
// quiet volume. Arithmetic alone would take it to a negative index and the
// clamp would catch it, but only by accident of the numbers; it is checked
// explicitly so that a future change to the range cannot quietly turn "mute"
// into "quiet".
//
// # Not floored at volumeButtonFloor, deliberately
//
// That floor exists because the PHYSICAL buttons should not spend presses
// crossing a third of the scale that is indistinguishable from silence.
// Nothing about that applies to a slider on a phone, which can be dragged
// anywhere in one gesture and whose owner means it. Same reasoning as
// explicit Set() calls not being floored: Home Assistant's volume 0.0 has to
// still mean silent.
//
// AirPlay's own minimum (-30 dB → index 67) lands above the button floor
// anyway, so in practice the range a phone can reach is entirely audible.
func LevelForAirPlayDB(db float64) int {
	if db <= AirPlayDBMute || math.IsNaN(db) {
		return volumeMin
	}
	if db > airPlayDBMax {
		db = airPlayDBMax
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
