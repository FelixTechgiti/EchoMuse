# Switching an existing install to this fork

You are running Revoice from `wilbowes/EchoMuse` — the add-on, the published
container, or both — and you want to run this fork instead.

**Read the whole page before you start.** Two of the steps cannot be undone
from the dashboard, and one of them (`tls/`) leaves every device unable to
connect in a way that shows no error anywhere you would think to look.

---

## What is and is not the same

The fork is the same program. Same wire protocol, same schema, same
Home Assistant entities, same dashboard. What differs:

| | upstream | this fork |
|---|---|---|
| Add-on repository | `github.com/wilbowes/EchoMuse` | `github.com/FelixTechgiti/Revoice` |
| Controller image | `ghcr.io/wilbowes/echomuse-controller` | `ghcr.io/felixtechgiti/revoice-controller` |
| Firmware releases | upstream's `v*` tags | this fork's `v*` tags |

And the features this fork adds on the device: the output chain running
post-mix on the Echo itself, the LED ring as a Home Assistant light with
notification effects, a one-way mute switch, and the device playing music of
its own (Sendspin, Spotify Connect, AirPlay — see
[configuration.md](configuration.md)).

**The database schema is byte-identical to upstream's.** That is deliberate
and it is what makes this reversible: a database this fork has run still
starts on the upstream controller. Nothing here is a one-way door except the
two things called out below, and both are recoverable with the backup you are
about to take.

---

## The two things that bite

### 1. A different repository means a different `/data`

Home Assistant derives an add-on's storage directory from the repository URL,
so installing this fork's add-on gives you a **new, empty** `/data` — a new
database, and, far more importantly, a **newly generated certificate
authority**.

Your devices hold the old CA. They take `wss` from the controller's mDNS
record rather than from `require_device_tls`, so they will dial TLS, fail to
verify a CA they have never seen, and drop the connection. They never reach
the point of appearing in the dashboard, so there is nothing there to explain
it.

Recovering from that means connecting each Dot over USB and pushing fresh
credentials with the provisioning wizard. **Copying the old `/data` across
first avoids it entirely** — the procedure is exactly the one in
[migrate-to-addon.md § Copy your data in](migrate-to-addon.md#3-copy-your-data-in),
with the source being the other add-on's directory rather than a
`docker-compose` host path:

```bash
# On the Home Assistant host (Advanced SSH & Web Terminal, protection mode off).
# The folder name is a hash of the repository URL, so there are now TWO
# *_controller directories and you need to know which is which.
ls -ld /mnt/data/supervisor/{apps,addons}/data/*_controller
```

The older one by mtime is upstream's. Copy it into the new one **before the
first start of the fork's add-on**, or right after starting it once and
stopping it again — the directory does not exist until then.

If you run the standalone container instead of the add-on, none of this
applies: the data directory is yours and does not move. Change the image name
in `docker-compose.deploy.yml` and `docker compose pull`.

### 2. Both add-ons use the same ports

Host networking, 8767 / 8768 / 8770. **Stop the upstream add-on before
starting this one** — whichever starts second fails to bind, and while both
are up devices attach to whichever mDNS answer arrived first, which reads as
the switch having half-worked at random.

---

## Version numbers

Fork releases carry an `-fx.N` suffix — `controller-v2.23.0-fx.1`,
`v2.15.0-fx.1`. Upstream's tags are fetched into this repository by the weekly
sync, so a fork release using an upstream number would collide on the next
fetch and would publish an image claiming to be a version it is not.

It has no other meaning: the controller ignores the suffix when comparing
versions, so `2.23.0-fx.1` is neither ahead of nor behind `2.23.0`.

## Update sources

`github_repo` in the database decides where **both** update paths look: the
OTA poller reads that repository's releases for device firmware, and the
dashboard's controller notice reads its `controller-v*` tags.

A fresh database gets this fork. A database **copied from an upstream
install** stored upstream's name when it was created, so the controller
repoints it once, on the first start, and says so in the log:

```
[db] Update source set to FelixTechgiti/Revoice — this database was created by an upstream controller.
```

Cached release information from the old repository is dropped at the same
time, so the dashboard polls fresh rather than showing upstream's latest
until the next check happens to land.

It happens **once**. If you would rather track upstream's firmware, set it
back and it stays set:

```
PATCH /api/system/config   {"github_repo": "wilbowes/EchoMuse"}
```

---

## Firmware

Controller and firmware version independently and any pairing works — the two
halves negotiate by capability, not by version — so **switch the controller
first and leave the devices alone**. Nothing breaks: a device on upstream
firmware simply does not announce the fork's capabilities, and every control
whose feature it lacks is shown disabled with the reason.

Update firmware afterwards, one device at a time, from the dashboard's Updates
tab. The device keeps its previous binary in the other slot and rolls back to
it on its own after three fast exits, so a bad update costs a reboot rather
than a device.

---

## Going back

1. Set `github_repo` back to `wilbowes/EchoMuse` (above) if you want upstream
   firmware offered again.
2. Stop this add-on, start upstream's.
3. Copy `/data` the other way if you have been running long enough to care
   about the history — the schema is identical, so it starts.

Device firmware from this fork runs against the upstream controller. The extra
capabilities it announces are ignored, which is the documented behaviour for
an unknown capability in both directions.

One caveat, and it is upstream's rather than the fork's: the upstream
controller does **not** stand down from shaping the audio for a device
announcing `output_chain`, so any firmware that runs the output chain itself —
upstream's own included — gets EQ, bass guard and limiter applied twice, once
at each end. Two limiters in series is audible. If you go back and the sound
is wrong, that is where to look; turning `limiterEnabled` off on the device
side is the quickest way to confirm it.
