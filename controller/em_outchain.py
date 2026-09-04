"""
Who shapes the audio — the controller, or the device.

From the moment firmware announces `output_chain` the device runs the whole
EQ → bass guard → limiter chain itself, at the ALSA write, post-mix. The
controller must then send UNSHAPED audio, and this module is the one place
that decides so.

**Never shape twice.** Two chains in series is two limiters in series, which
is audibly wrong rather than subtly wrong — the second one works against gain
reduction the first already applied, and the bass guard's law fires against a
signal from which its own band has already been removed. The capability gate
is the only thing standing between the two states, so it gets a module, a
pure function and tests rather than an `if` at three call sites.

Getting it wrong in the other direction is just as audible and quieter to
diagnose: a controller that stands down for a device with no chain of its own
ships audio nobody shaped — no EQ, and no limiter in front of the ±12dB
faders the dashboard offers, which is #231 all over again.

Split into its own module for `em_linkauth`'s reason: the test suite cannot
import `em_controller` or `em_player`, so a decision left inline in either is
a decision with no coverage.
"""

# The capability the DEVICE announces. Named here rather than typed at each
# call site: a typo in a capability string is a silent no-op — the set is a
# plain list of strings and nothing validates a name that is never in it.
CAPABILITY = "output_chain"


def controller_shapes(capabilities) -> bool:
    """
    Whether the CONTROLLER should apply the output chain for this device.

    True is the old behaviour and the default for everything unknown: a
    device that has not announced the capability — old firmware, a device
    that has not registered yet, a None — gets shaped here exactly as it
    always was. Degrade to old behaviour, never to a wrong answer.
    """
    return CAPABILITY not in (capabilities or [])


class Bypass:
    """
    A `StreamingEQ` that does nothing, for devices that shape their own audio.

    Deliberately a second class rather than a flag inside `StreamingEQ`: a
    bypassed chain there is not actually a passthrough. `em_mbc.BassGuard`
    with `enabled=False` still runs the Linkwitz-Riley crossover and sums the
    halves, and that sum is an ALLPASS — magnitude-flat, phase-shifted — so
    "disabled" is not the same bytes out as untouched. `em_limiter` likewise
    keeps holding its look-ahead tail when bypassed, on purpose, so the
    stream's latency does not jump on a toggle.

    Both of those behaviours are correct where they are and wrong here. When
    the device owns the chain the controller is not a bypassed processor, it
    is not a processor: `process` returns the caller's own bytes object.

    Implements the interface `stream_speaker_chunks` and `em_player._feed`
    actually use — `update`, `process`, `flush`, `limiter`, `guard` — and
    nothing else.
    """

    # None on both, which is what em_eq.describe_chain and describe_activity
    # already read as "not in the chain". They then report the stage as off,
    # which is the truth from the controller's side; what the device does with
    # its own chain is reported by the device.
    limiter = None
    guard = None

    def update(self, **_kwargs) -> bool:
        """
        Absorb the per-chunk live-settings push and report nothing moved.

        The music feed calls this ~23×/s with the device's current settings so
        a change is heard without a track skip. Those settings are on their
        way to the device over the config push; they have no business
        rebuilding a chain here. Returning False keeps the caller's "settings
        changed" log line honest.
        """
        return False

    def process(self, pcm: bytes) -> bytes:
        return pcm

    def flush(self) -> bytes:
        return b""
