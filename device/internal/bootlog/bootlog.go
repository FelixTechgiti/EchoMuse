// Package bootlog appends the firmware's own decisions to the supervisor log
// on /data, where a power cycle cannot reach them.
//
// # Why not the ordinary log
//
// The firmware logs to stdout, which `start_server.sh` puts in
// `/tmp/server.log` — RAM-backed. So the power cycle somebody performs to
// recover a device that will not come back destroys exactly the lines that
// would explain why. That was written down for OTA failures in 2026-08-01 and
// `start_server.sh` was given this file for it; the firmware never used it.
//
// 2026-09-10 showed what that costs. A device restarted after an OTA, stayed
// alive — the ring pulsed orange, so `main()` was fully up — and never found
// the controller. Four such restarts that day ended in a power cycle after
// 8 to 30 minutes, and every one of them took its own explanation with it.
//
// **And there was no way to ask.** The device's shell is proxied BY THE
// CONTROLLER, so the one situation where the evidence is needed is the one
// situation where the channel to fetch it does not exist. A file on /data is
// not a convenience here; it is the only mechanism that can work.
//
// # The cost this must not impose
//
// This is eMMC that cannot be replaced, on a device expected to run for
// years. Writing per attempt would spend a flash write every few seconds for
// as long as a fault lasts — which is precisely when the fault is longest. So
// callers report at ESCALATING milestones, not on a timer, and the file is
// bounded exactly as start_server.sh bounds it: trimmed BEFORE the append, so
// a loop cannot outrun the limit.
package bootlog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Path is the supervisor log. Shared with start_server.sh and with
// em_api.SUPERVISOR_LOG, which is what the controller fetches — one file, so
// the device's account and the supervisor's account read as one story in
// order, rather than two files somebody has to interleave by hand.
const Path = "/data/local/etc/echomuse/supervisor.log"

// logPath is what Appendf actually writes, so the package's own tests can
// redirect it. Everything outside this package uses Path.
var logPath = Path

// MaxBytes and keepBytes match start_server.sh's SUP_MAX and SUP_KEEP
// exactly. Two programs trim one file, and a firmware that kept more than the
// supervisor does would simply have its extra deleted at the next boot — a
// bound that reads as deliberate and is really the smaller of the two. Half
// rather than all-but-the-last-line so trimming is rare, instead of happening
// on nearly every append to a full file.
const MaxBytes = 64 * 1024
const keepBytes = MaxBytes / 2

var mu sync.Mutex

// procUptime is the kernel's own seconds-since-boot, and reading it is not an
// optimisation over timing ourselves — it is what makes the two writers of
// this file comparable. start_server.sh's `up=` comes from /proc/uptime, so a
// firmware line timed from PROCESS start would count from a different zero
// after every restart, and the supervisor's `start` and our own report of what
// happened next would sit in the same column meaning different things. The
// wall clock cannot rescue that: an Echo has no RTC that survives a power cut
// and boots reading 2010, which in this fault is never corrected because the
// correction arrives from the controller it cannot find.
const procUptime = "/proc/uptime"

// startElapsed is the fallback zero for a host with no /proc/uptime — only the
// test suite, since the target is Linux.
var startElapsed = time.Now()

// uptime is seconds since boot, matching the supervisor's field exactly.
func uptime() int {
	if data, err := os.ReadFile(procUptime); err == nil {
		// "1234.56 789.01" — the first field is what the supervisor reads.
		field, _, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
		if secs, err := strconv.ParseFloat(field, 64); err == nil {
			return int(secs)
		}
	}
	return int(time.Since(startElapsed).Seconds())
}

// Appendf writes one line, trimming the file first if it has grown past
// MaxBytes.
//
// Every failure is swallowed. This is diagnostics for a fault that has already
// happened; a device that cannot write it must still run, and a caller that
// had to handle the error would be a caller that might decide not to log.
//
// The wall clock is included and is NOT to be trusted for ordering: an Echo
// has no RTC that survives a power cut and boots reading 2010 until the
// controller tells it the time — which, in the fault this exists for, it never
// does. `up=` is the reliable field, and it is first for that reason.
//
// The `firmware:` tag is what separates these lines from the supervisor's own
// in a file both write to.
func Appendf(format string, args ...any) {
	// One record is one line, unconditionally. A message carrying a newline
	// — an error string wrapping a command's output, say — would otherwise
	// write continuation lines that do not begin with `up=`, and the trim
	// below would then be free to cut a record in half at a boundary that
	// looks legitimate. Cheaper to make it impossible here than to make
	// every caller remember.
	msg := strings.ReplaceAll(
		strings.TrimSpace(fmt.Sprintf(format, args...)), "\n", " | ")
	line := fmt.Sprintf("up=%ds wall=%s firmware: %s\n",
		uptime(), time.Now().Format("2006-01-02 15:04:05"), msg)

	mu.Lock()
	defer mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	trim(logPath, MaxBytes, keepBytes)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// trim drops the oldest bytes when the file has grown past max, keeping the
// newest keep bytes and starting at the first whole line.
//
// BEFORE the append, never after: a crash loop writing faster than a
// post-append trim can run would grow the file without bound, which is the
// same discipline start_server.sh applies for the same reason.
func trim(path string, max, keep int64) {
	st, err := os.Stat(path)
	if err != nil || st.Size() <= max {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if int64(len(data)) <= keep {
		return
	}
	tail := data[int64(len(data))-keep:]
	// Start at a line boundary, so the trimmed file never opens mid-record.
	if i := bytes.IndexByte(tail, '\n'); i >= 0 && i+1 < len(tail) {
		tail = tail[i+1:]
	}
	_ = os.WriteFile(path, tail, 0o644)
}
