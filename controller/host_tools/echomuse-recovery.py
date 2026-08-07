#!/usr/bin/env python3
"""
EchoMuse recovery tool — host-side, real adb, no controller required.

Runs on whatever machine has the USB cable, which is usually NOT the machine
running the controller. It deliberately talks to nothing but `adb`: the cases
this exists for are the ones where the device is off the network, half
provisioned, or will not boot, and a tool that needs the controller to be
reachable is no use in any of them.

What it is for:

  info           what state is this device actually in
  reimage        flash FireOS 5 back onto the device (destructive)
  install-patch  install the ADB-enablement patch on its own
  fix-slot       repair a firmware symlink pointing at neither slot
  logs           pull the supervisor and server logs
  shell          run a root command

What it is NOT: a second provisioning wizard. The wizard's 13 steps carry
workarounds that were expensive to find — how long to wait for the framework,
the two shapes `pm` failure takes, when to mute the OOBE, that SmartHomeWifid
must be stopped rather than killed. Reimplementing those here would give two
copies to keep in step and one of them would drift silently, so provisioning
stays in the dashboard and this tool stays out of it.

Requires: python 3.8+, adb on PATH (or --adb), a device in TWRP for reimage.
"""

import argparse
import hashlib
import os
import re
import shutil
import subprocess
import sys
import time

# Substituted with the controller's version when served from the dashboard;
# stays "dev" for a copy run straight out of the repo. A bug report is much
# easier to place when the tool says which vintage it is.
TOOL_VERSION = "dev"

# ── Pinned artefacts ──────────────────────────────────────────────────────────
#
# Both files are user-supplied — Amazon's firmware and a community patch,
# neither of which we redistribute. The hashes are the guard, and the two want
# DIFFERENT strictness:
#
#   f1r30s.zip is a single known artefact, and it is the file that decides
#   whether the freshly flashed device boots at all. A mismatch refuses, full
#   stop.
#
#   The FireOS image has legitimate variants in the wild, so a hard pin
#   against one build would reject a valid image someone already has. An
#   unknown hash refuses BY DEFAULT and can be overridden explicitly, which
#   still catches the failure that actually bricks a device: a corrupt
#   download, or an image for a different model.

F1R30S_SHA256 = "f1e522fb3b86a6c90d559e7b8085501ffc59165850310b39468a3acfbff5bbc8"

KNOWN_IMAGES = {
    "6ababc517529938f0d1e836c3410a91df19683ae62d7fca9e2ca57320d5d2faa":
        "Fire OS 5.5.5.4 (680767620) — update-kindle-csm_biscuit-272.6.8.0_user",
}

# Canonical device paths. Coupled with em_api.SUPERVISOR_LOG and the slot
# layout in device_payloads/start_server.sh; a test pins them together.
SUPERVISOR_LOG = "/data/local/etc/echomuse/supervisor.log"
SERVER_LOG = "/tmp/server.log"
BIN_DIR = "/data/local/bin"
SERVER_LINK = BIN_DIR + "/server"
SLOTS = ("server_a", "server_b")

# Where the patch is staged before anything destructive runs. TWRP's /tmp is a
# ramdisk, so it survives both the data wipe and the image flash — nothing
# reboots in between. Staging on /sdcard instead would put the file on the
# partition we are about to wipe, and the image flash may format it again.
STAGE_DIR = "/tmp"


class Fail(Exception):
    """An error with a message already written for the operator."""


# ── Output ────────────────────────────────────────────────────────────────────

def _tty() -> bool:
    return sys.stdout.isatty()


def say(msg: str = "") -> None:
    print(msg, flush=True)


def step(msg: str) -> None:
    print(f"\n── {msg}", flush=True)


def ok(msg: str) -> None:
    print(f"   ok    {msg}", flush=True)


def warn(msg: str) -> None:
    print(f"   warn  {msg}", flush=True)


def info(msg: str) -> None:
    print(f"         {msg}", flush=True)


# ── adb ───────────────────────────────────────────────────────────────────────

class Adb:
    def __init__(self, binary: str = "adb", serial: str = None):
        self.binary = binary
        self.serial = serial

    def _base(self):
        cmd = [self.binary]
        if self.serial:
            cmd += ["-s", self.serial]
        return cmd

    def run(self, args, *, timeout=120, check=False, stream=False):
        """Run an adb subcommand. Returns (rc, stdout+stderr)."""
        cmd = self._base() + list(args)
        if stream:
            # Long transfers print their own progress; showing it live is the
            # difference between "working" and "hung" for a multi-minute step.
            proc = subprocess.run(cmd, timeout=timeout)
            out = ""
            rc = proc.returncode
        else:
            proc = subprocess.run(cmd, capture_output=True, text=True,
                                  timeout=timeout)
            out = (proc.stdout or "") + (proc.stderr or "")
            rc = proc.returncode
        if check and rc != 0:
            raise Fail(f"adb {' '.join(args)} failed (rc={rc}):\n{out.strip()}")
        return rc, out.strip()

    def shell(self, cmd, *, timeout=120, check=False):
        return self.run(["shell", cmd], timeout=timeout, check=check)

    def shell_out(self, cmd, *, timeout=120) -> str:
        return self.shell(cmd, timeout=timeout)[1]

    def su(self, cmd, *, timeout=120):
        """Run as root in Android mode. TWRP's shell is already root."""
        if self.state() == "recovery":
            return self.shell(cmd, timeout=timeout)
        return self.shell(f"su -c '{cmd}'", timeout=timeout)

    def state(self) -> str:
        """'device', 'recovery', 'sideload', 'unauthorized' or 'offline'."""
        rc, out = self.run(["get-state"], timeout=20)
        if rc != 0:
            # get-state fails outright when nothing is attached; devices tells
            # us whether something is there but unusable, which is a different
            # problem with a different fix.
            _, devs = self.run(["devices"], timeout=20)
            if "unauthorized" in devs:
                return "unauthorized"
            return "offline"
        return out.strip()

    def wait_for(self, state: str, timeout: float = 180) -> None:
        """
        Wait for the device to come back in `state`.

        Every mode change here re-enumerates USB, so the handle we had is gone
        and the next command would fail against a device that is merely busy
        being reborn. `adb wait-for-<state>` is the supported way to block on
        that; it is polled rather than trusted blindly so a stuck device gives
        up with a message rather than hanging forever.
        """
        deadline = time.time() + timeout
        self.run(["wait-for-" + state], timeout=max(5, timeout))
        while time.time() < deadline:
            if self.state() == state:
                return
            time.sleep(1)
        raise Fail(f"device did not come back in '{state}' mode within "
                   f"{int(timeout)}s")


# ── Hashing ───────────────────────────────────────────────────────────────────

def sha256_file(path: str) -> str:
    h = hashlib.sha256()
    size = os.path.getsize(path)
    done = 0
    with open(path, "rb") as f:
        while True:
            chunk = f.read(1024 * 1024)
            if not chunk:
                break
            h.update(chunk)
            done += len(chunk)
            if _tty() and size > 32 * 1024 * 1024:
                pct = 100 * done / size
                print(f"\r         hashing {os.path.basename(path)} "
                      f"{pct:5.1f}%", end="", flush=True)
    if _tty() and size > 32 * 1024 * 1024:
        print("\r" + " " * 60 + "\r", end="", flush=True)
    return h.hexdigest()


def _require_file(path: str, what: str) -> str:
    if not path:
        raise Fail(f"no {what} given")
    if not os.path.isfile(path):
        raise Fail(f"{what} not found: {path}")
    return os.path.abspath(path)


def verify_patch(path: str) -> None:
    """The patch hash is a hard gate — see the note on the constants."""
    got = sha256_file(path)
    if got != F1R30S_SHA256:
        raise Fail(
            f"f1r30s.zip does not match the expected hash.\n"
            f"  expected {F1R30S_SHA256}\n"
            f"  got      {got}\n"
            "This is the file that makes the flashed system reachable, so a "
            "wrong or truncated copy is refused rather than risked.")
    ok(f"patch verified  {os.path.basename(path)}")


def verify_image(path: str, allow_unknown: bool) -> None:
    got = sha256_file(path)
    known = KNOWN_IMAGES.get(got)
    if known:
        ok(f"image verified  {known}")
        return
    if allow_unknown:
        warn(f"image hash not recognised: {got}")
        warn("proceeding because --allow-unknown-image was given")
        info("please report that hash so it can be added to the known list")
        return
    raise Fail(
        f"FireOS image hash not recognised: {got}\n"
        f"  file: {path}\n"
        "Known-good images:\n"
        + "".join(f"    {h}  {n}\n" for h, n in KNOWN_IMAGES.items())
        + "If you are sure this is a genuine FireOS 5 image for the Echo Dot "
          "Gen 2 (biscuit), re-run with --allow-unknown-image, and report the "
          "hash above so it can be added.\n"
          "An image for a different device is the one mistake here that can "
          "leave a Dot unrecoverable.")


# ── Device-side helpers ───────────────────────────────────────────────────────

def _md5_tool(adb: Adb) -> str:
    """
    Pick an md5 command the device actually has.

    Deliberately no `cut` anywhere downstream: md5sum prints
    `<hash>  <path>`, and the device that lacks busybox md5sum is exactly the
    device that lacks busybox cut. Matching a prefix needs no external tool.
    """
    for cand in ("md5sum", "busybox md5sum", "md5"):
        rc, out = adb.shell(f"{cand} /dev/null", timeout=30)
        if rc == 0 and out and "not found" not in out:
            return cand
    return ""


def push_verified(adb: Adb, local: str, remote: str) -> None:
    """
    Push a file and prove it arrived intact before it is used.

    Same discipline as the controller's OTA path: land in `.part`, rename only
    on a hash match. adb push exiting 0 says the transfer completed, not that
    the bytes match — and this particular file decides whether the device
    boots afterwards.

    This doubles as the free-space check. A device with no room fails the push
    or the comparison, and it fails HERE, before anything destructive has run.
    """
    part = remote + ".part"
    adb.run(["push", local, part], timeout=600, check=True)

    tool = _md5_tool(adb)
    if not tool:
        warn("no md5 tool on the device — transfer not verified")
    else:
        import hashlib as _h
        want = _h.md5(open(local, "rb").read()).hexdigest()
        out = adb.shell_out(f"{tool} {part}", timeout=120)
        # `<hash>  <path>` — compare on the prefix, no cut needed.
        if not out.lower().startswith(want.lower()):
            adb.shell(f"rm -f {part}")
            raise Fail(f"transfer of {os.path.basename(local)} did not verify "
                       f"(expected md5 {want}, device said: {out.strip()})")
        ok(f"staged and verified  {remote}")

    rc, out = adb.shell(f"mv {part} {remote}", timeout=60)
    if rc != 0:
        raise Fail(f"could not move staged file into place: {out}")


def read_system_build(adb: Adb) -> dict:
    """
    Read the flashed system's identity from TWRP, without booting it.

    Verifying by booting Android costs ~86s plus the OOBE we deliberately
    silence later; mounting /system and reading build.prop answers the same
    question in a second.
    """
    adb.shell("mount /system 2>/dev/null", timeout=60)
    out = adb.shell_out("cat /system/build.prop 2>/dev/null", timeout=60)
    props = {}
    for line in out.splitlines():
        if "=" in line and not line.startswith("#"):
            k, _, v = line.partition("=")
            props[k.strip()] = v.strip()
    return props


# ── Commands ──────────────────────────────────────────────────────────────────

def cmd_info(adb: Adb, args) -> int:
    state = adb.state()
    say(f"adb state      {state}")
    if state in ("offline", "unauthorized"):
        say("")
        if state == "unauthorized":
            say("The device is attached but has not authorised this host.")
            say("Accept the ADB prompt on the device, or re-run after "
                "`adb kill-server`.")
        else:
            say("No device visible. Check the cable, and note that a running "
                "adb server on the host can hold the interface — "
                "`adb kill-server` frees it.")
        return 1

    if state == "recovery":
        fp = adb.shell_out("getprop ro.build.fingerprint", timeout=30)
        say(f"mode           TWRP recovery")
        say(f"recovery fp    {fp or '(unknown)'}")
        props = read_system_build(adb)
        if props:
            say(f"system         Android "
                f"{props.get('ro.build.version.release', '?')}  "
                f"{props.get('ro.build.fingerprint', '?')}")
        else:
            say("system         /system not readable — unflashed or unmounted")
        return 0

    serial = (adb.shell_out("getprop ro.serialno", timeout=30)
              or adb.shell_out("getprop ro.boot.serialno", timeout=30))
    say(f"mode           Android")
    say(f"serial         {serial or '(unknown)'}")
    say(f"release        {adb.shell_out('getprop ro.build.version.release')}")
    say(f"fingerprint    {adb.shell_out('getprop ro.build.fingerprint')}")

    rc, _ = adb.shell("su -c id", timeout=30)
    say(f"root           {'yes' if rc == 0 else 'no'}")

    link = adb.su(f"readlink {SERVER_LINK}")[1].strip()
    present = adb.su(f"ls {BIN_DIR}")[1]
    if link in SLOTS:
        say(f"firmware slot  {link}")
    elif not link:
        say(f"firmware slot  MISSING — {SERVER_LINK} is not a symlink")
        say("               run `fix-slot` to repair it")
    else:
        say(f"firmware slot  BROKEN — points at '{link}', "
            f"neither {SLOTS[0]} nor {SLOTS[1]}")
        say("               the device runs but can never update; "
            "run `fix-slot` to repair it")
    say(f"binaries       {' '.join(s for s in SLOTS if s in present) or 'none'}")
    return 0


def cmd_fix_slot(adb: Adb, args) -> int:
    """
    Repair a firmware symlink pointing at neither slot.

    This state has no natural end: the device runs perfectly on whatever the
    link resolves to, while the controller's slot detection returns nothing
    and refuses every OTA before transferring a byte. start_server.sh logs
    `Unknown slot ... giving up` and the controller says it could not
    determine the active slot — both accurate, neither actionable, and until
    now neither offering the repair.
    """
    if adb.state() != "device":
        raise Fail("fix-slot needs the device booted into Android "
                   f"(adb state is '{adb.state()}')")

    rc, _ = adb.shell("su -c id", timeout=30)
    if rc != 0:
        raise Fail("root is not available — fix-slot needs su")

    link = adb.su(f"readlink {SERVER_LINK}")[1].strip()
    if link in SLOTS:
        ok(f"symlink already points at {link} — nothing to repair")
        return 0

    listing = adb.su(f"ls -1 {BIN_DIR}")[1]
    available = [s for s in SLOTS if re.search(rf"^{s}$", listing, re.M)]
    if not available:
        raise Fail(
            f"neither {SLOTS[0]} nor {SLOTS[1]} exists in {BIN_DIR} — there is "
            "no slot to point at. Re-run the wizard's Install EchoMuse step, "
            "or push a binary with the dashboard.")

    if args.slot:
        if args.slot not in available:
            raise Fail(f"{args.slot} does not exist on the device "
                       f"(present: {', '.join(available)})")
        target = args.slot
    elif len(available) == 1:
        target = available[0]
    else:
        # Both slots exist and we cannot tell which is good. Newest wins: it
        # is the one most recently deployed, and the other slot is the
        # rollback that start_server.sh will reach on its own if this one
        # fast-exits three times.
        times = {}
        for s in available:
            out = adb.su(f"busybox stat -c %Y {BIN_DIR}/{s} 2>/dev/null")[1]
            times[s] = int(out.strip()) if out.strip().isdigit() else 0
        target = max(available, key=lambda s: times[s])
        info(f"both slots present — choosing {target} (most recent)")

    say(f"repairing {SERVER_LINK} -> {target}")
    rc, out = adb.su(f"ln -sf {target} {SERVER_LINK}")
    if rc != 0:
        raise Fail(f"could not write the symlink: {out}")

    check = adb.su(f"readlink {SERVER_LINK}")[1].strip()
    if check != target:
        raise Fail(f"symlink still reads '{check}' after the repair")
    ok(f"symlink now points at {target}")
    say("")
    say("Reboot the device (or restart the server) to run from the repaired "
        "slot. OTA updates will work again from the dashboard.")
    return 0


def cmd_logs(adb: Adb, args) -> int:
    out_dir = os.path.abspath(args.out or ".")
    os.makedirs(out_dir, exist_ok=True)
    stamp = time.strftime("%Y%m%d-%H%M%S")
    got = 0

    for remote, label in ((SUPERVISOR_LOG, "supervisor"), (SERVER_LOG, "server")):
        text = adb.su(f"cat {remote} 2>/dev/null")[1]
        if not text.strip():
            warn(f"{remote} is empty or unreadable")
            continue
        dest = os.path.join(out_dir, f"echomuse-{label}-{stamp}.log")
        with open(dest, "w") as f:
            f.write(text + "\n")
        ok(f"{dest}  ({len(text.splitlines())} lines)")
        got += 1

    if not got:
        # /tmp/server.log is RAM-backed, so a power cycle to recover a device
        # wipes exactly the lines that would explain why it needed recovering.
        # supervisor.log survives precisely for that reason.
        warn("nothing collected. Note /tmp/server.log does not survive a "
             "reboot; supervisor.log does.")
        return 1
    return 0


def cmd_shell(adb: Adb, args) -> int:
    if not args.command:
        raise Fail("nothing to run — pass a command, e.g. `shell 'ls /data'`")
    rc, out = adb.su(" ".join(args.command), timeout=args.timeout)
    if out:
        say(out)
    return rc


def cmd_install_patch(adb: Adb, args) -> int:
    """
    Stage and install f1r30s.zip on its own.

    Exists as its own command because it is the recovery path for a reimage
    that failed after the wipe: the device is sitting in TWRP with a flashed
    system it cannot be reached on, and the fix is these two operations. Being
    separately runnable means the answer to that failure is one command rather
    than repeating a destructive sequence.
    """
    patch = _require_file(args.patch, "f1r30s.zip")
    if adb.state() != "recovery":
        raise Fail("this needs the device in TWRP recovery "
                   f"(adb state is '{adb.state()}')")

    step("Verifying the patch")
    verify_patch(patch)

    step("Staging onto the device")
    remote = f"{STAGE_DIR}/f1r30s.zip"
    push_verified(adb, patch, remote)

    step("Installing")
    rc, out = adb.shell(f"twrp install {remote}", timeout=900)
    say(out)
    if rc != 0 or "failed" in out.lower() or "error" in out.lower():
        raise Fail("the patch install reported a failure — see the output "
                   "above. The device is still in TWRP; fix the cause and "
                   "run this command again before rebooting.")
    ok("patch installed")
    return 0


def cmd_reimage(adb: Adb, args) -> int:
    image = _require_file(args.image, "FireOS image (--image)")
    patch = _require_file(args.patch, "f1r30s.zip (--patch)")

    say(f"EchoMuse recovery {TOOL_VERSION} — reimage")
    say("")
    say("This ERASES the device: all data and settings are wiped and FireOS 5")
    say("is flashed back on. TWRP survives it (it sits below the system "
        "image),")
    say("so a failure is recoverable while the device stays in recovery.")

    # ── Everything below this point that can be checked, is checked BEFORE
    # the first destructive command. The window between the wipe and the patch
    # being installed is the only part that cannot be undone by stopping, so
    # nothing unverified is allowed to enter it.

    step("Checking the device")
    state = adb.state()
    if state != "recovery":
        raise Fail(
            f"the device must be in TWRP recovery, but adb reports '{state}'.\n"
            "Boot it into recovery first — the wizard's 'Connect to TWRP' step "
            "describes how, and `adb reboot recovery` works from Android.")
    fp = adb.shell_out("getprop ro.build.fingerprint", timeout=30)
    ok(f"in TWRP recovery  ({fp or 'fingerprint unknown'})")

    rc, _ = adb.shell(f"touch {STAGE_DIR}/.em-write-test", timeout=30)
    if rc != 0:
        raise Fail(f"{STAGE_DIR} is not writable on the device — cannot stage "
                   "the patch safely")
    adb.shell(f"rm -f {STAGE_DIR}/.em-write-test", timeout=30)
    ok(f"{STAGE_DIR} is writable")

    step("Verifying the files")
    verify_image(image, args.allow_unknown_image)
    verify_patch(patch)

    step("Staging the patch before anything is erased")
    # /tmp is a ramdisk that survives the wipe and the flash, so the file the
    # device needs in order to be reachable afterwards is already on it before
    # the first destructive command runs. A push failure here costs nothing.
    remote_patch = f"{STAGE_DIR}/f1r30s.zip"
    push_verified(adb, patch, remote_patch)

    if not args.yes:
        say("")
        say("Everything checked. The next step erases the device.")
        try:
            typed = input('Type REIMAGE to continue (anything else aborts): ')
        except (EOFError, KeyboardInterrupt):
            typed = ""
        if typed.strip() != "REIMAGE":
            say("Aborted — nothing has been changed.")
            return 1

    # ── Danger window opens here ─────────────────────────────────────────────
    try:
        step("Wiping data")
        rc, out = adb.shell("twrp wipe data", timeout=900)
        say(out)
        if rc != 0:
            raise Fail("`twrp wipe data` failed — see the output above")

        step("Wiping cache")
        rc, out = adb.shell("twrp wipe cache", timeout=900)
        say(out)
        if rc != 0:
            raise Fail("`twrp wipe cache` failed — see the output above")

        step("Entering sideload mode")
        adb.shell("twrp sideload", timeout=60)
        # Sideload mode restarts adbd, which re-enumerates USB.
        adb.wait_for("sideload", timeout=180)
        ok("device is in sideload mode")

        step(f"Sideloading {os.path.basename(image)}")
        info("this takes several minutes and adb prints its own progress")
        rc, _ = adb.run(["sideload", image], timeout=3600, stream=True)
        if rc != 0:
            raise Fail(f"`adb sideload` failed (rc={rc})")
        ok("image sideloaded")

        step("Waiting for recovery to come back")
        adb.wait_for("recovery", timeout=300)
        ok("back in TWRP")

        step("Installing the ADB-enablement patch")
        rc, out = adb.shell(f"twrp install {remote_patch}", timeout=900)
        say(out)
        if rc != 0 or "failed" in out.lower():
            raise Fail("the patch install reported a failure")
        ok("patch installed")

    except (Fail, subprocess.TimeoutExpired, KeyboardInterrupt) as e:
        say("")
        say("=" * 72)
        say("REIMAGE DID NOT COMPLETE — THE DEVICE IS NOT READY TO BOOT")
        say("=" * 72)
        say(f"{e}")
        say("")
        say("Leave the device in TWRP with the cable attached. Do not reboot:")
        say("recovery is cheap from here and expensive once it has been power")
        say("cycled and put away.")
        say("")
        say("When the cause is fixed, finish the job with:")
        say("")
        say(f"    {_self()} install-patch --patch {args.patch}")
        say("")
        say("If the flash itself failed, re-run the full reimage instead.")
        return 2

    # ── Danger window closed ─────────────────────────────────────────────────

    step("Verifying the flashed system")
    props = read_system_build(adb)
    release = props.get("ro.build.version.release", "")
    if not props:
        warn("could not read /system/build.prop — cannot confirm the flash")
        warn("check the device manually before rebooting")
        return 3
    if not release.startswith("5"):
        warn(f"flashed system reports Android {release}, expected 5.x")
        warn("check the device manually before rebooting")
        return 3
    ok(f"Android {release}  {props.get('ro.build.fingerprint', '')}")

    say("")
    say("Reimage complete. The device has NOT been rebooted — that is left to")
    say("you so nothing is in flight when it goes.")
    say("")
    say("Next: reboot into Android (`adb reboot`), let it finish its first")
    say("boot, then run the provisioning wizard from the dashboard.")
    return 0


def _self() -> str:
    return os.path.basename(sys.argv[0]) or "echomuse-recovery.py"


# ── Entry point ───────────────────────────────────────────────────────────────

def build_parser() -> argparse.ArgumentParser:
    # --adb and --serial are accepted both before and after the subcommand.
    # argparse only allows the former by default, and `reimage --adb ...` is
    # how people actually type it — an unrecognised-argument error there is a
    # poor greeting for a tool someone reached for because something is
    # already wrong. The subcommand copies default to SUPPRESS so that
    # omitting them does not overwrite a value given before the subcommand.
    def _link_args(parser, *, suppress):
        default = argparse.SUPPRESS if suppress else None
        parser.add_argument("--adb", default="adb" if not suppress else default,
                            help="path to the adb binary")
        parser.add_argument("--serial", default=default,
                            help="target this device (adb -s), when more than "
                                 "one is attached")

    shared = argparse.ArgumentParser(add_help=False)
    _link_args(shared, suppress=True)

    p = argparse.ArgumentParser(
        prog=_self(),
        description="EchoMuse recovery tool — reimage and repair an Echo Dot "
                    "over USB, without the controller.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="Run `%(prog)s <command> --help` for a command's options.")
    _link_args(p, suppress=False)
    p.add_argument("--version", action="version",
                   version=f"echomuse-recovery {TOOL_VERSION}")
    sub = p.add_subparsers(dest="command", required=True)

    s = sub.add_parser("info", parents=[shared],
                       help="report what state the device is in")
    s.set_defaults(func=cmd_info)

    s = sub.add_parser("reimage", parents=[shared],
                       help="flash FireOS 5 back on (DESTRUCTIVE)")
    s.add_argument("--image", required=True,
                   help="Amazon FireOS 5 update .bin")
    s.add_argument("--patch", required=True, help="f1r30s.zip")
    s.add_argument("--allow-unknown-image", action="store_true",
                   help="proceed with a FireOS image whose hash is not in the "
                        "known list")
    s.add_argument("--yes", action="store_true",
                   help="skip the typed confirmation")
    s.set_defaults(func=cmd_reimage)

    s = sub.add_parser("install-patch", parents=[shared],
                       help="install f1r30s.zip (finishes an interrupted "
                            "reimage)")
    s.add_argument("--patch", required=True, help="f1r30s.zip")
    s.set_defaults(func=cmd_install_patch)

    s = sub.add_parser("fix-slot", parents=[shared],
                       help="repair a firmware symlink pointing at neither slot")
    s.add_argument("--slot", choices=SLOTS,
                   help="force a specific slot instead of the most recent")
    s.set_defaults(func=cmd_fix_slot)

    s = sub.add_parser("logs", parents=[shared], help="pull the supervisor and server logs")
    s.add_argument("--out", help="directory to write into (default: here)")
    s.set_defaults(func=cmd_logs)

    s = sub.add_parser("shell", parents=[shared], help="run a root command on the device")
    s.add_argument("command", nargs=argparse.REMAINDER)
    s.add_argument("--timeout", type=float, default=120)
    s.set_defaults(func=cmd_shell)

    return p


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)

    if not shutil.which(args.adb) and not os.path.isfile(args.adb):
        say(f"adb not found ({args.adb}).")
        say("Install the Android platform-tools, or pass --adb /path/to/adb.")
        return 1

    adb = Adb(args.adb, args.serial)
    try:
        return args.func(adb, args)
    except Fail as e:
        say("")
        say(f"error: {e}")
        return 1
    except subprocess.TimeoutExpired as e:
        say("")
        say(f"error: adb timed out after {e.timeout}s — the device may have "
            "disconnected")
        return 1
    except KeyboardInterrupt:
        say("")
        say("interrupted")
        return 130


if __name__ == "__main__":
    sys.exit(main())
