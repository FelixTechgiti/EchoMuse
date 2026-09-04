package server

import "testing"

// The ring is no longer a mute indicator (2026-09-03). Mute lives on the
// button's own GPIO LED, which was always in parallel and cannot be
// overpainted, and the twelve-LED ring belongs to whoever asks for it —
// which is what lets Home Assistant own it completely.
//
// The history is worth keeping, because the removed rule and its exception
// were a matched pair. Mute used to suppress every paint so a cancelled
// turn's LED cleanup could not clear the red ring; linkDown then had to be
// an exception to THAT, because a muted device with no controller sat
// showing red, and red says "muted and working" — false, not merely less
// useful. Taking the ring away from mute removed the reason for both.

func TestOnlyTheVolumeArcHoldsTheRing(t *testing.T) {
	cases := []struct {
		name         string
		volumeActive bool
		want         bool
	}{
		{"idle", false, false},
		// The arc repaints once and animations repaint ~every 100ms, so
		// without this the arc is stomped within a frame. Protection from
		// repaint churn, not from the user: a dot press still cancels it.
		{"volume arc holds the ring", true, true},
	}
	for _, c := range cases {
		if got := suppressPaint(c.volumeActive); got != c.want {
			t.Errorf("%s: suppressPaint(%v) = %v, want %v",
				c.name, c.volumeActive, got, c.want)
		}
	}
}

func TestMuteDoesNotHoldTheRing(t *testing.T) {
	// The invariant, stated as itself rather than as a row in the table
	// above: there is no input to suppressPaint by which mute can hold the
	// ring back, because mute is no longer one of its inputs. A future
	// change that re-adds one has to delete this test to do it.
	if suppressPaint(false) {
		t.Fatal("nothing but the volume arc may hold the ring")
	}
}

func TestLinkDownDefaultsToConnected(t *testing.T) {
	// A fresh Server must not start link-down: that would hand the ring away
	// from mute before any connection callback has run.
	s := &Server{}
	if s.LinkDown() {
		t.Fatal("a fresh Server must not start in link-down")
	}
	s.SetLinkDown(true)
	if !s.LinkDown() {
		t.Fatal("SetLinkDown(true) not recorded")
	}
	s.SetLinkDown(false)
	if s.LinkDown() {
		t.Fatal("SetLinkDown(false) not recorded — the ring would never go " +
			"back to the controller after a reconnect")
	}
}
