package spotify

import (
	"bufio"
	"log"
	"os"
	"time"
)

// readEventPipe reads librespot's player events until stop is closed.
//
// Opened O_RDWR for the reason in events.go: the writer is a shell process
// librespot spawns per event, and a FIFO with no reader blocks it on open.
// Holding a write end ourselves means the kernel always sees one, so a writer
// never parks and the reader never sees EOF.
//
// Every failure is logged once and the loop ends. This is an improvement on
// top of an endpoint that works without it — a device with no event pipe plays
// Spotify perfectly and merely takes the buffer's length to fall silent, which
// is what every device did before this existed.
func readEventPipe(path string, stop <-chan struct{}, on func(Event)) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		log.Printf("[spotify] could not open the event pipe %s: %v — a pause "+
			"will take the buffered music's length to fall silent", path, err)
		return
	}
	defer f.Close()

	// Closing the file is what unblocks the Read below, so the watcher has to
	// own that rather than the reader loop noticing a flag.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			f.Close()
		case <-done:
		}
	}()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		select {
		case <-stop:
			return
		default:
		}
		e, ok := ParseEvent(sc.Text())
		if !ok {
			continue
		}
		on(e)
	}
}

// eventReaderRetry is how long to wait before reopening the pipe if the reader
// returned. Long, because returning at all means something is wrong with the
// path rather than with this moment, and the endpoint works without it.
const eventReaderRetry = 30 * time.Second

// runEventReader keeps a reader alive for the life of stop.
func runEventReader(path string, stop <-chan struct{}, on func(Event)) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		readEventPipe(path, stop, on)
		select {
		case <-stop:
			return
		case <-time.After(eventReaderRetry):
		}
	}
}
