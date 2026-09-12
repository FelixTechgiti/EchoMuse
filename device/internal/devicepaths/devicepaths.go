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
	"path"
)

// `path`, not `path/filepath`, and the difference is not cosmetic: every
// directory named here is a path ON THE DEVICE, which is Linux, so the
// separator is `/` no matter what machine the code was compiled on.
//
// `path/filepath` asks the HOST, and on the target that is the same answer —
// the firmware runs on the device. It is a different answer on a developer's
// Windows workstation, where `filepath.Join` returns `\data\local\etc\…` and
// the four tests in this package failed against their `/`-shaped literals.
// That is a guard going red about the machine it runs on rather than about
// the code, which is the fastest way to teach somebody to skip a suite.

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
func Write(name string) string { return path.Join(CurrentDir, name) }

// AllDirs returns every directory a record must be written to, current first.
//
// **The difference from Write is who reads the file back**, and that is the
// whole reason both exist.
//
// Write is for a file this firmware reads itself — the discovery cache, the
// mute state. One writer and one reader, updated together by the same OTA, so
// writing the current path and falling back on read is enough: the first
// write migrates the file and the fallback stops being consulted.
//
// AllDirs is for a file a DIFFERENT program reads, on its own update
// schedule. emOS's init is the case that exists: it reads the console
// password and idle-timeout records, and it arrives only when somebody
// flashes a boot partition. So this firmware cannot know which directory the
// init in front of it opens, and cannot upgrade that init either — the same
// position the CONTROLLER is in when it pushes to a device, which is why
// em_devicepaths.write_dirs() writes both rather than choosing by version.
// There is no capability for "which directory do you read", and there could
// not be one: the reader is not on the wire.
//
// The cost is an empty legacy directory holding one file on a device that
// never carried the old name. The cost of the alternative is a console
// password that reports success and changes nothing — and, in the CLEARING
// direction, a password belonging to a previous owner that stays in front of
// the console after its new owner was told it was removed.
func AllDirs() []string {
	dirs := make([]string, 0, 1+len(LegacyDirs))
	dirs = append(dirs, CurrentDir)
	return append(dirs, LegacyDirs...)
}

// pick is the pure half, so the ordering can be tested without a filesystem
// shaped like a device's.
func pick(name, current string, legacy []string, exists func(string) bool) string {
	cur := path.Join(current, name)
	if exists(cur) {
		return cur
	}
	for _, d := range legacy {
		if p := path.Join(d, name); exists(p) {
			return p
		}
	}
	return cur
}
