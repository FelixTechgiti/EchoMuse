package mcast

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The packet is the one thing that can ruin this instrument silently: a
// malformed query is ignored by every responder, every probe reads zero, and
// the device is reported deaf for ever while hearing perfectly. So the bytes
// are pinned rather than eyeballed.
func TestQueryIsAWellFormedUnicastMdnsQuestion(t *testing.T) {
	q, err := Query("_services._dns-sd._udp.local")
	if err != nil {
		t.Fatal(err)
	}
	if len(q) < 12 {
		t.Fatalf("query is %d bytes, shorter than a DNS header", len(q))
	}
	if q[2]&0x80 != 0 {
		t.Error("the QR bit is set — this is a response, not a question")
	}
	if q[4] != 0 || q[5] != 1 {
		t.Errorf("QDCOUNT is %d%d, want exactly one question", q[4], q[5])
	}
	for _, c := range [][2]byte{{q[6], q[7]}, {q[8], q[9]}, {q[10], q[11]}} {
		if c[0] != 0 || c[1] != 0 {
			t.Error("an answer/authority/additional count is non-zero in a question")
		}
	}
	// Labels, then root, then QTYPE PTR, then QCLASS IN with the QU bit. The
	// QU bit is what lets this bind an ephemeral port instead of 5353, which is
	// the whole reason the probe cannot disturb the responders it measures.
	tail := q[len(q)-5:]
	if tail[0] != 0 {
		t.Error("the name does not end with the root label")
	}
	if tail[1] != 0 || tail[2] != 12 {
		t.Errorf("QTYPE is %d%d, want 12 (PTR)", tail[1], tail[2])
	}
	if tail[3] != 0x80 || tail[4] != 1 {
		t.Errorf("QCLASS is %#x%#x, want IN with the unicast-response bit set",
			tail[3], tail[4])
	}
	// The labels have to be there in length-prefixed form, or the question is
	// for a different name than the one asked for.
	if !strings.Contains(string(q), "_services") ||
		!strings.Contains(string(q), "local") {
		t.Error("the question does not carry the name it was given")
	}
}

// A name that cannot be encoded must be refused, not encoded wrongly. A query
// that no responder answers is indistinguishable from a deaf device.
func TestUnusableNamesAreRefused(t *testing.T) {
	for _, n := range []string{"", "..", "a..b", strings.Repeat("x", 64) + ".local"} {
		if _, err := Query(n); err == nil {
			t.Errorf("%q was accepted as an mDNS name", n)
		}
	}
}

func TestOnlyResponsesCount(t *testing.T) {
	if IsResponse([]byte{0, 0, 0x84, 0}) {
		t.Error("a four-byte datagram was read as a response")
	}
	q, _ := Query("_x._tcp.local")
	if IsResponse(q) {
		t.Error("our own question was read as a response")
	}
	resp := append([]byte(nil), q...)
	resp[2] |= 0x80
	if !IsResponse(resp) {
		t.Error("a packet with the QR bit set was not read as a response")
	}
}

// THE measurement. The fault reading had a complete, correct answer in it —
// from the device itself — and that is exactly why every on-device check looked
// healthy through the outage. A tracker that counted its own responder would
// never fire.
func TestItsOwnReplyIsNotAPeer(t *testing.T) {
	var tr Tracker
	now := time.Now()
	// Four answers from itself, nothing from the link: the 2026-09-12 reading.
	ev, _ := tr.Observe(now, Reading{Peers: 0, Self: 4})
	if ev != EventNone {
		t.Fatalf("the first silent probe already fired %v", ev)
	}
	ev, out := tr.Observe(now.Add(time.Minute), Reading{Peers: 0, Self: 4})
	if ev != EventDeaf {
		t.Fatalf("four replies from itself did not read as deaf (%v)", ev)
	}
	if out != time.Minute {
		t.Errorf("the outage was dated at %s, want 1m — it is measured from the "+
			"FIRST silent probe, not from the one that crossed the threshold", out)
	}
}

// One lost query is ordinary on WiFi and multicast is unacknowledged by design.
// Hearing something, by contrast, is unambiguous.
func TestOneSilentProbeIsNotDeafnessButOneReplyIsHearing(t *testing.T) {
	var tr Tracker
	now := time.Now()
	if ev, _ := tr.Observe(now, Reading{}); ev != EventNone {
		t.Fatal("a single lost probe declared the device deaf")
	}
	if ev, _ := tr.Observe(now.Add(time.Second), Reading{Peers: 1}); ev != EventNone {
		t.Fatal("recovering from a state we never entered produced an event")
	}
	if tr.Deaf() {
		t.Error("still deaf after hearing a host")
	}
}

// Failure to look is not evidence of absence — the rule the membership watcher
// and the wake-word reconcile both follow. A socket that could not be opened
// must not date an outage.
func TestAFailedProbeIsNotSilence(t *testing.T) {
	var tr Tracker
	now := time.Now()
	for i := 0; i < 10; i++ {
		if ev, _ := tr.Observe(now.Add(time.Duration(i)*time.Minute),
			Reading{Err: errors.New("no route to host")}); ev != EventNone {
			t.Fatalf("a failed probe produced %v", ev)
		}
	}
	if tr.Deaf() {
		t.Error("ten failures to look were counted as an outage")
	}
}

func TestTheOutageIsDatedFromItsFirstSilentProbe(t *testing.T) {
	var tr Tracker
	t0 := time.Unix(1_700_000_000, 0)
	tr.Observe(t0, Reading{Peers: 6})
	tr.Observe(t0.Add(1*time.Minute), Reading{})
	tr.Observe(t0.Add(2*time.Minute), Reading{})  // deaf here
	tr.Observe(t0.Add(20*time.Minute), Reading{}) // still deaf, no second event
	ev, out := tr.Observe(t0.Add(31*time.Minute), Reading{Peers: 6})
	if ev != EventHeard {
		t.Fatalf("recovery produced %v", ev)
	}
	if out != 30*time.Minute {
		t.Errorf("the outage measured %s, want 30m from the first silent probe", out)
	}
	if tr.Episodes() != 1 {
		t.Errorf("%d episodes recorded for one outage", tr.Episodes())
	}
}

// Deafness is reported ONCE. A device that stays deaf for hours must not put a
// warning on the liveness channel every minute — that is the shape #404 was.
func TestDeafnessIsReportedOnce(t *testing.T) {
	var tr Tracker
	now := time.Now()
	fired := 0
	for i := 0; i < 40; i++ {
		if ev, _ := tr.Observe(now.Add(time.Duration(i)*time.Minute), Reading{}); ev == EventDeaf {
			fired++
		}
	}
	if fired != 1 {
		t.Errorf("a single outage produced %d deaf events", fired)
	}
}

// The cadence is the part somebody else's network pays for. Healthy must be the
// slow one; silent must be the fast one, because the duration is measured from
// the recovery.
func TestTheSilentCadenceIsTheFastOne(t *testing.T) {
	if SilentInterval >= HealthyInterval {
		t.Fatalf("silent=%s is not faster than healthy=%s", SilentInterval, HealthyInterval)
	}
	var tr Tracker
	if tr.Interval() != HealthyInterval {
		t.Errorf("a device that has heard nothing yet probes at %s", tr.Interval())
	}
	now := time.Now()
	tr.Observe(now, Reading{})
	tr.Observe(now.Add(time.Minute), Reading{})
	if !tr.Deaf() || tr.Interval() != SilentInterval {
		t.Errorf("a deaf device probes at %s, want %s", tr.Interval(), SilentInterval)
	}
}

func TestEpisodesCountsEachOutageSeparately(t *testing.T) {
	var tr Tracker
	now := time.Now()
	step := func(r Reading) { now = now.Add(time.Minute); tr.Observe(now, r) }
	for i := 0; i < 2; i++ {
		step(Reading{})
		step(Reading{})
		step(Reading{Peers: 3})
	}
	if tr.Episodes() != 2 {
		t.Errorf("%d episodes for two outages", tr.Episodes())
	}
	last, peers := tr.LastHeard()
	if peers != 3 || last.IsZero() {
		t.Errorf("LastHeard reports %d host(s) at %v", peers, last)
	}
}
