package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/wilbowes/EchoMuse/internal/devicepaths"
)

// consoleDirs are the directories the console records are written to, current
// first — and EVERY one of them is written on every write.
//
// **The reader is on its own update schedule, which is why this is not one
// path.** The firmware writes these records; emOS's init reads them, and a
// new init arrives only when somebody flashes a boot partition. The rename
// moved the constant on both sides in the same commit, so an init older than
// the rename opens `/data/local/etc/echomuse/` while the firmware in front of
// it writes `/data/local/etc/revoice/`. Setting a password from the dashboard
// then does nothing, and CLEARING one does nothing either — which is the
// dangerous half, because clearing is what you do when the password belongs
// to a previous owner. See devicepaths.AllDirs for why this cannot be
// negotiated.
//
// A var rather than a call at each use so a test can point the records at a
// temporary directory: on a device they live under /data, and a test that
// wrote there would need root and a filesystem shaped like a device's.
var consoleDirs = devicepaths.AllDirs()

const (
	consolePasswordName = "console.pw"
	consoleTimeoutName  = "console.timeout"
)

// recordPaths is every path a console record must exist at, current first.
func recordPaths(name string) []string {
	paths := make([]string, 0, len(consoleDirs))
	for _, dir := range consoleDirs {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths
}

// ConsolePasswordPath is the CURRENT path of the console password record —
// where an init built since the rename looks. It sits beside the TLS
// credentials because it is the same kind of thing — a per-device secret
// pushed by the controller — and because that directory already survives an
// OTA.
//
// The FIRMWARE writes it and INIT reads it, which is the whole reason it is a
// file rather than something held in memory: the console has to work when
// Revoice is not running, since that is exactly when someone needs it.
//
// Not the only path written; see consoleDirs.
var ConsolePasswordPath = filepath.Join(devicepaths.CurrentDir, consolePasswordName)

// writeRecord stores `want` at EVERY path for `name`, or removes every one of
// them when `want` is empty. It reports whether anything on disk changed.
//
// Three properties this has to hold, each with a way of being wrong that is
// silent on hardware:
//
//   - **Every path is attempted even after one fails.** A legacy directory
//     that cannot be created must not cost the current one its write; the
//     first error is kept and returned once the rest have been tried.
//   - **A path is only touched when its content actually differs.** The
//     config push repeats every setting on every reconnect, and this device
//     runs for years on eMMC that cannot be replaced.
//   - **Written to a temp file and renamed**, so init can never read a
//     half-written record. A truncated password record parses as unusable,
//     which is read as NO password and would leave the console open exactly
//     while it looked configured; a truncated timeout reads as a SHORTER one,
//     which presents as the device dropping the link.
func writeRecord(name string, want []byte) (changed bool, err error) {
	want = bytes.TrimSpace(want)
	clearing := len(want) == 0

	for _, path := range recordPaths(name) {
		current, readErr := os.ReadFile(path)
		exists := readErr == nil

		if clearing {
			if !exists {
				continue
			}
			if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
				if err == nil {
					err = rmErr
				}
				continue
			}
			changed = true
			continue
		}

		if exists && bytes.Equal(bytes.TrimSpace(current), want) {
			continue
		}

		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			if err == nil {
				err = mkErr
			}
			continue
		}
		tmp := path + ".tmp"
		if wrErr := os.WriteFile(tmp, append(append([]byte{}, want...), '\n'), 0o600); wrErr != nil {
			if err == nil {
				err = wrErr
			}
			continue
		}
		if mvErr := os.Rename(tmp, path); mvErr != nil {
			os.Remove(tmp)
			if err == nil {
				err = mvErr
			}
			continue
		}
		changed = true
	}
	return changed, err
}

// WriteConsolePassword stores the record emOS's init checks against, or
// removes it when the record is empty.
//
// The record is already hashed — the controller hashes before it is stored or
// pushed, so no plaintext passes through here. Empty means no password, which
// is why the caller must distinguish an empty record from an ABSENT field: the
// config push repeats every setting on every connect, so "" has to mean
// "remove" rather than "nothing was said".
//
// Written only when the content actually changes. The push arrives on every
// reconnect, and this device runs for years on eMMC that cannot be replaced,
// so an unconditional write would spend a flash write per reconnect to store
// bytes that were already there.
//
// **Written to every path in consoleDirs, and an empty record removes every
// one of them.** The clearing direction is the one that matters: an init
// older than the rename reads the legacy path, so removing only the current
// record would report success to somebody who is clearing a password they do
// not know — and leave that password in front of the console.
func WriteConsolePassword(record string) (changed bool, err error) {
	return writeRecord(consolePasswordName, []byte(record))
}

// ConsoleTimeoutPath is where emOS's init looks for the console idle timeout,
// beside the password record and for the same reasons: the FIRMWARE writes it
// and INIT reads it, because the console has to work when Revoice is not
// running.
// Not the only path written; see consoleDirs.
var ConsoleTimeoutPath = filepath.Join(devicepaths.CurrentDir, consoleTimeoutName)

// ConsoleTimeoutMaxMin is the ceiling the controller validates against and
// init clamps to. 90 minutes is long enough that a real debugging session is
// never the thing being cut off.
const ConsoleTimeoutMaxMin = 90

// WriteConsoleTimeout stores the console idle timeout in MINUTES, or removes
// the record when the timeout is zero.
//
// Minutes rather than seconds because that is the unit the setting is chosen
// in (0, then 1-90). `TMOUT` is seconds, so init multiplies when it builds the
// shell's environment — one conversion, at the point of use, rather than a
// factor of sixty between the stored value, the pushed value and the number on
// screen.
//
// Zero means no timeout and is a CHOICE, not an absence: a device whose owner
// deliberately turned this off must not be moved by a later change of default.
// Absent is handled by the caller, which passes a pointer for the same reason
// ConsolePassword is one.
//
// Out of range is refused rather than clamped. The controller validates first,
// so a bad value reaching here is a bug rather than a user, and silently
// accepting it would hide the bug behind a plausible timeout.
//
// Written only when the content changes, for the eMMC reason above, and to
// every path in consoleDirs for the same reason the password record is: the
// init that reads it is updated by flashing a boot partition, not by the OTA
// that delivered this firmware. Splitting the two records would leave a
// device whose password migrated and whose timeout did not — a state nobody
// would think to look for, presenting as a console that expires when the
// dashboard says it should not.
func WriteConsoleTimeout(minutes int) (changed bool, err error) {
	if minutes < 0 || minutes > ConsoleTimeoutMaxMin {
		return false, fmt.Errorf("console timeout %d is outside 0-%d minutes",
			minutes, ConsoleTimeoutMaxMin)
	}

	// Zero means no timeout and is a CHOICE, so it clears the record rather
	// than storing a zero: init reads an absent record as no timeout, and
	// storing "0" would rely on the parser agreeing, in a second place.
	if minutes == 0 {
		return writeRecord(consoleTimeoutName, nil)
	}
	return writeRecord(consoleTimeoutName, []byte(strconv.Itoa(minutes)))
}
