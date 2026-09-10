package airplay

import (
	"log"
	"os"
	"syscall"
	"time"
)

// Reading shairport-sync's metadata off a FIFO, which has one trap that
// matters and one that would be worse.
//
// # Opening a FIFO for reading BLOCKS until a writer appears
//
// That is POSIX, not a shairport quirk: `open(fifo, O_RDONLY)` parks until
// somebody opens the write end. Doing it on the goroutine that starts the
// endpoint would hang the whole start path until a phone connected — which
// is to say, until after the thing that needed starting had started.
//
// O_NONBLOCK on the open fixes exactly that and nothing else: it returns
// immediately with no writer, and reads then return EOF rather than
// blocking. So the loop below reopens rather than spinning on a dead file
// descriptor.
//
// # A pipe with no reader eventually blocks the WRITER
//
// The worse trap, and it is why this exists at all rather than a config
// block on its own. A FIFO holds 64KB by default; once full, shairport-sync
// blocks trying to write metadata — and the process blocked is the one
// decoding audio. So the pipe is created and drained by us, or it is not
// asked for: `metadataPipe()` returns "" when nothing is listening, and no
// metadata block is written at all.

// pipeReopenDelay is how long to wait before reopening a FIFO that returned
// EOF, i.e. that currently has no writer.
//
// A second, because the thing being waited for is a phone deciding to play
// something — an event measured in minutes or hours. A tight loop here would
// spend a syscall pair every iteration for the entire time an Echo sits idle,
// which is nearly all of it.
const pipeReopenDelay = time.Second

// readMetadataPipe creates the FIFO if needed and reads it for the life of
// the stop channel, reporting AirPlay volume changes.
//
// Every failure is logged once and retried, never fatal. This is a
// convenience on top of a receiver that works without it: an AirPlay endpoint
// that refused to start because a pipe could not be made would be a strictly
// worse device than one whose volume slider does nothing.
func readMetadataPipe(path string, stop <-chan struct{}, onVolume func(db float64)) {
	if err := ensureFIFO(path); err != nil {
		log.Printf("[airplay] no metadata pipe at %s: %v — the AirPlay volume "+
			"slider will not move this device", path, err)
		return
	}

	for {
		select {
		case <-stop:
			return
		default:
		}

		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, os.ModeNamedPipe)
		if err != nil {
			log.Printf("[airplay] cannot open metadata pipe: %v", err)
			if !sleepOrStop(stop, pipeReopenDelay) {
				return
			}
			continue
		}

		// Blocking reads from here on. The non-blocking OPEN is what avoids
		// parking on a pipe with no writer; a non-blocking READ would turn
		// this into a busy loop burning a core while a track plays.
		if err := clearNonblock(f); err != nil {
			log.Printf("[airplay] metadata pipe stayed non-blocking: %v", err)
		}

		VolumeFromMetadata(f, onVolume)
		f.Close()

		// EOF means the writer let go — shairport-sync restarted, or no
		// session is in progress. Both are ordinary, so this is not logged.
		if !sleepOrStop(stop, pipeReopenDelay) {
			return
		}
	}
}

// ensureFIFO makes the path a named pipe, or reports why it cannot be.
//
// An existing FIFO is left alone: recreating it would break a writer already
// attached to it, which after a firmware restart is exactly the shairport-sync
// still running from before. Anything else at that path is replaced — a
// regular file there would accept writes for ever and read back as an
// ever-growing log nobody drains.
func ensureFIFO(path string) error {
	if st, err := os.Stat(path); err == nil {
		if st.Mode()&os.ModeNamedPipe != 0 {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return syscall.Mkfifo(path, 0o600)
}

// clearNonblock puts the descriptor back into blocking mode after the
// non-blocking open.
func clearNonblock(f *os.File) error {
	return syscall.SetNonblock(int(f.Fd()), false)
}

// sleepOrStop waits, and reports false if it was told to stop instead.
func sleepOrStop(stop <-chan struct{}, d time.Duration) bool {
	select {
	case <-stop:
		return false
	case <-time.After(d):
		return true
	}
}
