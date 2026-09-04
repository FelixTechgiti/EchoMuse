package speaker

import "testing"

// Verbatim from Test Device (G090LF1180570SPJ) 2026-08-09, while Android's
// mediaserver held the speaker and the server was stranded in snd_pcm_open.
const heldStatus = `state: PREPARED
owner_pid   : 659
trigger_time: 0.000000000
tstamp      : 1239.509457377
delay       : 0
avail       : 3072
avail_max   : 0
-----
hw_ptr      : 0
appl_ptr    : 0
`

// The same file once our own thread had taken the device over.
const runningStatus = `state: RUNNING
owner_pid   : 1094
trigger_time: 1786260229.525790163
tstamp      : 1786260262.082172242
delay       : 8064
avail       : 128
avail_max   : 6256
-----
hw_ptr      : 1562816
appl_ptr    : 1570880
`

func TestPcmFreeWhenClosed(t *testing.T) {
	if !pcmFree("closed\n") {
		t.Fatal("a closed substream must read as free")
	}
}

func TestPcmBusyWhenHeld(t *testing.T) {
	if pcmFree(heldStatus) {
		t.Fatal("a PREPARED substream held by another process must read as busy")
	}
	if pcmFree(runningStatus) {
		t.Fatal("a RUNNING substream must read as busy")
	}
}

// Unknown content must not block the open. Refusing on an unrecognised format
// would disable the speaker on hardware whose procfs we have never seen, to
// avoid a hang that device may not even have.
func TestUnknownStatusFailsOpen(t *testing.T) {
	for _, s := range []string{"", "   ", "something we have never seen"} {
		if !pcmFree(s) {
			t.Fatalf("unrecognised status %q must read as free, not busy", s)
		}
	}
}

func TestPcmOwner(t *testing.T) {
	if got := pcmOwner(heldStatus); got != 659 {
		t.Fatalf("owner_pid = %d, want 659", got)
	}
	if got := pcmOwner(runningStatus); got != 1094 {
		t.Fatalf("owner_pid = %d, want 1094", got)
	}
	if got := pcmOwner("closed\n"); got != 0 {
		t.Fatalf("a closed substream names no owner, got %d", got)
	}
}

func TestStatusPathMatchesTheDeviceWeOpen(t *testing.T) {
	// Pins the path against the card/device constants. The speaker is device
	// 23 and the mic is 24; checking pcm0p (Android's own) reads "closed" and
	// proves nothing, which cost a wrong conclusion during the #80 hunt.
	want := "/proc/asound/card0/pcm23p/sub0/status"
	if got := statusPath(cardNr, deviceNr); got != want {
		t.Fatalf("statusPath = %q, want %q", got, want)
	}
}

// ── Playback position ─────────────────────────────────────────────────────

func TestThePositionComesOffARealCapture(t *testing.T) {
	p := parsePlaybackPos(runningStatus)
	if !p.Valid {
		t.Fatal("a running substream's position did not parse")
	}
	if !p.Running() {
		t.Fatalf("state = %q, want RUNNING", p.State)
	}
	if p.HwPtr != 1562816 || p.ApplPtr != 1570880 {
		t.Fatalf("pointers = %d/%d", p.HwPtr, p.ApplPtr)
	}
	if p.Delay != 8064 {
		t.Fatalf("delay = %d, want 8064 (appl_ptr - hw_ptr)", p.Delay)
	}
	if p.Avail != 128 {
		t.Fatalf("avail = %d, want 128", p.Avail)
	}
}

func TestDelayIsDerivedFromThePointersNotTakenFromTheDriver(t *testing.T) {
	// They agree on every capture, and where they cannot both be right the
	// pointers are: `delay` is a driver-reported figure that some drivers
	// adjust for their own pipeline latency, while appl_ptr − hw_ptr is
	// arithmetic on two counters this file reports directly. Folding a
	// driver's idea of latency into a measurement of buffer occupancy is
	// how a corrector ends up chasing a constant.
	disagreeing := `state: RUNNING
delay       : 99999
avail       : 128
-----
hw_ptr      : 1000
appl_ptr    : 3048
`
	p := parsePlaybackPos(disagreeing)
	if p.Delay != 2048 {
		t.Fatalf("delay = %d, want 2048 from the pointers", p.Delay)
	}
}

func TestAPreparedSubstreamIsNotRunning(t *testing.T) {
	// Both pointers at zero and a delay that means nothing yet. A corrector
	// handed this would read an empty buffer on a stream that has not
	// started and try to make up time that was never lost.
	p := parsePlaybackPos(heldStatus)
	if p.Running() {
		t.Fatalf("state %q read as running", p.State)
	}
}

func TestAClosedSubstreamHasNoPosition(t *testing.T) {
	// Callers must check Valid: a zero Delay taken as real says the buffer
	// is empty, which on a running stream makes the corrector chase a rate
	// it cannot reach.
	if p := parsePlaybackPos("closed\n"); p.Valid {
		t.Fatalf("a closed substream produced a position: %+v", p)
	}
	if p := parsePlaybackPos(""); p.Valid {
		t.Fatal("an empty file produced a position")
	}
}

func TestAPartialFileDegradesRatherThanRefusing(t *testing.T) {
	// This file's shape differs across kernel versions. A parser that
	// refused the whole reading because one line moved would take the
	// corrector offline rather than degrade it — so Valid keys on the two
	// fields the position is actually derived from, and nothing else.
	p := parsePlaybackPos("state: RUNNING\nhw_ptr : 100\nappl_ptr : 340\n")
	if !p.Valid || p.Delay != 240 {
		t.Fatalf("a file without delay/avail did not parse: %+v", p)
	}
	if p.Avail != 0 {
		t.Fatalf("avail invented a value: %d", p.Avail)
	}
}

func TestAnUnparseableNumberDoesNotBecomeZero(t *testing.T) {
	// A garbled hw_ptr must read as "no position", not as position zero —
	// which on a long stream is a jump of hours.
	p := parsePlaybackPos("state: RUNNING\nhw_ptr : \nappl_ptr : 340\n")
	if p.Valid {
		t.Fatalf("a garbled pointer produced a position: %+v", p)
	}
}

func TestTheDriftSignalHasTheRightSign(t *testing.T) {
	// The whole reason this reading exists. Pushing 48000 frames a second
	// into hardware consuming 47973 leaves 27 more frames queued each
	// second, so Delay GROWS when we are ahead. A corrector wired to the
	// opposite sign doubles the drift instead of cancelling it, and the
	// symptom — a room walking steadily further out — is the same either
	// way.
	after1s := parsePlaybackPos("state: RUNNING\nhw_ptr : 47973\nappl_ptr : 48000\n")
	after2s := parsePlaybackPos("state: RUNNING\nhw_ptr : 95946\nappl_ptr : 96000\n")
	if after1s.Delay != 27 || after2s.Delay != 54 {
		t.Fatalf("delay = %d then %d, want 27 then 54", after1s.Delay, after2s.Delay)
	}
	if after2s.Delay <= after1s.Delay {
		t.Fatal("pushing faster than the hardware consumes did not grow the queue")
	}
}
