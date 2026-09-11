// Package hostname gives the device a resolvable name of its own.
//
// **The Echo boots reporting `localhost`**, and that is not cosmetic. Both
// streaming endpoints bring their own mDNS responder, and a responder
// publishes its service with an SRV record whose target is `<hostname>.local`
// plus an A record for that name. With the hostname `localhost` the record
// published is `localhost.local` — and a client that resolves the target by
// name gets 127.0.0.1, itself, so it connects to nothing and simply never
// lists the device. Measured on a live device 2026-09-11 while working out why
// an Echo was absent from Spotify Connect.
//
// It is set here rather than left to the base OS because both bases get it
// wrong in different ways: Android leaves the UTS name at `localhost` while
// putting a real name in the `net.hostname` property that `gethostname()` does
// not read, and emOS's init has no reason to invent one. Setting it in the
// firmware means one answer on both, before anything that publishes it starts.
//
// The name is derived from the SERIAL, never from the device's label. The
// label is controller-side, arrives on a config push long after the endpoints
// have launched, and is free text somebody can change — and a hostname that
// moves is worse than a dull one, because mDNS caches it and every client
// keeps the stale name until the TTL expires. The serial is the one identifier
// the device owns, knows at startup, and never changes.
package hostname

import (
	"strings"
	"syscall"
)

// Prefix goes in front of the serial. Not the empty string: a bare serial is
// not recognisable as anything, and this name shows up in packet captures and
// in other people's routers.
const Prefix = "revoice"

// maxLen is the DNS label limit. A serial is 16 characters so this never
// bites today, but a truncation that produced an invalid label would fail
// silently in the one place nothing reports errors.
const maxLen = 63

// For turns a serial into a name that is legal as a DNS label.
//
// Legal means: lowercase, only letters, digits and hyphens, no leading or
// trailing hyphen, non-empty. Anything else is dropped rather than escaped —
// a serial with a surprise character in it should still produce a working
// name, and the serial is already carried verbatim everywhere it matters.
//
// An unusable serial falls back to the bare prefix. Two devices would then
// share a name, which mDNS resolves by renaming one of them; that is a poor
// outcome and still better than publishing `localhost.local`, which cannot
// work for anybody.
func For(serial string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(serial)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ' || r == '.':
			// Collapsed rather than kept: a run of separators would leave
			// a double hyphen, and trailing ones are illegal outright.
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	suffix := strings.Trim(b.String(), "-")
	if suffix == "" {
		return Prefix
	}
	name := Prefix + "-" + suffix
	if len(name) > maxLen {
		name = strings.TrimRight(name[:maxLen], "-")
	}
	return name
}

// Set applies the name for this serial and reports what it set.
//
// A failure is returned rather than fatal, and the caller logs it and carries
// on: an unsettable hostname costs discoverability of the streaming endpoints
// and nothing else, while refusing to start costs the voice assistant, the
// ring, the microphone and the OTA that would fix it. The usual cause is not
// being root, which is a development environment rather than a device.
func Set(serial string) (string, error) {
	name := For(serial)
	if err := syscall.Sethostname([]byte(name)); err != nil {
		return name, err
	}
	return name, nil
}
