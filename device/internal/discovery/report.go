// The half of the controller search that is worth recording on /data, and
// the cadence for it lives in internal/bootlog — one escalator, shared with
// the other open-ended faults, rather than a copy per package.
package discovery

import (
	"fmt"
	"net"
)

// searchIface is the interface the browse is bound to, and the one whose
// address is recorded when a search goes long. Declared once, and beside the
// code that reports on it, so the browse and the record can never describe
// different interfaces — the same rule the direction sets for hardware
// generally: resolve by NAME, in one place, because the second copy is the
// one that goes stale silently.
const searchIface = "wlan0"

// LinkAddress is the IPv4 address of the interface the device looks for a
// controller on, or "" when it has none.
//
// **This is the field that separates the two faults.** "No controller found"
// is ambiguous between a device with no network at all and a device on the
// network whose controller is unreachable or unannounced, and those want
// completely different next steps. Nothing else in the record can tell them
// apart, and on 2026-09-10 that ambiguity is exactly what could not be
// resolved after the fact.
func LinkAddress(ifaceName string) string {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if v4 := ipn.IP.To4(); v4 != nil && !v4.IsLoopback() {
				return v4.String()
			}
		}
	}
	return ""
}

// DescribeLink is describeLink for the interface this device searches on. It
// is exported because the control client needs the same field in its own
// record and must not carry a second copy of the interface name to get it.
func DescribeLink() string { return describeLink(searchIface) }

// describeLink renders the address for a log line, naming the absence rather
// than leaving a blank that reads as an oversight.
func describeLink(ifaceName string) string {
	if addr := LinkAddress(ifaceName); addr != "" {
		return fmt.Sprintf("%s=%s", ifaceName, addr)
	}
	return fmt.Sprintf("%s=no address", ifaceName)
}
