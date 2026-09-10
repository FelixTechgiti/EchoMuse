package musicplane

import (
	"sync"
	"time"
)

// DefaultIdle is how long a pipe-fed source may go silent before it gives the
// plane back.
//
// Audio arrives from these sources in chunks of roughly 85ms, so this is
// twenty missed reads — far outside ordinary jitter, and short enough that a
// stopped stream is reported promptly. The controller smooths what is left:
// its own hold-off (audioHoldoffMs, 5s) is what keeps a gap between tracks
// from switching somebody's amplifier, so this value does not have to.
const DefaultIdle = 2 * time.Second

// IdleClaim owns the plane only while audio is actually flowing.
//
// It exists because the two pipe-fed endpoints — librespot and shairport-sync
// — are DAEMONS. They run continuously so the device stays in the Spotify and
// AirPlay pickers, and when the phone disconnects they simply stop writing to
// their pipe: the process does not exit, the read blocks, and a plane released
// on process exit is released when the device reboots.
//
// Nothing depended on that until Home Assistant did. The arbiter was only ever
// asked "may I write", and a stale owner answers that correctly — the
// controller preempts a local source at will, so HA-routed music was never
// blocked by it. What a stale owner cannot answer correctly is "is this Echo
// making a sound", which is the question behind the Audio entity, and it
// answered `airplay` for minutes after the session was disconnected.
//
// So the claim expires. Feed is called with every chunk of audio and holds it
// open; silence lets it lapse, and the next chunk claims again.
// Plane is one source's view of the arbiter, as the producer packages already
// declare it for themselves (airplay.PlaneOwner, spotify.PlaneOwner). Taken as
// an interface so those packages keep their own fake in tests, and so this
// works with a Scoped without either side knowing about the other's shape.
type Plane interface {
	Claim() bool
	Release()
	MayWrite() bool
}

type IdleClaim struct {
	scoped Plane
	idle   time.Duration

	mu    sync.Mutex
	held  bool
	timer *time.Timer
}

// NewIdleClaim wraps one source's view of the arbiter. A zero or negative idle
// means DefaultIdle rather than "expire immediately", because the second is
// never what a caller means and would release the plane between two chunks of
// a stream that is playing perfectly.
func NewIdleClaim(scoped Plane, idle time.Duration) *IdleClaim {
	if idle <= 0 {
		idle = DefaultIdle
	}
	return &IdleClaim{scoped: scoped, idle: idle}
}

// Feed reports whether this source may write the audio it is holding, taking
// the plane if it does not already have it and postponing the expiry.
//
// A refused claim is an ordinary answer, not an error: the controller owns the
// plane whenever Home Assistant is playing, and a local source waits rather
// than fighting it. The caller drops the chunk and asks again with the next
// one, which is what makes the handover instant when HA finishes.
func (i *IdleClaim) Feed() bool {
	i.mu.Lock()
	if !i.held {
		// Claim under our own lock but not the arbiter's — Claim runs the
		// change observer and the eviction callback outside it.
		if !i.scoped.Claim() {
			i.mu.Unlock()
			return false
		}
		i.held = true
	}
	if i.timer == nil {
		i.timer = time.AfterFunc(i.idle, i.expire)
	} else {
		i.timer.Reset(i.idle)
	}
	i.mu.Unlock()

	// Asked of the arbiter rather than remembered: something may have taken
	// the plane between chunks, and a source that kept writing on the
	// strength of its own bookkeeping is the second producer this whole
	// package exists to prevent.
	if i.scoped.MayWrite() {
		return true
	}
	i.mu.Lock()
	i.held = false
	i.mu.Unlock()
	return false
}

// Stop gives the plane back now — for a producer shutting down, where waiting
// out the idle period would leave the source reported as playing after its
// process is gone.
func (i *IdleClaim) Stop() {
	i.mu.Lock()
	if i.timer != nil {
		i.timer.Stop()
		i.timer = nil
	}
	was := i.held
	i.held = false
	i.mu.Unlock()

	if was {
		i.scoped.Release()
	}
}

// expire is the timer's callback: the source went quiet without saying so.
//
// The release happens OUTSIDE the lock for Claim's reason — it runs the change
// observer, and holding a lock across anything that can stall would block the
// next chunk of audio behind it.
func (i *IdleClaim) expire() {
	i.mu.Lock()
	if !i.held {
		i.mu.Unlock()
		return
	}
	i.held = false
	i.mu.Unlock()
	i.scoped.Release()
}
