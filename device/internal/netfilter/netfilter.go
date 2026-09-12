// Package netfilter opens the device's own ports in FireOS's firewall.
//
// **FireOS ships a default-deny INPUT policy with an allowlist of Amazon's
// own ports, and ours are not on it.** Read off a live device 2026-09-12:
//
//	-P INPUT DROP
//	-A INPUT -i wlan0 -p tcp -m state --state RELATED,ESTABLISHED -j ACCEPT
//	-A INPUT -i wlan0 -p udp -m udp --dport 5353 -j ACCEPT      <- mDNS
//	-A INPUT -i wlan0 -p tcp -m tcp --dport 4070 -j ACCEPT      <- Alexa
//	-A INPUT -i wlan0 -p udp -m udp --dport 5000 -j ACCEPT      <- UDP, not TCP
//	-A INPUT -p icmp -m state --state RELATED,ESTABLISHED -j ACCEPT
//	... policy DROP 605 packets, 89226 bytes
//
// That one page explains everything #77 spent weeks on. mDNS is allowed, so
// the announcements go out and are heard and every on-device measurement
// looks perfect. ESTABLISHED is allowed, so the three control planes — which
// the DEVICE dials — work faultlessly. And a phone answering that
// advertisement is a NEW inbound connection, which is dropped: Spotify
// Connect and AirPlay both need the phone to call the speaker.
//
// Note `udp dpt:5000` in that list. Amazon opened UDP 5000 for something of
// their own; shairport-sync's RTSP is **TCP** 5000, so even the port that
// looks allowed is not the one we need. A rule read at a glance would have
// sent the next person away satisfied.
//
// # Why iptables here rather than somewhere else
//
// This is the Linux interface, not an Android one — `iptables` is the kernel's
// own, and the project's direction says to prefer exactly that. Under emOS
// there is no such firewall at all, so `Sync` finds nothing to do and says so
// once. Nothing here is FireOS-specific except the reason it is needed.
//
// # The rules this may write, and the ones it must never
//
// A firewall on a device whose ONLY management path is the network is a
// loaded gun. Three constraints, all enforced by test:
//
//   - **Only `-I INPUT` and `-D INPUT`, with a fully specified rule.** Never
//     `-P` (a policy change can strand the device for good), never `-F`
//     (flushing takes Amazon's allowlist with it, and the control plane's
//     ESTABLISHED rule with that), never `-X`.
//   - **Every rule names an interface and a port.** A bare ACCEPT is not a
//     fix, it is the removal of a firewall.
//   - **Idempotent.** `-C` before `-I`, because this runs on every start and
//     on every config push, and a duplicate rule per reconnect is a table
//     that grows for the life of the device.
//
// # And the ports have to be PINNED, or no rule can match
//
// librespot picks a random zeroconf port per start and shairport-sync uses
// its own defaults. A firewall rule cannot be written against a number that
// changes, so both are pinned here and passed to the daemons from these same
// constants — one definition, so the rule and the listener cannot drift.
package netfilter

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// The interface the device is reachable on. Amazon's own rules are scoped to
// it; ours are too, so a rule can never widen anything on another link.
const iface = "wlan0"

// Ports we pin so a rule can name them. Changing one means changing the
// daemon's argument as well — they are read from here, which is the point.
const (
	// SpotifyZeroconfPort is librespot's discovery HTTP listener
	// (`--zeroconf-port`). Random by default, which is unfirewallable.
	SpotifyZeroconfPort = 36000

	// AirPlayRTSPPort is shairport-sync's control port. 5000 is the
	// conventional AirPlay port and shairport's own default; clients take
	// the real number from the SRV record, so this is a choice rather than
	// a protocol constant.
	AirPlayRTSPPort = 5000

	// AirPlayUDPBase/Range is shairport-sync's RTP range (`udp_port_base`,
	// `udp_port_range`) — audio, control and timing. The control port alone
	// gets a session started and no sound out of it.
	AirPlayUDPBase  = 6001
	AirPlayUDPRange = 10

	// NqptpPortA/B are the PTP ports the AirPlay 2 clock daemon needs
	// INBOUND. Fixed by the protocol rather than chosen: 319 is event and
	// 320 is general, and nqptp requires exclusive use of both.
	//
	// Unlike everything else here they are not our numbers to pick, so a rule
	// naming them cannot drift from a daemon argument — there is no argument.
	NqptpPortA = 319
	NqptpPortB = 320
)

// Rule is one INPUT accept that belongs to us.
type Rule struct {
	Proto string // "tcp", "udp" or "icmp"
	Port  string // "5000", "6001:6010"; empty only for icmp
	Why   string // for the log line, so a reader knows whose port this is
}

// Runner executes one iptables invocation. Injected so the whole decision is
// testable without a firewall — the alternative is a package that can only be
// exercised on a rooted Echo, which is the one place a mistake here is
// expensive.
type Runner func(args ...string) error

// SpotifyRules / AirPlayRules are what each endpoint needs open.
func SpotifyRules() []Rule {
	return []Rule{{
		Proto: "tcp",
		Port:  fmt.Sprint(SpotifyZeroconfPort),
		Why:   "Spotify Connect discovery (librespot)",
	}}
}

func AirPlayRules() []Rule {
	return []Rule{
		{Proto: "tcp", Port: fmt.Sprint(AirPlayRTSPPort),
			Why: "AirPlay RTSP (shairport-sync)"},
		{Proto: "udp",
			Port: fmt.Sprintf("%d:%d", AirPlayUDPBase,
				AirPlayUDPBase+AirPlayUDPRange-1),
			Why: "AirPlay RTP audio/control/timing"},
	}
}

// NqptpRules are what the AirPlay 2 clock daemon needs open.
//
// Separate from AirPlayRules because it is a separate PROCESS with a separate
// installed-or-not answer: a device can have an AirPlay 2 shairport-sync and
// no nqptp, and opening PTP ports for a daemon that is not there is a hole
// with nothing behind it.
func NqptpRules() []Rule {
	return []Rule{
		{Proto: "udp", Port: fmt.Sprint(NqptpPortA),
			Why: "PTP event (nqptp, AirPlay 2)"},
		{Proto: "udp", Port: fmt.Sprint(NqptpPortB),
			Why: "PTP general (nqptp, AirPlay 2)"},
	}
}

// # AirPlay 2 needs inbound ports THAT CANNOT BE NAMED, and no rule here
// closes that gap
//
// Read off rtsp.c on 2026-09-12, and unchanged between 4.3.7 and 5.5.1, so it
// is not something a version bump fixes:
//
//	conn->local_event_port = 0;          // any port   (rtsp.c:3098, :3172)
//	conn->local_buffered_audio_port = 0; // any port   (rtsp.c:3363)
//
// For every AirPlay 2 session shairport-sync binds TWO additional TCP sockets
// on KERNEL-CHOSEN ephemeral ports, tells the client their numbers in the
// SETUP response, and waits for the client to connect IN. There is no config
// option for them. So on a default-DROP firewall an AirPlay 2 session
// negotiates and then stalls — which presents as a speaker that appears,
// accepts a connection and plays nothing, rather than as a firewall.
//
// The choices, none of which is free, and none of which should be made by
// whoever happens to be adding a rule:
//
//   - Open the ephemeral TCP range on wlan0 (`/proc/sys/net/ipv4/
//     ip_local_port_range`, typically 32768:61000). Broad, and it is a
//     speaker on somebody's home network — that is the owner's call.
//   - Narrow ip_local_port_range system-wide and firewall the small result.
//     Possible as root; it changes every other process on the device.
//   - Patch shairport-sync to take the two ports from a configured base. The
//     narrowest answer and the only one that stays narrow, at the cost of a
//     patch this build has so far avoided entirely.
//
// Nothing here implements any of them, deliberately. It is written down where
// the next person will look for it, which is more than can be said for how
// the whole default-DROP problem was found.

// PingRule lets the device answer a ping.
//
// FireOS accepts only RELATED,ESTABLISHED ICMP, so an echo request — which is
// NEW — is dropped, and `icmp_echo_ignore_all` is 0, meaning the kernel would
// have answered. That distinction cost an afternoon: a device that does not
// answer ping reads as "off the network", and this one was never off it.
// Being pingable is the cheapest diagnosis anyone has.
func PingRule() Rule {
	return Rule{Proto: "icmp", Why: "answer ping, so the device can be diagnosed"}
}

// spec is the fully-specified rule, without the -I/-C/-D verb.
func (r Rule) spec() []string {
	a := []string{"INPUT", "-i", iface, "-p", r.Proto}
	if r.Proto == "icmp" {
		a = append(a, "--icmp-type", "echo-request")
	} else if r.Port != "" {
		a = append(a, "-m", r.Proto, "--dport", r.Port)
	}
	return append(a, "-j", "ACCEPT")
}

// String is what the log shows, and it is the rule itself rather than a
// summary: somebody reading a support bundle has to be able to check it.
func (r Rule) String() string {
	return strings.Join(r.spec(), " ") + "   # " + r.Why
}

// Sync makes exactly `want` present and everything else this package knows
// about absent, so turning an endpoint off closes its port.
//
// Errors are logged, never returned: this runs beside the code that starts
// the endpoints, and a firewall that could not be adjusted must not stop a
// device from booting. The endpoint then runs unreachable, which is the
// behaviour every device had before this existed.
//
// **No iptables at all is an ORDINARY answer, not a failure.** emOS has no
// such firewall, and that is the whole of what it has to do here. It is said
// once per process, at info level, because the log relay forwards lines
// matching "could not" to the controller — so a per-rule complaint on every
// config push would put four warnings into somebody's Home Assistant log
// every reconnect, about a device that has nothing wrong with it.
func Sync(run Runner, want []Rule) {
	if run == nil {
		return
	}
	wanted := map[string]bool{}
	for _, r := range want {
		wanted[r.String()] = true
		if !ensure(run, r) {
			return
		}
	}
	for _, r := range All() {
		if !wanted[r.String()] {
			remove(run, r)
		}
	}
}

// All is every rule this package may ever write — the set Sync removes from.
// Listed explicitly rather than remembered across runs: the process restarts,
// and a rule we forgot we added is a port left open for a service that is off.
func All() []Rule {
	out := append([]Rule{}, SpotifyRules()...)
	out = append(out, AirPlayRules()...)
	return append(out, PingRule())
}

// maxDupes bounds the delete loop below. Reaching it would mean a table with
// eight copies of one rule, which is a fault of its own; stopping is better
// than looping against an iptables that answers success for ever.
const maxDupes = 8

// ensure makes the rule present EXACTLY ONCE, by deleting every copy and
// inserting one.
//
// The obvious implementation is `-C` then `-I`, and it was the first one. It
// rests on `-C` working, which is an assumption about a binary we do not ship
// and cannot test here: an iptables where `-C` is missing or answers wrongly
// turns "check, then insert" into "insert", on a path that runs at every
// start and every config push — a table that grows for the life of the
// device. Delete-then-insert needs only `-D`, which every iptables has had
// for ever, and it REPAIRS duplicates rather than merely not creating them.
//
// Reports false if the firewall is not there at all, so the caller stops
// rather than saying the same thing about every remaining rule.
func ensure(run Runner, r Rule) bool {
	had := drop(run, r)
	if err := run(append([]string{"-I"}, r.spec()...)...); err != nil {
		if errors.Is(err, ErrUnavailable) {
			noFirewallOnce.Do(func() {
				log.Printf("[netfilter] no iptables on this device — " +
					"nothing to open, which is normal under emOS")
			})
			return false
		}
		log.Printf("[netfilter] could not open %s/%s (%s): %v — the endpoint "+
			"will run but nothing on the network can reach it",
			r.Proto, r.Port, r.Why, err)
		return true
	}
	if had == 0 {
		log.Printf("[netfilter] opened %s", r)
	}
	return true
}

func remove(run Runner, r Rule) {
	if drop(run, r) > 0 {
		log.Printf("[netfilter] closed %s/%s (%s)", r.Proto, r.Port, r.Why)
	}
}

// drop deletes every copy of the rule and reports how many there were. A
// failing `-D` is the ordinary "it was not there" answer and is silent —
// every call site reaches this with the rule quite possibly absent.
func drop(run Runner, r Rule) int {
	n := 0
	for n < maxDupes {
		if err := run(append([]string{"-D"}, r.spec()...)...); err != nil {
			break
		}
		n++
	}
	return n
}

// ErrUnavailable says there is no iptables to talk to. Distinguished from a
// rule that would not apply, because the two want opposite responses: one is
// a device with no firewall and nothing to do, the other is a firewall that
// refused us and left a port shut.
var ErrUnavailable = errors.New("no iptables on this device")

var noFirewallOnce sync.Once

// binary resolves iptables ONCE, by absolute path first.
//
// Bare-name exec works for `tinymix` and `getprop` here, so /system/bin is on
// PATH — but this runs on a device where being wrong means a port stays shut
// with nothing said about it, and the absolute path removes the question.
// LookPath stays as the fallback so a platform that puts it elsewhere still
// works.
var binary = func() string {
	for _, p := range []string{
		"/system/bin/iptables",
		"/sbin/iptables",
		"/system/xbin/iptables",
		"/usr/sbin/iptables",
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("iptables"); err == nil {
		return p
	}
	return ""
}()

// Exec is the real runner.
func Exec(args ...string) error {
	if binary == "" {
		return ErrUnavailable
	}
	out, err := exec.Command(binary, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables %s: %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
