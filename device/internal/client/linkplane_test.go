package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestNoCredentialsMeansThePlainPlane(t *testing.T) {
	got := choosePlane(false, 8770, 0)
	if got.UseTLS || !got.SendToken || got.Warn != "" {
		t.Fatalf("got %+v — a device with no CA has always dialled plain, quietly", got)
	}
}

func TestACaWithNoListenerKeepsTheRolloutFallback(t *testing.T) {
	got := choosePlane(true, 0, 0)
	if got.UseTLS || !got.SendToken {
		t.Fatalf("got %+v, want the plain plane with the token", got)
	}
	if !strings.Contains(got.Warn, "tls_port") {
		t.Fatalf("the rollout fallback must still say why: %q", got.Warn)
	}
}

func TestTheSecurePlaneIsTheDefault(t *testing.T) {
	got := choosePlane(true, 8770, 0)
	if !got.UseTLS || !got.SendToken || got.Warn != "" {
		t.Fatalf("got %+v — a healthy device dials wss and says nothing", got)
	}
}

func TestOneFailureDoesNotDowngrade(t *testing.T) {
	// A controller restarting in the middle of a handshake must not move a
	// device off TLS. The threshold is what buys that.
	for f := 0; f < tlsFallbackAfter; f++ {
		if got := choosePlane(true, 8770, f); !got.UseTLS {
			t.Fatalf("downgraded after %d failure(s): %+v", f, got)
		}
	}
}

func TestRepeatedVerificationFailureFallsBackWithoutTheToken(t *testing.T) {
	// The fault this exists for: the controller's leaf does not answer to
	// the name this firmware expects, so verification fails on every dial
	// for ever, and the device has no other way to reach anything —
	// including the shell, which the controller itself proxies.
	got := choosePlane(true, 8770, tlsFallbackAfter+1)
	if got.UseTLS {
		t.Fatal("still on TLS after repeated verification failures")
	}
	if got.SendToken {
		t.Fatal("the link token must NOT be sent over the fallback: if " +
			"verification failed because somebody is on the path, handing " +
			"them the shared secret is worse than the outage")
	}
	if got.Warn == "" {
		t.Fatal("a silent downgrade is the thing this must never be")
	}
}

func TestTlsIsRetestedSoARepairIsPickedUpOnItsOwn(t *testing.T) {
	// Otherwise one bad certificate moves a device to the plain plane until
	// somebody restarts it — and the whole point is that nobody can reach
	// this device to restart anything.
	retried := false
	for f := tlsFallbackAfter; f < tlsFallbackAfter+3*tlsRetryEvery; f++ {
		if choosePlane(true, 8770, f).UseTLS {
			retried = true
			break
		}
	}
	if !retried {
		t.Fatal("TLS is never re-tested once the fallback engages")
	}
}

func TestAnUnreachableControllerIsNotATlsFailure(t *testing.T) {
	// The safety property of the whole mechanism. Both planes refuse when
	// the controller is down; counting that would move a fleet off TLS for
	// the length of any ordinary outage, permanently on the next one.
	for _, err := range []error{
		&net.OpError{Op: "dial", Err: errors.New("connection refused")},
		errors.New("websocket: bad handshake"),
		fmt.Errorf("wrapped: %w", &net.OpError{Op: "dial", Err: errors.New("i/o timeout")}),
		nil,
	} {
		if isTLSVerifyFailure(err) {
			t.Fatalf("%v counted as a verification failure", err)
		}
	}
}

func TestAVerificationFailureIsRecognisedThroughWrapping(t *testing.T) {
	// The real error arrives wrapped by the websocket dialler, so matching
	// on the concrete type at the top level would see none of these.
	for _, err := range []error{
		&tls.CertificateVerificationError{Err: errors.New("x")},
		x509.HostnameError{Host: "revoice-controller"},
		x509.UnknownAuthorityError{},
		x509.CertificateInvalidError{Reason: x509.Expired},
		fmt.Errorf("dial: %w", x509.HostnameError{Host: "revoice-controller"}),
	} {
		if !isTLSVerifyFailure(err) {
			t.Fatalf("%T went unrecognised — the fallback would never engage", err)
		}
	}
}
