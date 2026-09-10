package speaker

// How deep the music plane fills before playback starts, and why it is not
// one number.
//
// NO BUILD TAG, for pcmwait.go's reason: pcm_speaker.go is `//go:build server`
// and cannot be compiled on the host at all, and this is arithmetic plus a
// policy decision that is worth stating where it can be read and tested.

// primePeriods — the music and voice planes hold on silence until this many
// periods are queued (or the stream's EOS has arrived, for clips shorter than
// the prime). 24 periods ≈ 1s of audio.
//
// It exists to protect the opening seconds of a CONTROLLER stream, when the
// sender's lead is still ~zero and a single WiFi stall used to stutter. That
// is the whole justification, and it is a justification about a network link.
const primePeriods = 24

// localPrimePeriods is the prime for a DEVICE-LOCAL producer — AirPlay,
// Spotify Connect, Sendspin. 4 periods ≈ 171ms.
//
// **A local source has no link to protect.** librespot and shairport-sync are
// processes on this device writing to a pipe, so there is no WiFi hop between
// the producer and this buffer and nothing for a 1s cushion to absorb: the
// measured 1.8–2.6s link stalls that sized `primePeriods` happen between the
// CONTROLLER and the device, on a path this audio never takes.
//
// And the cushion is not a start-up cost that goes away. Both these producers
// pace themselves at realtime — shairport-sync has its own clock and
// librespot is paced by pipe backpressure — so the buffer settles at whatever
// depth the prime gate demanded and stays there. Every sample then waits for
// the periods ahead of it, which makes the prime a PERMANENT latency for
// exactly the sources that can least afford it: AirPlay already carries ~2s of
// protocol latency, and a second on top of that is what "der Airplay-Ton ist
// mega verzögert" measures.
//
// Not zero, and not one. A pipe read still arrives in bursts, the ALSA loop
// still takes exactly one period per iteration, and the underrun accounting
// counts a mid-stream drain against the stream rather than against the
// producer — so a couple of periods of slack keeps a scheduling hiccup from
// reading as a fault. 171ms is inaudible as latency and is ~4× the 42.7ms
// period the hardware consumes in.
const localPrimePeriods = 4

// MusicPrimeFor is the prime depth for a music-plane owner, keyed on whether
// that owner is device-local (musicplane.Source.Local).
//
// Takes the boolean rather than the Source so this package does not import
// musicplane: the speaker knows about periods and the arbiter knows about
// producers, and the one fact that has to cross is which side of that line the
// current owner sits on.
func MusicPrimeFor(local bool) int {
	if local {
		return localPrimePeriods
	}
	return primePeriods
}

// LocalPrimeSeconds is how much latency localPrimePeriods adds, in seconds.
//
// Exported because a device-local producer that schedules its own playback has
// to be TOLD about it: shairport-sync plays each packet at the instant the
// sender stamped it, and `audio_backend_latency_offset_in_seconds` is how it is
// told what the backend behind it adds, so it can hand the audio over that much
// earlier and land on time. Deriving the number here rather than writing a
// literal into the config means the two cannot drift — changing the prime
// changes the compensation.
func LocalPrimeSeconds() float64 {
	return float64(localPrimePeriods) * float64(periodSize) / float64(sampleRate)
}
