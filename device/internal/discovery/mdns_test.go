package discovery

import (
	"context"
	"testing"
	"time"
)

// A browse round costs the full 10s mDNS timeout, so "returned in well under
// that" is the same assertion as "did not browse" — and it needs no network.
const browseIsSlowerThan = 3 * time.Second

func TestFindServerWithTakesTheRememberedAddressWithoutBrowsing(t *testing.T) {
	// The bug this covers: the caller probes the remembered controller ONCE,
	// and on failure handed the whole recovery to a browse-only loop that
	// does not return until multicast answers. The moment that probe is
	// guaranteed to fail is a controller RESTART — its listener is down for
	// the length of a container restart and back seconds later — so the
	// failure that cost the most was also the most recoverable one.
	//
	// Measured on the fleet 2026-09-11: four outages in one day, each
	// beginning within a minute of a controller restart, of 4m16s, 33m26s,
	// 38m5s and 36m56s, while the device held its address and the controller
	// was listening throughout.
	want := &ServerInfo{Host: "192.168.1.2", Port: 8767, Addr: "192.168.1.2:8767"}
	calls := 0

	start := time.Now()
	got, err := FindServerWith(context.Background(),
		func(context.Context) *ServerInfo {
			calls++
			return want
		})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("FindServerWith returned an error: %v", err)
	}
	if got != want {
		t.Fatalf("got %+v, want the remembered endpoint %+v", got, want)
	}
	if calls != 1 {
		t.Fatalf("recheck called %d times, want exactly 1", calls)
	}
	// The ordering is the point: the cheap test has to run BEFORE the
	// expensive one, or a working remembered address still costs a full
	// browse round on every reconnect.
	if elapsed >= browseIsSlowerThan {
		t.Fatalf("took %s — that is a browse round, so the recheck ran after "+
			"the browse rather than before it", elapsed)
	}
}

func TestFindServerWithKeepsBrowsingWhileTheRememberedAddressIsSilent(t *testing.T) {
	// A recheck that never answers must not short-circuit the search into
	// returning nil, and must not stop the loop: this is the ordinary case
	// where the controller really has moved, and the browse is the only way
	// to find it. The context deadline stands in for "eventually".
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	calls := 0
	got, err := FindServerWith(ctx, func(context.Context) *ServerInfo {
		calls++
		return nil
	})

	if err == nil {
		t.Fatalf("want the context error, got a server: %+v", got)
	}
	if calls == 0 {
		t.Fatal("the recheck was never called — the unicast path is dead again")
	}
}

func TestFindServerWithoutARecheckStillWorks(t *testing.T) {
	// FindServer delegates with a nil recheck, so nil must be a no-op rather
	// than a panic — every caller that has not been taught about the
	// remembered address keeps its old behaviour exactly.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, err := FindServerWith(ctx, nil); err == nil {
		t.Fatal("want the context error from a search with nothing to find")
	}
	if _, err := FindServer(ctx); err == nil {
		t.Fatal("FindServer must behave the same as FindServerWith(ctx, nil)")
	}
}
