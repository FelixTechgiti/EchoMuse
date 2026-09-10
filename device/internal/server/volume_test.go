package server

import (
	"sync"
	"testing"
	"time"

	"github.com/wilbowes/EchoMuse/pkg/led"
)

// A deliberate button press must outrank the volume arc's 2s hold. Before
// this, adjusting volume then immediately pressing the action button left
// the arc owning the ring for the remainder of its window, so the device
// gave no sign it had started listening.
func TestCancelDisplayReleasesTheRing(t *testing.T) {
	vc := newVolumeController(func() led.Controller { return nil })

	vc.mu.Lock()
	vc.displayActive = true
	vc.timer = time.AfterFunc(volumeLEDSecs*time.Second, func() {})
	vc.mu.Unlock()

	if !vc.DisplayActive() {
		t.Fatal("precondition: arc should own the ring")
	}

	vc.CancelDisplay()

	if vc.DisplayActive() {
		t.Fatal("arc still owns the ring after CancelDisplay — a listening " +
			"frame would be recorded but not painted")
	}
	// Idempotent: a second press must not panic on the already-stopped timer.
	vc.CancelDisplay()
}

// tinymix ctl 61 spans 0..175, but 127 is the codec's 0dB. Above it the DAC
// applies positive digital gain to near-full-scale PCM and saturates —
// measured on hardware at 65% THD by index 153, 89% by 170, with the output
// level flat from 153 up because it had stopped getting louder. Stock FireOS
// never writes this control at all. If this constant creeps back toward 175,
// the garbling above ~73% volume returns.
func TestVolumeMaxIsCodecUnityNotTheControlMaximum(t *testing.T) {
	if volumeMax != 127 {
		t.Fatalf("volumeMax = %d, want 127 (0dB). Anything higher clips the DAC.",
			volumeMax)
	}
	if volumeButtonFloor >= volumeMax {
		t.Fatalf("button floor %d must sit below the ceiling %d",
			volumeButtonFloor, volumeMax)
	}
}

// The button band must be crossable in a sane number of presses: too few and
// each press is a huge jump, too many and reaching the top is a chore.
func TestButtonBandTakesAReasonableNumberOfPresses(t *testing.T) {
	presses := (volumeMax - volumeButtonFloor) / volumeStep
	if presses < 6 || presses > 16 {
		t.Fatalf("%d presses to cross the band (step %d over %d..%d); "+
			"want roughly 8-12", presses, volumeStep, volumeButtonFloor, volumeMax)
	}
}

func TestStepsStayInsideTheButtonBand(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		// A level below the floor — HA can set one, and so could a stored
		// level from before the cap — must reach audible in ONE press, not
		// creep up 4dB at a time through inaudible territory.
		{"far below the floor lands on it", volumeButtonFloor - 40, volumeButtonFloor},
		{"just below the floor lands on it", volumeButtonFloor - 1, volumeButtonFloor},
		{"inside the band is untouched", volumeButtonFloor + volumeStep, volumeButtonFloor + volumeStep},
		{"above the ceiling clamps down", volumeMax + 30, volumeMax},
	}
	for _, tc := range cases {
		if got := clampToButtonBand(tc.in); got != tc.want {
			t.Errorf("%s: clampToButtonBand(%d) = %d, want %d",
				tc.name, tc.in, got, tc.want)
		}
	}
}

// Stepping up from the top and down from the bottom must settle, not
// oscillate or run away past the band.
func TestSteppingSaturatesAtBothEnds(t *testing.T) {
	level := volumeMax
	for i := 0; i < 5; i++ {
		level = clampToButtonBand(level + volumeStep)
	}
	if level != volumeMax {
		t.Errorf("stepping up from the ceiling reached %d, want %d", level, volumeMax)
	}

	level = volumeButtonFloor
	for i := 0; i < 5; i++ {
		level = clampToButtonBand(level - volumeStep)
	}
	if level != volumeButtonFloor {
		t.Errorf("stepping down from the floor reached %d, want %d",
			level, volumeButtonFloor)
	}
}

// recordingLED captures every frame painted, so a test can assert on what
// reached the hardware rather than on what the code meant to do.
type recordingLED struct {
	mu     sync.Mutex
	frames [][]led.Led
}

func (r *recordingLED) Init() error              { return nil }
func (r *recordingLED) GetNumLEDs() (int, error) { return numLEDs, nil }
func (r *recordingLED) SetLEDs(l ...led.Led) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, append([]led.Led(nil), l...))
	return nil
}
func (r *recordingLED) painted() [][]led.Led {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]led.Led(nil), r.frames...)
}

// The arc's expiry hands the ring back. It must not paint it itself.
//
// It used to paint a RED RING whenever the device was muted, and that was a
// leftover of a rule already removed: mute stopped owning the ring when the
// ring became Home Assistant's, and the indicator moved to the microphone
// button's own GPIO LED, which nothing can overpaint. This one site was
// missed. Reported from a device on 2026-09-10 — the ring glowed red while
// HA's light entity read `off` for the whole six hours, because the paint
// went straight to the hardware and HA was never told.
//
// The timer is driven directly rather than waited out: volumeLEDSecs is 2s
// and a test that sleeps is a test nobody runs.
func TestTheArcExpiryHandsBackAndNeverPaintsTheRingItself(t *testing.T) {
	rec := &recordingLED{}
	vc := newVolumeController(func() led.Controller { return rec })

	handed := false
	vc.onDisplayExpire = func() { handed = true }

	vc.showLEDs(100)
	if vc.timer != nil {
		vc.timer.Stop() // drive the expiry ourselves rather than waiting 2s
	}
	vc.expireDisplay(rec)

	if !handed {
		t.Fatal("the ring was not handed back to the controller's state")
	}
	for _, frame := range rec.painted() {
		for _, l := range frame {
			if l.R > 0 && l.G == 0 && l.B == 0 {
				t.Fatalf("the expiry painted red on the ring: %+v", l)
			}
		}
	}
}
