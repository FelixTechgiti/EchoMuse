// Package devicepaths says where this device's own files live, across a
// rename.
//
// **The rename moved four on-device paths in one commit, and nothing
// migrated any of them.** `/data/local/etc/echomuse/` became
// `/data/local/etc/revoice/`, and the firmware simply started looking
// somewhere else — so everything a device already had became invisible the
// moment it took the new firmware. The controller half of this was handled
// (controller/em_devicepaths.py writes both directories); the device half was
// not, and each file failed differently and quietly:
//
//   - `controller.json` — the remembered controller endpoint. Losing it puts
//     a device back on mDNS-only discovery, which is precisely the fault the
//     cache was added to remove. Measured 2026-09-11 on the fleet device: it
//     took the new firmware, lost the cache, and the next reconnect had
//     nothing but a browse to fall back on.
//   - `state.json` — mute. Device-sovereign state, silently reset to
//     unmuted, which is the wrong direction to fail in for a microphone.
//
// Reads look in the current directory first and fall back to the legacy one;
// writes always go to the current path, so the first write migrates the file
// and the fallback stops being consulted. Same asymmetry the controller's
// module settles on: the cost is a stale copy left behind, and the cost of
// the alternative is state that vanishes on upgrade.
//
// This is the DEVICE's answer to it and is deliberately separate from the
// controller's: they resolve different questions (where do I read my own
// files, versus where do I push a file to somebody else's device) and share
// nothing but a pair of string constants, which are pinned against each
// other by controller/tests/test_devicepaths.py.
package devicepaths

import (
	"os"
	"path/filepath"
)

// CurrentDir is where this firmware writes.
const CurrentDir = "/data/local/etc/revoice"

// LegacyDirs are directories previous names used, newest first. Each one is
// somewhere a device in the field still has files, and stays until no such
// device can exist.
var LegacyDirs = []string{"/data/local/etc/echomuse"}

// Read returns the path `name` should be READ from: the current one if it is
// there, otherwise the first legacy one that is, otherwise the current one —
// so a caller reporting "no such file" names the place a reader would look
// first rather than the last place tried.
func Read(name string) string {
	return pick(name, CurrentDir, LegacyDirs, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
}

// Write returns the path `name` should be WRITTEN to. Always the current
// directory: a write is what migrates the file.
func Write(name string) string { return filepath.Join(CurrentDir, name) }

// pick is the pure half, so the ordering can be tested without a filesystem
// shaped like a device's.
func pick(name, current string, legacy []string, exists func(string) bool) string {
	cur := filepath.Join(current, name)
	if exists(cur) {
		return cur
	}
	for _, d := range legacy {
		if p := filepath.Join(d, name); exists(p) {
			return p
		}
	}
	return cur
}
