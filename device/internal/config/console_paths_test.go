package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A record of the shape init parses: iterations, salt, hash. The values are
// synthetic — nothing here hashes anything, because what is under test is
// WHERE the record lands, not what it contains.
const testRecord = "100000:0f1e2d3c4b5a6978:" +
	"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// The suite below asserts things about "every path". If there were only ever
// one, all of it would pass while proving nothing — so the count is asserted
// first, against the real configuration rather than a test override.
//
// This is the test that fails the day somebody decides the legacy directory
// can go. That is a fine decision to make, but it has a precondition — no
// device in the field runs an init older than the rename — and it should be
// made deliberately rather than by deleting a line.
func TestConsoleDirsCarriesTheLegacyDirectory(t *testing.T) {
	if len(consoleDirs) < 2 {
		t.Fatalf("consoleDirs is %v; the legacy directory is what the "+
			"compatibility depends on", consoleDirs)
	}
	if !strings.Contains(consoleDirs[len(consoleDirs)-1], "echomuse") {
		t.Fatalf("consoleDirs is %v; the pre-rename directory is missing", consoleDirs)
	}
}

func TestWriteConsolePasswordReachesEveryPath(t *testing.T) {
	withTempDirs(t)

	changed, err := WriteConsolePassword(testRecord)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !changed {
		t.Fatal("writing a new record must report a change")
	}
	// An init older than the rename opens the legacy path and nothing else.
	// A record that only reached the current path is a password the dashboard
	// reports as set and the console has never heard of.
	for _, path := range recordPaths(consolePasswordName) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got := strings.TrimSpace(string(b)); got != testRecord {
			t.Fatalf("%s holds %q, want %q", path, got, testRecord)
		}
	}
}

// This is the assertion whose absence was the bug in #94.
//
// Clearing is not a symmetric case of setting. Somebody clears a console
// password because it belongs to a PREVIOUS OWNER and they cannot type it —
// emOS's init puts that record in front of the console, so a clear that
// silently misses a path leaves the new owner locked out by exactly the
// password the operation exists to remove, while the dashboard reports
// success.
func TestClearingTheConsolePasswordRemovesEveryPath(t *testing.T) {
	withTempDirs(t)

	if _, err := WriteConsolePassword(testRecord); err != nil {
		t.Fatalf("seed: %v", err)
	}
	changed, err := WriteConsolePassword("")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !changed {
		t.Fatal("clearing an existing record must report a change")
	}
	for _, path := range recordPaths(consolePasswordName) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived the clear: %v", path, err)
		}
	}

	// Clearing what is already clear is the ordinary path — the config push
	// repeats every setting on every reconnect, and most devices have no
	// console password — so it must be quiet rather than merely harmless.
	changed, err = WriteConsolePassword("")
	if err != nil {
		t.Fatalf("clear again: %v", err)
	}
	if changed {
		t.Fatal("clearing an absent record must not report a change")
	}
}

// The change detection has to consider a write needed when EITHER path
// disagrees, not just the current one. A device that took this firmware
// having already had a record written by an older build has the current path
// correct and the legacy path stale or missing; comparing against one file
// would report "no change" and leave the init that is actually running
// reading the old record for ever.
func TestWriteConsolePasswordRepairsAPathThatDisagrees(t *testing.T) {
	withTempDirs(t)

	if _, err := WriteConsolePassword(testRecord); err != nil {
		t.Fatalf("seed: %v", err)
	}
	paths := recordPaths(consolePasswordName)
	legacy := paths[len(paths)-1]
	if err := os.Remove(legacy); err != nil {
		t.Fatalf("remove legacy copy: %v", err)
	}

	changed, err := WriteConsolePassword(testRecord)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !changed {
		t.Fatal("a missing legacy copy must count as a change")
	}
	b, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("legacy copy was not restored: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != testRecord {
		t.Fatalf("legacy copy holds %q, want %q", got, testRecord)
	}
}

func TestWriteConsolePasswordIsIdempotentAcrossEveryPath(t *testing.T) {
	withTempDirs(t)

	if _, err := WriteConsolePassword(testRecord); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// The push arrives on every reconnect and this device runs for years on
	// eMMC that cannot be replaced. Writing both paths must not turn one
	// wasted flash write per reconnect into two.
	changed, err := WriteConsolePassword(testRecord)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if changed {
		t.Fatal("rewriting the same record must not report a change")
	}
}

func TestConsoleTimeoutReachesAndClearsEveryPath(t *testing.T) {
	withTempDirs(t)

	if _, err := WriteConsoleTimeout(15); err != nil {
		t.Fatalf("write: %v", err)
	}
	// MINUTES on every path. init multiplies to seconds at its one point of
	// use, so a 900 here would be a factor-of-sixty bug that presents as a
	// console which never times out.
	for _, path := range recordPaths(consoleTimeoutName) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got := strings.TrimSpace(string(b)); got != "15" {
			t.Fatalf("%s holds %q, want %q", path, got, "15")
		}
	}

	if _, err := WriteConsoleTimeout(0); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for _, path := range recordPaths(consoleTimeoutName) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived the clear: %v", path, err)
		}
	}
}

// A temp file left behind on any path is a record init could read half
// written, which for the password parses as unusable and is read as NO
// password — the console open while the dashboard says it is not.
func TestNoTempFileSurvivesOnAnyPath(t *testing.T) {
	withTempDirs(t)

	if _, err := WriteConsolePassword(testRecord); err != nil {
		t.Fatalf("password: %v", err)
	}
	if _, err := WriteConsoleTimeout(5); err != nil {
		t.Fatalf("timeout: %v", err)
	}
	for _, name := range []string{consolePasswordName, consoleTimeoutName} {
		for _, path := range recordPaths(name) {
			if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
				t.Fatalf("%s survived a successful write", filepath.Base(path)+".tmp")
			}
		}
	}
}
