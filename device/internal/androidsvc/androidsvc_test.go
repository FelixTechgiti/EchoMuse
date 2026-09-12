package androidsvc

import (
	"errors"
	"testing"

	"github.com/wilbowes/EchoMuse/internal/platform"
)

func fixed(base string) Base { return func() string { return base } }

func TestNothingIsStoppedOnEmOS(t *testing.T) {
	// There is no Android init, no property service, and nothing holding the
	// hardware — so the request has nobody to make it to.
	called := false
	attempted, err := stop(fixed(platform.EmOS), func(string) error {
		called = true
		return nil
	}, "acebutton")
	if called {
		t.Fatal("ran `stop` on a base with no Android services")
	}
	if attempted {
		t.Fatal("reported an attempt that never happened")
	}
	if err != nil {
		t.Fatalf("a no-op reported an error: %v", err)
	}
}

func TestFireOSStillStopsTheService(t *testing.T) {
	// The whole existing fleet depends on this: the firmware takes the mic,
	// the speaker, the ring and the buttons away from Amazon's services on
	// the way up, and a gate that silenced that would be far worse than the
	// wasted execs it exists to remove.
	var got string
	attempted, err := stop(fixed(platform.FireOS), func(s string) error {
		got = s
		return nil
	}, "mixer")
	if !attempted || got != "mixer" {
		t.Fatalf("attempted=%v service=%q, want true/\"mixer\"", attempted, got)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnknownCountsAsAndroid(t *testing.T) {
	// Every other reader of this value takes absence as Android, because
	// firmware that cannot tell must behave as the existing fleet does. The
	// cost of being wrong this way is two failed execs; the cost of the other
	// way is hardware nobody asked Android to let go of.
	attempted, _ := stop(fixed(platform.Unknown), func(string) error { return nil }, "media")
	if !attempted {
		t.Fatal("an unknown base skipped the stop — the existing fleet's behaviour changed")
	}
}

func TestTheCommandsErrorReachesTheCaller(t *testing.T) {
	// buttons.NewButtonController turns this into a fatal error, so it must
	// not be swallowed here.
	want := errors.New("exec: \"stop\": executable file not found in $PATH")
	_, err := stop(fixed(platform.FireOS), func(string) error { return want }, "acebutton")
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want it passed through", err)
	}
}
