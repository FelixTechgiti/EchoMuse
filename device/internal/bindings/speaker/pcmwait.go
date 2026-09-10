package speaker

import (
	"log"
	"time"
)

// The wait for Android to give the speaker back.
//
// NO BUILD TAG, deliberately — the same reasoning as internal/outchain and as
// pcmstatus.go beside it. This is a timing loop over two injected functions
// with nothing of ALSA in it, and the property worth pinning is its CADENCE,
// which the host suite can check in milliseconds. pcm_speaker.go carries
// `//go:build server`, so anything left in there is compiled only inside the
// pinned compiler image and cannot be tested at all.

// pcmPollInterval is how often the substream status is re-read.
const pcmPollInterval = 200 * time.Millisecond

// nudgeInterval is how often the holder is asked AGAIN to let go. Every poll
// would spawn fifty processes across the window for a service that takes about
// a second to die and come back; every two seconds gives it four more chances
// and costs four fork/execs on a path that runs once per process start.
const nudgeInterval = 2 * time.Second

// waitFree is the wait loop, with the status read and the stop command
// injected so its cadence can be tested without a device. It reports whether
// the holder let go before the timeout.
//
// The nudge is what makes an OTA RESTART behave like a cold boot. At boot,
// mediaserver is still starting and releases in about 200ms — measured on a
// device. After a restart it is fully up, and with a plug in the jack it takes
// the speaker for itself; `stop media` is issued once before the wait, and by
// the time it has died and come back there is nothing left to ask it again.
//
// The cost of losing that race is the whole device, not the audio. Measured
// 2026-09-10: after two OTAs the process restarted cleanly — the supervisor
// logged `start` two seconds later, both times — and never came back online,
// with the ring not even pulsing orange, because main() initialises the
// speaker BEFORE mDNS, the control client, the buttons and the LEDs. A power
// cycle was the only recovery. Not gating main() on the speaker at all is the
// real fix and is filed separately; this makes the race one we win.
func waitFree(held func() (int, bool), nudge func(),
	timeout, poll, nudgeEvery time.Duration) bool {

	start := time.Now()
	deadline := start.Add(timeout)
	nextNudge := start.Add(nudgeEvery)
	for time.Now().Before(deadline) {
		time.Sleep(poll)
		if _, busy := held(); !busy {
			log.Printf("[speaker] speaker released after %s",
				time.Since(start).Round(time.Millisecond))
			return true
		}
		if nudge != nil && !time.Now().Before(nextNudge) {
			log.Printf("[speaker] still held after %s — asking again",
				time.Since(start).Round(time.Millisecond))
			nudge()
			nextNudge = time.Now().Add(nudgeEvery)
		}
	}
	return false
}

// speakerRetryInterval is how long to wait before trying the ALSA open again
// after a device that would not let go.
//
// Seconds rather than milliseconds because the thing being waited for is an
// Android service being restarted by init, and because nothing is now blocked
// on the answer: the device registers, listens and answers its buttons with no
// speaker at all, so retrying patiently costs a few seconds of silence rather
// than the whole device.
const speakerRetryInterval = 3 * time.Second

// retryOpen calls open until it succeeds or stop is closed, waiting delay
// between attempts. It reports whether the open eventually succeeded.
//
// Injected rather than inlined for waitFree's reason — pcm_speaker.go is
// `//go:build server` and cannot be tested at all, and what is worth pinning
// here is that the loop RETRIES rather than giving up, and that a stop is
// honoured between attempts rather than only after another full delay.
func retryOpen(open func() error, stop <-chan struct{}, delay time.Duration) bool {
	for attempt := 1; ; attempt++ {
		select {
		case <-stop:
			return false
		default:
		}
		err := open()
		if err == nil {
			return true
		}
		log.Printf("[speaker] open attempt %d failed: %v — retrying in %s",
			attempt, err, delay)
		select {
		case <-stop:
			return false
		case <-time.After(delay):
		}
	}
}
