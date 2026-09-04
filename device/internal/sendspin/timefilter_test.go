package sendspin

import (
	"math"
	"math/rand"
	"testing"
)

// simulate runs a synthetic exchange against a server clock that is `offset`
// µs ahead of ours and running `ppm` parts per million fast, over a link with
// the given one-way delay and jitter. Returns the filter after n rounds.
//
// A synthetic clock is the only way to test this at all: the quantity being
// estimated is unobservable on real hardware, so a live test can only check
// that the audio sounds right, which is what this filter exists to make true.
func simulate(t *testing.T, n int, offsetUs float64, ppm float64,
	oneWayUs float64, jitterUs float64, intervalUs float64) *Filter {
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	var f Filter
	local := float64(1_000_000)
	srvAt := func(l float64) float64 { return l + offsetUs + ppm*1e-6*l }

	for i := 0; i < n; i++ {
		up := oneWayUs + rng.Float64()*jitterUs
		down := oneWayUs + rng.Float64()*jitterUs
		t1 := local
		t2 := srvAt(local + up)
		t3 := t2 + 50 // server turnaround
		t4 := local + up + 50 + down
		f.Measure(int64(t1), int64(t2), int64(t3), int64(t4))
		local = t4 + intervalUs
	}
	return &f
}

func TestAFreshFilterConvertsNothing(t *testing.T) {
	// The spec forbids a player reporting available before it is synced,
	// and a converter that answers anyway is how that rule gets broken by
	// accident.
	var f Filter
	if f.Synced() {
		t.Fatal("a filter with no measurements claims to be synced")
	}
	if _, ok := f.ToLocal(123); ok {
		t.Fatal("converted a timestamp with no measurements")
	}
	f.Measure(1000, 5000, 5050, 1100)
	if f.Synced() {
		t.Fatal("one measurement is not a sync — drift is still unknown")
	}
}

func TestItFindsAStaticOffset(t *testing.T) {
	f := simulate(t, 200, 250_000, 0, 3000, 500, 1_000_000)
	got, ok := f.ToServer(10_000_000)
	if !ok {
		t.Fatal("not synced after 200 measurements")
	}
	if err := math.Abs(float64(got) - 10_250_000); err > 1000 {
		t.Fatalf("offset error %.0fµs, want under 1000", err)
	}
}

func TestItTracksAClockRunningFast(t *testing.T) {
	// 50ppm is an ordinary consumer crystal. Over the ten minutes a track
	// might play, an unmodelled 50ppm is 30ms of skew — thirty times the
	// ±1ms the spec asks for.
	const ppm = 50.0
	f := simulate(t, 300, 100_000, ppm, 3000, 500, 1_000_000)

	if !f.driftApplies() {
		t.Fatalf("drift %.3g rejected as insignificant (σ²=%.3g)", f.drift, f.pDrift)
	}
	if err := math.Abs(f.drift-ppm*1e-6) / (ppm * 1e-6); err > 0.05 {
		t.Fatalf("drift = %.4gppm, want %.0fppm (%.1f%% off)",
			f.drift*1e6, ppm, err*100)
	}
}

func TestDriftIsWhatKeepsAGroupTogetherBetweenMeasurements(t *testing.T) {
	// The point of the second dimension, stated as the thing it buys: a
	// conversion made well after the last measurement. Offset alone walks
	// away at the crystal's rate; this is the comparison that shows it.
	const ppm = 50.0
	const offset = 100_000.0
	f := simulate(t, 300, offset, ppm, 3000, 500, 1_000_000)

	// Ten seconds past the last exchange.
	local := f.lastUpdate + 10_000_000
	trueServer := local + offset + ppm*1e-6*local

	withDrift, _ := f.ToServer(int64(local))
	offsetOnly := local + f.offset

	errDrift := math.Abs(float64(withDrift) - trueServer)
	errFlat := math.Abs(offsetOnly - trueServer)

	if errDrift >= errFlat {
		t.Fatalf("drift did not help: %.0fµs with, %.0fµs without", errDrift, errFlat)
	}
	if errDrift > 1000 {
		t.Fatalf("error %.0fµs ten seconds out, want under 1ms", errDrift)
	}
}

func TestAnUnknownDriftIsNotAppliedAsZero(t *testing.T) {
	// Extrapolating a drift estimate that is indistinguishable from noise
	// is the one way this filter can be worse than no filter, so the
	// significance test is load-bearing rather than a refinement.
	var f Filter
	f.Measure(1000, 501000, 501050, 1100)
	f.Measure(2000, 502000, 502050, 2100)
	if f.driftApplies() && f.drift*f.drift <= driftSignificance*driftSignificance*f.pDrift {
		t.Fatal("applied a drift below its own significance threshold")
	}
}

func TestTheRoundTripDecidesHowMuchASampleCounts(t *testing.T) {
	// 4.6–7.1% packet loss is measured on this fleet, and TCP turns a lost
	// segment into a stalled exchange. A 400ms round trip must not move the
	// estimate as far as a 6ms one does.
	build := func(rtt int64) *Filter {
		var f Filter
		f.Measure(0, 500_000, 500_050, 6_000)
		f.Measure(1_000_000, 1_500_000, 1_500_050, 1_006_000)
		for i := 0; i < 50; i++ {
			base := int64(2_000_000 + i*1_000_000)
			f.Measure(base, base+500_000, base+500_050, base+6_000)
		}
		// One badly delayed sample claiming a wildly different offset.
		base := int64(60_000_000)
		f.Measure(base, base+900_000, base+900_050, base+rtt)
		return &f
	}
	quick := build(6_000)
	slow := build(400_000)

	movedQuick := math.Abs(quick.offset - 500_000)
	movedSlow := math.Abs(slow.offset - 500_000)
	if movedSlow >= movedQuick {
		t.Fatalf("the slow sample moved the estimate as much as the quick one "+
			"(%.0fµs vs %.0fµs)", movedSlow, movedQuick)
	}
}

func TestItRecoversFromAServerClockStep(t *testing.T) {
	// A server that gets NTP for the first time steps its clock. Averaging
	// that in takes minutes; forgetting is what turns it into seconds.
	var f Filter
	step := func(from *Filter, base int64, offset int64) {
		from.Measure(base, base+offset, base+offset+50, base+6_000)
	}
	for i := 0; i < 200; i++ {
		step(&f, int64(i)*1_000_000, 500_000)
	}
	settled := f.offset

	// The server jumps two seconds forward.
	for i := 200; i < 215; i++ {
		step(&f, int64(i)*1_000_000, 2_500_000)
	}

	if math.Abs(f.offset-2_500_000) > 50_000 {
		t.Fatalf("offset = %.0fµs fifteen samples after a 2s step (was %.0f), "+
			"want within 50ms of 2500000", f.offset, settled)
	}
}

func TestConversionRoundTrips(t *testing.T) {
	f := simulate(t, 300, 250_000, 50, 3000, 500, 1_000_000)
	for _, local := range []int64{1_000_000, 500_000_000, 3_600_000_000} {
		srv, ok := f.ToServer(local)
		if !ok {
			t.Fatal("not synced")
		}
		back, ok := f.ToLocal(srv)
		if !ok {
			t.Fatal("not synced")
		}
		if d := back - local; d > 1 || d < -1 {
			t.Fatalf("round trip of %d came back %d (off by %d)", local, back, d)
		}
	}
}

func TestTheInverseIsAlgebraicNotIterative(t *testing.T) {
	// With a deliberately huge drift the two approaches diverge. An
	// iterative inverse converges to the same answer for small drift, which
	// is why substituting one would pass every other test here.
	f := &Filter{
		offset: 1000, drift: 0.01, lastUpdate: 0,
		pOffset: 1, pOffsetDrift: 0, pDrift: 1e-12, count: 10,
	}
	if !f.driftApplies() {
		t.Fatal("test setup: drift should be significant")
	}
	const local = 1_000_000
	srv, _ := f.ToServer(local)
	back, _ := f.ToLocal(srv)
	if d := back - local; d > 1 || d < -1 {
		t.Fatalf("inverse is not exact: %d came back %d", local, back)
	}
}

func TestASnapshotAgreesWithTheFilterItCameFrom(t *testing.T) {
	// The scheduler runs on the audio goroutine and reads a snapshot rather
	// than the live filter. A snapshot that converted differently would put
	// the audio somewhere the filter never said.
	f := simulate(t, 300, 250_000, 50, 3000, 500, 1_000_000)
	snap := f.Snapshot()
	for _, srv := range []int64{1_000_000, 10_000_000, 999_999_999} {
		want, _ := f.ToLocal(srv)
		got, ok := snap.ToLocal(srv)
		if !ok || got != want {
			t.Fatalf("snapshot %d, filter %d for server time %d", got, want, srv)
		}
	}
}

func TestAnImpossibleExchangeDoesNotProduceInfinities(t *testing.T) {
	// Clocks on this hardware are bogus before NTP and can step under a
	// measurement. A non-positive round trip makes R zero and the gain
	// infinite, and NaN in the offset silences the speaker for good.
	var f Filter
	f.Measure(1000, 5000, 5000, 1000) // zero round trip
	f.Measure(2000, 6000, 9000, 2000) // negative
	for i := 0; i < 20; i++ {
		b := int64(10_000 + i*1000)
		f.Measure(b, b+4000, b+4050, b+100)
	}
	if math.IsNaN(f.offset) || math.IsInf(f.offset, 0) {
		t.Fatalf("offset = %v", f.offset)
	}
	if math.IsNaN(f.drift) || math.IsInf(f.drift, 0) {
		t.Fatalf("drift = %v", f.drift)
	}
}

func TestALocalClockStepBackwardsDoesNotRunTheModelInReverse(t *testing.T) {
	var f Filter
	for i := 0; i < 10; i++ {
		b := int64(i) * 1_000_000
		f.Measure(b, b+500_000, b+500_050, b+6_000)
	}
	before := f.offset
	f.Measure(1_000, 501_000, 501_050, 2_000) // t4 far in the past
	if math.IsNaN(f.offset) || math.IsInf(f.offset, 0) {
		t.Fatalf("offset = %v after a backwards step (was %.0f)", f.offset, before)
	}
}

func TestItMeetsTheSpecsAccuracyFloorOnAJitteryLink(t *testing.T) {
	// ±1ms steady-state is the spec's floor. This link is deliberately
	// worse than the fleet's measured one: 20ms one way with 20ms of
	// jitter, which is the shape TCP retransmission produces.
	f := simulate(t, 400, 250_000, 30, 20_000, 20_000, 1_000_000)
	local := f.lastUpdate + 1_000_000
	trueServer := local + 250_000 + 30e-6*local
	got, ok := f.ToServer(int64(local))
	if !ok {
		t.Fatal("not synced")
	}
	if err := math.Abs(float64(got) - trueServer); err > 1000 {
		t.Fatalf("steady-state error %.0fµs, want under 1000", err)
	}
}
