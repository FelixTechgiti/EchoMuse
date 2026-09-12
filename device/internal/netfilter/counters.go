package netfilter

import (
	"os/exec"
	"strconv"
	"strings"
)

// Reading the chain's own packet counters, and why that is the right
// instrument for "is anything arriving".
//
// # The measurement this replaces
//
// `internal/mcast` used to answer "can this device hear the LAN" by SENDING an
// mDNS query from an ephemeral port with the unicast-response bit set and
// counting who answered. On this platform that measures the firewall rather
// than the network, and it measured it wrong: the replies come from foreign
// unicast addresses to a port no rule names, `-m state --state ESTABLISHED`
// does not match them (the outgoing packet went to 224.0.0.251, the reply comes
// from 192.168.178.x — two different flows to conntrack), and FireOS's
// `-P INPUT DROP` takes them. So the probe reported "this device hears nobody"
// on a device whose own 5353 rule had accepted **93,704 packets**, measured
// 2026-09-12 on hardware while that warning was in the log.
//
// The counter has none of that failure mode, because it IS the firewall: a rule
// that accepted a packet counted it. It also costs one exec and puts nothing on
// anybody's network, where the probe asked every host on the link to answer.
//
// # The one way a counter lies, and it is handled
//
// Counters are per-RULE and reset when the rule is re-inserted — which
// `Reconcile` and every config push do, since this package deletes and
// re-inserts rather than trusting `-C`. A count LOWER than the last one is
// therefore "the rule was replaced", never "no traffic", and the caller has to
// re-baseline rather than read it as silence.

// CountInput returns the INPUT chain with packet counters, as
// `iptables -L INPUT -v -x -n` prints it.
//
// `-n` matters: without it iptables resolves every address through DNS, which
// on a device whose fault may BE the network is a diagnostic that hangs.
func CountInput() (string, error) {
	if binary == "" {
		return "", ErrUnavailable
	}
	out, err := exec.Command(binary, "-L", "INPUT", "-v", "-x", "-n").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// PacketsFor totals the packets accepted by every rule matching proto, port and
// interface. Pure, so the parsing is testable against a real capture.
//
// Summed rather than first-match, because nothing forbids two rules for one
// port — a duplicate left by an older build is a case `Reconcile` exists to
// repair, and until it does, half the traffic would be invisible.
//
// The columns are positional and the tail is not:
//
//	pkts bytes target prot opt in out source destination  [match options]
//	93704  14M ACCEPT udp  --  wlan0 * 0.0.0.0/0 0.0.0.0/0 udp dpt:5353
//
// so the fixed part is read by index and the port out of the tail, the same
// split `Missing` uses and for the same reason: iptables rewrites what it was
// given, and only the fields survive that.
func PacketsFor(listing, proto, port, iface string) (int64, bool) {
	var total int64
	var found bool
	want := "dpt:" + port
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) < 9 {
			continue
		}
		n, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue // the header rows, and anything else that is not a rule
		}
		if f[2] != "ACCEPT" || f[3] != proto || f[5] != iface {
			continue
		}
		if !hasExact(f[9:], want) {
			continue
		}
		total += n
		found = true
	}
	return total, found
}

// hasExact looks for the option as a whole field. `dpt:5353` must not be
// satisfied by `dpts:5000:5353` or by `spt:5353` — a source port is the other
// direction, and a range that happens to end here is a different rule. Same
// adjacency care as `hasOpt`.
func hasExact(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}
