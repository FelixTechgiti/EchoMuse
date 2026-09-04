"""
The controller must shape exactly once — here, or on the device, never both.

Two chains in series is two limiters in series, which is audibly wrong; a
controller that stands down for a device with no chain of its own ships audio
nobody shaped. Neither failure raises anything, and neither module that makes
the decision is importable by this suite, so the gate is pinned two ways: the
decision itself is a pure function with unit tests, and the call sites are
checked by parsing the shipped source.
"""

import ast
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

import em_outchain
import em_eq
import em_limiter
import em_mbc

CONTROLLER = pathlib.Path(__file__).resolve().parent.parent


# ─── The decision ─────────────────────────────────────────────────────────

def test_a_device_that_says_nothing_is_shaped_here():
    # Degrade to old behaviour, never to a wrong answer: old firmware, a
    # device mid-registration, and a None all keep the controller-side chain.
    assert em_outchain.controller_shapes(None)
    assert em_outchain.controller_shapes([])
    assert em_outchain.controller_shapes(["mic", "speaker", "audio_mix"])


def test_announcing_the_capability_stands_the_controller_down():
    assert not em_outchain.controller_shapes(["output_chain"])
    assert not em_outchain.controller_shapes(["mic", "output_chain", "leds"])


def test_mixing_is_not_evidence_of_shaping():
    # The fleet already contains firmware that mixes and does not shape.
    # Reading audio_mix as output_chain puts two limiters in series on every
    # one of those devices.
    assert em_outchain.controller_shapes(["audio_mix"])


# ─── The bypass ───────────────────────────────────────────────────────────

def test_bypass_returns_the_callers_own_bytes():
    pcm = b"\x01\x02\x03\x04" * 100
    out = em_outchain.Bypass().process(pcm)
    assert out is pcm


def test_bypass_absorbs_the_live_settings_push_and_reports_nothing_moved():
    b = em_outchain.Bypass()
    assert b.update(bands=[6.0] * 8, loudness=True, limiter_enabled=True,
                    limiter_threshold=-3.0, limiter_release=100.0,
                    guard_enabled=True, guard_db=-30.0) is False
    assert b.flush() == b""


def test_bypass_reports_no_stages_to_the_instrumentation():
    b = em_outchain.Bypass()
    assert b.limiter is None and b.guard is None
    # describe_* are what the log lines call; they must not blow up on it.
    assert em_eq.describe_chain([0.0] * 8, False, b.limiter, b.guard)
    assert em_eq.describe_activity(b.limiter, b.guard)


def test_bypass_implements_everything_the_call_sites_use():
    # stream_speaker_chunks and em_player._feed use exactly these five.
    for name in ("update", "process", "flush", "limiter", "guard"):
        assert hasattr(em_outchain.Bypass(), name), name
        assert hasattr(em_eq.StreamingEQ(48000), name), name


def test_a_disabled_chain_is_not_a_passthrough():
    """
    The reason Bypass is a class and not `StreamingEQ(..., enabled=False)`.

    A disabled BassGuard still runs the Linkwitz-Riley crossover and sums the
    halves, and that sum is an allpass — magnitude-flat, phase-shifted. So
    "every stage disabled" does not return the input bytes, and standing the
    controller down by disabling stages would still put a filter in front of
    audio the device is about to shape.
    """
    import numpy as np
    rng = np.random.default_rng(7)
    pcm = (rng.integers(-8000, 8000, 4800, dtype=np.int64)
           .astype(np.int16).tobytes())
    off = em_eq.StreamingEQ(
        48000, [0.0] * 8, False,
        limiter=em_limiter.Limiter(48000, enabled=False),
        guard=em_mbc.BassGuard(48000, enabled=False),
    )
    assert off.process(pcm) != pcm
    assert em_outchain.Bypass().process(pcm) == pcm


# ─── The call sites ───────────────────────────────────────────────────────

CHAIN_CONSTRUCTORS = {
    ("em_eq", "StreamingEQ"),
    ("em_eq", "apply"),
    ("em_limiter", "Limiter"),
    ("em_mbc", "BassGuard"),
}


def _chain_building_functions(path):
    """Every function in `path` that builds or runs an output chain."""
    tree = ast.parse(path.read_text(), str(path))
    parents = {}
    for node in ast.walk(tree):
        for child in ast.iter_child_nodes(node):
            parents[child] = node

    found = {}
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        f = node.func
        if not (isinstance(f, ast.Attribute) and isinstance(f.value, ast.Name)):
            continue
        if (f.value.id, f.attr) not in CHAIN_CONSTRUCTORS:
            continue
        # Walk out to the nearest enclosing function.
        cur = node
        while cur in parents and not isinstance(
                cur, (ast.FunctionDef, ast.AsyncFunctionDef)):
            cur = parents[cur]
        if isinstance(cur, (ast.FunctionDef, ast.AsyncFunctionDef)):
            # A nested helper (em_controller's _prepare_pcm) is gated by its
            # caller, so attribute it to the outermost enclosing function.
            outer = cur
            walk = cur
            while walk in parents:
                walk = parents[walk]
                if isinstance(walk, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    outer = walk
            found.setdefault(outer.name, outer)
    return found


def _mentions_the_gate(fn) -> bool:
    for node in ast.walk(fn):
        if isinstance(node, ast.Attribute) and isinstance(node.value, ast.Name):
            if node.value.id == "em_outchain":
                return True
    return False


def test_every_shaping_site_is_gated_on_the_capability():
    """
    A new streaming path that builds a chain without consulting em_outchain
    compiles, passes every other test, and puts two limiters in series on
    every device that shapes its own audio. Nothing else would catch it.
    """
    ungated = []
    for name in ("em_controller.py", "em_player.py"):
        path = CONTROLLER / name
        for fn_name, fn in _chain_building_functions(path).items():
            if not _mentions_the_gate(fn):
                ungated.append(f"{name}:{fn_name}")
    assert not ungated, (
        "these build an output chain without asking em_outchain whether the "
        f"device shapes its own audio: {ungated}"
    )


def test_the_guard_would_notice_an_ungated_site():
    """Reintroduce the bug in a scratch module and check the guard fires."""
    import tempfile
    src = (
        "import em_eq\n"
        "def feed(device):\n"
        "    return em_eq.StreamingEQ(48000, device.eq_bands)\n"
    )
    with tempfile.TemporaryDirectory() as d:
        p = pathlib.Path(d) / "scratch.py"
        p.write_text(src)
        fns = _chain_building_functions(p)
        assert "feed" in fns
        assert not _mentions_the_gate(fns["feed"])
