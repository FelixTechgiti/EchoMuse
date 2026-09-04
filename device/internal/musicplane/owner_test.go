package musicplane

import (
	"sync"
	"testing"
)

// recorder captures the leave callbacks a source received.
type recorder struct {
	mu      sync.Mutex
	reasons []Reason
}

func (r *recorder) cb(why Reason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reasons = append(r.reasons, why)
}

func (r *recorder) got() []Reason {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Reason(nil), r.reasons...)
}

func TestTheZeroValueIsAFreePlane(t *testing.T) {
	var o Owner
	if o.Owner() != None {
		t.Fatalf("owner = %v, want none", o.Owner())
	}
	if o.MayWrite(Controller) {
		t.Fatal("nobody may write a plane nobody claimed")
	}
}

func TestAClaimOnAFreePlaneSucceedsAndEvictsNobody(t *testing.T) {
	var o Owner
	var ss recorder
	o.Register(Sendspin, ss.cb)

	if !o.Claim(Sendspin) {
		t.Fatal("claim on a free plane refused")
	}
	if !o.MayWrite(Sendspin) {
		t.Fatal("owner may not write")
	}
	if len(ss.got()) != 0 {
		t.Fatalf("evicted somebody: %v", ss.got())
	}
}

func TestHomeAssistantTakesThePlaneFromASendspinSession(t *testing.T) {
	// S3. "Play some jazz" spoken to the device runs through HA and arrives
	// on 0x04; the group was started from the Music Assistant app. Both are
	// legitimate, both are Music Assistant, and summing them is noise.
	var o Owner
	var ss recorder
	o.Register(Sendspin, ss.cb)
	o.Claim(Sendspin)

	if !o.Claim(Controller) {
		t.Fatal("the controller was refused the music plane")
	}
	if o.Owner() != Controller {
		t.Fatalf("owner = %v, want controller", o.Owner())
	}
	if o.MayWrite(Sendspin) {
		t.Fatal("a preempted source may still write")
	}
	if want := []Reason{ReasonPreempted}; len(ss.got()) != 1 || ss.got()[0] != want[0] {
		t.Fatalf("leave callbacks = %v, want exactly one %q", ss.got(), ReasonPreempted)
	}
}

func TestLeavingIsNotIgnoring(t *testing.T) {
	// The failure this pins is the one that looks like it works: a source
	// that loses the plane and merely stops writing leaves the server
	// streaming into nothing and the group showing a member that plays
	// silence. The eviction callback is the only thing that ends the session.
	var o Owner
	var ss recorder
	o.Register(Sendspin, ss.cb)
	o.Claim(Sendspin)
	o.Claim(Controller)

	if got := ss.got(); len(got) == 0 {
		t.Fatal("the displaced source was never told to leave")
	}
}

func TestALocalSourceDoesNotInterruptHomeAssistant(t *testing.T) {
	var o Owner
	var ctl recorder
	o.Register(Controller, ctl.cb)
	o.Claim(Controller)

	for _, src := range []Source{Sendspin, Spotify, AirPlay} {
		if o.Claim(src) {
			t.Fatalf("%v took the plane from the controller", src)
		}
		if o.Owner() != Controller {
			t.Fatalf("owner = %v after a refused %v claim", o.Owner(), src)
		}
	}
	if len(ctl.got()) != 0 {
		t.Fatalf("the controller was asked to leave: %v", ctl.got())
	}
}

func TestAmongLocalSourcesTheNewestRequestWins(t *testing.T) {
	// Not a priority ordering: Sendspin does not sit at a fixed rung under
	// AirPlay or over it. Somebody walked up and asked this speaker for
	// something, and that is the request to honour.
	var o Owner
	var ss, sp recorder
	o.Register(Sendspin, ss.cb)
	o.Register(Spotify, sp.cb)

	o.Claim(Sendspin)
	if !o.Claim(Spotify) {
		t.Fatal("a local source could not take over from another")
	}
	if o.Owner() != Spotify {
		t.Fatalf("owner = %v, want spotify", o.Owner())
	}
	if len(ss.got()) != 1 {
		t.Fatalf("sendspin leave callbacks = %v, want one", ss.got())
	}

	// And back the other way, so this is symmetry rather than an ordering
	// that happens to read the right way once.
	if !o.Claim(Sendspin) {
		t.Fatal("could not take the plane back")
	}
	if len(sp.got()) != 1 {
		t.Fatalf("spotify leave callbacks = %v, want one", sp.got())
	}
}

func TestReclaimingIsNotAnEvent(t *testing.T) {
	// Every write path re-checks ownership, and a client reconnecting
	// re-claims. Treating that as a transition would have a source send
	// itself a goodbye and tear down the session it just resumed.
	var o Owner
	var ss recorder
	o.Register(Sendspin, ss.cb)

	o.Claim(Sendspin)
	if !o.Claim(Sendspin) {
		t.Fatal("re-claiming its own plane was refused")
	}
	if len(ss.got()) != 0 {
		t.Fatalf("re-claiming evicted itself: %v", ss.got())
	}
}

func TestTeardownAfterPreemptionDoesNotEvictTheNewOwner(t *testing.T) {
	// Eviction is asynchronous by construction — the callback does protocol
	// I/O and runs outside the lock — so the new owner is always in place
	// before the old one finishes tearing down. A Release that cleared the
	// plane regardless would leave HA's music playing into a plane marked
	// free, and the next local claim would take it out from under HA.
	var o Owner
	o.Register(Sendspin, nil)
	o.Claim(Sendspin)
	o.Claim(Controller)

	o.Release(Sendspin) // the evicted client, finishing its teardown

	if o.Owner() != Controller {
		t.Fatalf("owner = %v, want controller", o.Owner())
	}
}

func TestNobodyRejoinsWhenHomeAssistantsMusicEnds(t *testing.T) {
	// Wil, 2026-08-22. A silent rejoin puts audio in the room nobody asked
	// for at that moment; the person who started the group can start it
	// again, and one extra tap is the cheap direction to be wrong in.
	var o Owner
	o.Register(Sendspin, nil)
	o.Claim(Sendspin)
	o.Claim(Controller)
	o.Release(Controller)

	if o.Owner() != None {
		t.Fatalf("owner = %v after HA released, want none", o.Owner())
	}
	if o.MayWrite(Sendspin) {
		t.Fatal("the preempted source was handed the plane back")
	}
}

func TestReleaseByANonOwnerChangesNothing(t *testing.T) {
	var o Owner
	o.Claim(Controller)
	o.Release(Sendspin)
	if o.Owner() != Controller {
		t.Fatalf("owner = %v, want controller", o.Owner())
	}
}

func TestNoneIsNeverAnOwner(t *testing.T) {
	var o Owner
	if o.Claim(None) {
		t.Fatal("None claimed the plane")
	}
}

func TestShutdownTellsEverySourceToLeaveOnce(t *testing.T) {
	var o Owner
	var ss, sp, ap recorder
	o.Register(Sendspin, ss.cb)
	o.Register(Spotify, sp.cb)
	o.Register(AirPlay, ap.cb)
	o.Claim(Sendspin)

	o.Shutdown()

	if o.Owner() != None {
		t.Fatalf("owner = %v after shutdown", o.Owner())
	}
	for name, r := range map[string]*recorder{"sendspin": &ss, "spotify": &sp, "airplay": &ap} {
		got := r.got()
		if len(got) != 1 || got[0] != ReasonStopped {
			t.Fatalf("%s leave callbacks = %v, want one %q", name, got, ReasonStopped)
		}
	}
}

func TestRegisterReplacesRatherThanStacks(t *testing.T) {
	var o Owner
	var first, second recorder
	o.Register(Sendspin, first.cb)
	o.Register(Sendspin, second.cb) // a reconnecting client
	o.Claim(Sendspin)
	o.Claim(Controller)

	if len(first.got()) != 0 {
		t.Fatalf("the stale callback fired: %v", first.got())
	}
	if len(second.got()) != 1 {
		t.Fatalf("the live callback fired %d times, want 1", len(second.got()))
	}
}

func TestAnEvictionCallbackMayReclaimWithoutBeingOverwritten(t *testing.T) {
	// The callback runs after the ownership change is committed, so a source
	// that decides to fight back gets a claim that sticks. Ordering the other
	// way round would silently discard it.
	var o Owner
	o.Register(Spotify, func(Reason) { o.Claim(Spotify) })
	o.Register(AirPlay, nil)

	o.Claim(Spotify)
	o.Claim(AirPlay)

	if o.Owner() != Spotify {
		t.Fatalf("owner = %v, want the re-claim to have stuck", o.Owner())
	}
}

func TestConcurrentClaimsLeaveExactlyOneOwner(t *testing.T) {
	var o Owner
	o.Register(Sendspin, nil)
	o.Register(Spotify, nil)
	o.Register(AirPlay, nil)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		for _, src := range []Source{Sendspin, Spotify, AirPlay} {
			wg.Add(1)
			go func(s Source) {
				defer wg.Done()
				o.Claim(s)
				o.MayWrite(s)
			}(src)
		}
	}
	wg.Wait()

	if !o.Owner().local() {
		t.Fatalf("owner = %v, want one of the local sources", o.Owner())
	}
}

func TestSourceNamesAreStable(t *testing.T) {
	// They reach the controller's logs and a Sendspin goodbye reason.
	for src, want := range map[Source]string{
		None: "none", Controller: "controller", Sendspin: "sendspin",
		Spotify: "spotify", AirPlay: "airplay",
	} {
		if got := src.String(); got != want {
			t.Fatalf("Source(%d).String() = %q, want %q", src, got, want)
		}
	}
}
