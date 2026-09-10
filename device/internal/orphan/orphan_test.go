package orphan

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"testing"
	"time"
)

// The bug: a firmware restart leaves librespot and shairport-sync running as
// orphans, still holding the ports their protocols are defined on, so every
// new instance exits immediately and the supervisor loops for ever. Measured
// on a device 2026-09-10 — AirPlay invisible after every OTA and back after a
// power cycle, for days, with two wrong mDNS theories in between.
//
// What is pinned here is argv[0] matching and the signal cadence. Both fail
// silently if they go: too loose a match kills something unrelated, and no
// SIGKILL leaves the port held by a process that ignored the SIGTERM.

func procfs(t *testing.T, entries map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, cmdline := range entries {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"),
			[]byte(cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const bin = "/data/local/bin/shairport-sync"

func TestTheOrphanIsFoundByItsArgv0(t *testing.T) {
	// The real observation: `-a EchoDot  -o stdout`, no -c, which is what
	// made it identifiable as older than the firmware running it.
	root := procfs(t, map[string]string{
		"1154": bin + "\x00-a\x00EchoDot \x00-o\x00stdout\x00",
	})
	if got := Find(root, bin, 1); len(got) != 1 || got[0] != 1154 {
		t.Fatalf("Find = %v, want [1154]", got)
	}
}

func TestAProcessThatMerelyMENTIONSThePathIsNotKilled(t *testing.T) {
	// A grep for the binary, a shell running it later, our own log line: all
	// contain the path and none of them is holding port 5000. Matching a
	// substring rather than argv[0] would kill the user's own shell.
	root := procfs(t, map[string]string{
		"4369": "busybox\x00grep\x00" + bin + "\x00",
		"4370": "/system/bin/sh\x00-c\x00" + bin + " --help\x00",
		"4371": bin + "-old\x00",
	})
	if got := Find(root, bin, 1); len(got) != 0 {
		t.Fatalf("Find = %v, want none of these", got)
	}
}

func TestOurOwnProcessIsNeverSelected(t *testing.T) {
	root := procfs(t, map[string]string{"77": bin + "\x00"})
	if got := Find(root, bin, 77); len(got) != 0 {
		t.Fatalf("Find = %v — it selected the caller", got)
	}
}

func TestSeveralOrphansAreAllFound(t *testing.T) {
	// Two OTAs without a reboot leaves two. The loop is per restart, not per
	// device, so the count is unbounded in principle.
	root := procfs(t, map[string]string{
		"100": bin + "\x00-o\x00stdout\x00",
		"200": bin + "\x00-o\x00stdout\x00",
		"300": "/data/local/bin/librespot\x00",
	})
	got := Find(root, bin, 1)
	sort.Ints(got)
	if len(got) != 2 || got[0] != 100 || got[1] != 200 {
		t.Fatalf("Find = %v, want [100 200]", got)
	}
}

func TestAnUnreadableProcEntryIsSkippedNotFatal(t *testing.T) {
	// /proc is live: a process exiting between the listing and the read is
	// ordinary. A pid that has gone is one we do not need to kill.
	root := procfs(t, map[string]string{"100": bin + "\x00"})
	if err := os.MkdirAll(filepath.Join(root, "999"), 0o755); err != nil {
		t.Fatal(err) // no cmdline file at all
	}
	if err := os.WriteFile(filepath.Join(root, "notapid"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Find(root, bin, 1); len(got) != 1 || got[0] != 100 {
		t.Fatalf("Find = %v, want [100]", got)
	}
}

func TestAMissingProcRootReportsNothingRatherThanPanicking(t *testing.T) {
	if got := Find(filepath.Join(t.TempDir(), "absent"), bin, 1); got != nil {
		t.Fatalf("Find = %v, want nil", got)
	}
}

// ─── the signal cadence ──────────────────────────────────────────────────────

func TestTermFirstThenKill(t *testing.T) {
	// SIGTERM alone is not enough: a process that ignores it keeps the
	// listening socket and the next bind fails exactly as before. SIGKILL
	// alone leaves the socket in TIME_WAIT, which is one more way to fail.
	var seen []string
	sig := func(pid int, s syscall.Signal) error {
		seen = append(seen, s.String())
		return nil
	}
	slept := time.Duration(0)
	n := Stop([]int{1154}, sig, 5*time.Millisecond, func(d time.Duration) { slept = d })
	if n != 1 {
		t.Fatalf("Stop reported %d", n)
	}
	if len(seen) != 2 || seen[0] != syscall.SIGTERM.String() ||
		seen[1] != syscall.SIGKILL.String() {
		t.Fatalf("signals = %v, want SIGTERM then SIGKILL", seen)
	}
	if slept != 5*time.Millisecond {
		t.Fatalf("grace = %v, want the one passed in", slept)
	}
}

func TestNothingToStopCostsNoWait(t *testing.T) {
	// The ordinary path on every start. A device with no orphan must not pay
	// the grace period before its endpoint can come up.
	waited := false
	n := Stop(nil, func(int, syscall.Signal) error { return nil },
		time.Hour, func(time.Duration) { waited = true })
	if n != 0 || waited {
		t.Fatalf("Stop(nil) = %d, waited=%v", n, waited)
	}
}

func TestEveryOrphanIsSignalledBeforeAnyIsWaitedOn(t *testing.T) {
	// One sleep for the whole set, not one each: they are signalled together
	// and the grace is the same for all of them, so three orphans must not
	// cost three graces on a path that runs at every start.
	sleeps := 0
	Stop([]int{1, 2, 3}, func(int, syscall.Signal) error { return nil },
		time.Millisecond, func(time.Duration) { sleeps++ })
	if sleeps != 1 {
		t.Fatalf("slept %d times for 3 orphans, want 1", sleeps)
	}
}
