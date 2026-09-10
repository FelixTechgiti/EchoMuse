package musicplane

import (
	"sync"
	"testing"
	"time"
)

// The bug these cover, reported from a real device on 2026-09-10: the Audio
// Source entity read "airplay" for minutes after the AirPlay session had been
// disconnected and nothing was playing. shairport-sync and librespot are
// daemons — they keep running so the device stays in the pickers — so a plane
// released on process exit is released at the next reboot.

func TestSilenceGivesThePlaneBack(t *testing.T) {
	var o Owner
	c := NewIdleClaim(o.For(AirPlay), time.Hour)

	if !c.Feed() {
		t.Fatal("first audio could not take the plane")
	}
	if o.Owner() != AirPlay {
		t.Fatalf("owner is %v, want AirPlay", o.Owner())
	}

	c.expire() // the timer firing: no audio for the idle period

	if o.Owner() != None {
		t.Fatalf("owner is %v after the stream went quiet, want None", o.Owner())
	}
}

func TestAudioAfterSilenceClaimsAgain(t *testing.T) {
	// A phone that reconnects, or a gap between tracks longer than the idle
	// period. The claim has to come back on its own — the alternative is a
	// source that plays audio into a plane it does not own.
	var o Owner
	c := NewIdleClaim(o.For(Spotify), time.Hour)

	c.Feed()
	c.expire()
	if !c.Feed() {
		t.Fatal("audio after a lapse could not re-take the plane")
	}
	if o.Owner() != Spotify {
		t.Fatalf("owner is %v, want Spotify", o.Owner())
	}
}

func TestAContinuousStreamNeverLapses(t *testing.T) {
	// Feed keeps postponing the expiry, so a stream that is playing holds the
	// plane no matter how long it runs. Real timer here, deliberately: this
	// is the one property that depends on the wiring rather than the logic.
	var o Owner
	c := NewIdleClaim(o.For(AirPlay), 40*time.Millisecond)

	for i := 0; i < 12; i++ {
		if !c.Feed() {
			t.Fatalf("chunk %d was refused mid-stream", i)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if o.Owner() != AirPlay {
		t.Fatalf("owner is %v during a live stream, want AirPlay", o.Owner())
	}

	time.Sleep(90 * time.Millisecond) // now stop feeding
	if o.Owner() != None {
		t.Fatalf("owner is %v after the stream stopped, want None", o.Owner())
	}
}

func TestHomeAssistantStillWins(t *testing.T) {
	// The controller takes the plane from a local source at any time, and the
	// local source must not fight it back. Claim refuses a local source while
	// the controller holds it, so Feed reports "do not write" and the caller
	// drops the chunk.
	var o Owner
	c := NewIdleClaim(o.For(AirPlay), time.Hour)
	c.Feed()

	o.Claim(Controller)

	if c.Feed() {
		t.Fatal("a local source wrote over Home Assistant's music")
	}
	if o.Owner() != Controller {
		t.Fatalf("owner is %v, want Controller", o.Owner())
	}
}

func TestBeingPreemptedDoesNotLeaveTheClaimHeld(t *testing.T) {
	// After a preemption the source must be able to claim again once the
	// controller is done. Holding stale `held` would leave it silent for
	// good, which is the failure mode of remembering rather than asking.
	var o Owner
	c := NewIdleClaim(o.For(Spotify), time.Hour)
	c.Feed()

	o.Claim(Controller)
	c.Feed() // refused
	o.Release(Controller)

	if !c.Feed() {
		t.Fatal("could not re-claim after the controller finished")
	}
	if o.Owner() != Spotify {
		t.Fatalf("owner is %v, want Spotify", o.Owner())
	}
}

func TestStopReleasesWithoutWaitingOutTheIdlePeriod(t *testing.T) {
	// The process exiting is a fact, not a silence to infer. Waiting the idle
	// period there would report a source as playing after its program is gone.
	var o Owner
	c := NewIdleClaim(o.For(AirPlay), time.Hour)
	c.Feed()
	c.Stop()

	if o.Owner() != None {
		t.Fatalf("owner is %v after Stop, want None", o.Owner())
	}
}

func TestStopOnAnUnheldClaimReleasesNothing(t *testing.T) {
	// Stop runs on every process exit, including one that never played a
	// note. Releasing there would take the plane from whoever holds it.
	var o Owner
	c := NewIdleClaim(o.For(AirPlay), time.Hour)
	o.Claim(Controller)

	c.Stop()

	if o.Owner() != Controller {
		t.Fatalf("owner is %v, want the controller left alone", o.Owner())
	}
}

func TestTheChangeObserverSeesTheLapse(t *testing.T) {
	// The whole point: Home Assistant is told the device went quiet. Without
	// this the Audio entity stays on until the device reboots.
	var o Owner
	var mu sync.Mutex
	var got []Source
	o.OnChange(func(s Source) { mu.Lock(); got = append(got, s); mu.Unlock() })

	c := NewIdleClaim(o.For(AirPlay), time.Hour)
	c.Feed()
	c.expire()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != AirPlay || got[1] != None {
		t.Fatalf("observer saw %v, want [airplay none]", got)
	}
}

func TestConcurrentFeedIsSafe(t *testing.T) {
	// Feed runs on the pump goroutine and expire on the timer's. Run under -race.
	var o Owner
	c := NewIdleClaim(o.For(Spotify), 5*time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c.Feed()
			}
		}()
	}
	wg.Wait()
	c.Stop()
}
