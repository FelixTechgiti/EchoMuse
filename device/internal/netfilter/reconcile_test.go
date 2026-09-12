package netfilter

import (
	"errors"
	"strings"
	"testing"
)

// Read off the device 2026-09-12, with our rules present. Note the ping rule:
// it was INSERTED as `--icmp-type echo-request` and reads back as `-m icmp
// --icmp-type 8`. That rewrite is the reason Missing matches on fields.
const listingHealthy = `-P INPUT DROP
-A INPUT -i wlan0 -p icmp -m icmp --icmp-type 8 -j ACCEPT
-A INPUT -i wlan0 -p tcp -m tcp --dport 36000 -j ACCEPT
-A INPUT -i wlan0 -p udp -m udp --dport 6001:6010 -j ACCEPT
-A INPUT -i wlan0 -p tcp -m tcp --dport 5000 -j ACCEPT
-A INPUT -i wlan0 -p udp -m udp --dport 319 -j ACCEPT
-A INPUT -i wlan0 -p udp -m udp --dport 320 -j ACCEPT
-A INPUT -i wlan0 -p tcp -m state --state RELATED,ESTABLISHED -j ACCEPT
-A INPUT -i wlan0 -p udp -m udp --dport 5353 -j ACCEPT
-A INPUT -i wlan0 -p tcp -m tcp --dport 4070 -j ACCEPT
-A INPUT -i wlan0 -p udp -m udp --dport 5000 -j ACCEPT
-A INPUT -i lo -j ACCEPT
`

// The same chain 39 minutes later: every rule is Amazon's, none is ours, and
// `policy DROP` had counted 137 packets. This is the state the reconcile
// exists to find.
const listingStripped = `-P INPUT DROP
-A INPUT -i wlan0 -p tcp -m state --state RELATED,ESTABLISHED -j ACCEPT
-A INPUT -i wlan0 -p udp -m state --state ESTABLISHED -j ACCEPT
-A INPUT -i wlan0 -p tcp -m tcp --dport 4070 -j ACCEPT
-A INPUT -i wlan0 -p udp -m udp --dport 16384:32767 -j ACCEPT
-A INPUT -i wlan0 -p udp -m udp --dport 5353 -j ACCEPT
-A INPUT -p icmp -m state --state RELATED,ESTABLISHED -j ACCEPT
-A INPUT -i wlan0 -p udp -m udp --dport 5000 -j ACCEPT
-A INPUT -i lo -j ACCEPT
`

func TestNothingIsMissingFromTheHealthyCapture(t *testing.T) {
	if got := Missing(listingHealthy, All()); len(got) != 0 {
		t.Fatalf("reported %d rules missing from a chain that has them all: %v",
			len(got), got)
	}
}

// The bug this whole file is for: our rules are gone and Amazon's remain.
func TestEveryRuleIsMissingFromTheStrippedCapture(t *testing.T) {
	want := SpotifyRules()
	want = append(want, AirPlayRules()...)
	want = append(want, PingRule())
	got := Missing(listingStripped, want)
	if len(got) != len(want) {
		t.Fatalf("reported %d of %d rules missing; the capture has none of them",
			len(got), len(want))
	}
}

// The trap, pinned on its own. iptables stores echo-request as type 8, so a
// string comparison against our own spec would report this rule missing on
// every single tick — a reconcile that never stops reconciling, and never
// says so.
func TestPingRuleMatchesTheRewrittenIcmpType(t *testing.T) {
	if !present(listingHealthy, PingRule()) {
		t.Fatal("the ping rule reads back as `-m icmp --icmp-type 8` and was " +
			"not recognised — the reconcile would repair it for ever")
	}
	// And the spelling we asked for, in case an iptables prints it back.
	named := "-A INPUT -i wlan0 -p icmp -m icmp --icmp-type echo-request -j ACCEPT\n"
	if !present(named, PingRule()) {
		t.Fatal("the named spelling was not recognised")
	}
}

// Amazon's own UDP 5000 rule must not satisfy our TCP 5000 rule. That exact
// confusion is recorded in the package comment as the thing that sends a
// reader away satisfied.
func TestAmazonsUdp5000DoesNotSatisfyOurTcp5000(t *testing.T) {
	only := "-A INPUT -i wlan0 -p udp -m udp --dport 5000 -j ACCEPT\n"
	tcp5000 := Rule{Proto: "tcp", Port: "5000", Why: "AirPlay RTSP"}
	if present(only, tcp5000) {
		t.Fatal("Amazon's UDP 5000 was counted as our TCP 5000")
	}
}

// A port must match as a destination, not merely appear on the line.
func TestSourcePortDoesNotSatisfyADestinationRule(t *testing.T) {
	line := "-A INPUT -i wlan0 -p udp -m udp --sport 6001 --dport 9999 -j ACCEPT\n"
	r := Rule{Proto: "udp", Port: "6001", Why: "AirPlay RTP"}
	if present(line, r) {
		t.Fatal("a source port satisfied a --dport rule")
	}
}

// A rule on a different interface is not ours: the device has wlan0 and p2p0,
// and opening a port on the wrong one leaves it shut where it matters.
func TestOtherInterfaceDoesNotCount(t *testing.T) {
	line := "-A INPUT -i p2p0 -p tcp -m tcp --dport 5000 -j ACCEPT\n"
	if present(line, Rule{Proto: "tcp", Port: "5000"}) {
		t.Fatal("a rule on p2p0 was counted as a rule on wlan0")
	}
}

// A DROP target with our ports on it is the opposite of what we want.
func TestNonAcceptTargetDoesNotCount(t *testing.T) {
	line := "-A INPUT -i wlan0 -p tcp -m tcp --dport 5000 -j DROP\n"
	if present(line, Rule{Proto: "tcp", Port: "5000"}) {
		t.Fatal("a DROP rule satisfied an ACCEPT rule")
	}
}

// ---- Reconcile ----

func TestReconcileIsSilentAndCheapWhenNothingIsMissing(t *testing.T) {
	calls := 0
	run := func(args ...string) error { calls++; return nil }
	list := func() (string, error) { return listingHealthy, nil }

	if n := Reconcile(list, run, All()); n != 0 {
		t.Fatalf("reported %d missing from a healthy chain", n)
	}
	if calls != 0 {
		t.Fatalf("ran iptables %d times on a healthy chain — the whole point "+
			"is that a tick costs one listing and nothing else", calls)
	}
}

func TestReconcileReappliesWhenRulesAreGone(t *testing.T) {
	var args []string
	run := func(a ...string) error {
		args = append(args, strings.Join(a, " "))
		return nil
	}
	list := func() (string, error) { return listingStripped, nil }

	want := SpotifyRules()
	if n := Reconcile(list, run, want); n != 1 {
		t.Fatalf("reported %d missing, want 1", n)
	}
	var inserted bool
	for _, a := range args {
		if strings.HasPrefix(a, "-I INPUT") && strings.Contains(a, "36000") {
			inserted = true
		}
	}
	if !inserted {
		t.Fatalf("never inserted the missing rule; ran: %v", args)
	}
}

// Failure to LOOK is not evidence of absence. A procfs or binary that moved
// would otherwise mean a full Sync on every tick for the life of the process.
func TestUnreadableListingDoesNothing(t *testing.T) {
	calls := 0
	run := func(args ...string) error { calls++; return nil }
	list := func() (string, error) { return "", errors.New("no iptables") }

	if n := Reconcile(list, run, All()); n != 0 {
		t.Fatalf("reported %d missing from a listing it could not read", n)
	}
	if calls != 0 {
		t.Fatalf("ran iptables %d times after failing to read the chain", calls)
	}
}

func TestReconcileWithNoRunnerOrListerIsHarmless(t *testing.T) {
	if Reconcile(nil, func(...string) error { return nil }, All()) != 0 {
		t.Error("acted with no lister")
	}
	if Reconcile(func() (string, error) { return "", nil }, nil, All()) != 0 {
		t.Error("acted with no runner")
	}
}
