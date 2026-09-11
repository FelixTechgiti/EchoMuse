package wifi

import "strings"

// The radio's own account of itself, for the supervisor log.
//
// **Holding an IP address is not the same as passing traffic, and the record
// could not tell them apart.** On 2026-09-11 a device was unreachable for 22
// hours while `wlan0=192.168.178.140` — the only network field the log
// carried — stayed correct throughout. The controller's mDNS scan during that
// window heard seven other Spotify Connect hosts and two other AirPlay hosts
// on the same network and not this one, and the device's own record showed
// the remembered controller address not answering either. Multicast and
// unicast failed together, on an interface that still had an address.
//
// That leaves two explanations, and they want OPPOSITE responses:
//
//   - the supplicant is associated and the link carries nothing — a zombie
//     association, which re-associating would clear; or
//   - the supplicant has lost the association and is scanning — an AP or
//     roaming problem, which re-associating would not fix and could prolong.
//
// Deliberately dropping the WiFi of a device whose only management path IS
// that WiFi has real teeth, so this measures and nothing acts on it. It is
// an instrument, not a fix (#51).
//
// Cost: two `wpa_cli` invocations per line. That is affordable only because
// the escalator makes these lines rare by construction — 1, 5, 15 and 30
// minutes, then half-hourly, with a healthy device writing none at all. If
// this is ever called on a path that repeats, re-read that sentence: cost
// comments written for a rare caller are exactly the ones that go stale
// silently when the caller stops being rare.

// Describe renders the supplicant's view of the radio as log fields.
//
// It never fails and never blocks on a missing tool: no answer from
// wpa_cli reads as `wifi=unknown`, which is a different statement from any
// supplicant state and must not be confused with one.
func Describe() string {
	status, _ := wpaCli("status")
	signal, _ := wpaCli("signal_poll")
	return describe(status, signal)
}

// describe is the pure half, so the field shapes can be pinned on the host —
// this device's own wpa_cli is the one thing a test here cannot have.
func describe(status, signal string) string {
	state := field(status, "wpa_state")
	if state == "" {
		return "wifi=unknown"
	}

	parts := []string{"wifi=" + state}
	// The BSSID is the field that shows a ROAM across an outage, which is
	// the one thing "associated" alone cannot say on a network with more
	// than one access point.
	for _, f := range []struct{ out, key, label string }{
		{status, "bssid", "bssid"},
		{status, "freq", "freq"},
		{signal, "RSSI", "rssi"},
	} {
		if v := field(f.out, f.key); v != "" {
			parts = append(parts, f.label+"="+v)
		}
	}
	return strings.Join(parts, " ")
}

// field reads `key=value` out of wpa_cli's output.
//
// Matched on the WHOLE key, not as a substring: `status` carries both
// `bssid=` and `ssid=`, and a suffix match would answer the first with the
// second — an SSID rendered as a BSSID, which reads as a plausible value
// rather than as a fault.
func field(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+"="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
