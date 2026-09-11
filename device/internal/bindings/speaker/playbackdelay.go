package speaker

// Where a sample pushed RIGHT NOW will actually come out.
//
// NO BUILD TAG, for musicprime.go's reason: pcm_speaker.go is
// `//go:build server` and cannot be compiled on the host, and this is
// arithmetic a scheduled protocol's correctness depends on.

// PlaybackFrames is how many frames stand between a sample handed to the
// music plane and the moment it is heard.
//
// **Two buffers, and only one of them is ALSA's.** `hwDelay` is what procfs
// reports — appl_ptr minus hw_ptr, the frames the hardware has been given and
// not yet played. In front of it sits the music plane's own software ring,
// which the DMA pointer cannot see: a producer's period goes into that ring,
// waits its turn to be mixed, and only THEN becomes a frame ALSA knows about.
//
// Counting only the hardware is a systematic UNDER-estimate of the pipeline,
// and for a scheduled protocol that is not noise it is a bias. Sendspin
// places each chunk by asking when the next sample would play and padding or
// trimming the difference; feeding it a figure that omits the ring makes it
// believe it is early by exactly the ring's depth, so it pads, the ring grows
// by what it padded, and the measurement does not move — the correction loop
// converges on the audio coming out LATE by the ring depth and holds it
// there. In a Music Assistant group with any other speaker that is an echo,
// and it is invisible from this end, because every number the corrector logs
// agrees with itself.
//
// Each ring entry is exactly one period by construction (PumpMusic takes one
// period and the channel carries them whole), so the depth is a count of
// periods rather than of bytes.
func PlaybackFrames(hwDelay int64, queuedPeriods int) int64 {
	if queuedPeriods < 0 {
		queuedPeriods = 0
	}
	if hwDelay < 0 {
		hwDelay = 0
	}
	return hwDelay + int64(queuedPeriods)*periodSize
}
