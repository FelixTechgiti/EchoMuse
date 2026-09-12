// Package mcast watches whether this device is still a member of the mDNS
// multicast group, and says so when it is not.
//
// # The fault
//
// Measured on a live device 2026-09-12, with both endpoints running, both
// holding UDP 5353, the firewall open and the controller talking to the device
// over WebSocket the whole time — and the controller's own network scan
// reporting:
//
//	Spotify Connect was not seen from this device, while 7 other host(s)
//	on the network did answer. The scan works; this device is not being heard.
//	AirPlay was not seen from this device, while 1 other host(s) did answer.
//
// `/proc/net/igmp` said why:
//
//	9  wlan0 :  1  V3
//	          010000E0  1        <- 224.0.0.1, joined by every interface
//
// **224.0.0.251 was gone.** The responders were listening on the port and had
// stopped being members of the group, so no query ever reached them and they
// answered nothing. Restarting both endpoints put it back —
//
//	9  wlan0 :  2  V3
//	          FB0000E0  2        <- 224.0.0.251, two sockets
//
// — and the same scan then read "Every enabled endpoint is visible on the
// network."
//
// # Why this is worth a watcher rather than a fix at the source
//
// A socket joins a group once, and the membership lives in the KERNEL against
// the interface. Anything that takes the interface down and back — a
// re-association, a supplicant reconfigure, netd rebuilding its state — drops
// it, and nothing tells the process: its socket is still open, still bound,
// still perfectly healthy from its own point of view. librespot and
// shairport-sync are third-party programs that do not re-join, and patching
// both is a maintenance cost on every version bump.
//
// So the answer is the one the rest of this tree already uses where Android
// writes state back underneath us: keep looking, and repair.
//
// It explains a run of reports that were previously filed as separate faults
// — the device vanishing from BOTH pickers at once for minutes at a time, a
// reboot always fixing it, and every on-device measurement reading healthy
// while it happened. Both services fail together because the membership
// belongs to the interface, not to either program.
//
// **What is NOT known is what drops it.** This treats the symptom, reliably
// and cheaply; the trigger is a separate measurement, and the log line below
// is the instrument for it.
package mcast

import (
	"encoding/binary"
	"encoding/hex"
	"net"
	"strings"
)

// Group is the mDNS multicast group. Both endpoints join it; so does anything
// else on the device that speaks mDNS.
const Group = "224.0.0.251"

// ProcPath is where the kernel lists group memberships per interface.
const ProcPath = "/proc/net/igmp"

// Joined reports whether `iface` is a member of `group` according to the
// contents of /proc/net/igmp.
//
// The format is two levels and the indentation is the only thing separating
// them — an interface header, then its groups, one per line:
//
//	Idx	Device    : Count Querier	Group    Users Timer	Reporter
//	1	lo        :     1      V3
//				010000E0     1 0:00000000		0
//	9	wlan0     :     2      V3
//				FB0000E0     2 0:00000000		0
//				010000E0     1 0:00000000		0
//
// **The group is printed as the in-memory 32-bit value, so on a little-endian
// machine the address reads backwards**: 224.0.0.251 is stored as the bytes
// E0 00 00 FB and prints as FB0000E0. That is derived here rather than
// hardcoded, because a hardcoded FB0000E0 is a fact about biscuit's byte order
// masquerading as a fact about mDNS, and the next board is where it would be
// found out. Both the device (armv7a) and the test host are little-endian; a
// big-endian port has to revisit this one line and nothing else.
func Joined(proc, iface, group string) bool {
	want := encodeGroup(group)
	if want == "" {
		return false
	}
	inSection := false
	for _, line := range strings.Split(proc, "\n") {
		if line == "" {
			continue
		}
		// An interface header starts at column 0 and carries the ':' that
		// separates the device name from its counters. A group line is
		// indented — that is the whole of the grammar.
		if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
			name, _, ok := strings.Cut(line, ":")
			if !ok {
				continue // the header row
			}
			inSection = strings.TrimSpace(fieldAfterIdx(name)) == iface
			continue
		}
		if !inSection {
			continue
		}
		f := strings.Fields(line)
		if len(f) > 0 && strings.EqualFold(f[0], want) {
			return true
		}
	}
	return false
}

// fieldAfterIdx drops the leading index column from an interface header,
// leaving the device name. "9\twlan0    " -> "wlan0    ".
func fieldAfterIdx(s string) string {
	f := strings.Fields(s)
	if len(f) < 2 {
		return ""
	}
	return f[len(f)-1]
}

// encodeGroup renders a dotted IPv4 address the way /proc/net/igmp prints it.
// Empty for anything that is not an IPv4 address, so a caller cannot match on
// a typo: returning a zero value that matches nothing would read as "not
// joined" and restart the endpoints for ever.
func encodeGroup(group string) string {
	ip := net.ParseIP(group)
	if ip == nil {
		return ""
	}
	v4 := ip.To4()
	if v4 == nil {
		return ""
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], binary.BigEndian.Uint32(v4))
	return strings.ToUpper(hex.EncodeToString(b[:]))
}
