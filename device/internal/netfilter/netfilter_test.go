package netfilter

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// fake is an iptables that keeps a table in order and answers the way the
// real one does: -D removes ONE copy and FAILS when there is none, which is
// the behaviour the delete-then-insert path is built on.
type fake struct {
	calls [][]string
	rules []string // table order; duplicates are possible, as in the real thing
	err   error    // when set, every invocation fails with it
}

func newFake(present ...string) *fake {
	return &fake{rules: append([]string{}, present...)}
}

func (f *fake) run(args ...string) error {
	f.calls = append(f.calls, args)
	if f.err != nil {
		return f.err
	}
	rule := strings.Join(args[1:], " ")
	switch args[0] {
	case "-I":
		f.rules = append([]string{rule}, f.rules...)
		return nil
	case "-D":
		for i, r := range f.rules {
			if r == rule {
				f.rules = append(f.rules[:i], f.rules[i+1:]...)
				return nil
			}
		}
		return errors.New("iptables: Bad rule (does a matching rule exist in that chain?)")
	}
	return errors.New("unexpected verb " + args[0])
}

func (f *fake) count(r Rule) int {
	n := 0
	for _, got := range f.rules {
		if got == strings.Join(r.spec(), " ") {
			n++
		}
	}
	return n
}

func (f *fake) verbs() []string {
	var v []string
	for _, c := range f.calls {
		v = append(v, c[0])
	}
	return v
}

// ── The rules that must never be written ─────────────────────────────────────
//
// This is a firewall on a device whose only management path IS the network.
// Getting it wrong does not produce a bug report, it produces an Echo nobody
// can reach, on a shelf, with its shell proxied by the controller it can no
// longer talk to.

func TestNoCallEverChangesPolicyOrFlushes(t *testing.T) {
	f := newFake()
	Sync(f.run, All())
	Sync(f.run, nil)
	Sync(f.run, SpotifyRules())
	for _, v := range f.verbs() {
		switch v {
		case "-P", "-F", "-X", "--policy", "--flush", "--delete-chain":
			t.Fatalf("netfilter used %q — that can strand the device for good", v)
		}
		if v != "-I" && v != "-D" {
			t.Fatalf("unexpected verb %q; only -I and -D are allowed", v)
		}
	}
}

func TestEveryRuleNamesAnInterfaceAndATarget(t *testing.T) {
	// A bare ACCEPT is not a fix, it is the removal of a firewall.
	for _, r := range All() {
		s := strings.Join(r.spec(), " ")
		if !strings.Contains(s, "-i "+iface) {
			t.Fatalf("rule is not scoped to an interface: %s", s)
		}
		if !strings.HasPrefix(s, "INPUT ") {
			t.Fatalf("rule is not in the INPUT chain: %s", s)
		}
		if !strings.HasSuffix(s, "-j ACCEPT") {
			t.Fatalf("rule does not end in an explicit ACCEPT: %s", s)
		}
	}
}

func TestEveryNonIcmpRuleHasAPort(t *testing.T) {
	for _, r := range All() {
		if r.Proto == "icmp" {
			continue
		}
		if !regexp.MustCompile(`^\d+(:\d+)?$`).MatchString(r.Port) {
			t.Fatalf("port %q is not a number or a range", r.Port)
		}
	}
}

// ── Idempotence ──────────────────────────────────────────────────────────────

func TestEachWantedRuleEndsUpPresentExactlyOnce(t *testing.T) {
	f := newFake()
	want := All()
	Sync(f.run, want)
	for _, r := range want {
		if n := f.count(r); n != 1 {
			t.Fatalf("rule present %d times, want 1: %s", n, r)
		}
	}
}

func TestSyncIsStableAcrossRepeatedRuns(t *testing.T) {
	// Sync runs on every start AND every config push. A device that
	// reconnects often must not grow a duplicate rule per reconnect for the
	// life of the device.
	f := newFake()
	want := AirPlayRules()
	Sync(f.run, want)
	first := len(f.rules)
	Sync(f.run, want)
	Sync(f.run, want)
	if len(f.rules) != first {
		t.Fatalf("rule count drifted: %d then %d", first, len(f.rules))
	}
}

func TestDuplicatesLeftByAnOlderBuildAreRepaired(t *testing.T) {
	// Delete-then-insert is chosen over check-then-insert precisely because
	// it repairs a table that is already wrong rather than merely declining
	// to make it worse.
	r := SpotifyRules()[0]
	spec := strings.Join(r.spec(), " ")
	f := newFake(spec, spec, spec)
	Sync(f.run, SpotifyRules())
	if n := f.count(r); n != 1 {
		t.Fatalf("left %d copies of the rule, want 1", n)
	}
}

// ── Turning an endpoint off closes its port ──────────────────────────────────

func TestDisablingAnEndpointRemovesItsRule(t *testing.T) {
	// A port left open for a service that is switched off is exposure with
	// nothing behind it, and nothing would ever report it.
	f := newFake()
	Sync(f.run, append(SpotifyRules(), AirPlayRules()...))
	Sync(f.run, SpotifyRules())

	for _, r := range AirPlayRules() {
		if f.count(r) != 0 {
			t.Fatalf("AirPlay rule survived the endpoint being turned off: %s", r)
		}
	}
	for _, r := range SpotifyRules() {
		if f.count(r) != 1 {
			t.Fatalf("Spotify rule was removed while still wanted: %s", r)
		}
	}
}

func TestSyncWithNothingWantedClosesEverything(t *testing.T) {
	f := newFake()
	Sync(f.run, All())
	Sync(f.run, nil)
	if len(f.rules) != 0 {
		t.Fatalf("rules left behind: %v", f.rules)
	}
}

// ── Failure is survivable ────────────────────────────────────────────────────

func TestAFirewallThatRefusesDoesNotStopTheDevice(t *testing.T) {
	// A device that will not boot because a firewall could not be adjusted is
	// far worse than one whose endpoints are unreachable — which is what
	// every device did before this existed.
	f := newFake()
	f.err = errors.New("iptables: Permission denied (you must be root?)")
	Sync(f.run, All()) // must not panic
}

func TestNoIptablesStopsTheSyncInsteadOfComplainingPerRule(t *testing.T) {
	// emOS has no iptables and that is not a fault. The log relay forwards
	// "could not" lines to the controller, so a per-rule complaint would put
	// four warnings into somebody's Home Assistant log on every reconnect,
	// about a device with nothing wrong with it.
	f := newFake()
	f.err = ErrUnavailable
	Sync(f.run, All())

	inserts := 0
	for _, v := range f.verbs() {
		if v == "-I" {
			inserts++
		}
	}
	if inserts != 1 {
		t.Fatalf("tried %d inserts against an absent firewall, want 1", inserts)
	}
}

func TestANilRunnerIsANoOp(t *testing.T) {
	Sync(nil, All())
}

// ── The ports are pinned, because a rule cannot name a random number ─────────

func TestTheAirPlayRangeMatchesBaseAndRange(t *testing.T) {
	// The daemon is told udp_port_base/udp_port_range from these same
	// constants. If the rule and the listener disagree, AirPlay negotiates a
	// session and plays nothing — the exact failure this package exists to
	// end, one layer down.
	var udp Rule
	for _, r := range AirPlayRules() {
		if r.Proto == "udp" {
			udp = r
		}
	}
	if udp.Port != "6001:6010" {
		t.Fatalf("got %q, want the base..base+range-1 span", udp.Port)
	}
}

func TestIcmpAcceptsOnlyEchoRequest(t *testing.T) {
	// Being pingable is worth having; accepting every ICMP type is not what
	// was asked for.
	s := strings.Join(PingRule().spec(), " ")
	if !strings.Contains(s, "--icmp-type echo-request") {
		t.Fatalf("ping rule is wider than echo-request: %s", s)
	}
}
