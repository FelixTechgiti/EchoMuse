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

# AirPlay 2's own service type, browsed alongside the endpoints and given NO
# verdict of its own.
#
# **The device can never advertise it, and that is a property of the build
# rather than a fault.** `device/shairport/build.sh` configures
# `--with-tinysvcmdns` and no `--with-airplay-2`, and of shairport-sync's four
# mDNS backends only `mdns_avahi.c` implements the second service —
# `mdns_tinysvcmdns.c` declares `ap2name` and `secondary_txt_records`
# `__attribute__((unused))` and registers no `mdns_update`. So this build
# offers classic AirPlay (`_raop._tcp`) and nothing else, for ever, until #79
# is done.
#
# It is browsed because that fact is INVISIBLE from here otherwise, and it is
# the first thing somebody needs when their Echo is advertised, reachable, and
# still absent from the AirPlay picker on their phone. Whether a client shows a
# RAOP-only receiver depends on the client and on where in its UI they look,
# and neither is knowable from the controller — but "this device offers classic
# AirPlay only, and N other hosts here offer AirPlay 2" turns an unanswerable
# question into a comparison they can act on.
#
# **Never a Verdict.** A service the user did not switch on and cannot switch
# on must not render as an endpoint that is down; that is the "a control whose
# feature the device lacks is shown disabled WITH THE REASON, never as a
# control that silently does nothing" rule, one layer out.
AIRPLAY2_TYPE = "_airplay._tcp.local."


def airplay_generation_note(ap2_others: int) -> str:
    """
    What to say next to the AirPlay verdict about which generation this is.

    Takes only the count of OTHER hosts advertising `_airplay._tcp`, because
    our own count is known at compile time: it is always zero, and asking the
    network about it would dress a build property up as a measurement.
    """
    base = ("This is classic AirPlay: the device advertises `_raop._tcp` and "
            "never `_airplay._tcp`, which this build cannot do at all.")
    if ap2_others > 0:
        return (base + f" {ap2_others} other host(s) on this network do "
                f"advertise it. If your player lists those and not this Echo, "
                f"that difference is the reason, not the network.")
    return (base + " No other host here advertises it either, so nothing on "
            "this network shows what the difference would look like.")


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


# ── Comparing our advertisement against the ones that work ───────────────────
#
# **The measurement that was missing on 2026-09-12.** By then everything the
# device controls had been verified: complete records, a resolvable SRV
# target, the advertised port answering, and — decisively — a SECOND HOST on
# the same network (the controller) hearing all of it. And the Spotify app on
# a phone on that same network still did not list the device, while it listed
# other Spotify Connect speakers.
#
# At that point the only question left is what those other speakers say that
# we do not. The scan already collects their records; it was throwing them
# away and reporting a count. So the count became a diff.
#
# **Same network, same instant, same browse** — which is what makes this worth
# more than any comparison against documentation. A key that every working
# receiver on this LAN carries and ours does not is a lead; one that only some
# carry is a difference, and this reports which of the two it is rather than
# deciding for the reader. That distinction is the whole of `txt_note`'s
# existing caution: a certified Denon receiver ships without `Stack`, so its
# absence cannot be a fault on its own.


class TxtComparison(NamedTuple):
    """How our TXT differs from the other hosts advertising this service."""
    others: int                 # how many other hosts were compared against
    missing: tuple              # (key, value, how_many_others) we do not send
    extra: tuple                # (key, value) only we send
    differing: tuple            # (key, ours, theirs, how_many_others)


def _common_value(values: list) -> tuple:
    """The most frequent value and how often it occurred."""
    best, count = "", 0
    for v in set(values):
        n = values.count(v)
        if n > count:
            best, count = v, n
    return best, count


def txt_compare(mine: dict, others: list) -> Optional[TxtComparison]:
    """
    Diff our TXT against the other hosts' TXT for the same service.

    `others` is a list of TXT dicts from OTHER hosts. None when there is
    nothing to compare against — which is a different statement from "no
    differences" and must not render as one.

    Keys are compared case-insensitively because mDNS TXT keys are, and
    because a receiver writing `CPath` where another writes `cpath` is not a
    finding; reporting it as one is noise that buries the real entry.
    """
    if not others:
        return None

    def lower(d):
        return {str(k).lower(): str(v) for k, v in (d or {}).items()}

    ours = lower(mine)
    theirs = [lower(o) for o in others if o]
    if not theirs:
        return None

    every_key = set()
    for t in theirs:
        every_key |= set(t)

    missing, differing = [], []
    for k in sorted(every_key):
        values = [t[k] for t in theirs if k in t]
        value, count = _common_value(values)
        if k not in ours:
            missing.append((k, value, count))
        elif ours[k] != value:
            differing.append((k, ours[k], value, count))

    extra = tuple(sorted((k, v) for k, v in ours.items() if k not in every_key))
    # Keyword arguments, because the fields are three same-shaped tuples and
    # positionally they swapped silently the first time this was written.
    return TxtComparison(others=len(theirs), missing=tuple(missing),
                         extra=extra, differing=tuple(differing))


def describe_comparison(c: Optional[TxtComparison]) -> Optional[str]:
    """
    One line for the panel, or None when there is nothing to say.

    Silent when the records agree: a comparison that prints "no differences"
    every time is a line everybody learns to skip, and this one has to be
    read on the day it says something.
    """
    if c is None or not (c.missing or c.differing):
        return None

    parts = []
    for k, v, n in c.missing:
        # "all of them" is the shape of a lead; "2 of 8" is the shape of a
        # difference, and the reader needs to see which this is.
        parts.append(f"missing {k}={v!r} ({n} of {c.others} others send it)")
    for k, ours, theirs, n in c.differing:
        parts.append(f"{k} is {ours!r} here, {theirs!r} on {n} of {c.others}")
    return "Compared with the other devices on this network: " + "; ".join(parts)
