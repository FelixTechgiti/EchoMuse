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
// a second to die and come back; every two seconds gives it four more chances.
const nudgeInterval = 2 * time.Second

// maxNudges bounds how many times ONE wait may ask, and it exists because the
// comment above used to end "on a path that runs once per process start" —
// which stopped being true the moment the open began retrying.
//
// With a plug in the jack, Android's mediaserver holds the speaker for as long
// as the plug is there and no amount of asking changes that. Unbounded nudging
// then means killing an Android system service roughly every 2.6 seconds for
// the life of the process, on a board sharing 512MB with Android — a permanent
// cost paid to lose a race that cannot be won.
//
// Four is what the original ten-second window already spent, so the case this
// was built for — mediaserver restarting after an OTA and letting go within a
// second or two — is untouched.
const maxNudges = 4

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
	nudges := 0
	for time.Now().Before(deadline) {
		time.Sleep(poll)
		if _, busy := held(); !busy {
			log.Printf("[speaker] speaker released after %s",
				time.Since(start).Round(time.Millisecond))
			return true
		}
		if nudge != nil && nudges < maxNudges && !time.Now().Before(nextNudge) {
			log.Printf("[speaker] still held after %s — asking again",
				time.Since(start).Round(time.Millisecond))
			nudge()
			nudges++
			nextNudge = time.Now().Add(nudgeEvery)
		}
	}
	return false
}

// speakerRetryInterval is the FIRST wait before trying the ALSA open again,
// and speakerRetryMax the ceiling it backs off to.
//
// Seconds rather than milliseconds because the thing being waited for is an
// Android service being restarted by init, and because nothing is blocked on
// the answer: the device registers, listens and answers its buttons with no
// speaker at all, so retrying patiently costs silence rather than the device.
//
// The CEILING is what a flat interval was missing. The two cases look the same
// from here and are nothing alike: mediaserver restarting after an OTA lets go
// within seconds, and mediaserver holding the speaker because a plug is in the
// jack never lets go at all. A flat three seconds serves the first and, in the
// second, spends a fork/exec pair and a codec probe every three seconds for
// the life of the process. Backing off to a minute costs the recoverable case
// nothing — it is over long before the delay grows — and turns the
// unrecoverable one from a busy loop into a heartbeat.
const speakerRetryInterval = 3 * time.Second
const speakerRetryMax = 60 * time.Second

// nextDelay doubles a retry delay up to the ceiling.
func nextDelay(d, max time.Duration) time.Duration {
	d *= 2
	if d > max {
		return max
	}
	return d
}

// retryOpen calls open until it succeeds or stop is closed, backing off from
// delay to max between attempts. It reports whether the open eventually
// succeeded.
//
// Injected rather than inlined for waitFree's reason — pcm_speaker.go is
// `//go:build server` and cannot be tested at all, and what is worth pinning
// here is that the loop RETRIES rather than giving up, that it BACKS OFF
// rather than hammering, and that a stop is honoured between attempts rather
// than only after another full delay.
func retryOpen(open func() error, stop <-chan struct{},
	delay, max time.Duration) bool {

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
		delay = nextDelay(delay, max)
	}
}
