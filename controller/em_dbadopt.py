"""
Adopting the database a previous name left behind.

The project was renamed, and `config.yaml`'s `DB_PATH` went from
`/data/echomuse.db` to `/data/revoice.db`. The add-on slug did not change, so
the `/data` volume is the same one and nothing was deleted — but SQLite was
pointed at a filename that did not exist, created it, and every device,
user, token and setting sat in the file beside it. The visible result is a
controller that comes up perfectly and knows nothing.

Everything else in the data directory survived the rename by accident rather
than design: `tls/`, `endpoint_bins/`, `oww_models/` and the recordings are
all resolved from the DB path's PARENT, which did not change. Only the
SQLite file itself carries the name, which is why this is narrow.

**The decision is pure and the movement is not**, for em_linkauth.decide's
reason: the cost of being wrong is somebody's fleet, in one direction or the
other. Adopting when we should not have overwrites a database someone is
already using; refusing when we should have leaves them staring at an empty
dashboard with their data one filename away.
"""

from __future__ import annotations

import logging
import os
import sqlite3
import time
from pathlib import Path
from typing import NamedTuple

log = logging.getLogger("revoice.dbadopt")

# Names this project has used for its database, newest first. A file found
# under one of these beside the configured path is ours to adopt; anything
# else in the directory is somebody's and is not touched.
LEGACY_NAMES = ("echomuse.db",)

# SQLite in WAL mode keeps committed transactions in sidecars until a
# checkpoint. Moving the main file alone would leave them behind — which is
# data loss wearing the appearance of a successful migration.
SIDECAR_SUFFIXES = ("-wal", "-shm")


class Decision(NamedTuple):
    adopt: bool
    reason: str


def decide(*, legacy_exists: bool, legacy_devices: int,
           target_exists: bool, target_devices: int) -> Decision:
    """
    Whether the legacy database should become the live one.

    Ordered so the first answer is the one worth telling an operator, and
    written so that every "no" is a statement about evidence rather than a
    guess:

      * no legacy file            → nothing to do, and this is the ordinary
                                    case on every install that never had one.
      * legacy holds no devices   → nothing to carry over. A controller with
                                    no devices is not data anybody is missing.
      * no target yet             → adopt. There is nothing to lose.
      * target holds devices      → REFUSE. Somebody is using this database,
                                    and a migration that reverts a week of
                                    work is worse than the problem it fixes.
      * target holds none         → adopt, displacing an empty file.

    `devices` is the right measure because it is what the product is. A
    count that could not be read arrives here as 0, so an unreadable file is
    never adopted and never overwritten — both directions fail safe.
    """
    if not legacy_exists:
        return Decision(False, "no database under a previous name")
    if legacy_devices <= 0:
        return Decision(
            False, "the database under the previous name has no devices — "
                   "nothing to carry over")
    if not target_exists:
        return Decision(True, f"adopting {legacy_devices} device(s) from the "
                              f"database under the previous name")
    if target_devices > 0:
        return Decision(
            False, f"the current database already has {target_devices} "
                   f"device(s) — leaving both alone")
    return Decision(True, f"the current database is empty; adopting "
                          f"{legacy_devices} device(s) from the previous name")


def count_devices(path: Path) -> int:
    """
    How many APPROVED devices a database holds, or 0 when that cannot be read.

    **Approved, not all — and this is the whole correctness of the guard.**
    A device that connects to a controller under `strict` approval inserts a
    row immediately and waits at the door. So the moment the renamed
    controller came up empty, the fleet reconnected and filled `devices` with
    pending rows. Counting those would have made the new database look "in
    use" and refused the adoption on exactly the installations that needed
    it — the failure would have been indistinguishable from the migration
    simply not working.

    A pending row is not data anybody can lose: the device is still out
    there and will present itself again within seconds.

    Opened read-WRITE on purpose. A database left in WAL mode may need
    recovery before it can be read at all, and a read-only open fails on
    exactly that — which would report a perfectly good file as unreadable
    and refuse to migrate it. Nothing here writes rows; SQLite's own
    recovery is the only change, and it is one the file needed anyway.
    """
    if not path.is_file():
        return 0
    conn = None
    try:
        conn = sqlite3.connect(str(path))
        try:
            row = conn.execute(
                "SELECT COUNT(*) FROM devices WHERE approved = 1").fetchone()
        except sqlite3.OperationalError:
            # A schema old enough to predate the column. Counting everything
            # is the conservative answer there: it can only make this refuse
            # to adopt, never make it overwrite something.
            row = conn.execute("SELECT COUNT(*) FROM devices").fetchone()
        return int(row[0]) if row else 0
    except Exception as e:
        log.info(f"could not count devices in {path.name}: {e}")
        return 0
    finally:
        if conn is not None:
            try:
                # Fold the WAL into the main file so the move below carries
                # everything even if a sidecar is lost.
                conn.execute("PRAGMA wal_checkpoint(TRUNCATE)")
            except Exception:
                pass
            conn.close()


def legacy_path(db_path: str) -> Path | None:
    """The first previous-name database sitting beside `db_path`, or None."""
    target = Path(db_path).resolve()
    for name in LEGACY_NAMES:
        candidate = target.parent / name
        if candidate != target and candidate.is_file():
            return candidate
    return None


def _move(src: Path, dst: Path) -> None:
    """Move a database and its WAL sidecars together."""
    os.replace(src, dst)
    for suffix in SIDECAR_SUFFIXES:
        side = Path(str(src) + suffix)
        if side.exists():
            os.replace(side, Path(str(dst) + suffix))


def adopt_if_needed(db_path: str) -> str | None:
    """
    Run the adoption, returning the reason it acted or None when it did not.

    **Never deletes.** A displaced empty database is renamed aside with a
    timestamp rather than removed: it costs a few kilobytes and it means a
    wrong call here is recoverable by hand, which a delete would not be.

    Every failure is swallowed and logged. A controller that refuses to
    start because a migration could not run is strictly worse than one that
    starts on the empty database it already had — the data is still on disk
    either way, and only one of those two lets anybody look at it.
    """
    try:
        target = Path(db_path).resolve()
        legacy = legacy_path(db_path)
        d = decide(
            legacy_exists=legacy is not None,
            legacy_devices=count_devices(legacy) if legacy else 0,
            target_exists=target.is_file(),
            target_devices=count_devices(target),
        )
        if not d.adopt:
            if legacy is not None:
                log.info(f"[dbadopt] {d.reason}")
            return None

        if target.is_file():
            aside = target.with_name(
                f"{target.name}.replaced-{time.strftime('%Y%m%d-%H%M%S')}")
            _move(target, aside)
            log.warning(f"[dbadopt] moved the empty {target.name} aside as "
                        f"{aside.name}")
        _move(legacy, target)
        log.warning(f"[dbadopt] {d.reason}: {legacy.name} → {target.name}")
        return d.reason
    except Exception as e:
        log.error(f"[dbadopt] could not adopt a previous database ({e}) — "
                  f"starting on the configured one")
        return None
