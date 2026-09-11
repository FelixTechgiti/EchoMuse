package client

import (
	"os"
	"strings"
	"testing"
)

// The behavioural fix lives in discovery.FindServerWith, and the whole of its
// value is that THIS file calls it. Reverting the call site to the browse-only
// FindServer leaves every other test green and silently restores a fault
// measured at four outages in one day, of up to 36m56s each, on a device that
// was reachable the whole time.
//
// A source guard rather than a behavioural one because the alternative is
// driving a reconnect loop that dials real sockets; what is worth pinning is
// which function the recovery path reaches for.
func TestTheReconnectLoopRetestsTheRememberedAddress(t *testing.T) {
	src, err := os.ReadFile("control.go")
	if err != nil {
		t.Fatalf("cannot read control.go: %v", err)
	}
	text := string(src)

	// Strip comments, or this matches the paragraph explaining the rule
	// rather than the code obeying it — the recurring source-guard trap.
	var code strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "//") {
			continue
		}
		code.WriteString(line)
		code.WriteString("\n")
	}
	body := code.String()

	if !strings.Contains(body, "discovery.FindServerWith(") {
		t.Error("the reconnect loop does not call discovery.FindServerWith — " +
			"one failed probe retires the unicast path for the whole outage again")
	}
	if strings.Contains(body, "discovery.FindServer(") {
		t.Error("the reconnect loop still calls the browse-only discovery.FindServer")
	}
	if !strings.Contains(body, "probeRecheckTimeout") {
		t.Error("the re-probe does not use probeRecheckTimeout, so its cost per " +
			"round is not the bounded one the constant documents")
	}
}
