package netfilter

import "testing"

// A real capture, read off G090L91180250AN1 on 2026-09-12 while the device was
// wrongly reporting that it could hear nothing. The 5353 row is the whole point:
// 93,704 packets accepted, at the same moment the active probe said zero.
const countedCapture = `Chain INPUT (policy DROP 1237 packets, 239K bytes)
 pkts bytes target     prot opt in     out     source               destination
    0     0 ACCEPT     icmp --  wlan0  *       0.0.0.0/0            0.0.0.0/0            icmptype 8
    4   216 ACCEPT     tcp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            tcp dpt:5000
    6   565 ACCEPT     tcp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            tcp dpt:36000
 212000 146M ACCEPT     tcp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            state RELATED,ESTABLISHED
  475 83261 ACCEPT     udp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            state ESTABLISHED
 3054  150K ACCEPT     udp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            udp dpts:16384:32767
  257 50661 ACCEPT     udp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            udp dpt:1900
93704   14M ACCEPT     udp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            udp dpt:5353
    0     0 ACCEPT     udp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            udp dpt:5000
  886 50074 ACCEPT     all  --  lo     *       0.0.0.0/0            0.0.0.0/0
`

func TestTheMdnsCounterIsReadOffARealCapture(t *testing.T) {
	got, ok := PacketsFor(countedCapture, "udp", "5353", "wlan0")
	if !ok {
		t.Fatal("no 5353 rule found in a capture that has one")
	}
	if got != 93704 {
		t.Errorf("read %d packets, want 93704", got)
	}
}

// The reading that mattered: udp/5000 is Amazon's and ours is tcp/5000. A
// parser that ignored the protocol would report AirPlay's control port as
// silent, since the Amazon rule has never matched anything.
func TestProtocolSeparatesTheTwo5000Rules(t *testing.T) {
	tcp, ok := PacketsFor(countedCapture, "tcp", "5000", "wlan0")
	if !ok || tcp != 4 {
		t.Errorf("tcp/5000 = %d (found=%v), want 4", tcp, ok)
	}
	udp, ok := PacketsFor(countedCapture, "udp", "5000", "wlan0")
	if !ok || udp != 0 {
		t.Errorf("udp/5000 = %d (found=%v), want 0", udp, ok)
	}
}

// A range that happens to end at the port is a different rule, and so is a
// source port. Both would silently inflate the count.
func TestARangeOrSourcePortIsNotThisPort(t *testing.T) {
	listing := ` pkts bytes target     prot opt in     out     source               destination
 9999     0 ACCEPT     udp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            udp dpts:5000:5353
 8888     0 ACCEPT     udp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            udp spt:5353
`
	if n, ok := PacketsFor(listing, "udp", "5353", "wlan0"); ok || n != 0 {
		t.Errorf("matched %d packets against a range and a source port", n)
	}
}

// Absent is distinguishable from zero, because they want opposite responses:
// a rule at zero is a real measurement, a missing rule means the firewall is
// not in the state we think and the reading says nothing.
func TestAMissingRuleIsNotAZeroReading(t *testing.T) {
	if n, ok := PacketsFor(countedCapture, "udp", "9999", "wlan0"); ok || n != 0 {
		t.Errorf("a port with no rule read as %d (found=%v)", n, ok)
	}
}

func TestAnotherInterfaceDoesNotCount(t *testing.T) {
	if _, ok := PacketsFor(countedCapture, "udp", "5353", "eth0"); ok {
		t.Error("wlan0's rule was credited to eth0")
	}
}

// Duplicates are summed. An older build could leave two rules for one port —
// the case Reconcile repairs — and first-match would make half the traffic
// invisible until it did.
func TestDuplicateRulesAreSummed(t *testing.T) {
	listing := countedCapture +
		"  296     0 ACCEPT     udp  --  wlan0  *       0.0.0.0/0            0.0.0.0/0            udp dpt:5353\n"
	if n, _ := PacketsFor(listing, "udp", "5353", "wlan0"); n != 93704+296 {
		t.Errorf("summed to %d, want %d", n, 93704+296)
	}
}

// The header rows must not parse as rules, and neither must an empty listing.
func TestHeadersAndEmptyInputAreNotRules(t *testing.T) {
	for _, s := range []string{"", "\n\n", "Chain INPUT (policy DROP 1237 packets, 239K bytes)\n"} {
		if _, ok := PacketsFor(s, "udp", "5353", "wlan0"); ok {
			t.Errorf("found a rule in %q", s)
		}
	}
}
