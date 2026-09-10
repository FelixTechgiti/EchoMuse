// Package endpoint carries what the device can say about the streaming
// programs it runs — librespot and shairport-sync — while they are running.
//
// # "Installed" is not "working", and only the first was ever reported
//
// `spotify_status` and `airplay_status` ride the register message and answer
// whether the BINARY is on the device: present, a file, executable, its size.
// That was written for the question "why is this off" when the answer is a
// missing file, and it answers it well.
//
// It cannot answer the question that actually gets asked. On 2026-09-10 a
// device had shairport-sync installed, executable, the right size, reported
// `ok: true` — and did not appear in any AirPlay picker for two hours,
// because an orphaned copy of it from before the last OTA still held TCP
// 5000 and every new instance exited immediately:
//
//	14:44:29 [airplay] shairport-sync exited: exit status 1     (and every minute after)
//
// Every panel said the endpoint was fine. The one line that said otherwise
// was on the device, in a log nobody could read. Diagnosing it took five
// shell sessions and two wrong diagnoses.
//
// # Three fields, because each rules out a different thing
//
//   - Enabled — the supervisor is up, i.e. the user turned this on. `Running()`
//     has always meant this and is easily mistaken for the next one.
//   - Alive — a process exists RIGHT NOW. Enabled without Alive, sampled
//     repeatedly, is the fault above.
//   - Restarts — how many times the supervisor has had to start it. Steady is
//     healthy; climbing is the signature, and it is the field that separates
//     "briefly between sessions" from "failing every minute for two hours"
//     without needing two samples to be sure.
//
// LastExit carries the reason the last one ended, because `exit status 1`
// against `signal: killed` is the difference between a port it cannot bind
// and a preemption we asked for.
//
// # Why this rides the STATS tick and not the register message
//
// The register message is for static properties of the boot — `base_os`,
// `ambient_light_status`, whether a binary is installed. Whether a process is
// alive is none of those: it is true at 14:44 and false at 14:45, and a
// device that reported it once at registration would report it wrong for
// however long it stayed connected. The rule from the compatibility section
// applies unchanged — ask when the consumer needs the answer.
package endpoint

// Health is what one streaming endpoint can say about itself.
//
// Absence of the whole object means the firmware does not report it, which is
// NOT the same as "not running" and must not render as it — the same
// NULL-not-zero rule the rest of the protocol follows.
type Health struct {
	Enabled  bool   `json:"enabled"`
	Alive    bool   `json:"alive"`
	Restarts int    `json:"restarts"`
	UptimeS  int    `json:"uptimeS,omitempty"`
	LastExit string `json:"lastExit,omitempty"`
}
