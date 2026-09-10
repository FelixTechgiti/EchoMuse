// Package logrelay forwards a SELECTION of the firmware's own log to the
// controller, so a fault on the device can be read from somewhere other than
// the device.
//
// # Why
//
// The firmware logs to stdout, which start_server.sh puts in /tmp/server.log —
// RAM-backed, on a box with no remote access of its own. The only lines that
// ever reached the controller were the `[mem]` heap summaries. So
// `[airplay] shairport-sync exited: exit status 1`, repeating every minute for
// two hours, was visible to nobody but somebody willing to open a root shell
// on their own hardware. That is what made 2026-09-10 cost five shell sessions
// and two wrong diagnoses; see the endpoint-orphan section in device/CLAUDE.md.
//
// # Why it is a SELECTION, and tightly bounded
//
// The control plane is the LIVENESS channel: RTT is measured on it, the
// keepalive pong rides it, and `writeJSON` serialises everything through one
// mutex on one TCP stream. Putting bulk traffic there is exactly #404, where
// BLE advertisements on this socket produced 3615 idle RTT excursions in 24h
// against a neighbour's 2. A log relay that forwarded freely would rebuild
// that fault with a different payload.
//
// So: only lines that would make somebody act, never more than `maxPerWindow`
// of them per window, and the count of what was dropped rides the next one
// through. Everything still goes to stdout unchanged — this ADDS a copy for a
// few lines, it does not move the log.
package logrelay

import "strings"

// Level is what the controller stores the line as. Only two are produced:
// anything worth the bandwidth is at least a warning, and "info" exists for
// the lifecycle lines that explain a warning arriving later.
const (
	LevelWarn = "warn"
	LevelInfo = "info"
)

// failureMarkers are the substrings that make a line worth a person's
// attention. Lower-cased before matching, so `Error`, `ERROR` and `error`
// are one entry rather than three.
//
// Deliberately about OUTCOMES rather than components: a new subsystem that
// fails should be relayed the day it is written, without anyone remembering
// to add it here.
var failureMarkers = []string{
	"error",
	"failed",
	"cannot",
	"could not",
	"unable",
	"denied",
	"refused",
	"timeout",
	"timed out",
	"exited",
	"panic",
	"giving up",
	"not installed",
}

// lifecycleMarkers are lines that are not failures and are the context a
// failure is unreadable without — which of the endpoints came up, and whether
// the speaker ever opened.
//
// `PcmSpeaker initialised` is here for a specific reason: its ABSENCE is the
// tell for a device whose PCM Android will not release, and an absence can
// only be read if the presence is normally there to compare against.
var lifecycleMarkers = []string{
	"[airplay] enabled",
	"[airplay] disabled",
	"[spotify] enabled",
	"[spotify] disabled",
	"pcmspeaker initialised",
	"pcmspeaker closed",
	"orphaned instance",
}

// noiseMarkers are relayed by something else or are pure volume. `[mem]` has
// its own relay on the stats path and is 89% of the device_logs table; the
// AEC and mic telemetry lines are ~1/s during playback and belong in the
// per-turn instrumentation, not here.
var noiseMarkers = []string{
	"[mem]",
	"[aec]",
	"[mic] clock",
}

// Classify decides whether a log line is worth the liveness channel, and at
// what level.
//
// Pure and tested because the cost of getting it wrong is asymmetric and
// silent in both directions: too narrow and the next fault is invisible again,
// too broad and the relay degrades the connection it reports over.
func Classify(line string) (level string, forward bool) {
	low := strings.ToLower(line)
	for _, m := range noiseMarkers {
		if strings.Contains(low, m) {
			return "", false
		}
	}
	for _, m := range failureMarkers {
		if strings.Contains(low, m) {
			return LevelWarn, true
		}
	}
	for _, m := range lifecycleMarkers {
		if strings.Contains(low, m) {
			return LevelInfo, true
		}
	}
	return "", false
}
