package server

import (
	"log"
	"os/exec"
	"sync"

	internalLed "github.com/wilbowes/EchoMuse/internal/bindings/led"
	"github.com/wilbowes/EchoMuse/pkg/led"
)

type muteController struct {
	mu      sync.Mutex
	muted   bool
	ledCtrl func() led.Controller
	// dotMuted is set externally to block dot button events while muted
	onMuteChange func(muted bool)
	// persist, when set, is called after every Toggle() so the mute state
	// survives reboots and OTA restarts (state.json). Separate from
	// onMuteChange: that one is the controller-notification hook wired by
	// cmd, this one is internal.
	persist func()
}

func newMuteController(ledGetter func() led.Controller, onMuteChange func(muted bool)) *muteController {
	return &muteController{
		ledCtrl:      ledGetter,
		onMuteChange: onMuteChange,
	}
}

// SetOnMuteChange wires a callback invoked when mute state changes.
// B7 fix (2026-07-05 review): previously Server.SetMuteChangeCallback
// reached directly into m.mu/m.onMuteChange from outside this struct.
// Encapsulating the lock here keeps muteController responsible for its
// own synchronisation, matching every other muteController method.
func (m *muteController) SetOnMuteChange(cb func(muted bool)) {
	m.mu.Lock()
	m.onMuteChange = cb
	m.mu.Unlock()
}

func (m *muteController) IsMuted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.muted
}

func (m *muteController) Toggle() {
	m.mu.Lock()
	m.muted = !m.muted
	muted := m.muted
	// Copy under the lock — SetOnMuteChange writes this field under mu from
	// the main goroutine, and button events can fire before that wiring
	// completes (SubscribeToButton starts the evdev goroutines first).
	cb := m.onMuteChange
	persist := m.persist
	m.mu.Unlock()

	if muted {
		m.applyMute()
	} else {
		m.applyUnmute()
	}
	if persist != nil {
		persist()
	}

	if cb != nil {
		cb(muted)
	}
}

// MuteOnly mutes, and can only mute. Idempotent: muting an already-muted
// device does nothing at all rather than toggling it back.
//
// This is the entry point for a mute arriving over the network (Home
// Assistant). Toggle() stays the button's, and remains the ONLY way back to
// a live microphone. See the mute_set case in internal/client/control.go for
// why unmuting is a physical act.
func (m *muteController) MuteOnly() {
	m.mu.Lock()
	if m.muted {
		m.mu.Unlock()
		return
	}
	m.muted = true
	cb := m.onMuteChange
	persist := m.persist
	m.mu.Unlock()

	m.applyMute()
	if persist != nil {
		persist()
	}
	if cb != nil {
		cb(true)
	}
}

// adcMuteCtls are the per-chip ADC mute control pairs, all four codecs
// (A: ch0/ch1 … D: ch6 + unused). C5 hardware fix (2026-07-07): only chip
// A (105/106) was muted before, leaving chips B–D — including ch6, the mic
// wake word and STT actually use — physically hot; the mic stream-stop was
// what made mute effective. Sibling controls confirmed from the full
// `tinymix -D 0` dump in device/tools/tinymix_controls_output.txt
// (captured 2026-07-06).
var adcMuteCtls = []string{
	"105", "106", // ADC_A
	"123", "124", // ADC_B
	"141", "142", // ADC_C
	"159", "160", // ADC_D
}

func setAdcMute(val string) {
	for _, ctl := range adcMuteCtls {
		exec.Command("tinymix", "-D", "0", ctl, val).Run()
	}
}

// RestoreMuted re-applies a persisted muted state at boot: flag + ADC mute
// only. The LED hardware isn't up yet when this runs (NewServer, before the
// LED-init goroutine finishes), so the red ring and button LED are painted
// by that goroutine once the controllers exist.
func (m *muteController) RestoreMuted() {
	m.mu.Lock()
	m.muted = true
	m.mu.Unlock()
	log.Println("Mute: restoring persisted muted state")
	setAdcMute("1")
}

// applyMute / applyUnmute deliberately do NOT touch the ring.
//
// Mute used to paint all twelve LEDs red and suppress every other paint,
// which made the ring the mute indicator and left it unavailable for
// anything else. The button's own LED has reported mute since v2.9.5 —
// stock parity, and the comment on setMuteButtonLED already said "the
// button itself shows muted, NOT JUST THE RING" — so the ring was the
// second copy of a signal that has dedicated hardware.
//
// Giving it up buys a ring that Home Assistant can own completely. What it
// costs is that a glance at the ring no longer tells you the mic is off;
// the red button LED does, and it is the one indicator that cannot be
// overpainted, because it is a GPIO rather than part of the ring driver.
// That is a deliberate trade, asked for and made with the cost stated.
func (m *muteController) applyMute() {
	log.Println("Mute: mic muted")
	setAdcMute("1")
	setMuteButtonLED(true)
}

func (m *muteController) applyUnmute() {
	log.Println("Mute: mic unmuted")
	setAdcMute("0")
	setMuteButtonLED(false)
}

// setMuteButtonLED drives the discrete red LED under the mic-off button —
// stock-Alexa parity: the button itself shows muted, not just the ring.
// GPIO-backed and independent of the ring driver, so it needs no repaint
// protection (ring repaints can't stomp it) and survives every LED-mode
// transition for free. Direct binding call, same precedent as setAdcMute's
// tinymix exec above.
func setMuteButtonLED(on bool) {
	if err := internalLed.SetMuteButtonLED(on); err != nil {
		log.Printf("Mute button LED: %v", err)
	}
}
