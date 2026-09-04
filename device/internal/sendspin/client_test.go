package sendspin

import (
	"testing"
	"time"
)

// The supervisor is driven by the config push, which re-sends the whole
// config on every controller reconnect — so Start and Stop are called
// repeatedly on a client that is already in that state, and both have to be
// no-ops there. These run without a network: nothing here reaches session().

func newTestClient() *Client {
	return NewClient(Options{TimeSyncInterval: time.Hour}, "",
		&fakeSink{}, &fakePlane{})
}

func TestStartIsIdempotent(t *testing.T) {
	c := newTestClient()
	c.Start()
	c.Start()
	c.Start()
	if !c.Running() {
		t.Fatal("not running after Start")
	}
	c.Stop(GoodbyeUserRequest)
	if c.Running() {
		t.Fatal("still running after Stop")
	}
}

func TestStopBeforeStartDoesNothing(t *testing.T) {
	// The connect-time config push arrives with the setting off, on a client
	// that has never started.
	c := newTestClient()
	c.Stop(GoodbyeUserRequest)
	if c.Running() {
		t.Fatal("Stop started something")
	}
}

func TestStopAndStartAgainWorks(t *testing.T) {
	// Somebody toggles the switch in Home Assistant twice.
	c := newTestClient()
	c.Start()
	c.Stop(GoodbyeUserRequest)
	c.Start()
	if !c.Running() {
		t.Fatal("the client did not come back")
	}
	c.Stop(GoodbyeShutdown)
}

func TestLeaveWithNoSessionIsSafe(t *testing.T) {
	// The arbiter holds this callback and calls it on a preemption, which
	// can land between sessions — the supervisor is mid-backoff, or the
	// client was never started at all.
	c := newTestClient()
	c.Leave("preempted")
	c.Start()
	c.Leave("preempted")
	c.Stop(GoodbyeUserRequest)
}

func TestLeavingDoesNotStopTheSupervisor(t *testing.T) {
	// The plane may come back — Home Assistant's track ends — and the
	// backoff loop reconnects. What it must NOT do is rejoin the group by
	// itself: reconnecting is not rejoining, and that is the server's
	// decision once this client reappears.
	c := newTestClient()
	c.Start()
	c.Leave("preempted")
	if !c.Running() {
		t.Fatal("a preemption stopped the supervisor — the device would " +
			"never come back when the plane was free again")
	}
	c.Stop(GoodbyeUserRequest)
}

func TestTheBackoffIsBoundedAtBothEnds(t *testing.T) {
	// The floor is short because a Music Assistant restart is the common
	// cause and the group is waiting; the ceiling is long because a server
	// that is simply not installed must not have this device browsing
	// forever. Reconnecting is the normal case on links measured at
	// 4.6–7.1% loss, so it also has to be quiet.
	if reconnectMin < time.Second {
		t.Fatalf("reconnect floor %s would browse in a tight loop", reconnectMin)
	}
	if reconnectMin > 10*time.Second {
		t.Fatalf("reconnect floor %s is a long time to be silent after a "+
			"server restart", reconnectMin)
	}
	if reconnectMax < reconnectMin {
		t.Fatal("the backoff ceiling is below its floor")
	}
	if reconnectMax > 10*time.Minute {
		t.Fatalf("reconnect ceiling %s is longer than anyone will wait "+
			"before deciding the feature is broken", reconnectMax)
	}
}
