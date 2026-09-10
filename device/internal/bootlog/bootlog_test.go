package bootlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func redirect(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sub", "supervisor.log")
	old := logPath
	logPath = p
	t.Cleanup(func() { logPath = old })
	return p
}

// The line has to be readable beside the supervisor's own, in the same file,
// with the same leading field — and it has to say which of the two wrote it.
func TestLineShapeMatchesTheSupervisor(t *testing.T) {
	p := redirect(t)
	Appendf("no controller for %s", "3m0s")

	out, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("nothing written: %v", err)
	}
	line := string(out)
	if !strings.HasPrefix(line, "up=") {
		t.Fatalf("line does not lead with the uptime the supervisor leads with: %q", line)
	}
	if !strings.Contains(line, " wall=") {
		t.Fatalf("no wall clock hint: %q", line)
	}
	if !strings.Contains(line, "firmware: no controller for 3m0s") {
		t.Fatalf("message missing or untagged: %q", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("line not terminated, the next append would join it: %q", line)
	}
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("one call wrote more than one line: %q", line)
	}
}

// Appendf is called from a fault path on a device with no operator. It must
// never be able to fail the caller — and the directory not existing is the
// ordinary case on a fresh device, not an error.
func TestUnwritablePathIsSilent(t *testing.T) {
	old := logPath
	logPath = "/proc/definitely/not/writable/supervisor.log"
	t.Cleanup(func() { logPath = old })
	Appendf("this must not panic or block")
}

// The bound is what keeps a crash loop from filling /data on a device with no
// operator, and the trim happens BEFORE the append for exactly that reason:
// a post-append trim can be outrun by a loop writing faster than it runs.
func TestFileStaysBounded(t *testing.T) {
	p := redirect(t)
	// ~140 bytes a line, so this is several times the cap.
	for i := 0; i < 2000; i++ {
		Appendf("controller search still failing, round %d, padding %s", i, strings.Repeat("x", 100))
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > MaxBytes {
		t.Fatalf("file grew to %d bytes, past the %d cap", st.Size(), MaxBytes)
	}
	out, _ := os.ReadFile(p)
	// The NEWEST lines are the ones worth keeping: the fault being diagnosed
	// is whatever the device was doing most recently.
	if !strings.Contains(string(out), "round 1999") {
		t.Fatal("the newest line did not survive the trim")
	}
	if strings.Contains(string(out), "round 0,") {
		t.Fatal("the oldest line survived — nothing was trimmed")
	}
}

// A trimmed file must never open mid-record: a half line at the top reads as
// corruption to whoever opens it, and to the controller's own fetch.
func TestTrimStartsAtALineBoundary(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "supervisor.log")

	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("up=1s wall=2026-09-10 00:00:00 firmware: padding line to be trimmed away\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	trim(p, MaxBytes, keepBytes)

	out, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(out)) > keepBytes {
		t.Fatalf("kept %d bytes, want at most %d", len(out), keepBytes)
	}
	if !strings.HasPrefix(string(out), "up=") {
		t.Fatalf("trimmed file opens mid-record: %q", string(out)[:40])
	}
}

// Trimming on every append would spend a read and a full rewrite of 64KB per
// line on a device whose flash cannot be replaced. A file under the cap must
// be left entirely alone.
func TestTrimLeavesASmallFileAlone(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "supervisor.log")
	const content = "up=1s wall=2026-09-10 00:00:00 firmware: one line\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)
	trim(p, MaxBytes, keepBytes)
	out, _ := os.ReadFile(p)
	if string(out) != content {
		t.Fatalf("a file under the cap was rewritten: %q", string(out))
	}
	after, _ := os.Stat(p)
	if before.ModTime() != after.ModTime() {
		t.Fatal("a file under the cap was touched — that is a flash write per line")
	}
}

// The uptime is the field the supervisor and the firmware share, so it has to
// come from the same source: the kernel's, not the process's.
func TestUptimeReadsTheKernel(t *testing.T) {
	if _, err := os.Stat(procUptime); err != nil {
		t.Skip("no /proc/uptime on this host")
	}
	if uptime() <= 0 {
		t.Fatalf("uptime() = %d, want the host's seconds since boot", uptime())
	}
}

// One record is one line. A caller passing an error that wraps a command's
// output would otherwise write continuations that do not begin with `up=`,
// and the trim is then free to cut a record in half at a boundary that looks
// legitimate.
func TestAMultilineMessageStaysOneRecord(t *testing.T) {
	p := redirect(t)
	Appendf("open failed: %v", "cannot open pcm23p\nheld by mediaserver")

	out, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), "\n") != 1 {
		t.Fatalf("one call wrote %d lines: %q", strings.Count(string(out), "\n"), string(out))
	}
	if !strings.Contains(string(out), "held by mediaserver") {
		t.Fatalf("the second half of the message was lost: %q", string(out))
	}
}
