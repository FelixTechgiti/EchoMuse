package speaker

import (
	"fmt"
	"strconv"
	"strings"
)

// The speaker is card 0 device 23; the mic is device 24. Here rather than in
// pcm_speaker.go because that file is ARM-only (build tag `server`) and the
// status-path test needs to pin these on the host.
const cardNr = 0
const deviceNr = 23

// The playback substream's status file, which is how we find out whether
// anyone else holds the speaker BEFORE trying to open it.
//
// This exists because tinyalsa's pcm_open is open(fn, O_RDWR) with no
// O_NONBLOCK, and we link -ltinyalsa from the compiler image's sysroot rather
// than vendoring its source — so the open itself cannot be made non-blocking
// without patching the toolchain's library. ALSA core parks a blocking open on
// pcm->open_wait for as long as the substream is busy, with no timeout, which
// on this hardware means forever.
//
// Measured on a stranded device (issue #80, 2026-08-09): a Dot booted with a
// headphone plug inserted has Android's mediaserver holding this substream,
// and our thread sat in snd_pcm_open for eighteen minutes with the whole of
// main() behind it — no buttons, no wake word, no controller registration.
func statusPath(card, device int) string {
	return fmt.Sprintf("/proc/asound/card%d/pcm%dp/sub0/status", card, device)
}

// pcmFree reports whether the substream is available to open.
//
// A free substream's status file contains exactly "closed"; a held one leads
// with "state: <STATE>" and an owner_pid. Anything we do not recognise counts
// as FREE, deliberately: this guard exists to avoid a hang we know how to
// detect, and treating an unfamiliar format as busy would refuse to open a
// perfectly good speaker on some device whose procfs we have never seen.
// Failing open costs us the old behaviour; failing closed costs the speaker.
func pcmFree(status string) bool {
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(status), "\n", 2)[0])
	if strings.EqualFold(first, "closed") {
		return true
	}
	// Busy has one shape and we match it POSITIVELY: a held substream leads
	// with "state: <STATE>". Treating everything-that-is-not-closed as busy
	// would make an unfamiliar procfs format indistinguishable from a device
	// we cannot have, and refuse to open a speaker that was never held.
	key, _, found := strings.Cut(first, ":")
	return !(found && strings.EqualFold(strings.TrimSpace(key), "state"))
}

// pcmOwner returns the pid holding the substream, or 0 if the status does not
// name one. Reported in the log line so a stall names the culprit — the whole
// diagnosis of #80 turned on finding "owner_pid: 659" in this file, and that
// took a day of hardware round trips to reach.
func pcmOwner(status string) int {
	for _, line := range strings.Split(status, "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "owner_pid" {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			return n
		}
	}
	return 0
}

// ── Playback position ─────────────────────────────────────────────────────
//
// The same status file answers a question nothing else on this device can:
// where playback ACTUALLY is. A scheduled protocol needs it. Our own
// bookkeeping — frames handed to ALSA, divided by the nominal rate — is
// perfect by construction and therefore useless as an error signal: it cannot
// see that the hardware is consuming 47973 frames a second rather than 48000,
// which is the whole of the drift a synchronised group has to correct.
//
// `delay` is the number the corrector wants. It is appl_ptr minus hw_ptr —
// frames handed over and not yet played — so it GROWS when we push faster
// than the hardware consumes and shrinks when we push slower. On the captured
// sample it is exactly 1570880 − 1562816 = 8064, and it is accurate to well
// below a period: hw_ptr on this hardware is a real DMA position that
// advances in 112–144 frame steps every ~2.4ms, not software bookkeeping that
// would jump a whole 2048-frame period every 42.7ms.
//
// `tstamp` is deliberately NOT used, though it looks like the more precise
// answer. It is in the kernel's own clock domain, and mixing that with the
// clock the time filter estimates against would be a domain error of exactly
// the kind that produces a plausible constant offset — the hardest sort to
// notice. The read is stamped by the caller instead, which costs the file
// read's own duration (microseconds) and stays in one clock.

// PlaybackPos is a reading of the playback substream's position.
type PlaybackPos struct {
	// State is the ALSA state: RUNNING, PREPARED, XRUN, SETUP …
	State string
	// HwPtr is total frames the hardware has played.
	HwPtr int64
	// ApplPtr is total frames handed to it.
	ApplPtr int64
	// Delay is frames queued and not yet played. THE error signal.
	Delay int64
	// Avail is free frames in the ring buffer.
	Avail int64
	// Valid is false when the file did not contain a position at all — a
	// closed substream, or a procfs shape we have not seen. Callers must
	// check it: a zero Delay read as real says the buffer is empty, which
	// on a running stream would make the corrector chase a rate it cannot
	// reach.
	Valid bool
}

// Running reports whether the hardware is actually consuming frames. A
// PREPARED substream has appl_ptr and hw_ptr both at zero and a Delay that
// means nothing yet.
func (p PlaybackPos) Running() bool { return p.Valid && p.State == "RUNNING" }

// parsePlaybackPos reads a status file's body.
//
// Every field is optional and a missing one leaves its zero: this file's
// shape differs across kernel versions and the fields we need have been
// present on everything seen, but a parser that refuses the whole reading
// because one line moved would take the corrector offline rather than
// degrade it. Valid is keyed on the two fields the position is actually
// derived from.
func parsePlaybackPos(status string) PlaybackPos {
	var p PlaybackPos
	var sawHw, sawAppl bool
	for _, line := range strings.Split(status, "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "state":
			p.State = val
		case "hw_ptr":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				p.HwPtr, sawHw = n, true
			}
		case "appl_ptr":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				p.ApplPtr, sawAppl = n, true
			}
		case "delay":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				p.Delay = n
			}
		case "avail":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				p.Avail = n
			}
		}
	}
	p.Valid = sawHw && sawAppl
	// Prefer the derived difference over the reported `delay`.
	//
	// They agree on every capture, and where they cannot both be right the
	// pointers are: `delay` is a driver-reported figure that some drivers
	// adjust for their own pipeline latency, while appl_ptr − hw_ptr is
	// arithmetic on two counters this file reports directly. A driver's own
	// idea of latency is a fine thing to know and a bad thing to fold into
	// a measurement of buffer occupancy without noticing.
	if p.Valid {
		p.Delay = p.ApplPtr - p.HwPtr
	}
	return p
}
