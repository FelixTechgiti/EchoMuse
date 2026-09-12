package wifi

import (
	"errors"
	"strings"
	"testing"
)

type fakeSupplicant struct {
	calls  []string
	downOK bool
	upErr  error
}

func (f *fakeSupplicant) how() string { return "fake" }
func (f *fakeSupplicant) down() error {
	f.calls = append(f.calls, "down")
	if !f.downOK {
		return errors.New("did not go down")
	}
	return nil
}
func (f *fakeSupplicant) up() error {
	f.calls = append(f.calls, "up")
	return f.upErr
}

// ── The order that cost a day on FireOS, kept for both bases ────────────────

func TestTheConfIsWrittenWhileTheRadioIsDown(t *testing.T) {
	// On FireOS, WifiStateMachine saves its in-memory network list back over
	// wpa_supplicant.conf as it goes down, so a conf written first is lost
	// and the device silently rejoins the OLD network — with every gate
	// passing, because the gates were written before that was known.
	f := &fakeSupplicant{downOK: true}
	var wrote []string
	if err := reloadWithWriter(f, "net", func(string) error {
		wrote = append(wrote, strings.Join(f.calls, ","))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(wrote) != 1 || wrote[0] != "down" {
		t.Fatalf("the conf was written with the radio in state %v, want after down only", wrote)
	}
	if strings.Join(f.calls, ",") != "down,up" {
		t.Fatalf("sequence was %v, want down then up", f.calls)
	}
}

func TestARadioThatWillNotGoDownIsNotWrittenOver(t *testing.T) {
	f := &fakeSupplicant{downOK: false}
	written := false
	err := reloadWithWriter(f, "net", func(string) error { written = true; return nil })
	if err == nil {
		t.Fatal("a failed down reported success")
	}
	if written {
		t.Fatal("wrote the conf anyway — on FireOS that write is the one that gets clobbered")
	}
}

func TestAFailedWriteLeavesTheRadioUsable(t *testing.T) {
	// Down next to a bad conf is a device off the network with nothing
	// scheduled to put it back.
	f := &fakeSupplicant{downOK: true}
	err := reloadWithWriter(f, "net", func(string) error { return errors.New("read-only fs") })
	if err == nil {
		t.Fatal("a failed write reported success")
	}
	if strings.Join(f.calls, ",") != "down,up" {
		t.Fatalf("sequence was %v — the radio was left down", f.calls)
	}
}

// ── The emOS supplicant ─────────────────────────────────────────────────────

func TestEmOSRereadsTheConfBeforeAssociating(t *testing.T) {
	// `reassociate` alone re-joins what the supplicant already holds in
	// memory, which is the OLD network. The gates would then pass against
	// the old SSID and commit a change that never happened — the same shape
	// as the FireOS clobber, arrived at from the other side.
	var got []string
	e := emosSupplicant{cli: func(args ...string) (string, error) {
		got = append(got, args[0])
		return "", nil
	}}
	if err := e.up(); err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || got[0] != "reconfigure" || got[1] != "reassociate" {
		t.Fatalf("issued %v, want reconfigure then reassociate", got)
	}
}

func TestEmOSNeverTerminatesTheSupplicant(t *testing.T) {
	// Killing it removes the control socket every later step needs — the
	// rollback's included — on a device whose only management path is that
	// radio.
	var got []string
	e := emosSupplicant{cli: func(args ...string) (string, error) {
		got = append(got, args[0])
		return "", errors.New("stop here")
	}}
	_ = e.down()
	_ = e.up()
	for _, verb := range got {
		if verb == "terminate" || verb == "quit" {
			t.Fatalf("issued %q — that ends the process we steer the radio with", verb)
		}
	}
}

func TestAFailingCliIsReportedRatherThanAssumedFine(t *testing.T) {
	e := emosSupplicant{cli: func(...string) (string, error) {
		return "", errors.New("Failed to connect to non-global ctrl_ifname")
	}}
	if err := e.up(); err == nil {
		t.Fatal("a wpa_cli that could not reach the supplicant reported success")
	}
}
