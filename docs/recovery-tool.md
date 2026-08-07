# The recovery tool

The dashboard's provisioning wizard talks to a Dot over USB from your browser,
and it is the right tool for setting one up. It cannot help you when the
device is off the network, half provisioned, or will not boot far enough to
be reached — and those are the moments you most need something.

The recovery tool is a single Python script for exactly those moments. It runs
on whatever machine has the USB cable, drives `adb` directly, and needs
nothing from the controller once you have downloaded it.

**Settings → Support → Download recovery tool**, or
`GET /api/support/recovery_tool`. Admin only.

```
python3 echomuse-recovery.py --help
```

Requirements: Python 3.8 or newer, and `adb` from the Android platform-tools.
No packages to install — it uses only the standard library, so there is
nothing to go wrong on a machine you borrowed to rescue a device.

## What it does

| Command | What it is for |
|---|---|
| `info` | What state is this device actually in — Android or recovery, rooted or not, which firmware slot, and whether that slot is broken. |
| `reimage` | Flash FireOS 5 back onto the device. **Erases everything.** |
| `install-patch` | Install `f1r30s.zip` on its own. This is how you finish a reimage that failed part way. |
| `fix-slot` | Repair a firmware symlink that points at neither slot — see below. |
| `logs` | Pull the supervisor and server logs off the device. |
| `shell` | Run a root command. |

If more than one device is attached, pass `--serial`. If `adb` is not on your
PATH, pass `--adb /path/to/adb`. Both work before or after the command name.

## A device that runs fine but can never update

There is a failure with no natural end: `/data/local/bin/server` is a symlink
that should point at `server_a` or `server_b`, and if it points anywhere else
the device runs perfectly on whatever it resolves to while the controller
refuses every update before sending a byte. Nothing looks wrong. Updates
simply never happen.

`info` reports it, and `fix-slot` repairs it:

```
python3 echomuse-recovery.py info
python3 echomuse-recovery.py fix-slot
```

The device needs to be booted into Android with root available. Reboot it
afterwards and OTA updates work again from the dashboard.

## Reimaging

This erases the device completely and flashes FireOS 5 back on. You want it
when a device is in a state you cannot talk your way out of, or when you are
starting again from scratch.

You supply two files. Neither ships with EchoMuse — one is Amazon's firmware
and the other is a community patch:

- **The FireOS 5 image**, `update-kindle-csm_biscuit-272.6.8.0_user_680767620.bin`
- **`f1r30s.zip`**, the ADB-enablement patch

Both are checked against known SHA-256 hashes before anything happens. The
patch must match exactly — it is what makes the flashed system reachable, so
a wrong or truncated copy is refused rather than risked. The FireOS image is
checked against a list of known builds; if yours is not on the list the tool
stops and tells you the hash, and `--allow-unknown-image` proceeds if you are
sure. Please report an unrecognised hash so it can be added.

**The device must be in TWRP recovery.** `adb reboot recovery` gets you there
from Android.

```
python3 echomuse-recovery.py reimage \
    --image update-kindle-csm_biscuit-272.6.8.0_user_680767620.bin \
    --patch f1r30s.zip
```

It checks both files against their hashes first, then asks you to type
`REIMAGE` to confirm. Nothing on the device is touched before that point.

Then it wipes, pushes the patch to `/sdcard`, sideloads the image, and
installs the patch — checking after the flash that the patch is still there
and still intact, and re-pushing it if not.

TWRP survives a reimage — it sits below the system image — so recovery is
always possible from here as long as the device stays in recovery.

### If it fails part way

**Do not reboot a device that reported a failure.** Recovery is cheap while
it is sitting in TWRP and a much longer afternoon once it has been power
cycled and put away.

The tool tells you which of two situations you are in, because they need
different things:

- **The image was not flashed.** The device still has its previous system and
  has only been wiped. Fix the cause and run the same reimage command again.
- **The image was flashed but the patch was not installed.** This is the state
  where the device cannot be reached, and finishing the job is one command:

  ```
  python3 echomuse-recovery.py install-patch --patch f1r30s.zip
  ```

### After a successful reimage

The tool does not reboot the device — that is left to you, so nothing is in
flight when it goes. Reboot into Android, let it finish its first boot (this
takes a couple of minutes on a fresh image), then run the provisioning wizard
from the dashboard as normal.

## Getting the logs

```
python3 echomuse-recovery.py logs --out ./
```

Two logs come off the device. `supervisor.log` records the startup decisions —
which slot booted, when the server started and exited, and why. `server.log`
is the running log, and it lives in RAM: **power cycling a device to recover
it erases exactly the lines that would explain why it needed recovering**, so
grab it before you pull the plug if you can.

Both are useful on an issue, and neither contains speech.
