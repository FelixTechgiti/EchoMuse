"""
Firmware auto-update inside a window the operator chooses (#21).

Home Assistant updates the controller add-on by itself. Firmware has no
equivalent: every OTA is a click per device, and a fleet stays behind until
somebody remembers.

**The window is the whole feature, not a refinement of it.** An OTA here is
not a background download — the device reboots, and on the way it can roll
back (`start_server.sh` counts fast exits and flips the A/B symlink after
three). Anyone in the kitchen experiences that as the assistant being dead for
the length of it. "Install whenever a release appears" is therefore a worse
product than no auto-update at all, because it converts a chore into an
ambush.

Everything here is PURE, for em_linkauth.decide's reason: the cost of a wrong
answer is a reboot in somebody's evening, and that is worth being able to
enumerate rather than read out of an `if` chain in a coroutine. The scheduler
in em_api does the awaiting; this file does the deciding.

## Two absences that must not read the same

A device that CANNOT report what owns its music plane (`audio_state` absent —
firmware that predates the capability) is not a device that reported silence.
The compatibility rule is to degrade to the old behaviour, never to a wrong
answer, so the unknown is handled by asking a narrower question: does this
device have any local endpoint turned on at all? If it has none, there is
nothing an unknown could be hiding and the update proceeds. If it has one, the
update waits for a device that can say — and the operator's remedy is the
button that was always there.

Refusing ALL older firmware would have been the other reading, and it is the
wrong one twice over: it makes the feature useless for exactly the fleet most
behind, and the way out of old firmware is an update.
"""

from __future__ import annotations

from typing import NamedTuple, Optional

# Local sources a device can own its music plane with. Mirrors
# em_audiostate.LOCAL_SOURCES; duplicated rather than imported so this module
# stays free of everything but the decision — the same reason the sendspin
# runtime takes a PlaneOwner interface rather than the arbiter.
LOCAL_SOURCES = ("sendspin", "spotify", "airplay")

# Anything the device names as owning the speaker means "not idle", including
# the controller's own voice and media. A source this module does not know is
# treated as busy rather than ignored: an unrecognised name is the one case
# where guessing costs somebody their music.
IDLE_SOURCES = ("none", "", None)

MINUTES_PER_DAY = 24 * 60


class Window(NamedTuple):
    """Minutes since local midnight. `end` may be < `start` (23:00–02:00)."""

    start: int
    end: int


class Decision(NamedTuple):
    update: bool
    reason: str


def parse_hhmm(text: str) -> Optional[int]:
    """
    "03:00" → 180. None for anything that is not a real time of day.

    Strict rather than forgiving: this comes from a text field, and a window
    silently read as something else is a reboot at a time nobody chose.
    """
    if not isinstance(text, str):
        return None
    parts = text.strip().split(":")
    if len(parts) != 2:
        return None
    try:
        hh, mm = int(parts[0]), int(parts[1])
    except ValueError:
        return None
    if not (0 <= hh <= 23 and 0 <= mm <= 59):
        return None
    return hh * 60 + mm


def parse_window(text: Optional[str]) -> Optional[Window]:
    """
    "03:00-05:00" → Window(180, 300). None when there is no usable window.

    A window whose ends are EQUAL is None, not a 24-hour window and not an
    instant. Both readings are defensible and that is exactly the problem:
    whichever one this picked, the other is somebody's whole fleet rebooting
    at a time they thought they had excluded. An unusable window disables the
    feature, which is the failure that costs nothing.
    """
    if not text:
        return None
    parts = str(text).split("-")
    if len(parts) != 2:
        return None
    start = parse_hhmm(parts[0])
    end = parse_hhmm(parts[1])
    if start is None or end is None or start == end:
        return None
    return Window(start, end)


def in_window(now_minutes: int, window: Window) -> bool:
    """
    Whether a local time of day falls inside the window.

    Half-open at the end: a window of 03:00–05:00 includes 03:00 and excludes
    05:00, so two adjacent windows cannot both claim the same minute.

    A window that wraps past midnight is the normal case for this feature, not
    an edge one — "overnight" is what most people mean — so it is handled by
    the comparison rather than by rejecting it at parse time.
    """
    now = now_minutes % MINUTES_PER_DAY
    if window.start < window.end:
        return window.start <= now < window.end
    return now >= window.start or now < window.end


class DeviceView(NamedTuple):
    """
    Everything about one device the decision needs, and nothing else.

    Passed as a plain tuple so a test can state a case in one line; assembled
    in em_api, which is the only place that knows where each field lives.
    """

    device_id: str
    # Whether the operator has admitted this device to the fleet. An
    # unapproved device is one somebody has not yet decided about, and
    # installing firmware on it unattended decides for them.
    approved: bool
    online: bool
    # Busy as the controller can see it: a turn in flight, speaking, or the
    # controller's own stream playing.
    busy: bool
    # What the DEVICE said owns its music plane, or None when it has not said.
    # None is "cannot tell", never "silent" — see the module docstring.
    audio_source: Optional[str]
    # Whether the firmware can report the above at all.
    audio_state_capable: bool
    # Whether any device-local endpoint is switched on. Only consulted when
    # the device cannot report, to tell "nothing to interrupt" apart from
    # "something that cannot be seen".
    endpoints_enabled: bool
    firmware_ver: Optional[str]
    # True while this device already has an update queued or running.
    updating: bool


def device_idle(dev: DeviceView) -> Decision:
    """
    Whether this device can be rebooted without taking something from
    somebody. Split out because it is the half worth reading on its own.
    """
    if dev.busy:
        return Decision(False, "in a voice turn or playing from Home Assistant")
    if dev.audio_source is not None:
        if dev.audio_source not in IDLE_SOURCES:
            return Decision(False, f"playing {dev.audio_source}")
        return Decision(True, "idle")
    if not dev.audio_state_capable:
        if dev.endpoints_enabled:
            return Decision(
                False,
                "cannot report what it is playing and has a streaming "
                "endpoint switched on",
            )
        return Decision(True, "idle, and has no local endpoint to interrupt")
    # Capable but silent so far: it has not reported since the controller
    # started. Same treatment as incapable — the capability says it CAN say,
    # not that it HAS.
    if dev.endpoints_enabled:
        return Decision(False, "has not yet reported what it is playing")
    return Decision(True, "idle, and has no local endpoint to interrupt")


def decide(
    *,
    enabled: bool,
    window: Optional[Window],
    now_minutes: int,
    target_version: Optional[str],
    device: DeviceView,
) -> Decision:
    """
    Whether to update ONE device, right now.

    Order matters only for the message: the first refusal is the one an
    operator is told, so the checks run from the most general ("this is
    switched off") to the most specific ("this device is playing").

    It never bypasses `_post_device_update`'s own refusals — this decides
    whether to ASK. `already_running` and the rest still apply, and the
    duplicate check here exists so the common case does not spend a request
    to be told no.
    """
    if not enabled:
        return Decision(False, "automatic updates are off")
    if window is None:
        return Decision(False, "no update window is set")
    if not in_window(now_minutes, window):
        return Decision(False, "outside the update window")
    if not target_version:
        return Decision(False, "no release to install")
    if not device.approved:
        return Decision(False, "not approved")
    if not device.online:
        return Decision(False, "offline")
    if device.updating:
        return Decision(False, "already updating")
    if device.firmware_ver == target_version:
        return Decision(False, f"already running {target_version}")
    idle = device_idle(device)
    if not idle.update:
        return Decision(False, idle.reason)
    return Decision(True, f"idle and behind — installing {target_version}")


def next_candidate(
    *,
    enabled: bool,
    window: Optional[Window],
    now_minutes: int,
    target_version: Optional[str],
    devices: list[DeviceView],
) -> tuple[Optional[DeviceView], dict[str, str]]:
    """
    The ONE device to update this tick, plus why each of the others was left.

    One at a time is not tidiness: three concurrent OTAs stalled the event
    loop for 11.1 seconds, measured 2026-09-02, and that loop is what sends
    speaker periods and LED frames — so a device answering somebody pays for a
    device being updated. At 03:00, unattended, across a fleet, that is the
    shape of the failure nobody is awake to see.

    Devices are taken in the order given, which em_api keeps stable, so a
    fleet converges in a predictable order rather than by dict iteration.
    """
    skipped: dict[str, str] = {}
    chosen: Optional[DeviceView] = None
    for dev in devices:
        d = decide(
            enabled=enabled,
            window=window,
            now_minutes=now_minutes,
            target_version=target_version,
            device=dev,
        )
        if d.update and chosen is None:
            chosen = dev
            continue
        if not d.update:
            skipped[dev.device_id] = d.reason
    return chosen, skipped
