package als

import "testing"

// The threshold policy decides what reaches Home Assistant promptly and what
// waits up to 30s for the stats tick. Too sensitive and a flickering lamp
// floods the control plane; too coarse and "someone turned a light on" is the
// thing it misses.
func TestSignificant(t *testing.T) {
	cases := []struct {
		name          string
		baseline, now int
		want          bool
	}{
		// Measured noise on a still room: 309/311/313/308/312 on consecutive
		// reads, about ±1.5%. None of it may report.
		{"still room drift up", 309, 313, false},
		{"still room drift down", 313, 308, false},

		// The case this exists for.
		{"lamp switched on", 40, 300, true},
		{"lamp switched off", 300, 40, true},

		// Hand over the sensor, measured 309 -> 0.
		{"covered", 309, 0, true},
		{"uncovered", 0, 308, true},

		// Near darkness must not produce infinite ratios: a 2 lux wobble in a
		// dark room is not a room lighting up.
		{"tiny change near zero", 0, 2, false},
		{"tiny change near zero, down", 3, 0, false},

		// Big RELATIVE change but small absolute — still noise-ish.
		{"5 to 9 lux", 5, 9, false},

		// Daylight: 50 lux is invisible against 20000 and must not report,
		// which an absolute threshold would get wrong.
		{"daylight jitter", 20000, 20050, false},
		{"cloud clears", 8000, 20000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Significant(c.baseline, c.now); got != c.want {
				t.Fatalf("Significant(%d, %d) = %v, want %v",
					c.baseline, c.now, got, c.want)
			}
		})
	}
}

// Symmetry matters: a light going off should be as reportable as one coming
// on. Comparing against the baseline rather than the new value is what makes
// that true, and it is easy to get backwards.
func TestSignificantIsSymmetricEnough(t *testing.T) {
	if !Significant(300, 40) || !Significant(40, 300) {
		t.Fatal("a lamp must be reportable in both directions")
	}
}
