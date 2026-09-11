"""
A moved path is the one change that breaks silently at both ends.

The rename took `/data/local/etc/echomuse/` to `/data/local/etc/revoice/` in
a single commit, on the controller AND the firmware. This project's own
compatibility rule names that shape: adding a message is safe unnegotiated,
MOVING one never is — the old path stops being used, the new one is not there
yet, and neither side reports it.

Firmware in the field predates the rename. A current controller pushing
credentials to the new directory writes a file nothing will ever open,
reports success, and leaves the device on whatever it had.

The asymmetry decides who carries the compatibility: repairing the controller
repairs every fielded device at once, while repairing the device needs an
update that, under REQUIRE_DEVICE_TLS, it may no longer be able to
authenticate for. So the controller writes both and reads either.
"""

import em_devicepaths as p


def test_the_current_directory_comes_first():
    assert p.write_dirs()[0] == p.CURRENT_DIR


def test_every_previous_name_is_still_written():
    for legacy in p.LEGACY_DIRS:
        assert legacy in p.write_dirs(), (
            f"{legacy} is a directory some fielded device still reads; "
            f"dropping it strands that device")


def test_the_echomuse_directory_is_among_them():
    # Named explicitly rather than left to LEGACY_DIRS: this is the one that
    # every device in the field today is using, and a future tidy-up that
    # removes it should have to delete this line and read why.
    assert "/data/local/etc/echomuse" in p.write_dirs()


def test_read_paths_try_the_current_one_first():
    paths = p.read_paths("token")
    assert paths[0] == f"{p.CURRENT_DIR}/token"
    assert len(paths) == len(p.write_dirs())


def test_the_read_command_falls_through_each_path():
    cmd = p.first_readable_command("supervisor.log", "busybox tail -c 4096")
    for path in p.read_paths("supervisor.log"):
        assert path in cmd
    assert cmd.count("||") == len(p.write_dirs()) - 1


def test_a_missing_file_does_not_become_output():
    # Every attempt discards stderr, so a path that is not there falls
    # through silently instead of putting an error into the log text the
    # caller then has to recognise and strip.
    cmd = p.first_readable_command("supervisor.log", "busybox tail -c 4096")
    assert cmd.count("2>/dev/null") == len(p.write_dirs())


def test_one_mkdir_covers_every_directory():
    cmd = p.mkdir_command()
    assert cmd.startswith("mkdir -p ")
    for d in p.write_dirs():
        assert d in cmd


def test_no_directory_is_listed_twice():
    dirs = p.write_dirs()
    assert len(set(dirs)) == len(dirs)
