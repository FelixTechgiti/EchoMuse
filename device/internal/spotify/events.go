package spotify

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// librespot's player events, and why the device needs them.
//
// # The fault they exist for
//
// **Pausing Spotify left the Echo playing for another six to seven seconds**,
// measured by its owner on 2026-09-12 and reproduced by arithmetic: the music
// plane holds `audioChanDepth` (128) periods of `periodSize` (2048) frames at
// 48kHz, which is 5.46 seconds, and librespot is paced by the pipe filling
// up — so it keeps that buffer FULL rather than merely primed. Add the Linux
// pipe between us and librespot's own buffer and the number lands where it was
// heard.
//
// Nothing flushed it. librespot simply stops writing on a pause, and whatever
// is already queued plays out.
//
// **The asymmetry is the proof**: starting takes 1-2 seconds, because
// `MusicPrimeFor(local)` releases the stream after four periods (171ms).
// Stopping takes six, because that is how much had accumulated behind it. A
// buffer that is cheap to fill and expensive to drain reads as latency in one
// direction only, which is exactly what was reported.
//
// # Why a FIFO and a shell script
//
// `--onevent` is the only channel librespot offers, and `run_program` splits
// the argument on whitespace and calls `Command::new(argv[0])` — there is no
// shell, so the value has to name a program. `/system/bin/sh <script>` is that
// program, and the script is written by us next to the endpoint's other state.
//
// # The read end is opened O_RDWR, and that is not an accident
//
// `internal/airplay`'s metadata reader opens its FIFO `O_RDONLY|O_NONBLOCK`
// and reopens on EOF, which is right there: shairport-sync opens the write end
// itself and holds it.
//
// Here the writer is a SHELL PROCESS THAT LIBRESPOT SPAWNS PER EVENT, and
// opening a FIFO for writing blocks until a reader exists. So a moment with no
// reader does not lose one line — it strands a process, and librespot spawns
// another on the next event. Holding the read end O_RDWR means the kernel
// always sees a reader: writers never block, and the read side never sees the
// EOF that the reopen loop exists to handle. One flag removes both problems.
//
// It is still gated at the call site — `--onevent` is passed only when
// something is listening — for the same reason AirPlay's metadata block is.
// The flag is the belt; the gate is the braces.

// EventPipe and EventScript live beside the endpoint's other state, under the
// directory the rename moved (see internal/devicepaths).
const (
	eventPipeName   = "spotify-events"
	eventScriptName = "spotify-event.sh"
)

// Event is one line from the pipe.
type Event struct {
	Kind   string // librespot's PLAYER_EVENT, verbatim
	Volume uint16 // only meaningful for Kind == "volume_changed"
	HasVol bool
}

// EndsPlayback reports whether this event means the source has stopped
// producing audio and whatever is buffered is now stale.
//
// `end_of_track` is deliberately NOT in the list. It fires at every track
// boundary in normal playback, and flushing there would cut the tail of every
// song to start the next one — turning a latency fix into a gap.
//
// `session_disconnected` is: the phone has gone, so nothing more is coming and
// what is queued belongs to a session that no longer exists.
func (e Event) EndsPlayback() bool {
	switch e.Kind {
	case "paused", "stopped", "session_disconnected":
		return true
	}
	return false
}

// ParseEvent reads one line written by the script. Pure, so the contract with
// the script is testable without a device.
//
// Unknown events parse successfully and simply answer false to everything —
// librespot has twenty of them and adds more between releases, and a parser
// that rejected the ones it did not know would turn every upgrade into a
// silent loss of the ones it did.
func ParseEvent(line string) (Event, bool) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return Event{}, false
	}
	e := Event{Kind: f[0]}
	if len(f) > 1 && f[1] != "" {
		if v, err := strconv.ParseUint(f[1], 10, 16); err == nil {
			e.Volume = uint16(v)
			e.HasVol = true
		}
	}
	return e, true
}

// eventScript is what librespot runs. One line per event, and the volume only
// when there is one — `${VOLUME:-}` rather than `$VOLUME` so an event without
// it writes an empty field instead of the shell complaining under `set -u`.
//
// `>>` rather than `>`: two events can overlap, and truncating a FIFO is
// meaningless anyway.
func eventScript(pipe string) string {
	return "#!/system/bin/sh\n" +
		"# Written by the Revoice firmware. librespot runs this on every player\n" +
		"# event; it exists only to put one line on the FIFO the firmware reads.\n" +
		"printf '%s %s\\n' \"$PLAYER_EVENT\" \"${VOLUME:-}\" >> " + pipe + " 2>/dev/null\n"
}

// writeEventPlumbing creates the FIFO and the script, and returns the value
// for --onevent. Empty on any failure, so a caller that cannot build the
// plumbing simply does not ask librespot to use it.
//
// Same rule as the AirPlay config file: librespot would not refuse to start
// over this, but an --onevent pointing at a script that is not there spawns a
// failing process per event for the life of the endpoint.
func writeEventPlumbing(dir string) (onevent, pipe string) {
	if dir == "" {
		return "", ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[spotify] could not create %s: %v — player events off, so a "+
			"pause will take the buffered music's length to fall silent", dir, err)
		return "", ""
	}
	pipe = filepath.Join(dir, eventPipeName)
	if err := mkfifo(pipe); err != nil {
		log.Printf("[spotify] could not create the event pipe %s: %v — player "+
			"events off", pipe, err)
		return "", ""
	}
	script := filepath.Join(dir, eventScriptName)
	if err := writeFileAtomic(script, eventScript(pipe)); err != nil {
		log.Printf("[spotify] could not write the event script %s: %v — player "+
			"events off", script, err)
		return "", ""
	}
	return "/system/bin/sh " + script, pipe
}

// mkfifo creates the FIFO if it is not already one. An existing FIFO is
// reused: the path is fixed and the endpoint restarts often.
func mkfifo(path string) error {
	if fi, err := os.Stat(path); err == nil {
		if fi.Mode()&os.ModeNamedPipe != 0 {
			return nil
		}
		// Something else is in the way — a regular file from an older
		// firmware, most likely. Replacing it is safe; it has no other reader.
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return syscall.Mkfifo(path, 0o600)
}

// writeFileAtomic writes through a temp file and renames, so librespot can
// never run a half-written script. Same reason the console password and the
// shairport config are written this way.
func writeFileAtomic(path, content string) error {
	tmp := path + ".part"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
