package wifi

import (
	"fmt"
	"log"
	"time"

	"github.com/wilbowes/EchoMuse/internal/platform"
)

// THE RELOAD IS THE ONE PART OF A WIFI CHANGE THAT IS NOT PORTABLE, and under
// emOS the Android path cannot work at all.
//
// Everything else in this package — the backup, the pending marker, the
// association/address/registration gates, the automatic restore, the
// crash-recovery at process start — is ALSA-free, Android-free and correct on
// both bases. What is not is the two lines in the middle that take
// wpa_supplicant down and bring it back up:
//
//   - **FireOS** runs wpa_supplicant under the framework, so the only safe
//     lever is `svc wifi disable` / `enable` and the package comment above
//     lists what happens to anyone who reaches past it.
//   - **emOS** has no framework: init starts wpa_supplicant directly against
//     `/data/misc/wifi/wpa_supplicant.conf` and nudges it with `wpa_cli`.
//     There is no `svc` on PATH, so the Android path does not fail loudly
//     there — `exec.Command` reports a missing binary, `disableWifi` returns
//     an error, and the change is refused. A dashboard control that refuses
//     every time is the "shown as a control that silently does nothing" the
//     project's capability rule exists to forbid.
//
// **What makes an unproven path acceptable here is that the safety model is
// the proven part.** A wrong reload cannot strand the device: the gates fail,
// the backup is restored through the same reload, and if even that leaves it
// without an address, `RecoverIfPending` puts the old conf back at the next
// process start — which init reaches by rebooting. The failure mode is a
// reboot, not a device on a network nobody can reach.
//
// Deliberately NOT done on emOS: touching dhcpcd. init starts it once after
// association, and killing it to force a fresh lease rests on init respawning
// it — which is true for a service in its table and catastrophic if it is
// not, because the rollback would then also come up without an address. The
// existing client renews on its own or the IPv4 gate fails and rolls back,
// which is the safe direction.

// supplicant is the per-base half of a reload.
type supplicant interface {
	// down leaves the radio associated to nothing, verifiably.
	down() error
	// up re-reads the conf and associates again.
	up() error
	// how names the mechanism, for the log line that a field report quotes.
	how() string
}

// cliRunner is wpa_cli, injected so the emOS path is testable from a host
// that has neither a supplicant nor a socket.
type cliRunner func(args ...string) (string, error)

func pickSupplicant() supplicant {
	if platform.IsAndroid() {
		return androidSupplicant{}
	}
	return emosSupplicant{cli: wpaCli}
}

// ── FireOS: the framework owns the supplicant ────────────────────────────────

type androidSupplicant struct{}

func (androidSupplicant) how() string { return "svc wifi" }
func (androidSupplicant) down() error { return disableWifi() }
func (androidSupplicant) up() error   { return enableWifi() }

// ── emOS: we own the supplicant ─────────────────────────────────────────────

type emosSupplicant struct{ cli cliRunner }

func (emosSupplicant) how() string { return "wpa_cli" }

// down asks the supplicant to stop associating and VERIFIES it stopped.
//
// `disconnect` is the right verb rather than `terminate`: it leaves the
// process running, so the control socket every later step needs — including
// the rollback's — stays open. Killing the supplicant on a device whose only
// management path is that radio removes the means of putting it back.
func (e emosSupplicant) down() error {
	if _, err := e.cli("disconnect"); err != nil {
		return fmt.Errorf("wpa_cli disconnect: %w", err)
	}
	if !waitFor("disassociation after wpa_cli disconnect", 10*time.Second,
		func() bool { return !associated() }) {
		_, _ = e.cli("reconnect")
		return fmt.Errorf("wifi did not go down after 'wpa_cli disconnect'")
	}
	return nil
}

// up re-reads the conf and then associates. BOTH, IN THAT ORDER.
//
// `reassociate` alone re-joins whatever the supplicant already has in memory,
// which is the OLD network — the same shape as the FireOS failure the package
// comment records, where a conf written at the wrong moment was silently
// clobbered and the device rejoined the network it was being moved off. The
// gates would then pass against the old SSID and commit a change that never
// happened. `reconfigure` is what makes the file the source of truth.
func (e emosSupplicant) up() error {
	if _, err := e.cli("reconfigure"); err != nil {
		return fmt.Errorf("wpa_cli reconfigure: %w", err)
	}
	if _, err := e.cli("reassociate"); err != nil {
		return fmt.Errorf("wpa_cli reassociate: %w", err)
	}
	// The same settle the Android path takes after `svc wifi enable`: the
	// caller's first gate poll should not land before the supplicant has
	// begun.
	time.Sleep(3 * time.Second)
	return nil
}

// reloadWith is reloadConf's body, with the supplicant injected.
//
// The ORDER is load-bearing on FireOS and merely harmless on emOS, so there
// is one order rather than two: on disable, WifiStateMachine saves its
// in-memory network list back over wpa_supplicant.conf, and a conf written
// beforehand is lost (hardware, 2026-07-11). emOS has nothing that does that,
// and writing while down is still the honest sequence to describe.
func reloadWith(s supplicant, content string) error {
	return reloadWithWriter(s, content, writeConf)
}

// reloadWithWriter is the same with the conf write injected, so the ORDER can
// be pinned by test. The order is what a day on hardware bought, and it is
// the part a refactor would quietly lose.
func reloadWithWriter(s supplicant, content string, write func(string) error) error {
	log.Printf("[wifi] reloading via %s", s.how())
	if err := s.down(); err != nil {
		return err
	}
	if err := write(content); err != nil {
		// Leave WiFi usable rather than down next to a bad conf.
		_ = s.up()
		return err
	}
	return s.up()
}
