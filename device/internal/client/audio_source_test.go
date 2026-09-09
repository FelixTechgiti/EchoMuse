package client

import (
	"sync"
	"testing"
	"time"
)

// SendAudioSource is called from the music plane arbiter's OnChange, which
// runs on the goroutine feeding audio. Its whole shape exists so that a
// control socket which has stopped draining — writeJSON parks for
// wsWriteWait, and this fleet has measured 1.4s TCP stalls — cannot delay the
// start of somebody's music.
//
// The sender goroutine is not driven here: with no connection writeJSON is a
// no-op, so these test the handoff, which is the part that runs on the audio
// path.

func TestSendAudioSourceDoesNotWaitForTheSocket(t *testing.T) {
	c := NewControlClient("dev", nil, nil, nil)
	// Held for the length of the test, which is what a stalled write looks
	// like from outside: writeJSON takes connMu and does not give it back.
	c.connMu.Lock()
	defer c.connMu.Unlock()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			c.SendAudioSource("spotify")
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SendAudioSource blocked behind the connection write mutex")
	}
}

func TestTheLatestOwnerWins(t *testing.T) {
	// What travels is the plane's current owner, which is a level and not an
	// event: a handover still waiting when a newer one arrives is superseded,
	// never queued behind it. Queueing would let the controller be told about
	// a source that stopped playing minutes ago.
	c := NewControlClient("dev", nil, nil, nil)
	c.SendAudioSource("sendspin")
	c.SendAudioSource("spotify")
	c.SendAudioSource("airplay")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.audioMu.Lock()
		next, has := c.audioNext, c.audioHas
		c.audioMu.Unlock()
		if !has {
			return // the sender took the last one; nothing is stacked up
		}
		if next != "airplay" {
			t.Fatalf("pending owner is %q, want the newest (airplay)", next)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("a handover was still pending a second later")
}

func TestAZeroValuedClientDoesNotPanic(t *testing.T) {
	// The struct is constructed by NewControlClient everywhere in the tree,
	// but a nil wake channel is what a zero value has, and a send on one
	// blocks for ever rather than failing loudly.
	var c ControlClient
	c.SendAudioSource("spotify")
	if c.currentAudioSource() != "none" {
		t.Fatalf("a client with no source hook reported %q, want none",
			c.currentAudioSource())
	}
}

func TestConcurrentHandoversAreSafe(t *testing.T) {
	// Claims come from whichever goroutine is playing, and three endpoints
	// can each be trying to take the plane. Run under -race.
	c := NewControlClient("dev", nil, nil, nil)
	var wg sync.WaitGroup
	for _, src := range []string{"sendspin", "spotify", "airplay", "none"} {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				c.SendAudioSource(s)
			}
		}(src)
	}
	wg.Wait()
}
