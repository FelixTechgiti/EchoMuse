package netfilter

import (
	"log"
	"os/exec"
	"strings"
)

// Reconcile exists because the rules DO NOT SURVIVE, and nothing noticed.
//
// Measured on a live device 2026-09-12. The firmware wrote them at startup
// and said so:
//
//	15:01:29 [netfilter] opened INPUT -i wlan0 -p tcp -m tcp --dport 5000 -j ACCEPT
//	15:01:29 [netfilter] opened INPUT -i wlan0 -p udp -m udp --dport 6001:6010 -j ACCEPT
//
// Thirty-nine minutes later the INPUT chain held nineteen rules, every one of
// them Amazon's, and none of ours — while `policy DROP` had counted 137
// packets. Android's netd rebuilds the filter table on network events and
// keeps only what it wrote itself, so anything we insert is transient and its
// lifetime is however long the radio stays quiet.
//
// `Sync` runs at startup and on each config push (`applyFirewall`), and those
// can be hours apart. In between, a device announces services it cannot be
// reached on — which is exactly the "advertised is not reachable" fault this
// whole package exists to end, arriving by a second route.
//
// This is the same shape as `reconcileJackRouting`, and for the same reason:
// Android writes state back underneath us, so the only durable answer is to
// keep looking.
//
// # Why a listing rather than just re-running Sync
//
// `Sync` is not free. `ensure` deletes every copy of a rule before inserting
// one, so a healthy table still costs two execs per rule — roughly nine
// fork/execs for the current set, on a board sharing 512MB with Android. At a
// tick that is a permanent cost paid to discover that nothing is wrong, and
// this project has that lesson written down already: the speaker's `stop
// media` nudge was justified as "four fork/execs on a path that runs once"
// right up until the retry loop made it run for ever.
//
// One `iptables -S INPUT` answers the same question in one exec, and Sync
// runs only when the answer is "something is gone".
//
// **Note this is NOT `-C`.** The package refuses `-C` deliberately: it is a
// check whose failure mode is silent and unverifiable here. Reading a listing
// and matching it ourselves has neither property — the text is in the log when
// it goes wrong, and `Missing` is a pure function with tests.

// Lister returns the current INPUT chain as `iptables -S` prints it.
// Injected for the same reason Runner is: the decision has to be testable on
// a host with no firewall.
type Lister func() (string, error)

// ListInput is the real lister.
func ListInput() (string, error) {
	if binary == "" {
		return "", ErrUnavailable
	}
	out, err := exec.Command(binary, "-S", "INPUT").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Missing reports which of `want` the listing does not contain.
//
// **Matching is by FIELDS, not by string, because iptables rewrites the rule
// it was given.** Measured on the device, the same afternoon: a rule inserted
// as
//
//	-i wlan0 -p icmp --icmp-type echo-request -j ACCEPT
//
// reads back from `-S` as
//
//	-A INPUT -i wlan0 -p icmp -m icmp --icmp-type 8 -j ACCEPT
//
// — the match extension is added and the type name is resolved to its number.
// A `strings.Contains(listing, r.spec())` would therefore report the ping rule
// missing on every tick for ever, and the reconcile would spend a full Sync
// each time to "repair" a rule that was already there. It would work, and it
// would quietly never stop working.
//
// So each rule is matched on what actually distinguishes it: the interface,
// the protocol, the destination port (or the ICMP type, under either
// spelling), and that the target is ACCEPT.
func Missing(listing string, want []Rule) []Rule {
	var out []Rule
	for _, r := range want {
		if !present(listing, r) {
			out = append(out, r)
		}
	}
	return out
}

func present(listing string, r Rule) bool {
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 2 || f[0] != "-A" || f[1] != "INPUT" {
			continue
		}
		if !hasOpt(f, "-i", iface) || !hasOpt(f, "-p", r.Proto) ||
			!hasOpt(f, "-j", "ACCEPT") {
			continue
		}
		if r.Proto == "icmp" {
			// Either spelling: 8 is what iptables stores, echo-request is
			// what we asked for, and a busybox iptables that does not
			// resolve it would print the name back.
			if hasOpt(f, "--icmp-type", "8") ||
				hasOpt(f, "--icmp-type", "echo-request") {
				return true
			}
			continue
		}
		if r.Port == "" || hasOpt(f, "--dport", r.Port) {
			return true
		}
	}
	return false
}

// hasOpt reports whether `name val` appears as an adjacent pair. Adjacency
// rather than Contains, so `--dport 5000` cannot be satisfied by a line whose
// only 5000 is a source port — Amazon's own table has both.
func hasOpt(fields []string, name, val string) bool {
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == name && fields[i+1] == val {
			return true
		}
	}
	return false
}

// Reconcile re-applies `want` if any of it has gone, and reports how many
// rules were missing.
//
// Silent when nothing is wrong, because this runs on a ticker and a device
// that is behaving must not fill the log relay's ration — six lines a minute,
// shared with every other subsystem. Loud exactly once per repair, with the
// count, because that line is the only evidence anybody will ever have of how
// often the table is being rebuilt underneath us.
//
// A listing that cannot be read is NOT treated as "everything is missing".
// Failure to look is not evidence of absence — the same rule reconcile-on-
// connect follows for the wake-word assets — and the cost of being wrong here
// is a full Sync against a firewall that is simply not there, on every tick,
// for the life of the process.
func Reconcile(list Lister, run Runner, want []Rule) int {
	if list == nil || run == nil {
		return 0
	}
	listing, err := list()
	if err != nil {
		return 0
	}
	gone := Missing(listing, want)
	if len(gone) == 0 {
		return 0
	}
	log.Printf("[netfilter] %d of %d rules had been removed from INPUT — "+
		"re-applying (first: %s)", len(gone), len(want), gone[0])
	Sync(run, want)
	return len(gone)
}
