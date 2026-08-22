#!/usr/bin/env python3
"""
Generate the golden fixture for the device-side output chain (#243, #272).

WHY THIS EXISTS. The output chain — EQ, bass guard, limiter — is moving from
the controller onto the device (docs/audio-states.md section 8). The one risk
in that move with no known method is whether the port SOUNDS THE SAME; every
other requirement is engineering. This fixture is the instrument that turns
that question from a listening test at the end into a test failure in the
middle.

The precedent is internal/wakeword/testdata/gen_fixture.py, which validated
the wake pipeline against Python tensor-for-tensor and is the reason on-device
wake word was trustworthy the day it arrived rather than after a fortnight of
doubt. Same shape here: Python is the reference implementation, its output is
captured, and the Go port must reproduce it.

WHAT IS CAPTURED, and why it is a sequence of STEPS rather than one buffer.
The chain carries state — biquad histories, the limiter's look-ahead and gain
envelope, the guard's crossover and detector — so a fixture of independent
buffers would prove nothing about the part most likely to be wrong. Each case
is therefore a sequence of steps, each with its own parameters, and a step
whose parameters DIFFER from the one before exercises the in-place update path
that section 8.3 requires to be click-free. A Go implementation that rebuilt
its filters on a parameter change would produce a transient exactly there, and
this file is what catches it.

Run:
    cd device/internal/outchain/testdata && python3 gen_chain_fixture.py
Needs the controller's deps (numpy, scipy) and imports the controller modules
directly — there is deliberately no second copy of the chain.
"""
from __future__ import annotations

import os
import struct
import sys

import numpy as np

# The reference implementation is the controller's, imported rather than
# reimplemented. If this path breaks, fix the path — do not vendor a copy,
# because two copies of a DSP chain drift and the fixture would then certify
# agreement with the wrong one.
_HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.abspath(os.path.join(_HERE, "..", "..", "..", "..", "controller")))

import em_eq       # noqa: E402
import em_limiter  # noqa: E402
import em_mbc      # noqa: E402

SAMPLE_RATE = 48000
# 2048 samples matches the device's ALSA period (periodSize in pcm_speaker.go),
# so the fixture's chunk boundaries are the boundaries the real implementation
# will actually see. A fixture chunked differently would leave the per-period
# behaviour — which is where state bugs live — untested.
CHUNK = 2048

MAGIC = b"EMCHAIN1"
FLAT = [0.0] * em_eq.NUM_BANDS


# ─── Signals ─────────────────────────────────────────────────────────────────
#
# Deterministic, and chosen to exercise different parts of the chain. A single
# signal would leave whole stages effectively untested: white noise never
# triggers the limiter, and a quiet tone never engages the bass guard.

def _rng():
    """Fixed seed. The fixture must be byte-reproducible or a regeneration
    looks like a regression."""
    return np.random.default_rng(20260822)


def sig_noise(n: int, amp: float = 0.25) -> np.ndarray:
    """Broadband, moderate level. Exercises every EQ band at once."""
    return _rng().normal(0.0, amp * 32767.0, n)


def sig_bass(n: int, amp: float = 0.7) -> np.ndarray:
    """60Hz — below the 115Hz crossover, so the bass guard has something to
    act on. Loud, because the guard is a threshold device."""
    t = np.arange(n) / SAMPLE_RATE
    return amp * 32767.0 * np.sin(2 * np.pi * 60.0 * t)


def sig_hot(n: int) -> np.ndarray:
    """Near full scale with transients, so the limiter engages hard and its
    release curve is exercised rather than just its threshold."""
    t = np.arange(n) / SAMPLE_RATE
    base = 0.95 * 32767.0 * np.sin(2 * np.pi * 440.0 * t)
    # Bursts, to make the release envelope visible between them.
    env = np.ones(n)
    for start in range(0, n, CHUNK * 2):
        env[start:start + CHUNK // 4] = 0.15
    return base * env


def sig_sweep(n: int) -> np.ndarray:
    """Log sweep 30Hz→16kHz. Crosses every band centre and the crossover, so a
    wrong coefficient anywhere shows up somewhere in the output."""
    t = np.arange(n) / SAMPLE_RATE
    f0, f1 = 30.0, 16000.0
    dur = max(t[-1], 1e-9)
    phase = 2 * np.pi * f0 * dur / np.log(f1 / f0) * (np.power(f1 / f0, t / dur) - 1.0)
    return 0.5 * 32767.0 * np.sin(phase)


def to_pcm(x: np.ndarray) -> bytes:
    return np.clip(x, -32768, 32767).astype(np.int16).tobytes()


# ─── Parameter sets ──────────────────────────────────────────────────────────

class Params:
    """One settable state of the whole chain — exactly the arguments
    StreamingEQ.update() takes, so a step is a call."""

    def __init__(self, bands=None, loudness=False,
                 limiter_enabled=False, limiter_threshold=-1.0,
                 limiter_release=120.0,
                 guard_enabled=False, guard_db=-30.0):
        self.bands = list(bands) if bands is not None else list(FLAT)
        self.loudness = bool(loudness)
        self.limiter_enabled = bool(limiter_enabled)
        self.limiter_threshold = float(limiter_threshold)
        self.limiter_release = float(limiter_release)
        self.guard_enabled = bool(guard_enabled)
        self.guard_db = float(guard_db)

    def pack(self) -> bytes:
        out = struct.pack("<H", len(self.bands))
        for b in self.bands:
            out += struct.pack("<f", b)
        out += struct.pack("<BBffBf",
                           1 if self.loudness else 0,
                           1 if self.limiter_enabled else 0,
                           self.limiter_threshold,
                           self.limiter_release,
                           1 if self.guard_enabled else 0,
                           self.guard_db)
        return out


P_FLAT = Params()
P_EQ = Params(bands=[6.0, 4.0, -3.0, 0.0, 2.0, -5.0, 3.0, 5.0])
P_EQ_LOUD = Params(bands=[6.0, 4.0, -3.0, 0.0, 2.0, -5.0, 3.0, 5.0], loudness=True)
P_LIM = Params(limiter_enabled=True, limiter_threshold=-3.0, limiter_release=120.0)
P_LIM_HARD = Params(limiter_enabled=True, limiter_threshold=-12.0, limiter_release=40.0)
P_GUARD = Params(guard_enabled=True, guard_db=-30.0)
P_ALL = Params(bands=[6.0, 4.0, -3.0, 0.0, 2.0, -5.0, 3.0, 5.0],
               limiter_enabled=True, limiter_threshold=-3.0, limiter_release=120.0,
               guard_enabled=True, guard_db=-30.0)


# ─── Cases ───────────────────────────────────────────────────────────────────

def build_cases():
    """
    (name, signal, [params per chunk]) — the params list length sets the
    number of chunks. Repeating a Params object means steady state; changing
    it mid-list is the in-place update path.
    """
    n8 = CHUNK * 8
    return [
        # Each stage alone first, so a failure localises to one stage instead
        # of to "the chain".
        ("passthrough",     sig_noise(n8),  [P_FLAT] * 8),
        ("eq_only",         sig_noise(n8),  [P_EQ] * 8),
        ("eq_loudness",     sig_noise(n8),  [P_EQ_LOUD] * 8),
        ("limiter_only",    sig_hot(n8),    [P_LIM] * 8),
        ("limiter_hard",    sig_hot(n8),    [P_LIM_HARD] * 8),
        ("guard_only",      sig_bass(n8),   [P_GUARD] * 8),
        ("full_chain",      sig_sweep(n8),  [P_ALL] * 8),

        # Then the transitions. These are the cases section 8.3 is about: a
        # port that rebuilds its filters rather than updating them in place
        # passes every case above and fails these.
        ("switch_flat_to_eq",   sig_noise(n8),  [P_FLAT] * 4 + [P_EQ] * 4),
        ("switch_eq_curve",     sig_sweep(n8),  [P_EQ] * 4 + [P_EQ_LOUD] * 4),
        ("switch_limiter_on",   sig_hot(n8),    [P_FLAT] * 4 + [P_LIM] * 4),
        ("switch_limiter_thr",  sig_hot(n8),    [P_LIM] * 4 + [P_LIM_HARD] * 4),
        ("switch_guard_on",     sig_bass(n8),   [P_FLAT] * 4 + [P_GUARD] * 4),
        # Every chunk different: the pathological case for anything that
        # rebuilds state, and the one a user sweeping a slider produces.
        ("sweep_params",        sig_noise(n8),
         [P_FLAT, P_EQ, P_EQ_LOUD, P_ALL, P_LIM, P_GUARD, P_EQ, P_ALL]),
    ]


def run_case(signal: np.ndarray, params: list) -> tuple[list[bytes], bytes]:
    """Drive the reference chain and capture what it produced per chunk.

    Built ONCE and updated in place, exactly as em_player and em_controller do
    it — a fresh instance per chunk would reset the filter states and produce
    a fixture that certifies the wrong behaviour.
    """
    first = params[0]
    eq = em_eq.StreamingEQ(
        SAMPLE_RATE, first.bands, first.loudness,
        limiter=em_limiter.Limiter(SAMPLE_RATE,
                                   threshold_db=first.limiter_threshold,
                                   release_ms=first.limiter_release,
                                   enabled=first.limiter_enabled),
        guard=em_mbc.BassGuard(SAMPLE_RATE,
                               bass_guard_db=first.guard_db,
                               enabled=first.guard_enabled))
    outs = []
    for i, p in enumerate(params):
        eq.update(bands=p.bands, loudness=p.loudness,
                  limiter_enabled=p.limiter_enabled,
                  limiter_threshold=p.limiter_threshold,
                  limiter_release=p.limiter_release,
                  guard_enabled=p.guard_enabled,
                  guard_db=p.guard_db)
        chunk = signal[i * CHUNK:(i + 1) * CHUNK]
        outs.append(eq.process(to_pcm(chunk)))
    # The limiter holds a look-ahead tail; end of stream must emit it or the
    # last few milliseconds of every track go missing.
    return outs, eq.flush()


def main():
    cases = build_cases()
    blob = bytearray(MAGIC)
    blob += struct.pack("<II", SAMPLE_RATE, CHUNK)
    blob += struct.pack("<I", len(cases))

    for name, signal, params in cases:
        outs, tail = run_case(signal, params)
        nm = name.encode()
        blob += struct.pack("<H", len(nm)) + nm
        blob += struct.pack("<I", len(params))
        for i, p in enumerate(params):
            chunk_pcm = to_pcm(signal[i * CHUNK:(i + 1) * CHUNK])
            blob += p.pack()
            blob += struct.pack("<I", len(chunk_pcm)) + chunk_pcm
            blob += struct.pack("<I", len(outs[i])) + outs[i]
        blob += struct.pack("<I", len(tail)) + tail
        print(f"  {name:22} {len(params)} steps, tail {len(tail) // 2} samples")

    out_path = os.path.join(_HERE, "chain_fixture.bin")
    with open(out_path, "wb") as f:
        f.write(blob)
    print(f"\nwrote {out_path} ({len(blob)} bytes, {len(cases)} cases)")


if __name__ == "__main__":
    main()
