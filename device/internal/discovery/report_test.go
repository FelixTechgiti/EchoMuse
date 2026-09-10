package discovery

import (
	"net"
	"os"
	"strings"
	"testing"
)

// A device with no address and a device on the network whose controller is
// missing produce the same "no controller found" and want completely
// different next steps. The line has to say which it was, and it has to say
// so in words rather than by leaving a field blank — a blank reads as an
// oversight in the logger, not as a finding about the device.
func TestAbsenceIsNamed(t *testing.T) {
	got := describeLink("definitely-not-an-interface")
	if !strings.Contains(got, "no address") {
		t.Fatalf("describeLink on a missing interface = %q, want it to say so", got)
	}
	if !strings.Contains(got, "definitely-not-an-interface") {
		t.Fatalf("describeLink = %q, does not name the interface it looked at", got)
	}
	if LinkAddress("definitely-not-an-interface") != "" {
		t.Fatal("LinkAddress invented an address for an interface that does not exist")
	}
}

// Loopback is not a link. A device whose WiFi is down still has lo, and
// reporting 127.0.0.1 would say "this device is on the network" about the one
// case where it certainly is not.
func TestLoopbackIsNotALink(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skip("no interfaces readable on this host")
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback == 0 {
			continue
		}
		if got := LinkAddress(iface.Name); got != "" {
			t.Fatalf("LinkAddress(%s) = %q, want \"\" — loopback is not a link", iface.Name, got)
		}
	}
}

// The interface is named once and used at both sites. They described
// different interfaces for as long as one of them was a literal, which is the
// kind of drift that reads correctly and reports about the wrong hardware —
// and the direction (`event2` is the volume button on biscuit and the
// touchscreen on checkers) says why resolving by name matters here.
func TestOneInterfaceName(t *testing.T) {
	if searchIface == "" {
		t.Fatal("searchIface is empty")
	}
	src := readSource(t, "mdns.go")
	if strings.Contains(src, `"wlan0"`) {
		t.Fatal("mdns.go carries a literal interface name again — use searchIface")
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("cannot read %s: %v", name, err)
	}
	return string(b)
}
