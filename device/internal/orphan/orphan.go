// Package orphan finds and stops a previous instance of one of the endpoint
// subprocesses the firmware supervises.
//
// # Why this exists
//
// librespot and shairport-sync are children of the firmware, and a firmware
// RESTART does not take them with it: the shutdown path exits the process, the
// children are reparented to init, and they keep running — still holding the
// ports their protocol is defined on. shairport-sync listens on TCP 5000 for
// RTSP, so the new instance cannot bind it and exits immediately; the
// supervisor then retries for ever.
//
// Measured on a device 2026-09-10, after an OTA from v2.19.0 to v2.21.0-fx.1:
//
//	1154 /data/local/bin/shairport-sync -a EchoDot  -o stdout   (alive, port 5000)
//	14:44:29 [airplay] shairport-sync exited: exit status 1      (and every minute after)
//
// Three things made it certain rather than likely. `/tmp` is RAM-backed and
// the log still held lines from the previous hour, so the device had not
// rebooted — only the process had restarted. The surviving command line
// carried no `-c`, a flag the firmware only started passing in the version
// that was now running, so that process was older than its supposed parent.
// And port 5000 was listening while our own supervisor was looping, which is
// only possible if the listener is not ours.
//
// It reads as "AirPlay disappears after every update and comes back after a
// power cycle", which is exactly how it was reported, for days, and it is why
// two earlier theories about mDNS were both wrong: the announcement was never
// the problem, the process was never up to make one.
//
// # Why takeover at START, not cleanup at exit
//
// Stopping the children on the way down is worth doing and is NOT sufficient.
// It cannot run on `kill -9`, on a panic, or on the supervisor's own restart
// path, and it does nothing for a device already sitting in the loop — which
// on a fielded fleet is every device that has ever been updated. Taking the
// resource over at start is the same posture the firmware already uses for
// Android's `mediaserver` and `mixer`: whoever holds it is asked to let go,
// every time we start, so one bad exit cannot strand the feature for ever.
//
// # Why /proc rather than pkill
//
// `pkill` is not on FireOS and busybox's applet set varies by SKU, so a shell
// out would be a check that silently cannot run — the failure this tree names
// most often. Reading /proc is the Linux interface, needs nothing installed,
// and matches argv[0] exactly rather than by pattern, so it cannot pick up an
// unrelated process whose command line happens to contain the path.
package orphan

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// DefaultProcRoot is Linux's process table.
const DefaultProcRoot = "/proc"

// termGrace is how long a signalled process is given to exit before it is
// killed outright. These are audio daemons with no state to flush and nothing
// to lose by dying, so this is short — long enough for a normal exit, short
// enough that Start does not stall behind it.
const termGrace = 500 * time.Millisecond

// Find returns the pids whose argv[0] is exactly binary, excluding self.
//
// argv[0] EXACTLY, not a substring: /proc/<pid>/cmdline is NUL-separated, so
// the first field is the executable as invoked, and matching the whole field
// means a process that merely mentions the path — a grep, a shell, our own
// supervisor's log line — can never be selected.
//
// Errors on individual entries are skipped rather than returned: /proc is a
// live directory and a process exiting between the listing and the read is
// ordinary, not a failure. A pid that has gone is a pid we do not need to
// kill.
func Find(procRoot, binary string, self int) []int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self || pid <= 0 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		argv0 := raw
		if i := indexByte(raw, 0); i >= 0 {
			argv0 = raw[:i]
		}
		if string(argv0) == binary {
			pids = append(pids, pid)
		}
	}
	return pids
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// Signal is how a pid is asked, and then told, to go away. Injected so the
// cadence is testable without spawning processes.
type Signal func(pid int, sig syscall.Signal) error

// SysKill is the real implementation.
func SysKill(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }

// Stop signals each pid to terminate and then kills anything still there.
// Returns how many pids it acted on.
//
// SIGTERM first, SIGKILL after termGrace: an orphan holding a listening socket
// releases it either way, but a terminating process closes it cleanly, and
// TIME_WAIT on a killed listener is one more way for the next bind to fail.
// The wait is a single sleep for the whole set rather than one each, because
// they are signalled together and the grace is the same for all of them.
func Stop(pids []int, sig Signal, grace time.Duration, sleep func(time.Duration)) int {
	if len(pids) == 0 {
		return 0
	}
	for _, pid := range pids {
		_ = sig(pid, syscall.SIGTERM)
	}
	sleep(grace)
	for _, pid := range pids {
		// ESRCH here is the good outcome and needs no branch: the process
		// took the SIGTERM and is gone.
		_ = sig(pid, syscall.SIGKILL)
	}
	return len(pids)
}

// Takeover is the whole operation for one binary, against the real system.
// Returns how many stale instances were stopped.
func Takeover(binary string) int {
	pids := Find(DefaultProcRoot, binary, os.Getpid())
	return Stop(pids, SysKill, termGrace, time.Sleep)
}
