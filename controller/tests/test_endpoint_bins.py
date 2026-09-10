"""
Tests for em_endpoint_bins — the Spotify and AirPlay binary store.

Three things are guarded here, and each one is a failure that would be
invisible from the dashboard:

  * the destination paths, against the device's own constants. A mismatch
    installs a perfectly good binary somewhere the firmware never looks and
    reports success for it.
  * the ELF check, which is what stops a host build from being stored. The
    device cannot exec one, and the observable is an endpoint that enables,
    says "pushed" and plays nothing.
  * the four device states, which exist because "we have not heard from this
    device" and "the binary is missing" want different things said, and
    collapsing them tells an offline device's owner their file is gone.
"""

import re
from pathlib import Path

import pytest

import em_endpoint_bins as ebins


REPO = Path(__file__).resolve().parents[2]


# ─── The device agrees about where these go ──────────────────────────────────
# The controller writes to a path and the firmware execs a constant. Nothing
# at runtime compares them, and a disagreement is a silent no-op: the file
# lands, md5 verifies, the install reports success, and the endpoint still
# says not_installed.

@pytest.mark.parametrize("kind_key,go_file", [
    ("spotify", "device/internal/spotify/spotify.go"),
    ("airplay", "device/internal/airplay/airplay.go"),
])
def test_dest_matches_device_binary_path(kind_key, go_file):
    src = (REPO / go_file).read_text()
    m = re.search(r'const\s+BinaryPath\s*=\s*"([^"]+)"', src)
    assert m, f"BinaryPath constant not found in {go_file}"
    assert ebins.KINDS[kind_key].dest == m.group(1)


def test_filename_is_the_basename_of_dest():
    # The store names the file the same as the device does, so what is
    # uploaded, what is stored and what is installed are one name in the UI.
    for k in ebins.KINDS.values():
        assert k.dest.endswith("/" + k.filename)


def test_capability_names_match_the_firmware_list():
    # These are the strings the device puts in its register message; the
    # controller reads them via Device.spotify_capable / airplay_capable.
    src = (REPO / "device/internal/client/control.go").read_text()
    for k in ebins.KINDS.values():
        assert f'"{k.capability}"' in src


def test_kind_lookup_is_forgiving_and_never_raises():
    assert ebins.kind("spotify") is ebins.KINDS["spotify"]
    assert ebins.kind(" AirPlay ") is ebins.KINDS["airplay"]
    assert ebins.kind("firmware") is None
    assert ebins.kind("") is None
    assert ebins.kind(None) is None


# ─── ELF validation ──────────────────────────────────────────────────────────

def _elf(machine: int, ei_class: int = 1, ei_data: int = 1) -> bytes:
    """A minimal ELF header — enough for elf_problem, which reads 20 bytes."""
    b = bytearray(b"\x7fELF" + bytes([ei_class, ei_data]) + b"\x00" * 14)
    b[18:20] = machine.to_bytes(2, "little")
    return bytes(b)


def test_armv7_elf_is_accepted():
    assert ebins.elf_problem(_elf(40)) is None


def test_host_build_is_named_as_such():
    # The likeliest mistake by a wide margin: cargo or ./configure finding
    # the host compiler. The message has to say WHICH mistake, because the
    # build it came from succeeded.
    problem = ebins.elf_problem(_elf(62, ei_class=2))
    assert problem is not None
    assert "64-bit" in problem


def test_x86_32_build_is_named():
    problem = ebins.elf_problem(_elf(3))
    assert problem is not None
    assert "x86" in problem


def test_arm64_build_is_rejected_and_named():
    # A plausible near-miss: the right family, the wrong ABI for a 32-bit
    # MT8163, and a file that looks entirely correct in `ls`.
    problem = ebins.elf_problem(_elf(183))
    assert problem is not None
    assert "ARM64" in problem


def test_non_elf_uploads_are_rejected():
    assert ebins.elf_problem(b"#!/bin/sh\necho hi\n") is not None
    assert ebins.elf_problem(b"") is not None
    assert ebins.elf_problem(b"\x7fELF") is not None  # header truncated


def test_big_endian_elf_is_rejected():
    assert ebins.elf_problem(_elf(40, ei_data=2)) is not None


# ─── The store ───────────────────────────────────────────────────────────────

class FakeDevice:
    def __init__(self, capabilities, **status):
        self.capabilities   = capabilities
        self.spotify_status = status.get("spotify_status")
        self.airplay_status = status.get("airplay_status")


@pytest.fixture
def store(tmp_path, monkeypatch):
    """A DB_PATH whose neighbour dir is this test's own store."""
    monkeypatch.setenv("DB_PATH", str(tmp_path / "revoice.db"))
    return tmp_path / ebins.STORE_SUBDIR


def _put(store_dir: Path, k, data: bytes):
    store_dir.mkdir(parents=True, exist_ok=True)
    (store_dir / k.filename).write_bytes(data)


def test_store_dir_sits_beside_the_database(store, tmp_path):
    assert ebins.store_dir() == tmp_path / "endpoint_bins"


def test_empty_store_reports_every_kind_as_absent(store):
    assert ebins.scan() == {"spotify": None, "airplay": None}


def test_stored_reports_size_and_md5(store):
    k = ebins.KINDS["spotify"]
    _put(store, k, b"binary-bytes")
    entry = ebins.stored(k)
    assert entry["size"] == len(b"binary-bytes")
    assert entry["md5"] == ebins.md5_hex(b"binary-bytes")
    assert entry["filename"] == "librespot"


def test_md5_is_recomputed_rather_than_cached(store):
    # A sidecar md5 can go stale, and a stale one reports a device as up to
    # date against a binary it is not running.
    k = ebins.KINDS["spotify"]
    _put(store, k, b"first")
    first = ebins.stored(k)["md5"]
    _put(store, k, b"second")
    assert ebins.stored(k)["md5"] != first


# ─── Device state ────────────────────────────────────────────────────────────

def test_offline_device_is_unknown_not_missing(store):
    # The one that matters most: an offline device must never be told its
    # binary is absent. A controller restart puts every device here.
    st = ebins.device_state(ebins.KINDS["spotify"], None)
    assert st["status"] == "unknown"
    assert st["installable"] is False


def test_device_that_has_not_reported_is_unknown(store):
    live = FakeDevice(["spotify"], spotify_status=None)
    assert ebins.device_state(ebins.KINDS["spotify"], live)["status"] == "unknown"


def test_firmware_without_the_capability_is_unsupported(store):
    # Distinct from "missing" on purpose: there is nothing to install, and
    # offering an install would put a file on disk with nothing to exec it.
    live = FakeDevice(["mic", "speaker"])
    st = ebins.device_state(ebins.KINDS["spotify"], live)
    assert st["status"] == "unsupported"
    assert st["installable"] is False


def test_capable_device_without_the_binary_is_missing(store):
    live = FakeDevice(["spotify"],
                      spotify_status={"ok": False, "reason": "not_installed"})
    st = ebins.device_state(ebins.KINDS["spotify"], live)
    assert st["status"] == "missing"
    assert st["reason_text"] == "not installed"
    # Nothing uploaded yet, so there is nothing to install FROM.
    assert st["installable"] is False


def test_missing_becomes_installable_once_a_binary_is_uploaded(store):
    k = ebins.KINDS["spotify"]
    _put(store, k, b"x" * 32)
    live = FakeDevice(["spotify"],
                      spotify_status={"ok": False, "reason": "not_installed"})
    assert ebins.device_state(k, live)["installable"] is True


def test_installed_device_can_be_reinstalled_over(store):
    # A rebuilt binary is the ordinary case, so "installed" stays
    # installable — as long as there is something in the store to send.
    k = ebins.KINDS["airplay"]
    live = FakeDevice(["airplay"], airplay_status={"ok": True, "size": 900})
    assert ebins.device_state(k, live)["status"] == "installed"
    assert ebins.device_state(k, live)["installable"] is False
    _put(store, k, b"w" * 900)
    assert ebins.device_state(k, live)["installable"] is True


def test_matches_store_is_none_when_nothing_can_be_compared(store):
    # The device stats the file, it does not hash it, so a size is all
    # there is. No stored binary means no comparison at all.
    live = FakeDevice(["spotify"], spotify_status={"ok": True, "size": 64})
    assert ebins.device_state(ebins.KINDS["spotify"], live)["matches_store"] is None


def test_matches_store_compares_sizes_when_both_are_known(store):
    k = ebins.KINDS["spotify"]
    _put(store, k, b"y" * 64)
    live = FakeDevice(["spotify"], spotify_status={"ok": True, "size": 64})
    assert ebins.device_state(k, live)["matches_store"] is True
    live_other = FakeDevice(["spotify"], spotify_status={"ok": True, "size": 65})
    assert ebins.device_state(k, live_other)["matches_store"] is False


def test_matches_store_is_none_when_the_device_reported_no_size(store):
    # Old firmware, or a stat whose size could not be read. Absence must
    # read as "cannot tell", never as "does not match" — the same
    # NULL-not-zero rule the wire protocol already keeps.
    k = ebins.KINDS["spotify"]
    _put(store, k, b"z" * 8)
    live = FakeDevice(["spotify"], spotify_status={"ok": True})
    assert ebins.device_state(k, live)["matches_store"] is None


# ─── refuse_install ──────────────────────────────────────────────────────────
# Every refusal here prevents a file landing on a device that nothing will
# run, with the install reporting success for it.

def test_refuses_when_the_device_is_offline(store):
    assert ebins.refuse_install(ebins.KINDS["spotify"], None) is not None


def test_refuses_when_the_firmware_cannot_run_the_endpoint(store):
    k = ebins.KINDS["spotify"]
    _put(store, k, b"x" * 32)
    why = ebins.refuse_install(k, FakeDevice(["mic"]))
    assert why is not None and "firmware" in why


def test_refuses_when_nothing_has_been_uploaded(store):
    why = ebins.refuse_install(ebins.KINDS["airplay"], FakeDevice(["airplay"]))
    assert why is not None
    # Names the recipe, so the message is a next step rather than a verdict.
    assert "device/shairport/build.sh" in why


def test_allows_when_capable_and_stocked(store):
    k = ebins.KINDS["airplay"]
    _put(store, k, b"x" * 32)
    assert ebins.refuse_install(k, FakeDevice(["airplay"])) is None


# ─── Reading the binary back off the device ──────────────────────────────────

def test_stat_command_names_the_destination():
    cmd = ebins.stat_command(ebins.KINDS["spotify"])
    assert "/data/local/bin/librespot" in cmd
    # No `stat`: Android's toolbox does not always have it and busybox's
    # format flags differ from coreutils'.
    assert "stat " not in cmd


@pytest.mark.parametrize("out,expect", [
    ("EMBIN:missing", {"ok": False, "reason": "not_installed"}),
    ("EMBIN:dir",     {"ok": False, "reason": "not_a_file"}),
    ("EMBIN:noexec",  {"ok": False, "reason": "not_executable"}),
    ("EMBIN:ok:2048", {"ok": True, "size": 2048}),
])
def test_parse_stat_mirrors_the_firmware_report_shape(out, expect):
    assert ebins.parse_stat(out) == expect


def test_parse_stat_tolerates_surrounding_shell_noise():
    assert ebins.parse_stat(
        "sh: warning: something\r\nEMBIN:ok:16\r\n$ "
    ) == {"ok": True, "size": 16}


def test_parse_stat_takes_the_last_answer():
    # The shell plane is one long-lived session, so a previous command's
    # output can still be in the buffer. Answering with the first match
    # would report whatever was there before this install.
    assert ebins.parse_stat(
        "EMBIN:missing\nEMBIN:ok:99\n"
    ) == {"ok": True, "size": 99}


def test_parse_stat_returns_none_when_the_shell_said_nothing_usable():
    # NOT "not installed". A verified transfer followed by an unreadable
    # stat is a link problem, and recording a guess there would undo a
    # successful install in the dashboard.
    assert ebins.parse_stat("") is None
    assert ebins.parse_stat("bash: no such thing\n") is None


def test_parse_stat_keeps_ok_when_only_the_size_is_unreadable():
    # The executable bit is the gate; the size is presentation.
    assert ebins.parse_stat("EMBIN:ok:") == {"ok": True}
