package server

import "testing"

// Mute arriving over the network is one-way: Home Assistant can close the
// microphone and cannot open it. Only the physical button gives it back.
//
// The rule is enforced twice, and the outer half is the one that cannot be
// forgotten: the `mute_set` control message carries no boolean, so there is
// nothing on the wire that could ask for an unmute. MuteOnly is the inner
// half — the thing that would still be correct if someone added one.

func newTestMute() *muteController {
	// No LED controller and no ADC on a host test; applyMute's tinymix exec
	// and GPIO write both fail harmlessly and are not what is under test.
	return &muteController{}
}

func TestMuteOnlyMutes(t *testing.T) {
	m := newTestMute()
	if m.IsMuted() {
		t.Fatal("a fresh controller must not start muted")
	}
	m.MuteOnly()
	if !m.IsMuted() {
		t.Fatal("MuteOnly did not mute")
	}
}

func TestMuteOnlyIsIdempotentAndNeverUnmutes(t *testing.T) {
	m := newTestMute()
	m.MuteOnly()
	for i := 0; i < 5; i++ {
		m.MuteOnly()
		if !m.IsMuted() {
			t.Fatalf("call %d unmuted the device — MuteOnly must never toggle", i+2)
		}
	}
}

func TestMuteOnlyDoesNotNotifyWhenAlreadyMuted(t *testing.T) {
	// A repeated mute is not a state change, and reporting one would make
	// Home Assistant and the dashboard show a transition that did not
	// happen — the same reason a volume seed suppresses its own report.
	m := newTestMute()
	calls := 0
	m.SetOnMuteChange(func(bool) { calls++ })

	m.MuteOnly()
	if calls != 1 {
		t.Fatalf("first mute reported %d times, want 1", calls)
	}
	m.MuteOnly()
	m.MuteOnly()
	if calls != 1 {
		t.Fatalf("repeated mutes reported %d times, want 1", calls)
	}
}

func TestTheButtonIsStillTheWayBack(t *testing.T) {
	// Toggle stays two-way. Taking that away too would leave a device that
	// nothing can unmute, which is not privacy, it is a broken microphone.
	m := newTestMute()
	m.MuteOnly()
	m.Toggle()
	if m.IsMuted() {
		t.Fatal("the physical button must still unmute")
	}
}
