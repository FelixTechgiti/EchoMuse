# mdnsprobe — ask the network an mDNS question, on the device

Sends an mDNS query from the Echo Dot itself and prints every answer, decoded:
PTR, SRV (with port and target), TXT and A records.

```
$ ./probe _spotify-connect._tcp.local 4
probing _spotify-connect._tcp.local for 4s

=== 252 bytes from 192.168.178.140 - 4 answers, 0 additional
  -> PTR _spotify-connect._tcp.local  EchoDot._spotify-connect._tcp.local
  -> SRV EchoDot._spotify-connect._tcp.local  port 44644 target echodot.local
  -> TXT EchoDot._spotify-connect._tcp.local  VERSION=1.0 | CPath=/ |
  -> A   echodot.local  192.168.178.140
```

## Why it exists

"My Echo is not in my AirPlay / Spotify Connect list" was diagnosed three
times against **the wrong evidence**, because until this existed the only
generator of mDNS traffic was a person opening an app on their phone. A quiet
network and a dead responder produce identical readings, and the difference
between them is the whole diagnosis. Two wrong conclusions were published
before that was noticed — one of them confidently.

It also settles the question the packet captures could not: not *whether*
something answers, but **what it says**. A complete advertisement with a
resolvable SRV target moves the investigation off the device entirely.

## Usage

```
probe <service> [seconds] [unicast-target]
```

- `service` — e.g. `_spotify-connect._tcp.local`, `_raop._tcp.local`,
  `_services._dns-sd._udp.local` (the meta-query every responder answers, and
  the fastest way to see whether anything on the box is advertising at all).
- `seconds` — how long to listen. It re-asks once a second for the first three,
  because this link has been measured at 4.6-7.1% packet loss and a single
  unanswered query proves nothing.
- `unicast-target` — aim at ONE host instead of the group. "The multicast never
  left the box" and "nobody chose to answer" look identical without it.

## Two properties that are deliberate

**It binds an ephemeral port, never 5353.** Whether two responders on one host
interfere is exactly the kind of thing this gets used to test, so the tool must
not become a third one. It sets the unicast-response bit so answers come back
to its own socket.

**The consequence, and it matters when reading a negative:** a responder that
replies only to the multicast group is invisible here. `tinysvcmdns` —
shairport-sync's bundled responder — does exactly that, so **no `_raop._tcp`
reply from this tool is not evidence about AirPlay.** `libmdns`, which
librespot uses, answers unicast and is visible.

## Build

`./build.sh` — needs `gcc-arm-linux-gnueabi` and `libc6-dev-armel-cross`
(headers only; nothing is linked). The script prints how to get the result onto
a device through the controller's shell plane, and why the md5 has to be
compared at both ends.

It links **no libc**, only raw ARM EABI syscalls, which is what makes it ~3KB
and makes bionic a non-question. Note ARM has *direct* socket syscalls rather
than i386's `socketcall` multiplexer; getting that wrong fails as a bare
"socket failed" with no other clue.
