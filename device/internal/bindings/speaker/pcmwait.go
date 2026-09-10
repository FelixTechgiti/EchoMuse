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
