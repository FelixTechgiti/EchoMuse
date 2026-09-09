"""
em_audiostate — is this Echo making a sound, and what is making it.

Exists for one job a person asked for and could not do: an amplifier wired to
the Dot's jack has to switch its input when the Echo has something to play,
and switch back when it stops. Home Assistant could see none of that. The
media_player entity reports what the CONTROLLER sent, and Spotify Connect,
AirPlay and Sendspin play from programs on the device that the controller
never hears about — which is the gap `em_db.DEFAULT_DEVICE_CONFIG` has warned
about since Sendspin shipped: "the HA media_player would report idle over
audible audio".

So this aggregates four signals into one answer, and the aggregation is here
rather than inside em_controller because the test suite cannot import that
module — the same reason em_barge, em_linkauth and em_runbarrier are their
own files.

WHAT COUNTS AS ACTIVE, AND THE ONE THAT DOES NOT

`thinking` counts. An amplifier that switches input when the audio STARTS has
already lost the first word: Home Assistant needs to run speech-to-text, an
intent and text-to-speech before a single sample exists, and an amp takes a
moment to settle on a new input. Thinking begins the instant the user stops
talking, which is the earliest honest signal that something is about to be
said.

`listening` does NOT count, and that is the deliberate one. It begins at the
wake word, before anyone knows whether a turn will produce anything — a false
wake, or a turn ending `no_speech`, would switch somebody's amplifier over
for nothing and switch it back seconds later. The margin thinking gives is
smaller and it is real.

THE HOLD-OFF IS THE WHOLE DIFFERENCE BETWEEN THIS WORKING AND NOT

Audio here is bursty in a way that has nothing to do with the listener: a
turn ends, an announcement follows, a track gaps. Reporting each lull would
switch an amplifier's input back and forth, which is worse than not
automating it at all — and it is the failure this feature would be judged by,
because it happens in front of whoever built the automation.

So going ACTIVE is immediate and going INACTIVE waits. Never the other way
round: delaying the rise is the case that loses the beginning of a sentence.
"""

from __future__ import annotations

from dataclasses import dataclass

# Reported as the source, in the order they outrank each other.
SOURCE_NONE      = "none"
SOURCE_VOICE     = "voice"
SOURCE_MEDIA     = "media"       # the controller's own 0x04 stream
SOURCE_SENDSPIN  = "sendspin"
SOURCE_SPOTIFY   = "spotify"
SOURCE_AIRPLAY   = "airplay"

# What the device may name as the owner of its music plane. Anything else it
# sends is treated as "not playing" rather than trusted — a source this
# controller does not know is one it cannot describe to Home Assistant, and
# inventing a name for it would put a string nobody can automate against into
# somebody's dashboard.
LOCAL_SOURCES = (SOURCE_SENDSPIN, SOURCE_SPOTIFY, SOURCE_AIRPLAY)

# Default hold-off. Long enough to bridge the gap between an answer and the
# announcement that follows it, short enough that a room does not sit on the
# wrong amplifier input after the music genuinely stopped. A taste value, like
# duckDb — it wants tuning against the amplifier in the room, which is why it
# is config rather than a constant.
DEFAULT_HOLDOFF_MS = 5000


@dataclass(frozen=True)
class Inputs:
    """
    Everything that can make this device audible.

    `local_source` is what the DEVICE reported owning its music plane, and
    None means it has not said — firmware too old to report, or a device that
    has not registered since the controller started. None must read as "not
    playing locally", never as unknown-therefore-active: the compatibility
    rule is to degrade to the old behaviour (voice and controller media, which
    the controller can see for itself), never to a wrong answer.
    """
    speaking: bool = False
    thinking: bool = False
    media: bool = False
    local_source: str | None = None


def source_of(inp: Inputs) -> str:
    """
    The one source to report, highest priority first.

    Voice outranks everything because it is what the mixer itself does: a turn
    DUCKS music rather than pausing it, so when both are audible the voice is
    the thing being listened to. Controller media outranks a local source for
    the same reason the device's own arbiter does — Home Assistant wins the
    music plane, and a local source is ended rather than starved when it does.
    """
    if inp.speaking or inp.thinking:
        return SOURCE_VOICE
    if inp.media:
        return SOURCE_MEDIA
    if inp.local_source in LOCAL_SOURCES:
        return inp.local_source
    return SOURCE_NONE


def is_live(inp: Inputs) -> bool:
    """Whether anything is audible right now, before the hold-off is applied."""
    return source_of(inp) != SOURCE_NONE


class AudioState:
    """
    The hold-off, as a state machine over an injected clock.

    Injected rather than read from time.monotonic() inside, so the tests can
    step it — the interesting behaviour is entirely about WHEN, and a test
    that sleeps is a test nobody runs.
    """

    def __init__(self, holdoff_ms: int = DEFAULT_HOLDOFF_MS) -> None:
        self.holdoff_ms = holdoff_ms
        self._active = False
        self._source = SOURCE_NONE
        # When the last live signal stopped. None while live, or while the
        # hold-off has already expired and been reported.
        self._quiet_since: float | None = None

    @property
    def active(self) -> bool:
        return self._active

    @property
    def source(self) -> str:
        return self._source

    def update(self, inp: Inputs, now: float) -> bool:
        """
        Fold in a new reading. `now` is monotonic seconds.

        Returns True when the reported state changed and Home Assistant needs
        telling — so the caller can push on a transition rather than on a
        tick, which is what keeps this off the audio path.
        """
        live = is_live(inp)

        if live:
            # Rising, and rising immediately: a delay here is the case that
            # costs the first word of an answer.
            self._quiet_since = None
            new_source = source_of(inp)
            changed = (not self._active) or (self._source != new_source)
            self._active = True
            self._source = new_source
            return changed

        if not self._active:
            return False

        # Quiet, but still holding. The SOURCE is held too: the amplifier is
        # still on that input, and reporting "none" while the entity is still
        # on would be two halves of one state disagreeing.
        if self.holdoff_ms <= 0:
            return self._go_quiet()

        if self._quiet_since is None:
            self._quiet_since = now
            return False

        if (now - self._quiet_since) * 1000.0 >= self.holdoff_ms:
            return self._go_quiet()
        return False

    def _go_quiet(self) -> bool:
        self._active = False
        self._source = SOURCE_NONE
        self._quiet_since = None
        return True

    def deadline(self, now: float) -> float | None:
        """
        Seconds REMAINING on the hold-off, or None when nothing is pending.

        The caller needs this because nothing else will wake it: the last
        `update` of a stream is the one that starts the hold-off, and no
        further signal arrives to end it. A timer armed on this is what turns
        the machine's decision into a message.

        Remaining rather than the full hold-off, because a caller may ask at
        any point — re-arming after an unrelated wake-up would otherwise
        extend the hold-off every time it was asked. Never negative: an
        expired hold-off is due now, not overdue.
        """
        if self._quiet_since is None or not self._active or self.holdoff_ms <= 0:
            return None
        left = self.holdoff_ms / 1000.0 - (now - self._quiet_since)
        return max(0.0, left)
