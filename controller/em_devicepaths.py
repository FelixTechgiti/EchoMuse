"""
Where a device's files live, across a rename.

The project was renamed and `/data/local/etc/echomuse/` became
`/data/local/etc/revoice/` — **on both halves in the same commit**. That is
the one shape this project's compatibility rules name explicitly: adding a
message is safe unnegotiated, MOVING one never is, because the old path stops
being used and the new one is not there yet, and nothing at either end
reports it.

Firmware in the field predates the rename and reads the old directory. So a
current controller pushing credentials to the new one writes a file nothing
will ever open, reports success, and leaves the device on whatever it had.

**The asymmetry decides the fix.** Repairing the controller repairs every
fielded device at once; repairing the device needs an update, and under
`REQUIRE_DEVICE_TLS` a device whose credentials went to the wrong place
cannot authenticate to receive one. So the controller carries the
compatibility: it WRITES both, and READS either.

The cost is two small files on a device instead of one. The cost of the
alternative is a fleet that cannot be talked to.
"""

from __future__ import annotations

# Where this controller's own firmware puts things.
CURRENT_DIR = "/data/local/etc/revoice"

# Directories previous names used, newest first. Not history for its own
# sake: every one of these is a directory some device in the field is still
# reading, and it stays here until no such device can exist.
LEGACY_DIRS = ("/data/local/etc/echomuse",)


def write_dirs() -> tuple[str, ...]:
    """
    Every directory a pushed file must land in, current first.

    All of them, unconditionally, rather than choosing by firmware version —
    the project negotiates by capability and never by version string, and
    there is no capability for "which directory do you read". Writing both
    needs no answer from the device at all, which is the point: it also
    works for a device that is offline when the decision is made.
    """
    return (CURRENT_DIR, *LEGACY_DIRS)


def read_paths(filename: str) -> tuple[str, ...]:
    """Every place `filename` might be, current first."""
    return tuple(f"{d}/{filename}" for d in write_dirs())


def every_readable_command(filename: str, reader: str) -> str:
    """
    A shell one-liner that runs `reader` against EVERY path that exists,
    labelling each with the path it came from.

    It read only the FIRST one until 2026-09-11, and that is wrong for any
    file with more than one writer — because the two writers cross the
    rename on their own schedules. `supervisor.log` is written by BOTH
    `start_server.sh`, which the controller pushes, and the firmware's own
    `internal/bootlog`, which arrives by OTA. A controller that has been
    updated and a device that has not therefore write to two different
    directories, and "first readable" silently returns the half belonging to
    whichever program moved first.

    Measured on the fleet that day: a device on v2.27.0-fx.1 (bootlog still
    on `/data/local/etc/echomuse`) with a current `start_server.sh` (writing
    `/data/local/etc/revoice`). The fetch returned two lines — the boot and
    the start — and the firmware's account of a 22-hour outage, which is the
    entire reason the file exists, sat unread in the other directory while
    the endpoint reported success.

    Absence stays silent: a missing file contributes nothing rather than an
    error the caller would have to recognise and strip, so "no log at all"
    still comes back empty and the existing message for it still applies.
    """
    parts = [
        f'[ -f {p} ] && echo "--- {p} ---" && {reader} {p} 2>/dev/null'
        for p in read_paths(filename)
    ]
    return "; ".join(parts)


def mkdir_command() -> str:
    """One `mkdir -p` covering every directory written to."""
    return "mkdir -p " + " ".join(write_dirs())
