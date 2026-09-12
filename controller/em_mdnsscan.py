"""
em_mdnsscan.py — what the network can actually see of a device's endpoints.

**This exists because the same question was answered wrong three times in one
afternoon**, chasing "my Echo is not in my AirPlay / Spotify Connect list".
Every measurement available at the time was taken ON the Echo, and a
measurement taken on the Echo cannot distinguish the two answers that matter:
the records exist and go out, or the records exist and nobody else ever sees
them. The controller sits on the same LAN and is a second vantage point, which
is the whole of what was missing.

The browse itself lives in em_api (it needs the shared AsyncZeroconf). What is
here is the judgement, which is where the mistakes were:

**"Found nothing" and "found other hosts but not ours" are opposite
conclusions and must never render the same.** Nothing at all means the scan
itself did not work — a quiet moment, a browse that never ran, multicast not
reaching this host — and says NOTHING about the device. Other hosts but not
ours is a real, positive finding about the device. Reading the first as the
second is exactly the error that produced two confident wrong diagnoses; it is
the same shape as "a failure to LOOK is not evidence of absence" elsewhere in
this tree, and it is encoded here rather than left to whoever reads the output.
"""

from typing import NamedTuple, Optional

# The endpoint service types. Keyed by the config key that turns each on, so a
# caller can say "this device has Spotify enabled and it is not visible".
SERVICES = {
    "spotify": ("_spotify-connect._tcp.local.", "Spotify Connect"),
    "airplay": ("_raop._tcp.local.", "AirPlay"),
}


class Finding(NamedTuple):
    """One service instance seen on the network."""
    service: str          # "spotify" / "airplay"
    name: str             # the instance name as advertised
    address: str          # resolved IPv4, or "" when it could not be resolved
    port: int
    target: str           # the SRV target, e.g. "echodot.local."
    txt: dict


class Verdict(NamedTuple):
    """What can honestly be said about one device and one service.

    Three booleans rather than two, because there are three states and
    collapsing any pair of them is how this gets reported wrongly:
    `enabled` — was there anything to look for; `reachable` — did the scan
    work at all; `visible` — was it found. A switched-off endpoint is not a
    failure and must not land in the denominator of "N of M visible".
    """
    service: str
    enabled: bool
    reachable: bool       # the scan worked at all — see the module docstring
    visible: bool         # this device's endpoint was seen
    detail: str
    # Whether the endpoint can be running at all. False only for the one
    # case where "not seen" has a complete explanation that is not about the
    # network: the device has no controller session, so it never received
    # the configuration that STARTS the endpoint. Defaulted, so every
    # existing construction keeps its meaning.
    running: bool = True


# An unresolvable SRV target is the failure that cost the most time here: the
# device advertised `localhost.local`, every client resolved that to its own
# loopback, and nothing said so. Worth naming wherever it appears again.
BAD_TARGETS = ("localhost.", "localhost.local.")


def verdict(service: str, findings: list, device_ip: str,
            enabled: bool, connected: bool = True) -> Verdict:
    """
    What this scan proves about one device's one endpoint.

    `findings` is everything seen for this service, from every host — the
    other hosts are what make a negative meaningful.

    `connected` is whether the device has a controller session right now,
    and it changes what a negative MEANS rather than merely decorating it.
    **Spotify Connect and AirPlay are off by default on the device and are
    started only by the controller's config push**, so a device with no
    session runs neither and advertises nothing. Its silence is then a
    consequence of the lost session and says nothing whatever about the
    network — which is exactly how it was misread on 2026-09-11, as a second
    independent symptom when it was the same symptom seen twice.

    A signal downstream of the fault cannot corroborate the fault.
    """
    label = SERVICES.get(service, (None, service))[1]

    if not enabled:
        return Verdict(service, False, True, False,
                       f"{label} is switched off for this device.")

    mine = [f for f in findings if f.address and f.address == device_ip]
    if mine:
        f = mine[0]
        if f.target.lower() in BAD_TARGETS:
            # Advertised, resolvable by nobody: the SRV target sends every
            # client to its own loopback.
            return Verdict(service, True, True, False,
                           f"{label} is advertised as {f.name!r}, but its "
                           f"address record says {f.target!r} — every client "
                           f"resolves that to itself. The device needs a real "
                           f"hostname.")
        return Verdict(service, True, True, True,
                       f"{label} is visible on the network as {f.name!r} at "
                       f"{f.address}:{f.port}.")

    if not findings:
        # THE important branch. Nothing was seen from anybody, so this says
        # nothing about the device — only that the scan came back empty.
        return Verdict(service, True, False, False,
                       f"Nothing at all answered for {label}, from any host. "
                       f"That is a result about this scan, not about the "
                       f"device — try again, and check whether anything else "
                       f"on the network offers {label} at all.")

    if not connected:
        # Complete explanation, and not about the network. Said before the
        # others-answered branch, which would otherwise render this as a
        # finding about the radio.
        return Verdict(service, True, True, False,
                       f"{label} is not running: this Echo has no controller "
                       f"session, and the endpoints are started by the "
                       f"controller's configuration. This says nothing about "
                       f"the network — reconnect the device first.",
                       running=False)

    others = len({f.address for f in findings if f.address})
    return Verdict(service, True, True, False,
                   f"{label} was not seen from this device, while "
                   f"{others} other host(s) on the network did answer. "
                   f"The scan works; this device is not being heard.")


def summarise(verdicts: list) -> str:
    """One line for the top of the panel, honest about a scan that failed."""
    live = [v for v in verdicts if v.enabled]
    if not live:
        return "Nothing was checked."
    if not any(v.reachable for v in live):
        return ("The scan found no mDNS on this network at all — check the "
                "result again before concluding anything about the device.")
    # Only endpoints that are BOTH switched on and actually scanned belong in
    # the count. A switched-off one is not a failure, and a scan that did not
    # work is not a finding.
    checked = [v for v in live if v.reachable]
    if checked and all(not v.running for v in checked):
        return ("This Echo has no controller session, so its endpoints are "
                "not running. Nothing here is about the network.")
    visible = [v for v in checked if v.visible]
    if len(visible) == len(checked):
        return "Every enabled endpoint is visible on the network."
    if not visible:
        return "No enabled endpoint is visible on the network."
    return (f"{len(visible)} of {len(checked)} enabled endpoint(s) are "
            f"visible on the network.")


def txt_note(txt: dict) -> Optional[str]:
    """
    Anything about the advertised TXT worth showing next to a verdict.

    Deliberately not a pass/fail: a certified Denon Spotify Connect receiver
    ships without `Stack`, so its absence is a difference and not a fault, and
    presenting it as one would send the next person down the wrong path — the
    one this module exists to stop.
    """
    if not txt:
        return None
    keys = {k.lower() for k in txt}
    if "cpath" in keys and "stack" not in keys:
        return ("No `Stack` key. librespot has never sent one and certified "
                "receivers exist without it, so this is a difference rather "
                "than a fault.")
    return None
