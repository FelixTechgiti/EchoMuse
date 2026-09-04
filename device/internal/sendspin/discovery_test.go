package sendspin

import (
	"net"
	"testing"
)

func TestTheURLIsWsNotWss(t *testing.T) {
	// The protocol's design, not an oversight: encryption is at the
	// APPLICATION layer, inside the WebSocket, so the Noise handshake
	// authenticates the peer rather than a certificate chain nobody can
	// provision on a speaker. Reaching for wss would put TLS underneath an
	// already-encrypted channel and need a CA on every device.
	s := Server{Host: "192.168.1.10", Port: 8927, Path: "/sendspin"}
	if got := s.URL(); got != "ws://192.168.1.10:8927/sendspin" {
		t.Fatalf("URL = %q", got)
	}
}

func TestAnIPv6HostIsBracketed(t *testing.T) {
	// Without brackets the colons in the address are read as the port
	// separator, and the dial fails with an error about the port.
	s := Server{Host: "fe80::1", Port: 8927, Path: "/sendspin"}
	if got := s.URL(); got != "ws://[fe80::1]:8927/sendspin" {
		t.Fatalf("URL = %q", got)
	}
}

func TestAMissingPathFallsBackRatherThanProducingABareHost(t *testing.T) {
	// `path` is an optional TXT record. A server that omits it is not a
	// broken server, and dialling ws://host:8927 with no path reaches an
	// endpoint that is not the protocol's.
	s := Server{Host: "10.0.0.5", Port: 8927}
	if got := s.URL(); got != "ws://10.0.0.5:8927/sendspin" {
		t.Fatalf("URL = %q", got)
	}
}

func TestAPathWithoutALeadingSlashIsStillAPath(t *testing.T) {
	s := Server{Host: "10.0.0.5", Port: 8927, Path: "sendspin"}
	if got := s.URL(); got != "ws://10.0.0.5:8927/sendspin" {
		t.Fatalf("URL = %q", got)
	}
}

func TestAnEntryWithNoAddressIsSkippedRatherThanDialled(t *testing.T) {
	// It happens: zeroconf publishes the PTR before the A record on some
	// stacks, and the entry arrives with a hostname and nothing to dial.
	if _, ok := ServerFromEntry("MA", nil, nil, 8927, nil); ok {
		t.Fatal("an entry with no address produced a server")
	}
}

func TestIPv4IsPreferredWhenBothArePresent(t *testing.T) {
	s, ok := ServerFromEntry("MA",
		[]net.IP{net.ParseIP("192.168.1.10")},
		[]net.IP{net.ParseIP("fe80::1")}, 8927, nil)
	if !ok || s.Host != "192.168.1.10" {
		t.Fatalf("host = %q", s.Host)
	}
}

func TestIPv6IsUsedWhenItIsAllThereIs(t *testing.T) {
	s, ok := ServerFromEntry("MA", nil,
		[]net.IP{net.ParseIP("fe80::1")}, 8927, nil)
	if !ok || s.Host != "fe80::1" {
		t.Fatalf("host = %q", s.Host)
	}
}

func TestTheTxtRecordsAreRead(t *testing.T) {
	s, ok := ServerFromEntry("instance-name",
		[]net.IP{net.ParseIP("10.0.0.5")}, nil, 9000,
		[]string{"path=/spin", "name=Kitchen MA", "unrelated=x"})
	if !ok {
		t.Fatal("entry rejected")
	}
	if s.Name != "Kitchen MA" || s.Path != "/spin" || s.Port != 9000 {
		t.Fatalf("server = %+v", s)
	}
}

func TestAnEmptyTxtValueIsAbsentRatherThanEmpty(t *testing.T) {
	// A server publishing `path=` means it did not set one, not that the
	// path is "". Taking it literally produces ws://host:8927 with no path.
	s, ok := ServerFromEntry("MA", []net.IP{net.ParseIP("10.0.0.5")}, nil,
		8927, []string{"path=", "name="})
	if !ok {
		t.Fatal("entry rejected")
	}
	if s.Path != DefaultPath {
		t.Fatalf("path = %q, want the default", s.Path)
	}
	if s.Name != "MA" {
		t.Fatalf("name = %q, want the instance name", s.Name)
	}
}

func TestAnAbsurdPortFallsBackToTheSpecsDefault(t *testing.T) {
	for _, port := range []int{0, -1, 70000} {
		s, ok := ServerFromEntry("MA", []net.IP{net.ParseIP("10.0.0.5")},
			nil, port, nil)
		if !ok || s.Port != DefaultServerPort {
			t.Fatalf("port %d became %d, want %d", port, s.Port, DefaultServerPort)
		}
	}
}

func TestTheServiceTypesAreTheSpecsAndAreDistinct(t *testing.T) {
	// Discovery runs both ways in this protocol and the two names are one
	// character apart. Browsing for the client type finds other speakers
	// and never a server.
	if ServerServiceType != "_sendspin-server._tcp" {
		t.Fatalf("server service type = %q", ServerServiceType)
	}
	if ClientServiceType != "_sendspin._tcp" {
		t.Fatalf("client service type = %q", ClientServiceType)
	}
	if ServerServiceType == ClientServiceType {
		t.Fatal("the two service types collapsed into one")
	}
}
