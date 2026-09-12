"""
em_netdiag.py — why a device does not answer when something calls IT.

**Every connection in this system is made BY the device.** The three
WebSocket planes, the shell, the OTA transfer: all of them dial outward to
the controller. Nothing has ever dialled the other way, and that is why
"the Echo is reachable from the network" could sit on an issue for weeks
marked as verified while being untested — the only measurement behind it was
taken ON the Echo, against localhost.

It is not a theoretical gap. Measured 2026-09-12 from a Windows PC at
192.168.178.114, same /24 as the device:

    http://192.168.178.140:35936/   (librespot)      hangs, no response
    http://192.168.178.140:5000/    (shairport-sync) hangs, no response
    ping 192.168.178.140                             no reply

Two unrelated programs on two ports, and ICMP as well — so it is not a
protocol or a path, it is the device refusing or never seeing inbound
traffic. Everything else on that network works, including other AirPlay and
Spotify Connect receivers, so the network is exonerated.

**Spotify Connect and AirPlay both require the phone to call the speaker.**
A device that only ever dials out cannot host either, which makes this the
root cause of what was filed as a discovery problem (#77) — the records were
always fine.

## What this collects, and why each line

Read-only, every one of them, and each answers a different candidate:

- `iptables -S` / `-L INPUT` — the whole question, first. We install no
  rules anywhere in this project (checked), so anything here is FireOS's
  own, and an INPUT policy of DROP would end the investigation on one line.
- listening sockets — whether the endpoints bind the wildcard address or
  loopback. A server on 127.0.0.1 answers its own `getInfo` perfectly and
  is unreachable from anywhere else, which is exactly the shape of the
  contradiction in #77.
- `ip addr` / `ip route` — the address and the route the device believes it
  has, from its own side rather than from the controller's registration.
- `icmp_echo_ignore_all` — whether the failed ping means anything at all. A
  device configured to ignore echo requests is not evidence of a firewall,
  and reading it as such would send the next person down the wrong path.

Nothing here changes anything on the device. That is a property worth
keeping: this runs while someone is already confused, and a diagnostic that
mutates state removes the evidence it was called to collect.
"""

from __future__ import annotations

# (label, command). Ordered so the most decisive answer is at the top of the
# output — whoever reads this is looking for one thing.
CHECKS: tuple[tuple[str, str], ...] = (
    ("iptables filter rules",
     "iptables -S 2>&1 || echo '(iptables not available)'"),
    ("iptables INPUT chain with counters",
     "iptables -L INPUT -n -v 2>&1 || echo '(iptables not available)'"),
    ("listening TCP sockets",
     "netstat -ltn 2>/dev/null || busybox netstat -ltn 2>/dev/null "
     "|| cat /proc/net/tcp"),
    ("wlan0 address",
     "ip addr show wlan0 2>&1 || busybox ifconfig wlan0 2>&1"),
    ("routes",
     "ip route 2>&1 || busybox route -n 2>&1"),
    ("ICMP echo ignored?",
     "cat /proc/sys/net/ipv4/icmp_echo_ignore_all 2>&1"),
    ("ICMP echo ignored for broadcasts?",
     "cat /proc/sys/net/ipv4/icmp_echo_ignore_broadcasts 2>&1"),
)


def script() -> str:
    """
    One shell line running every check, each under its own heading.

    Headings rather than bare output because this lands in a log that people
    read weeks later: an `iptables` dump and a `netstat` dump are not
    distinguishable at a glance, and a reader who cannot tell which is which
    will guess.

    Every command swallows its own failure. A device without `iptables` is
    an ordinary answer to this question — not an error that should abort the
    remaining checks, which is what a bare `&&` chain would do.
    """
    parts = []
    for label, cmd in CHECKS:
        parts.append(f"echo '=== {label} ==='; {cmd}")
    return "; ".join(parts)
