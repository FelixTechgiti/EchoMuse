package wifi

import (
	"strings"
	"testing"
)

// Real output, trimmed. wpa_cli prints the socket path first on some
// builds, so the parse must not depend on the first line being data.
const sampleStatus = `Selected interface 'wlan0'
bssid=3c:a6:2f:11:22:33
freq=2412
ssid=Fritzbox
id=0
mode=station
wpa_state=COMPLETED
ip_address=192.168.178.140
address=ac:52:98:96:59:0d
`

const sampleSignal = `RSSI=-54
LINKSPEED=65
NOISE=9999
FREQUENCY=2412
`

func TestDescribeCarriesTheFieldsThatSeparateTheTwoFaults(t *testing.T) {
	// A device unreachable for 22 hours while its address stayed correct
	// (2026-09-11) is ambiguous between a zombie association and a lost one,
	// and those want opposite responses. The state is what separates them;
	// the BSSID is what shows a roam on a multi-AP network.
	got := describe(sampleStatus, sampleSignal)
	for _, want := range []string{
		"wifi=COMPLETED",
		"bssid=3c:a6:2f:11:22:33",
		"freq=2412",
		"rssi=-54",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestTheSsidIsNeverRenderedAsABssid(t *testing.T) {
	// `status` carries both bssid= and ssid=, so a suffix match answers the
	// first with the second — and an SSID in the bssid field reads as a
	// plausible value rather than as a fault.
	got := describe(sampleStatus, sampleSignal)
	if strings.Contains(got, "bssid=Fritzbox") {
		t.Fatalf("the ssid leaked into the bssid field: %q", got)
	}
}

func TestNoAnswerFromTheSupplicantIsItsOwnStatement(t *testing.T) {
	// wpa_cli missing, the socket gone, the supplicant dead — none of those
	// is a wpa_state, and reporting one of them AS a state would be a wrong
	// answer where the whole point is to distinguish states.
	got := describe("", "")
	if got != "wifi=unknown" {
		t.Fatalf("got %q, want wifi=unknown", got)
	}
}

func TestAScanningRadioReportsWhatItHas(t *testing.T) {
	// Mid-scan there is no BSSID and no RSSI. The line must still carry the
	// state — that IS the finding — rather than degrading to unknown.
	got := describe("wpa_state=SCANNING\n", "")
	if got != "wifi=SCANNING" {
		t.Fatalf("got %q, want just the state", got)
	}
}

func TestDescribeSurvivesADeviceWithNoWpaCli(t *testing.T) {
	// The host runs this suite and has no supplicant. Describe must return a
	// line rather than panicking or hanging, because its caller is a log
	// site in a fault path.
	if got := Describe(); got == "" {
		t.Fatal("Describe returned nothing")
	}
}
