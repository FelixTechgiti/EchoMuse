package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

// Which plane to dial when the secure one will not verify.
//
// **A device that cannot verify the controller used to go silently dead for
// ever, and that is the fault this exists for.** Verification fails when the
// controller's leaf does not answer to the name the firmware expects — which
// the EchoMuse→Revoice rename caused for every device that had credentials
// from a pre-rename controller, because the SAN is baked into a certificate
// that PERSISTS while both constants moved in one commit. The device holds a
// CA and mDNS advertises tls_port, so it redialled wss for ever, with no
// route to a shell (the shell is proxied by the controller it cannot reach)
// and nothing in any log anybody could read. Measured 2026-09-11: a device
// connected over wss on v2.27.0-fx.1 took v2.29.0-fx.1 and never registered
// again.
//
// The project's rule is to degrade to the OLD behaviour and never to a wrong
// answer. The old behaviour is the plain plane, which every device used
// before TLS existed and which `em_linkauth.decide` still admits.
//
// Two things make the downgrade safe enough to be worth having:
//
//   - **The token is withheld on the fallback.** If verification failed
//     because somebody is on the path rather than because of a stale name,
//     handing them the shared secret is worse than the outage. The
//     controller admits a device that presents no token at all (rule 2 in
//     em_linkauth: the credential push itself rides the plain plane), so
//     withholding costs the connection nothing.
//   - **`REQUIRE_DEVICE_TLS` still forbids it.** An operator who has closed
//     the downgrade path keeps it closed: the controller refuses a plain
//     connection outright, which is a visible refusal rather than a device
//     that quietly reports healthy.
//
// And the dashboard shows `ws (plain)` where it showed `wss (TLS)`, which is
// the difference between a fault somebody can see and one nobody can.
const (
	// Consecutive verification failures before the plain plane is used.
	// Three is ~15s of retries: enough that a controller restarting in the
	// middle of a handshake cannot cause a downgrade.
	tlsFallbackAfter = 3

	// How often TLS is re-tested once we are on the plain plane. ~1 minute
	// at the 5s reconnect interval, so a repaired controller is picked up
	// without anybody restarting anything.
	tlsRetryEvery = 12
)

// planeChoice is what one dial should do.
type planeChoice struct {
	UseTLS    bool
	SendToken bool
	// Warn is non-empty when the choice is a degradation somebody should
	// see in the log. Empty on the ordinary paths, so a healthy device says
	// nothing.
	Warn string
}

// choosePlane decides ws vs wss for a single dial.
//
// `failures` counts CONSECUTIVE verification failures — not dial failures.
// The distinction is the whole safety of this: a controller that is merely
// down refuses both planes equally, and counting that would downgrade a
// device for an outage that had nothing to do with TLS.
func choosePlane(haveCA bool, tlsPort int, failures int) planeChoice {
	if !haveCA {
		// No credentials installed. The pre-TLS arrangement, and the state
		// every device is in before its first credential push.
		return planeChoice{UseTLS: false, SendToken: true}
	}
	if tlsPort <= 0 {
		// CA on disk but the controller has no TLS listener (or predates
		// one). Deliberate rollout fallback, and it has always been here.
		return planeChoice{UseTLS: false, SendToken: true,
			Warn: "CA installed but controller advertises no tls_port — dialling plain ws"}
	}
	if failures < tlsFallbackAfter {
		return planeChoice{UseTLS: true, SendToken: true}
	}
	if (failures-tlsFallbackAfter)%tlsRetryEvery == 0 {
		return planeChoice{UseTLS: true, SendToken: true,
			Warn: "re-testing the TLS link after repeated verification failures"}
	}
	return planeChoice{UseTLS: false, SendToken: false,
		Warn: "TLS verification failed repeatedly — falling back to the plain " +
			"plane WITHOUT the link token. The controller's certificate does " +
			"not answer to the name this firmware expects, or something is on " +
			"the path"}
}

// isTLSVerifyFailure reports whether a dial failed because the certificate
// could not be verified, as opposed to the controller being unreachable.
//
// Only this counts towards the fallback. A refused or timed-out connect is
// the controller being down, which the plain plane cannot fix either — and
// treating it as a TLS fault would quietly move a whole fleet off TLS for
// the length of any ordinary outage.
func isTLSVerifyFailure(err error) bool {
	if err == nil {
		return false
	}
	var cve *tls.CertificateVerificationError
	if errors.As(err, &cve) {
		return true
	}
	// Older wrappings, and the cases tls reports directly.
	var hostErr x509.HostnameError
	var authErr x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &hostErr) ||
		errors.As(err, &authErr) ||
		errors.As(err, &invalid)
}
