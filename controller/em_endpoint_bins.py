"""
em_endpoint_bins.py — the Spotify and AirPlay binaries, and getting them onto a device
=====================================================================================

`librespot` (Spotify Connect) and `shairport-sync` (AirPlay) run as
subprocesses of the firmware, and neither is part of it. Both are third-party
programs with no Android build published anywhere, so they are cross-compiled
once from `device/librespot/` and `device/shairport/` and then have to reach
every device — which until this module meant `adb push` over USB, a cable on
the Dot for every install and every update. That is the friction OTA exists to
remove, and #16 is it arriving for the last two payloads that still had it.

**The store is a fleet-level thing, the install is per device.** A binary is
uploaded once into `endpoint_bins/` beside the SQLite DB — inside the data
volume, so it survives an image upgrade the way `oww_models/` does — and is
then pushed to each device from there. Uploading per device would mean sending
the same 20MB up the dashboard once per Dot, and would leave no way to answer
"is this device running the binary I built?", which is the question a fleet
asks after a rebuild.

**Why the ELF header is checked and the version is not.** The single most
likely upload mistake is the host build: `cargo build --release` without the
target, or the x86 shairport-sync that `./configure` produces when it finds
the host compiler — both are plausible files with plausible names that the
device cannot exec. An ARM32 ELF check catches that at the dashboard, where
there is somebody to tell. It cannot go further: these are upstream programs
with no Revoice version string in them, and there is no manifest to compare
against, so `md5` is the only identity either end can agree on.

This module is pure path, planning and header logic — no aiohttp, no db, no
websockets — so what decides to push, and what refuses to, is unit-tested
rather than only exercised against a live device. The transport lives in
em_api.py and is `_stream_file_to_device`, unchanged: it writes to
`{dest}.part` and renames only once the md5 matches, which is exactly the
ordering these two payloads need. A failed install must leave the previous
binary in place, because that binary is what the endpoint falls back to.
"""

from __future__ import annotations

import hashlib
import os
from pathlib import Path

STORE_SUBDIR = "endpoint_bins"

# Generous against a ~20MB librespot (Rust, stripped) and a ~1MB
# shairport-sync. Bounds what a POST holds in RAM before it is written out.
MAX_BINARY_BYTES = 64 * 1024 * 1024


class Kind:
    """
    One installable endpoint binary.

    `dest` must match the device's own constant — `spotify.BinaryPath` in
    `device/internal/spotify/spotify.go` and `airplay.BinaryPath` in
    `device/internal/airplay/airplay.go`. There is a test. A disagreement
    here installs a perfectly good binary somewhere the firmware never
    looks, and reports success for it.

    `capability` is what the firmware announces when it can run the endpoint
    at all, and `status_attr` is where the runtime answer lands on the live
    Device. Two gates, never one: the capability says the supervisor exists,
    the status says whether the program is there. Collapsing them tells
    somebody their firmware is too old when a file was simply never pushed.

    `config_key` is the device's own toggle, and it is what the automatic
    install is gated on. Pushing ~9MB to every device in the fleet because a
    release exists would spend a lossy link on a program nobody asked to run;
    turning the toggle on IS the ask, and it is the only signal that carries
    the user's intent for this device rather than for the store.
    """

    __slots__ = ("key", "filename", "dest", "capability", "status_attr",
                 "label", "source", "config_key")

    def __init__(self, key, filename, dest, capability, status_attr, label,
                 source, config_key):
        self.key         = key
        self.filename    = filename
        self.dest        = dest
        self.capability  = capability
        self.status_attr = status_attr
        self.label       = label
        self.source      = source
        self.config_key  = config_key


KINDS: dict[str, Kind] = {
    "spotify": Kind(
        key="spotify",
        filename="librespot",
        dest="/data/local/bin/librespot",
        capability="spotify",
        status_attr="spotify_status",
        label="Spotify Connect (librespot)",
        source="device/librespot/build.sh",
        config_key="spotifyEnabled",
    ),
    "airplay": Kind(
        key="airplay",
        filename="shairport-sync",
        dest="/data/local/bin/shairport-sync",
        capability="airplay",
        status_attr="airplay_status",
        label="AirPlay (shairport-sync)",
        source="device/shairport/build.sh",
        config_key="airplayEnabled",
    ),
}


def kind(key: str) -> Kind | None:
    """The Kind for a URL segment, or None. Never raises on user input."""
    return KINDS.get((key or "").strip().lower())


def store_dir(db_path: str | None = None) -> Path:
    """
    Resolve `endpoint_bins/` beside the SQLite DB (DB_PATH env, the same
    default as em_controller). Absolute, so it does not move with the
    process cwd.
    """
    if db_path is None:
        db_path = os.environ.get("DB_PATH", "revoice.db")
    return (Path(db_path).resolve().parent / STORE_SUBDIR)


def store_path(k: Kind, db_path: str | None = None) -> Path:
    """Where this kind's binary lives in the store."""
    return store_dir(db_path) / k.filename


def md5_hex(data: bytes) -> str:
    """The md5 both ends compare. The only identity these binaries have."""
    return hashlib.md5(data).hexdigest()


# ─── ELF validation ──────────────────────────────────────────────────────────

_ELF_MAGIC = b"\x7fELF"

# e_machine values, from the ELF spec. 40 is what both build recipes target;
# the alternatives are named only so a refusal can say what the file IS
# rather than only what it is not — "this is your host build" is a fix,
# "not ARM" is a puzzle.
_EM_ARM     = 40
_EM_386     = 3
_EM_X86_64  = 62
_EM_AARCH64 = 183

_MACHINE_NAMES = {
    _EM_386:     "x86",
    _EM_X86_64:  "x86-64",
    _EM_AARCH64: "ARM64 (aarch64)",
}


def elf_problem(data: bytes) -> str | None:
    """
    Why this upload cannot be an armv7a device binary, or None if it can.

    The message is the whole point: a rejection that only says "invalid"
    sends somebody back to a build that succeeded. Both realistic mistakes
    have a specific, fixable cause and this names them —

      * the host build (x86-64), i.e. the target flag was missed
      * an ARM64 build, i.e. the wrong ABI for a 32-bit MT8163

    Deliberately NOT checked: the API level, the interpreter path, and
    whether the thing is dynamically linked. Those are properties of the
    pinned recipes rather than of the upload, and a check that guessed at
    them would refuse a good binary built a slightly different way — worse
    than accepting one the device then reports as broken, because the
    device's own status is the authority on that and it is read back after
    every install.
    """
    if len(data) < 20:
        return "the file is too small to be a program"
    if data[:4] != _ELF_MAGIC:
        return ("not an ELF executable — this looks like a script, an archive "
                "or the wrong file entirely")
    ei_class = data[4]
    ei_data  = data[5]
    if ei_class != 1:
        return ("a 64-bit ELF — the Echo Dot is 32-bit ARM. Build it with the "
                "recipe in the repo rather than for your own machine")
    if ei_data != 1:
        return "a big-endian ELF, and this device is little-endian"
    # e_machine is a 16-bit little-endian field at offset 18 in a 32-bit ELF.
    machine = int.from_bytes(data[18:20], "little")
    if machine != _EM_ARM:
        named = _MACHINE_NAMES.get(machine)
        if named:
            return (f"built for {named}, not 32-bit ARM — almost certainly "
                    f"your host build rather than the cross-compiled one")
        return f"built for machine type {machine}, not 32-bit ARM"
    return None


# ─── The store ───────────────────────────────────────────────────────────────

def stored(k: Kind, db_path: str | None = None) -> dict | None:
    """
    What the store holds for this kind: {filename, size, md5, mtime}, or
    None when nothing has been uploaded.

    The md5 is computed on read rather than cached beside the file. It is a
    handful of milliseconds on 20MB and it cannot go stale, which a sidecar
    can — and a stale md5 here would report a device as up to date against
    a binary it is not running.
    """
    path = store_path(k, db_path)
    try:
        raw = path.read_bytes()
        st  = path.stat()
    except OSError:
        return None
    return {
        "filename": k.filename,
        "size":     len(raw),
        "md5":      md5_hex(raw),
        "mtime":    int(st.st_mtime),
    }


def scan(db_path: str | None = None) -> dict[str, dict | None]:
    """The whole store, keyed by kind. Missing dir → every kind None."""
    return {key: stored(k, db_path) for key, k in KINDS.items()}


# ─── Device state ────────────────────────────────────────────────────────────

# Mirrors spotify.Report() / airplay.Report() in the firmware, which is also
# what the shell re-read after an install produces. Phrased for the dashboard.
STATUS_REASONS = {
    "not_installed":  "not installed",
    "not_a_file":     "a directory exists where the binary should be",
    "not_executable": "installed but not executable",
}


def device_state(k: Kind, live, db_path: str | None = None) -> dict:
    """
    What a device has, what the store has, and whether an install is needed.

    Four states, and they are four rather than two because they want four
    different things said:

      `unsupported`  the firmware does not announce the capability at all.
                     Nothing to install — a binary here would sit on disk
                     with nothing to exec it.
      `unknown`      the device has not reported. NOT the same as missing:
                     an offline device must not be told its binary is absent,
                     and a controller restart puts every device here until it
                     registers again.
      `missing`      firmware support, no working binary. The install case.
      `installed`    a binary the device reports as runnable.

    `matches_store` is deliberately three-valued. The device reports a size
    and no md5 — the firmware stats the file, it does not hash it — so a
    size match is suggestive and never proof, and None means "cannot tell"
    rather than "no". Claiming a match from a size is how a device would be
    reported as carrying a rebuild it does not have.
    """
    have = stored(k, db_path)
    st   = (getattr(live, k.status_attr, None) if live is not None else None)

    if live is None:
        status = "unknown"
    elif k.capability not in (getattr(live, "capabilities", None) or []):
        status = "unsupported"
    elif st is None:
        status = "unknown"
    elif st.get("ok"):
        status = "installed"
    else:
        status = "missing"

    matches = None
    if status == "installed" and have is not None:
        size = (st or {}).get("size")
        if isinstance(size, int):
            matches = (size == have["size"])

    return {
        "kind":          k.key,
        "label":         k.label,
        "dest":          k.dest,
        "source":        k.source,
        "status":        status,
        "reason":        (st or {}).get("reason"),
        "reason_text":   STATUS_REASONS.get((st or {}).get("reason") or ""),
        "device_size":   (st or {}).get("size"),
        "stored":        have,
        "matches_store": matches,
        "installable":   status in ("missing", "installed") and have is not None,
    }


def refuse_install(k: Kind, live, db_path: str | None = None) -> str | None:
    """
    Why this install must not start, or None to go ahead.

    Checked before a byte is sent, because every one of these produces a
    file on a device that nothing will ever run, and the install would
    otherwise report success for it.
    """
    if live is None:
        return "device is not connected"
    if k.capability not in (getattr(live, "capabilities", None) or []):
        return (f"this firmware has no {k.label} endpoint — installing the "
                f"binary would leave a file nothing runs. Update the firmware "
                f"first")
    if stored(k, db_path) is None:
        return (f"no {k.filename} has been uploaded — build one with "
                f"{k.source} and upload it first")
    return None


def install_needed(k: Kind, capabilities, effective: dict, status,
                   db_path: str | None = None) -> str | None:
    """
    Why this device should be sent this binary now, or None to leave it be.

    A string rather than a bool so the log says WHICH reason it was: "never
    installed" and "the store has a different build" want the same action and
    completely different reading when somebody is working out why a device
    keeps being pushed to.

    Pure and here rather than in em_api for `refuse_install`'s reason — the
    suite cannot import em_api, and this decides whether ~9MB crosses a link
    measured at 5-7% packet loss, on every connect, for every device.

    Four ways of declining, each a rule from elsewhere in this tree:

      * the toggle is off — the user has not asked for this endpoint HERE.
        The store being full is not an instruction to fill the fleet.
      * the firmware does not announce the capability — a binary with nothing
        to exec it, which is what `refuse_install` already says by hand.
      * the shell said nothing we understand (`status is None`) — failure to
        LOOK is not evidence of absence, and moments after a connect the
        shell plane is very likely not up yet. Pushing on that is a guess,
        and the same rule `reconcile_oww_assets` is built on.
      * the device reports a file of exactly the store's size — the only
        agreement the two ends can reach, since the firmware stats the file
        rather than hashing it. Suggestive and never proof, and the
        alternative is re-pushing every binary on every connect for ever.
    """
    if not (effective or {}).get(k.config_key):
        return None
    if k.capability not in (capabilities or []):
        return None
    have = stored(k, db_path)
    if have is None:
        return None
    if status is None:
        return None
    if not status.get("ok"):
        return f"device reports {status.get('reason') or 'no usable binary'}"
    size = status.get("size")
    if isinstance(size, int) and size == have["size"]:
        return None
    return f"device has {size} bytes, the store has {have['size']}"


# ─── Reading the binary back off the device ──────────────────────────────────

# Mirrors Report() in the firmware, over the shell plane, so an install can
# report the device's own answer rather than assume its own success. Written
# with `[` tests and `wc -c` rather than `stat`, because Android's toolbox
# `stat` is not on every SKU and busybox's format flags differ from
# coreutils' — the tests and `wc` are in every shell this has to run in.
STAT_MARKER = "EMBIN:"


def stat_command(k: Kind) -> str:
    """The shell one-liner whose output `parse_stat` reads."""
    p = k.dest
    return (
        f'if [ ! -e "{p}" ]; then echo {STAT_MARKER}missing; '
        f'elif [ -d "{p}" ]; then echo {STAT_MARKER}dir; '
        f'elif [ ! -x "{p}" ]; then echo {STAT_MARKER}noexec; '
        f'else echo {STAT_MARKER}ok:$(wc -c < "{p}"); fi'
    )


def parse_stat(output: str) -> dict | None:
    """
    Turn `stat_command`'s output into the same shape the device reports on
    its register message, so it can be assigned straight onto the live
    Device and read by everything that already reads that field.

    None when the marker is absent — the shell said nothing we understand,
    which is a link problem and must not be recorded as "not installed". An
    install that verified its own md5 and then recorded the file as missing
    would undo a successful install in the dashboard.

    The LAST marker line wins. The shell plane is a long-lived session
    rather than a fresh process per command, so a previous command's output
    can still be in the buffer when this one is read; taking the first match
    would answer with whatever was there before. The echoed command line
    itself is not a hazard — it starts with `if`, not with the marker.
    """
    line = None
    for raw in (output or "").splitlines():
        raw = raw.strip()
        if raw.startswith(STAT_MARKER):
            line = raw[len(STAT_MARKER):]
    if line is None:
        return None
    if line == "missing":
        return {"ok": False, "reason": "not_installed"}
    if line == "dir":
        return {"ok": False, "reason": "not_a_file"}
    if line == "noexec":
        return {"ok": False, "reason": "not_executable"}
    if line.startswith("ok:"):
        try:
            return {"ok": True, "size": int(line[3:].strip())}
        except ValueError:
            # It ran, the file is there and executable, and only the size is
            # unreadable. Reporting ok without one beats reporting nothing:
            # the size is presentation, the executable bit is the gate.
            return {"ok": True}
    return None
