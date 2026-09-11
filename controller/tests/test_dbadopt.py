"""
Adopting the database a previous name left behind.

The rename moved `DB_PATH` from `/data/echomuse.db` to `/data/revoice.db`.
Same add-on slug, same `/data` volume, different filename — so SQLite created
the new file and the controller came up perfectly, knowing nothing. Reported
from a live instance: "jetzt habe ich eine Leere Instanz".

The decision is pure because both wrong answers cost somebody their fleet:
adopting over a database in use destroys it, refusing leaves an empty
dashboard with the data one filename away.
"""

import sqlite3

import pytest

import em_dbadopt as a


def _db(path, devices=0, pending=0):
    conn = sqlite3.connect(str(path))
    conn.execute("CREATE TABLE devices (device_id TEXT PRIMARY KEY, "
                 "approved INTEGER NOT NULL DEFAULT 0)")
    for i in range(devices):
        conn.execute("INSERT INTO devices VALUES (?, 1)", (f"dev{i}",))
    for i in range(pending):
        conn.execute("INSERT INTO devices VALUES (?, 0)", (f"pending{i}",))
    conn.commit()
    conn.close()
    return path


# ── the decision ──────────────────────────────────────────────────────────

def test_nothing_to_do_without_a_legacy_file():
    d = a.decide(legacy_exists=False, legacy_devices=0,
                 target_exists=True, target_devices=3)
    assert not d.adopt


def test_an_empty_legacy_is_not_worth_adopting():
    d = a.decide(legacy_exists=True, legacy_devices=0,
                 target_exists=False, target_devices=0)
    assert not d.adopt
    assert "no devices" in d.reason


def test_adopted_when_the_new_file_does_not_exist_yet():
    d = a.decide(legacy_exists=True, legacy_devices=2,
                 target_exists=False, target_devices=0)
    assert d.adopt


def test_the_live_case_an_empty_new_file_beside_a_full_old_one():
    d = a.decide(legacy_exists=True, legacy_devices=1,
                 target_exists=True, target_devices=0)
    assert d.adopt
    assert "empty" in d.reason


def test_a_database_in_use_is_never_replaced():
    # The failure this guard exists for: somebody who set up fresh under the
    # new name and would otherwise be reverted to a stale file.
    d = a.decide(legacy_exists=True, legacy_devices=9,
                 target_exists=True, target_devices=1)
    assert not d.adopt
    assert "already has 1 device" in d.reason


def test_an_unreadable_count_fails_safe_in_both_directions():
    # count_devices returns 0 for anything it cannot read, so an unreadable
    # legacy is not adopted and an unreadable target is not overwritten...
    assert not a.decide(legacy_exists=True, legacy_devices=0,
                        target_exists=True, target_devices=0).adopt


# ── the filesystem half ───────────────────────────────────────────────────

def test_counting_reads_a_real_database(tmp_path):
    assert a.count_devices(_db(tmp_path / "x.db", devices=4)) == 4
    assert a.count_devices(tmp_path / "missing.db") == 0


def test_counting_a_file_that_is_not_a_database_is_zero_not_a_crash(tmp_path):
    junk = tmp_path / "junk.db"
    junk.write_text("this is not sqlite")
    assert a.count_devices(junk) == 0


def test_legacy_is_found_beside_the_configured_path(tmp_path):
    _db(tmp_path / "echomuse.db", devices=1)
    found = a.legacy_path(str(tmp_path / "revoice.db"))
    assert found is not None and found.name == "echomuse.db"


def test_a_legacy_name_equal_to_the_target_is_not_itself(tmp_path):
    # Configured AS the legacy name — there is no migration to do, and
    # adopting a file onto itself would be a very bad move.
    _db(tmp_path / "echomuse.db", devices=1)
    assert a.legacy_path(str(tmp_path / "echomuse.db")) is None


def test_end_to_end_the_old_database_becomes_the_live_one(tmp_path):
    legacy = _db(tmp_path / "echomuse.db", devices=3)
    target = _db(tmp_path / "revoice.db", devices=0)
    reason = a.adopt_if_needed(str(target))
    assert reason, "the live case must adopt"
    assert a.count_devices(target) == 3, "the devices did not come across"
    assert not legacy.exists(), "the old file should have been moved, not copied"
    displaced = list(tmp_path.glob("revoice.db.replaced-*"))
    assert len(displaced) == 1, "the empty database must be kept, never deleted"


def test_end_to_end_a_database_in_use_is_left_exactly_as_it_was(tmp_path):
    legacy = _db(tmp_path / "echomuse.db", devices=3)
    target = _db(tmp_path / "revoice.db", devices=2)
    assert a.adopt_if_needed(str(target)) is None
    assert a.count_devices(target) == 2
    assert a.count_devices(legacy) == 3
    assert not list(tmp_path.glob("*.replaced-*"))


def test_rows_committed_only_to_the_wal_survive_the_move(tmp_path):
    """
    The invariant that matters, stated as data rather than as filenames.

    SQLite in WAL mode keeps committed transactions in a `-wal` sidecar
    until a checkpoint, so moving the main file alone loses them — data
    loss wearing the appearance of a successful migration.

    This does NOT assert the sidecars are moved, because they are not:
    `count_devices` checkpoints with TRUNCATE, which folds the WAL into the
    main file and removes it. That is the stronger guarantee — there is
    nothing left to lose track of — and asserting on the files rather than
    on the rows would have called that correct behaviour a failure. (It
    did: the first version of this test failed for exactly that reason.)
    """
    legacy = tmp_path / "echomuse.db"
    conn = sqlite3.connect(str(legacy))
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("CREATE TABLE devices (device_id TEXT PRIMARY KEY)")
    conn.execute("INSERT INTO devices VALUES ('in-the-wal')")
    conn.commit()
    conn.close()                      # committed; may still be in the -wal

    a.adopt_if_needed(str(tmp_path / "revoice.db"))

    assert a.count_devices(tmp_path / "revoice.db") == 1, (
        "a row committed to the WAL did not survive the adoption")
    assert not legacy.exists()
    # And nothing is orphaned under the old name.
    assert not list(tmp_path.glob("echomuse.db*"))


def test_running_twice_changes_nothing_the_second_time(tmp_path):
    _db(tmp_path / "echomuse.db", devices=3)
    target = tmp_path / "revoice.db"
    assert a.adopt_if_needed(str(target))
    assert a.adopt_if_needed(str(target)) is None
    assert a.count_devices(target) == 3


def test_a_pending_device_does_not_count_as_one_in_use(tmp_path):
    """
    The bug this nearly shipped with, and the one it would have hurt most.

    Under `strict` approval a device inserts its row the moment it connects
    and then waits. So a controller that came up on an empty database had its
    fleet reconnect and fill `devices` with pending rows within seconds — and
    counting those would have made the empty database look "in use",
    refusing the adoption on exactly the installations that needed it. The
    symptom would have been the migration appearing not to work at all.

    A pending row is not data anybody loses: the device is still out there
    and presents itself again within seconds.
    """
    _db(tmp_path / "echomuse.db", devices=2)
    target = _db(tmp_path / "revoice.db", devices=0, pending=1)
    assert a.count_devices(target) == 0, "a pending device is not an approved one"
    assert a.adopt_if_needed(str(target)), "the live case must still adopt"
    assert a.count_devices(target) == 2


def test_a_schema_without_the_approved_column_still_counts(tmp_path):
    # Old enough to predate the column. Counting everything can only make
    # this REFUSE, never overwrite — the safe direction.
    legacy = tmp_path / "old.db"
    conn = sqlite3.connect(str(legacy))
    conn.execute("CREATE TABLE devices (device_id TEXT PRIMARY KEY)")
    conn.execute("INSERT INTO devices VALUES ('a')")
    conn.commit()
    conn.close()
    assert a.count_devices(legacy) == 1
