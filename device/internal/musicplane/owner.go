// Package musicplane decides who is filling the device's music stream.
//
// The music plane has exactly ONE producer at a time (audio-states §9,
// invariant 7). Until now that was true by construction — the controller's
// 0x04 frames were the only thing that could reach PumpMusic — and it stops
// being true the moment the device speaks a protocol of its own. Sendspin,
// Spotify Connect and AirPlay are each a second producer of the SAME plane,
// not a plane of their own: they carry the same kind of audio by a different
// route, so the duck, music_flush, the saturating mix, the prime gate and the
// underrun accounting all apply to them unmodified.
//
// Summing two of them is noise. Two music sources are not two instruments;
// they are two songs, and the mixer would hand the speaker their sum.
//
// # Home Assistant wins
//
// A request routed through Home Assistant — "play some jazz" spoken to the
// device, a play_media from an automation, a track started from the HA app —
// beats any local session. It is the direct user request, made to THIS
// device, and it is the one an unanswered request would be most confusing
// about. So the controller can take the plane at any time and never has to
// ask.
//
// That is precedence, not a priority ladder. A local source is not parked
// somewhere below HA waiting its turn: it owns the plane outright whenever HA
// is not asking for it. Among themselves local sources are last-claim-wins,
// which is the same shape the speaker ladder already uses when a user command
// during a turn overrides the controller's auto-resume — the newest direct
// request is the one to honour.
//
// # Leaving is not ignoring
//
// This is the rule that makes the whole thing usable, and it is easy to get
// wrong because the wrong version LOOKS like it works. A source that loses
// the plane must not simply stop writing: a Sendspin server still streaming
// to a client that went quiet keeps filling a buffer nobody hears, the
// group's view of the device stays wrong, and the person who started it sees
// a speaker that joined and plays nothing. Same for AirPlay and for Spotify
// Connect, where the phone keeps showing the Echo as the active device.
//
// So every source registers a Leave callback and this package calls it,
// exactly once, on the transition that takes the plane away. What the source
// does with it is protocol-specific — Sendspin sends `client/goodbye` with a
// reason — but doing nothing is not one of the options.
//
// # No rejoin
//
// When HA-routed music ends the plane is simply free; nothing is handed back.
// A silent rejoin puts audio in the room nobody asked for at that moment, and
// whoever started the group can start it again. The cost of being wrong this
// way is one extra tap, which is the cheap direction.
//
// Untagged and dependency-free so it is testable on the host: this is a state
// machine, and every one of its transitions is a thing that would otherwise
// be discovered on someone's hardware.
package musicplane

import "sync"

// Source identifies a producer of the music plane.
type Source int

const (
	// None means the plane is free.
	None Source = iota
	// Controller is the 0x04 stream from the controller — anything routed
	// through Home Assistant. It has precedence over every local source.
	Controller
	// Sendspin is a Music Assistant group joined directly by the device.
	Sendspin
	// Spotify is a Spotify Connect session on the device.
	Spotify
	// AirPlay is an AirPlay session on the device.
	AirPlay
)

func (s Source) String() string {
	switch s {
	case None:
		return "none"
	case Controller:
		return "controller"
	case Sendspin:
		return "sendspin"
	case Spotify:
		return "spotify"
	case AirPlay:
		return "airplay"
	}
	return "unknown"
}

// local reports whether a source is a protocol spoken by the device itself,
// as opposed to the controller's stream. Written as an explicit switch rather
// than `s != Controller`: None is neither, and a future source added to the
// const block should have to be classified deliberately.
func (s Source) local() bool {
	switch s {
	case Sendspin, Spotify, AirPlay:
		return true
	}
	return false
}

// Reason is why a source is being asked to give up the plane. It is passed
// through to the source's own protocol — Sendspin puts it in `client/goodbye`
// — so it is a small closed set rather than free text.
type Reason string

const (
	// ReasonPreempted: something else took the plane.
	ReasonPreempted Reason = "preempted"
	// ReasonStopped: the device is shutting the plane down (speaker gone,
	// process exiting).
	ReasonStopped Reason = "stopped"
)

// Owner tracks the current producer and evicts the previous one cleanly.
//
// The zero value is usable and means the plane is free.
type Owner struct {
	mu    sync.Mutex
	owner Source
	leave map[Source]func(Reason)
}

// Register records how to make a source leave its session cleanly. It must be
// called before the source ever claims, and it is idempotent — a reconnecting
// client re-registering its own callback replaces it rather than stacking.
//
// A nil callback is accepted and means the source has nothing to tear down.
// It is NOT the same as not registering: an unregistered source that claims
// is a programming error this package cannot see, since the claim itself is
// indistinguishable.
func (o *Owner) Register(src Source, leave func(Reason)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.leave == nil {
		o.leave = make(map[Source]func(Reason))
	}
	o.leave[src] = leave
}

// Claim asks for the music plane. It reports whether the caller may write.
//
// The eviction callback of the source being displaced runs AFTER the
// ownership change is committed and OUTSIDE the lock. Outside because it
// performs protocol I/O — a `client/goodbye` on a socket that may be stalled
// — and holding the lock across that would block every other claim behind a
// network write. After, because a callback that raced back in and re-claimed
// would otherwise be overwritten by the claim that evicted it.
func (o *Owner) Claim(src Source) bool {
	o.mu.Lock()
	if src == None {
		o.mu.Unlock()
		return false
	}
	prev := o.owner
	if prev == src {
		o.mu.Unlock()
		return true // already ours; claiming again is not an event
	}
	// The controller takes the plane from anyone. A local source takes it
	// from another local source (last direct request wins) and from nobody
	// else — while HA is streaming, the request that arrived through HA is
	// the one being honoured.
	if src.local() && prev == Controller {
		o.mu.Unlock()
		return false
	}
	o.owner = src
	evict := o.leave[prev]
	o.mu.Unlock()

	if prev != None && evict != nil {
		evict(ReasonPreempted)
	}
	return true
}

// Release gives up the plane, if the caller still holds it.
//
// Deliberately a no-op for a source that has already been preempted: it was
// told to leave, it is tearing down, and its teardown must not evict whoever
// took the plane from it. That ordering is not theoretical — eviction is
// asynchronous by construction, so the new owner is always in place first.
//
// Nothing is handed back. See the package comment: no rejoin.
func (o *Owner) Release(src Source) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.owner == src {
		o.owner = None
	}
}

// Owner reports the current producer.
func (o *Owner) Owner() Source {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.owner
}

// MayWrite reports whether src is currently allowed to fill the plane.
//
// Every write path calls this, so a source that lost the plane between
// claiming and its next period stops on the period boundary rather than
// carrying on until its own teardown catches up.
func (o *Owner) MayWrite(src Source) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.owner == src
}

// Shutdown releases the plane and tells every registered source to leave,
// once, with ReasonStopped. For the speaker going away or the process
// exiting: a Sendspin group that never hears a goodbye holds the device as a
// member until its own timeout.
func (o *Owner) Shutdown() {
	o.mu.Lock()
	o.owner = None
	cbs := make([]func(Reason), 0, len(o.leave))
	for _, cb := range o.leave {
		if cb != nil {
			cbs = append(cbs, cb)
		}
	}
	o.mu.Unlock()

	for _, cb := range cbs {
		cb(ReasonStopped)
	}
}

// Scoped is one source's view of the arbiter.
//
// It exists so a producer can be handed something that only speaks for
// itself. Passing the whole Owner around means every call site names its own
// Source, and the failure mode of naming the wrong one is a producer that
// releases somebody else's claim — silent, and visible only as music that
// stops for no reason.
//
// A value type, so it can be copied freely; the arbiter behind it is shared.
type Scoped struct {
	owner *Owner
	src   Source
}

// For returns a source's view.
func (o *Owner) For(src Source) Scoped { return Scoped{owner: o, src: src} }

// Source reports which producer this view speaks for.
func (s Scoped) Source() Source { return s.src }

// Claim asks for the plane on this source's behalf.
func (s Scoped) Claim() bool {
	if s.owner == nil {
		return false
	}
	return s.owner.Claim(s.src)
}

// Release gives it up, if this source still holds it.
func (s Scoped) Release() {
	if s.owner == nil {
		return
	}
	s.owner.Release(s.src)
}

// MayWrite reports whether this source may fill the plane right now.
//
// A zero Scoped answers false rather than panicking: it is what a producer
// constructed before the arbiter would hold, and refusing to write is the
// safe answer to "I do not know who owns this".
func (s Scoped) MayWrite() bool {
	if s.owner == nil {
		return false
	}
	return s.owner.MayWrite(s.src)
}
