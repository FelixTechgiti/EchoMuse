package sendspin

import "math"

// The time filter: server time, in this device's clock.
//
// Sendspin is a SCHEDULED protocol. Every audio chunk carries the server
// timestamp at which its first sample must leave the speaker, and a group is
// in sync because every member converts that one number into its own clock
// and hits it. Nothing else in the protocol makes multi-room work; get this
// wrong and every symptom is "the audio is fine but the rooms are apart",
// which is indistinguishable from a buffering problem and is not one.
//
// The spec requires the time-filter algorithm specifically, not merely "some
// clock sync": a two-dimensional Kalman filter tracking clock OFFSET and
// DRIFT. Both are needed. Offset alone is corrected by every measurement and
// then walks away between them — these are two free-running crystals, and the
// speaker's own is measurably off nominal (47973 fps against 48000, −0.056%,
// measured off /proc/asound). A filter that models drift predicts where the
// other clock will be; one that does not chases where it was.
//
// This is a port of the reference implementation (Sendspin/time-filter, and
// aiosendspin's client/time_sync.py, which is what Music Assistant runs), and
// correctness means AGREEMENT with them rather than plausibility. Everything
// below is in MICROSECONDS, as the wire is; drift is dimensionless (µs per
// µs). Pure and dependency-free so the whole of it is testable on the host —
// a clock filter that can only be exercised against a live server is one
// nobody exercises.
const (
	// measurementScale (α) scales the worst-case round-trip bound into a
	// measurement standard deviation: R = (α · max_error)². max_error is a
	// BOUND on the asymmetry, not an estimate of it, so using it raw would
	// tell the filter every sample is far worse than it is and leave the
	// gain permanently small.
	measurementScale = 0.5

	// driftProcessStdDev is the crystal's frequency wander, per √µs. Small
	// because a quartz oscillator's RATE is stable even when its offset is
	// not — that stability is the whole reason modelling drift pays.
	driftProcessStdDev = 1e-11

	// offsetProcessStdDev is 0 in the reference: the offset's own process
	// noise is already accounted for by the drift term integrated over dt.
	// Kept as a named constant rather than dropped so the Q matrix below
	// still reads as the matrix from the specification.
	offsetProcessStdDev = 0.0

	// forgettingCutoff: a residual this many measurement-sigmas out is
	// treated as the world having changed (a server clock step, a network
	// path change) rather than as noise to be averaged away.
	forgettingCutoff = 3.0

	// forgettingMinCount: no forgetting until the filter has enough history
	// for an outlier to mean something. Early on every residual is large,
	// and forgetting then would just keep the filter permanently unsure.
	forgettingMinCount = 100

	// forgetFactor inflates the covariance — by its SQUARE, since P is in
	// squared units — so the next measurements are trusted more than the
	// state is.
	forgetFactor = 2.0

	// driftSignificance (k): drift is only applied to a conversion when
	// |drift| > k·σ_drift. Below that the estimate is indistinguishable
	// from zero, and applying it extrapolates noise into the future — the
	// one place this filter can make a conversion WORSE than not filtering.
	driftSignificance = 2.0
)

// Filter estimates the offset and drift between the server's clock and this
// device's, from NTP-style four-timestamp exchanges.
//
// Not safe for concurrent use: it is driven by one goroutine — the connection
// read loop — and read by the playback scheduler through a snapshot. Adding a
// mutex here would put a lock on the audio path for no reason.
type Filter struct {
	offset float64 // µs to add to a local timestamp to get server time
	drift  float64 // dimensionless: additional µs of offset per µs elapsed

	// Covariance, held as its three distinct entries rather than a 2×2
	// array: P is symmetric, and naming them is what makes the update
	// equations below readable as the equations.
	pOffset      float64 // σ²_offset
	pOffsetDrift float64 // σ_offset,drift
	pDrift       float64 // σ²_drift

	count      int
	lastUpdate float64 // local time (µs) of the last measurement
	lastZ      float64 // that measurement's offset, for the k=1 drift seed
}

// Measure folds in one completed time exchange.
//
//	t1 = client_transmitted   (our clock, when we sent client/time)
//	t2 = server_received      (their clock)
//	t3 = server_transmitted   (their clock)
//	t4 = client_received      (our clock, when server/time arrived)
//
// The measurement is the NTP offset ((t2−t1) + (t3−t4)) / 2, and its
// uncertainty is half the round trip minus the server's own turnaround —
// which is a bound on how asymmetric the path could have been, and is
// therefore the right thing to weight by. A sample that took 400ms to come
// back over a link with 4.6–7.1% loss deserves less of a say than one that
// took 6ms, and this is what says so.
func (f *Filter) Measure(t1, t2, t3, t4 int64) {
	z := (float64(t2-t1) + float64(t3-t4)) / 2
	maxError := (float64(t4-t1) - float64(t3-t2)) / 2
	// A non-positive bound is impossible on a sane exchange and means the
	// clocks moved under us or a timestamp is bogus. Clamp rather than
	// reject: R must be positive or S can be zero and the gain infinite.
	if maxError <= 0 {
		maxError = 1
	}
	r := (measurementScale * maxError) * (measurementScale * maxError)
	now := float64(t4)

	switch f.count {
	case 0:
		// Nothing to predict from. Take the measurement as the state and
		// its own variance as the uncertainty; drift is unknown, which is
		// not the same as zero, and the significance test below is what
		// keeps an unknown drift out of the conversions.
		f.offset = z
		f.drift = 0
		f.pOffset = r
		f.pOffsetDrift = 0
		f.pDrift = math.Inf(1)
		f.lastUpdate = now
		f.lastZ = z
		f.count = 1
		return

	case 1:
		dt := now - f.lastUpdate
		if dt <= 0 {
			// Two samples at the same instant say nothing about rate.
			// Keep the better offset and wait.
			f.offset = z
			f.pOffset = r
			f.lastZ = z
			return
		}
		f.drift = (z - f.lastZ) / dt
		f.pDrift = (f.pOffset + r) / (dt * dt)
		f.offset = z
		f.pOffset = r
		f.pOffsetDrift = 0
		f.lastUpdate = now
		f.lastZ = z
		f.count = 2
		return
	}

	dt := now - f.lastUpdate
	if dt < 0 {
		dt = 0 // a local clock step backwards; do not run the model in reverse
	}

	// ── Predict ───────────────────────────────────────────────────────────
	// x = F·x with F = [[1, dt], [0, 1]]
	offsetPred := f.offset + f.drift*dt
	driftPred := f.drift

	// P = F·P·Fᵀ + Q, written out because the matrix is 2×2 and the
	// expansion is shorter and clearer than a loop over it.
	pOO := f.pOffset + 2*dt*f.pOffsetDrift + dt*dt*f.pDrift
	pOD := f.pOffsetDrift + dt*f.pDrift
	pDD := f.pDrift
	pOO += offsetProcessStdDev * offsetProcessStdDev * dt
	pDD += driftProcessStdDev * driftProcessStdDev * dt

	// ── Update ────────────────────────────────────────────────────────────
	y := z - offsetPred // innovation
	s := pOO + r        // innovation covariance
	kOffset := pOO / s  // Kalman gain, H = [1, 0]
	kDrift := pOD / s

	f.offset = offsetPred + kOffset*y
	f.drift = driftPred + kDrift*y

	// P = (I − K·H)·P
	f.pOffset = (1 - kOffset) * pOO
	f.pOffsetDrift = (1 - kOffset) * pOD
	f.pDrift = pDD - kDrift*pOD

	f.lastUpdate = now
	f.lastZ = z
	f.count++

	// ── Adaptive forgetting ───────────────────────────────────────────────
	// A residual far outside what the measurement noise can explain is not
	// noise: the server's clock stepped, or the path changed. Averaging it
	// in would take minutes to work off. Inflating the covariance instead
	// makes the next few measurements dominate, which is exactly the
	// behaviour wanted after a disruption — and it is gated on having
	// enough history that "far outside" means something.
	if f.count >= forgettingMinCount && math.Abs(y) > forgettingCutoff*maxError {
		f.pOffset *= forgetFactor * forgetFactor
		f.pOffsetDrift *= forgetFactor * forgetFactor
		f.pDrift *= forgetFactor * forgetFactor
	}
}

// Synced reports whether the filter has enough measurements to convert.
//
// The spec forbids a player reporting available:true before it is synced, and
// that rule is worth keeping literally: an unsynced player admitted to a group
// plays at the wrong time, which sounds like a fault in the group rather than
// in the member that just joined.
func (f *Filter) Synced() bool { return f.count >= 2 }

// driftApplies reports whether the drift estimate is distinguishable from
// zero. Below the threshold the estimate is noise, and extrapolating noise
// forward is the one way this filter can be worse than none.
func (f *Filter) driftApplies() bool {
	if math.IsInf(f.pDrift, 1) || f.pDrift < 0 {
		return false
	}
	return f.drift*f.drift > driftSignificance*driftSignificance*f.pDrift
}

// ToServer converts a local timestamp (µs) to server time (µs).
func (f *Filter) ToServer(local int64) (int64, bool) {
	if !f.Synced() {
		return 0, false
	}
	t := float64(local)
	srv := t + f.offset
	if f.driftApplies() {
		srv += f.drift * (t - f.lastUpdate)
	}
	return int64(math.Round(srv)), true
}

// ToLocal converts a server timestamp (µs) to local time (µs) — the direction
// that actually matters, since every audio chunk arrives stamped in server
// time and has to be turned into a moment on this device's playback clock.
//
// The inverse is algebraic rather than a second estimate: with
// srv = loc + offset + drift·(loc − lastUpdate), solving for loc gives
// loc = (srv − offset + drift·lastUpdate) / (1 + drift). Iterating instead —
// convert, measure the error, correct — is the obvious approach and is wrong
// in a way that hides: it converges to the same answer for small drift and
// silently loses accuracy exactly when drift is large enough to matter.
func (f *Filter) ToLocal(server int64) (int64, bool) {
	if !f.Synced() {
		return 0, false
	}
	s := float64(server)
	if !f.driftApplies() {
		return int64(math.Round(s - f.offset)), true
	}
	denom := 1 + f.drift
	if denom == 0 {
		return int64(math.Round(s - f.offset)), true
	}
	return int64(math.Round((s - f.offset + f.drift*f.lastUpdate) / denom)), true
}

// Snapshot is an immutable copy for the playback scheduler, which runs on the
// audio goroutine and must not touch the filter the read loop is updating.
type Snapshot struct {
	Offset     float64
	Drift      float64
	LastUpdate float64
	UseDrift   bool
	Ready      bool
}

// Snapshot copies the current estimate.
func (f *Filter) Snapshot() Snapshot {
	return Snapshot{
		Offset:     f.offset,
		Drift:      f.drift,
		LastUpdate: f.lastUpdate,
		UseDrift:   f.driftApplies(),
		Ready:      f.Synced(),
	}
}

// ToLocal converts server time to local time from a snapshot, with the same
// algebra as Filter.ToLocal.
func (s Snapshot) ToLocal(server int64) (int64, bool) {
	if !s.Ready {
		return 0, false
	}
	v := float64(server)
	if !s.UseDrift {
		return int64(math.Round(v - s.Offset)), true
	}
	denom := 1 + s.Drift
	if denom == 0 {
		return int64(math.Round(v - s.Offset)), true
	}
	return int64(math.Round((v - s.Offset + s.Drift*s.LastUpdate) / denom)), true
}
