"""The undefined-name gate, runnable before a push rather than after one.

CI already runs pyflakes over the controller and fails on "undefined name".
This is the same check in the suite, and it is here because the gap between
the two is a round trip: `em_controller.py` imports `em_api as api`, a handler
written against the module name read `em_api.notify_endpoint_restart_result`,
and every test passed — the branch is a device message no unit test sends, so
the NameError lived on a path only a real device reaches. The install would
have reported "the device never answered" forever.

Nothing here replaces the CI job: that one is the gate. This one is the same
answer twenty seconds earlier.

Skipped rather than failed when pyflakes is absent, because the suite's stated
dependencies are pytest/numpy/scipy/pyyaml and a contributor should not
discover a new one as a red test.
"""

import pathlib
import subprocess
import sys

import pytest

ROOT = pathlib.Path(__file__).resolve().parent.parent


def _has_pyflakes() -> bool:
    try:
        import pyflakes  # noqa: F401
    except ImportError:
        return False
    return True


@pytest.mark.skipif(not _has_pyflakes(), reason="pyflakes not installed")
def test_no_undefined_names():
    targets = sorted(
        str(p.relative_to(ROOT))
        for pattern in ("em_*.py", "tools/*.py", "tests/*.py", "esphome/*.py")
        for p in ROOT.glob(pattern)
    )
    assert targets, "found no controller sources to lint"
    proc = subprocess.run(
        [sys.executable, "-m", "pyflakes", *targets],
        cwd=ROOT,
        capture_output=True,
        text=True,
    )
    bad = [
        line
        for line in (proc.stdout + proc.stderr).splitlines()
        if "undefined name" in line.lower()
    ]
    assert not bad, "pyflakes found undefined names:\n" + "\n".join(bad)
