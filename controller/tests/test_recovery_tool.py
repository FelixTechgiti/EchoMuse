"""
Guards for the host-side recovery tool.

The tool is the thing someone runs when the dashboard cannot reach their
device, so it is exercised exactly when nothing else is available to catch a
mistake. These tests cover the couplings that fail silently: the version
placeholder the API substitutes, the device paths shared with the controller,
and — most importantly — the ordering that keeps every verifiable thing
verified before the first destructive command.

Importing the tool is safe: it runs nothing at import time and shells out only
from inside its commands.
"""

import ast
import importlib.util
import re
from pathlib import Path

import pytest

CONTROLLER = Path(__file__).resolve().parents[1]
TOOL_PATH = CONTROLLER / "host_tools" / "echomuse-recovery.py"
TOOL_SRC = TOOL_PATH.read_text()
API_SRC = (CONTROLLER / "em_api.py").read_text()


@pytest.fixture(scope="module")
def tool():
    spec = importlib.util.spec_from_file_location("echomuse_recovery", TOOL_PATH)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


# ── Shipping and serving ──────────────────────────────────────────────────────

def test_dockerfile_copies_host_tools():
    """Absent the COPY, the endpoint 500s on a file that is right there in git."""
    dockerfile = (CONTROLLER / "Dockerfile").read_text()
    assert re.search(r"^COPY\s+host_tools/", dockerfile, re.M), (
        "Dockerfile does not COPY host_tools/ — the recovery tool would be "
        "missing from the image and the Support tab download would fail"
    )


def test_every_host_tool_is_reachable_over_http():
    """
    Same rule the device payloads live under: a file shipped in the image but
    referenced by nothing has no way to reach the person who needs it, and
    nothing says so.
    """
    for path in (CONTROLLER / "host_tools").glob("*.py"):
        assert path.name in API_SRC, (
            f"{path.name} ships in host_tools/ but em_api.py never references "
            f"it — it has no download path"
        )


def test_version_placeholder_matches_what_the_api_substitutes():
    """
    The API rewrites this exact line. Renaming the constant or changing the
    default in the tool leaves the substitution silently doing nothing, and
    every downloaded copy then reports itself as "dev" — which is precisely
    the confusion the stamp exists to prevent.
    """
    assert 'TOOL_VERSION = "dev"' in TOOL_SRC, (
        "the tool no longer contains the literal em_api substitutes on"
    )
    assert 'TOOL_VERSION = "dev"' in API_SRC, (
        "em_api no longer substitutes the tool's version placeholder"
    )


def test_download_endpoint_is_admin_and_read_only():
    """
    It is a GET of a static file and must stay one. The tool itself is
    destructive; the endpoint that hands it over is not, and should never grow
    a way to run it server-side.
    """
    assert 'add_get("/api/support/recovery_tool"' in API_SRC
    assert "/api/support/recovery_tool" not in API_SRC.split("add_post")[-1][:200]

    handler = API_SRC.split("async def _get_recovery_tool")[1].split("\nasync def ")[0]
    decorator = API_SRC.split("async def _get_recovery_tool")[0].rstrip().splitlines()[-1]
    assert "require_admin" in decorator, (
        "_get_recovery_tool is not admin-guarded"
    )
    for forbidden in ("subprocess", "os.system", "shell_run"):
        assert forbidden not in handler, (
            f"the download handler references {forbidden} — it serves a file "
            f"and must not execute anything"
        )


# ── Pinned artefacts ──────────────────────────────────────────────────────────

def test_hashes_are_well_formed(tool):
    assert re.fullmatch(r"[0-9a-f]{64}", tool.F1R30S_SHA256)
    assert tool.KNOWN_IMAGES, "no known FireOS image hashes are pinned"
    for h in tool.KNOWN_IMAGES:
        assert re.fullmatch(r"[0-9a-f]{64}", h), f"malformed image hash: {h}"


def test_patch_hash_mismatch_always_refuses(tool, tmp_path):
    """
    Strict by design: f1r30s.zip is what makes the flashed system reachable,
    so there is no override and no warn-and-continue path.
    """
    bad = tmp_path / "f1r30s.zip"
    bad.write_bytes(b"not the patch")
    with pytest.raises(tool.Fail):
        tool.verify_patch(str(bad))


def test_unknown_image_refuses_by_default_and_can_be_overridden(tool, tmp_path):
    """
    The opposite strictness, deliberately: FireOS 5 has legitimate build
    variants, so an unknown hash must be refusable-but-overridable rather than
    fatal. What it must never do is pass silently.
    """
    img = tmp_path / "update.bin"
    img.write_bytes(b"some other build")

    with pytest.raises(tool.Fail):
        tool.verify_image(str(img), allow_unknown=False)

    tool.verify_image(str(img), allow_unknown=True)  # must not raise


def test_known_image_is_accepted_without_the_override(tool, tmp_path):
    """A pinned hash must actually match a file with that hash."""
    import hashlib

    img = tmp_path / "update.bin"
    img.write_bytes(b"x")
    digest = hashlib.sha256(b"x").hexdigest()
    tool.KNOWN_IMAGES[digest] = "test fixture"
    try:
        tool.verify_image(str(img), allow_unknown=False)  # must not raise
    finally:
        del tool.KNOWN_IMAGES[digest]


# ── Device coupling ───────────────────────────────────────────────────────────

def test_device_paths_match_the_controller(tool):
    """
    Drift here means the tool reads a log nobody writes or repairs a symlink
    nobody reads, and reports success either way.
    """
    assert f'SUPERVISOR_LOG = "{tool.SUPERVISOR_LOG}"' in API_SRC, (
        "supervisor log path has drifted from em_api.SUPERVISOR_LOG"
    )
    start_script = (CONTROLLER / "device_payloads" / "start_server.sh").read_text()
    for slot in tool.SLOTS:
        assert slot in start_script, (
            f"slot name {slot} does not appear in start_server.sh"
        )
    assert tool.SERVER_LINK in start_script, (
        f"{tool.SERVER_LINK} does not appear in start_server.sh — the symlink "
        f"repair would target a path the device does not use"
    )


def test_staging_dir_is_the_ramdisk(tool):
    """
    /tmp in TWRP is a ramdisk, which is the whole reason the patch is staged
    there: it survives the data wipe and the image flash because nothing
    reboots in between. Staging on /sdcard puts the file on the partition
    about to be erased.
    """
    assert tool.STAGE_DIR == "/tmp"


# ── The ordering that matters ─────────────────────────────────────────────────

def _call_lines(func_src: str, name: str):
    """
    Line offsets of CALLS to `name` within a function's source.

    Anchored on the call site, parsed rather than grepped: a previous guard in
    this project searched a function body for an argument and matched the
    docstring explaining it, so it would have gone on passing with the call
    deleted.
    """
    tree = ast.parse(func_src)
    out = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Call):
            fn = node.func
            called = getattr(fn, "id", None) or getattr(fn, "attr", None)
            if called == name:
                out.append(node.lineno)
    return out


def _reimage_src() -> str:
    body = TOOL_SRC.split("def cmd_reimage(")[1].split("\ndef ")[0]
    return "def cmd_reimage(" + body


def test_reimage_verifies_and_stages_before_anything_destructive():
    """
    The invariant the whole design rests on.

    Nothing after the wipe can be undone by stopping, and the device is
    unbootable until the patch is installed — so both files must be hash
    checked and the patch must already be on the device's ramdisk before the
    first destructive command runs.
    """
    src = _reimage_src()

    verify_image = _call_lines(src, "verify_image")
    verify_patch = _call_lines(src, "verify_patch")
    staged = _call_lines(src, "push_verified")

    assert verify_image and verify_patch and staged, (
        "reimage no longer verifies both files and stages the patch"
    )

    # The wipe is issued through adb.shell; find the first destructive one by
    # its command text, which is what actually erases the device.
    lines = src.splitlines()
    destructive = [i + 1 for i, line in enumerate(lines)
                   if "twrp wipe" in line or '"sideload"' in line]
    assert destructive, "no destructive command found in reimage"
    first_destructive = min(destructive)

    for label, calls in (("verify_image", verify_image),
                         ("verify_patch", verify_patch),
                         ("push_verified", staged)):
        assert max(calls) < first_destructive, (
            f"{label} runs at line {max(calls)}, after the first destructive "
            f"command at line {first_destructive} — the device could be wiped "
            f"while carrying an unverified dependency"
        )


def test_reimage_confirms_before_wiping():
    """A typed confirmation, and it must sit before the destructive block."""
    src = _reimage_src()
    lines = src.splitlines()
    confirm = [i + 1 for i, line in enumerate(lines) if "REIMAGE" in line
               and "input(" not in line]
    wipe = [i + 1 for i, line in enumerate(lines) if "twrp wipe" in line]
    assert confirm and wipe and min(confirm) < min(wipe), (
        "the typed confirmation does not precede the wipe"
    )
    assert "args.yes" in src, "there is no way to skip the prompt unattended"


def test_reimage_never_reboots_the_device():
    """
    Rebooting is left to the operator. A failed run must leave the device in
    recovery, where the fix is one command; a reboot turns that into a
    hardware trip.
    """
    tree = ast.parse(_reimage_src())
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        called = getattr(node.func, "attr", None)
        if called not in ("shell", "run", "su", "shell_out"):
            continue
        for arg in ast.walk(node):
            if isinstance(arg, ast.Constant) and isinstance(arg.value, str):
                assert "reboot" not in arg.value, (
                    f"reimage issues a reboot to the device: {arg.value!r}"
                )


def test_failure_after_the_wipe_names_the_recovery_command():
    """
    The one moment the tool must be most useful is the one where it failed.
    A bare traceback here leaves someone holding an unbootable device with no
    idea that recovery is cheap while it stays in TWRP.
    """
    src = _reimage_src()
    tail = src.split("except (Fail")[1]
    assert "install-patch" in tail, (
        "the post-wipe failure path does not name the command that finishes "
        "the job"
    )
    assert "not reboot" in tail.lower() or "do not reboot" in tail.lower(), (
        "the post-wipe failure path does not tell the operator to stay in "
        "recovery"
    )


def test_install_patch_is_separately_runnable(tool):
    """It is the documented recovery path, so it must exist as its own command."""
    parser = tool.build_parser()
    args = parser.parse_args(["install-patch", "--patch", "f1r30s.zip"])
    assert args.func is tool.cmd_install_patch


# ── The flow, actually run ────────────────────────────────────────────────────

class FakeAdb:
    """
    Records every adb interaction in order, so the test can assert what the
    tool DID rather than what its source looks like.
    """

    def __init__(self, md5_of=None):
        self.calls = []
        self.mode = "recovery"
        self._md5 = md5_of or {}

    # -- surface the tool uses ------------------------------------------------
    def state(self):
        return self.mode

    def run(self, args, *, timeout=120, check=False, stream=False):
        self.calls.append(("run", " ".join(args)))
        if args and args[0] == "sideload":
            self.mode = "recovery"
        return 0, ""

    def shell(self, cmd, *, timeout=120, check=False):
        self.calls.append(("shell", cmd))
        if cmd.startswith("md5sum ") or cmd.startswith("busybox md5sum "):
            target = cmd.split()[-1]
            if target == "/dev/null":
                return 0, "d41d8cd98f00b204e9800998ecf8427e  /dev/null"
            return 0, f"{self._md5.get(target, 'nope')}  {target}"
        if cmd.startswith("getprop"):
            return 0, "Amazon/omni_biscuit/biscuit:5.1.1/LMY49M/eng:eng/test-keys"
        if "build.prop" in cmd:
            return 0, ("ro.build.version.release=5.1.1\n"
                       "ro.build.fingerprint=Amazon/csm_biscuit/biscuit:5.1.1")
        if cmd.startswith("twrp sideload"):
            self.mode = "sideload"
        return 0, ""

    def shell_out(self, cmd, *, timeout=120):
        return self.shell(cmd, timeout=timeout)[1]

    def su(self, cmd, *, timeout=120):
        return self.shell(cmd, timeout=timeout)

    def wait_for(self, state, timeout=180):
        self.calls.append(("wait", state))
        self.mode = state

    # -- helpers --------------------------------------------------------------
    def index_of(self, kind, needle):
        for i, (k, text) in enumerate(self.calls):
            if k == kind and needle in text:
                return i
        raise AssertionError(f"no {kind} call containing {needle!r} in "
                             f"{self.calls}")


class Args:
    def __init__(self, **kw):
        self.__dict__.update(kw)


@pytest.fixture
def staged(tool, tmp_path, monkeypatch):
    """A reimage set up to succeed: real files whose hashes are made to match."""
    import hashlib

    image = tmp_path / "update.bin"
    image.write_bytes(b"pretend fireos")
    patch = tmp_path / "f1r30s.zip"
    patch.write_bytes(b"pretend patch")

    monkeypatch.setattr(tool, "F1R30S_SHA256",
                        hashlib.sha256(patch.read_bytes()).hexdigest())
    monkeypatch.setattr(tool, "KNOWN_IMAGES", {
        hashlib.sha256(image.read_bytes()).hexdigest(): "test build",
    })

    adb = FakeAdb(md5_of={
        f"{tool.STAGE_DIR}/f1r30s.zip.part":
            hashlib.md5(patch.read_bytes()).hexdigest(),
    })
    args = Args(image=str(image), patch=str(patch),
                allow_unknown_image=False, yes=True)
    return tool, adb, args


def test_reimage_runs_the_sequence_in_the_right_order(staged):
    tool, adb, args = staged
    assert tool.cmd_reimage(adb, args) == 0

    push = adb.index_of("run", "push")
    verify = adb.index_of("shell", "md5sum /tmp/f1r30s.zip.part")
    rename = adb.index_of("shell", "mv /tmp/f1r30s.zip.part")
    wipe_data = adb.index_of("shell", "twrp wipe data")
    wipe_cache = adb.index_of("shell", "twrp wipe cache")
    sideload_mode = adb.index_of("shell", "twrp sideload")
    sideload = adb.index_of("run", "sideload")
    install = adb.index_of("shell", "twrp install")

    # The patch is on the device, verified, before anything is erased.
    assert push < verify < rename < wipe_data, (
        "the patch was not staged and verified before the wipe"
    )
    assert wipe_data < wipe_cache < sideload_mode < sideload < install, (
        "the flash sequence ran out of order"
    )
    # And the device is left alone at the end.
    assert not any("reboot" in text for _, text in adb.calls)


def test_reimage_that_fails_after_the_wipe_reports_and_does_not_reboot(
        staged, capsys):
    """
    The failure this tool exists to handle well. It must exit non-zero, say
    the device is unbootable, and leave it in recovery.
    """
    tool, adb, args = staged
    real_shell = adb.shell

    def fail_on_install(cmd, **kw):
        if cmd.startswith("twrp install"):
            return 1, "E: unable to install"
        return real_shell(cmd, **kw)

    adb.shell = fail_on_install
    rc = tool.cmd_reimage(adb, args)
    out = capsys.readouterr().out

    assert rc == 2, "a post-wipe failure must not report success"
    assert "NOT READY TO BOOT" in out
    assert "install-patch" in out, "the resume command is not offered"
    assert not any("reboot" in text for _, text in adb.calls)


def test_reimage_refuses_when_the_device_is_not_in_recovery(staged):
    tool, adb, args = staged
    adb.mode = "device"
    with pytest.raises(tool.Fail) as e:
        tool.cmd_reimage(adb, args)
    assert "recovery" in str(e.value)


def test_reimage_stops_before_the_wipe_when_the_patch_will_not_verify(
        staged, tmp_path):
    """
    A staging failure must cost nothing. If the bytes on the device do not
    match, the device must still be untouched.
    """
    tool, adb, args = staged
    adb._md5 = {}  # device reports a hash that matches nothing

    with pytest.raises(tool.Fail):
        tool.cmd_reimage(adb, args)

    assert not any("twrp wipe" in text for _, text in adb.calls), (
        "the device was wiped despite the patch failing verification"
    )


# ── CLI shape ─────────────────────────────────────────────────────────────────

def test_cli_exposes_the_documented_commands(tool):
    parser = tool.build_parser()
    for argv, expected in (
        (["info"], "cmd_info"),
        (["fix-slot"], "cmd_fix_slot"),
        (["logs"], "cmd_logs"),
        (["reimage", "--image", "a.bin", "--patch", "b.zip"], "cmd_reimage"),
    ):
        args = parser.parse_args(argv)
        assert args.func.__name__ == expected


def test_tool_has_no_third_party_imports():
    """
    It runs on someone else's machine in a bad moment. stdlib only, so
    `python3 echomuse-recovery.py` is the whole install instruction.
    """
    tree = ast.parse(TOOL_SRC)
    stdlib_ok = {
        "argparse", "hashlib", "os", "re", "shutil", "subprocess", "sys",
        "time",
    }
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                root = alias.name.split(".")[0]
                assert root in stdlib_ok, f"non-stdlib import: {alias.name}"
        elif isinstance(node, ast.ImportFrom) and node.level == 0:
            root = (node.module or "").split(".")[0]
            assert root in stdlib_ok, f"non-stdlib import: {node.module}"


def test_no_cut_in_device_commands():
    """
    md5sum prints `<hash>  <path>`, and the device lacking busybox md5sum is
    the device lacking busybox cut. Matching a prefix needs no external tool —
    the same rule the OTA path follows.
    """
    assert not re.search(r"\|\s*cut\b", TOOL_SRC), (
        "a device-side command pipes through cut"
    )
