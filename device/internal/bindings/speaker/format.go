package speaker

// The speaker path's audio format.
//
// NO BUILD TAG, deliberately, for pcmwait.go's and pcmstatus.go's reason:
// pcm_speaker.go is `//go:build server` and cannot be compiled on the host at
// all, so anything the host suite needs to do arithmetic with has to live
// outside it — musicprime.go turns a period count into seconds, and a
// duplicated 48000 in a second file is how the two would drift.

// sampleRate is the speaker path's rate everywhere — the wire, the ALSA
// config and the output chain's filter design all assume it.
const sampleRate = 48000

// periodSize is one ALSA period in frames; 2048 at 48kHz is 42.7ms.
const periodSize = 2048
