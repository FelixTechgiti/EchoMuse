# emOS changelog

Release notes for the emOS init, one section per version. The heading is
the version WITHOUT the `emos-v` prefix, because that is what
`.github/workflows/cut-release.yml` matches when it builds the tag
annotation from this file — `## 0.4.0-fx.1` for the tag `emos-v0.4.0-fx.1`.

Headings INSIDE an entry are `###` or deeper. A `## ` line starts a new
version section, and that is what the extractor stops at.

**Why three components where upstream uses two.** Upstream tags `emos-v0.4`.
This fork carries an `-fx.N` suffix on every release (see the root
CLAUDE.md), and every guard that reads a version here — the shape check in
`cut-release.yml`, `tests/test_changelog_headings.py` — is written for
`MAJOR.MINOR.PATCH`. Padding the patch digit keeps all of them exactly as
they are, and `0.4.0-fx.1` is an honest name for this fork's first build of
upstream's 0.4. Nothing parses the emOS version numerically: `build.sh`
stamps `git describe` into `/etc/os-release` and
`em_api._fetch_latest_emos_release` selects on the `emos-v` prefix and an
`init` asset.

Newest first. Written for the person deciding whether to write this to the
boot partition of a device they rely on.

## 0.4.0-fx.1

**This fork's first published emOS init.** The code is upstream's 0.4 with
this fork's rename applied and nothing else — `emos/init/init.c` differs from
upstream only in the strings that say Revoice instead of EchoMuse and in the
paths that moved with the rename (`/data/local/etc/revoice/console.pw`, the
`svc_add("revoice", …)` service entry, the console banner, the USB
`iManufacturer`).

### Why this release exists at all

Until now **nobody using this fork could install emOS**, and nothing said so.
The provisioning wizard fetches the init from the repository named in
`github_repo`, which on this fork is `FelixTechgiti/Revoice` — and there was
no `emos-v*` tag on it, so `/api/provision/emos_init` answered 404 and the
wizard's emOS flow stopped at its build step. The emOS code has been in the
tree the whole time; only the published artifact was missing.

### What you get

emOS replaces Amazon's Android userspace entirely, keeping only the device's
own kernel. Against FireOS on the same hardware:

- **No `mediaserver`**, so nothing takes the speaker away from Revoice after
  an update and nothing rewrites the mixer behind us. That is the whole of
  the jack-detect and PCM-blocked class of faults.
- **No default-deny firewall.** Spotify Connect and AirPlay are reachable
  without the iptables rules firmware 2.31.0-fx.1 has to write under FireOS.
- **A USB serial console** on `/dev/ttyGS0` with a boot progress bar on the
  LED ring, so a device that will not come up can be asked why over a cable
  rather than guessed at.
- **`/init recovery`**, which reboots into TWRP from that console. Amazon's
  own `reboot` cannot: it talks to a property service emOS does not run and
  fails with `No such file or directory`, naming a socket rather than the
  real reason.
- **Rollback**: a boot is confirmed when the network comes up, and three
  unconfirmed boots restore the known-good image and show an amber ring.

### What it costs, stated plainly

**Status 0.4: bench-proven, not field-proven** — a small number of devices
over a handful of days. The known gaps are listed in `emos/README.md` and
none of them is a research problem, but read them before you flash a device
you depend on.

`/data` survives a boot-partition write, so a device that crosses from FireOS
keeps its Revoice install, its link credentials, its remembered controller
and its WiFi configuration. **Keep the boot image the wizard hands you**: it
is the undo, and restoring it takes about ten seconds.

> **⚠️ emOS needs the FireOS 5 kernel, so do not install amonet-biscuit
> v2.0.0.** v2.0.0 (10 September 2026) replaces the Echo's bootloaders, and
> after that FireOS 5 — and with it emOS — no longer boots. Unlock with
> **v1.1.0**. If you have already installed v2.0.0, **do not try to go back
> by flashing FireOS 5 or an older amonet**: that rewrites bootloaders by
> hand, which is how an Echo gets hard-bricked.
