"""
VoiceAssistantAnnounceFinished is HA's completion signal, and HA BLOCKS on it.

`assist_satellite.entity.async_internal_announce` documents `async_announce`
as "should block until the announcement is done playing", holds
`_is_announcing` and the RESPONDING state for its duration, and raises
`SatelliteBusyError` if another announcement arrives meanwhile. The esphome
integration implements that by awaiting our reply
(`send_voice_assistant_announcement_await_response`).

We used to answer it synchronously, before a byte had played. So the
`assist_satellite.announce` service returned early, the entity left RESPONDING
early, and two chained announcements overlapped on the device instead of
queueing behind HA's own guard.

The justification in the code was that the setup wizard would otherwise time
out. It would not: `_ANNOUNCEMENT_TIMEOUT_SEC` is 5 minutes, and the wizard's
connection test does not wait on this message at all — it fires when the device
fetches the `CONNECTION_TEST_URL_BASE` media id.

Both directions matter and both are pinned here: the reply must not come early,
and it must always come. A reply that never arrives is worse than a late one —
it parks HA for five minutes with `_is_announcing` held, and every announcement
after it fails.

Async tests run through asyncio.run(), the idiom the rest of this suite uses —
pytest-asyncio is not in the test environment and this is not worth adding it
for.
"""

import asyncio

import em_esphome
from esphome.vendor import api_pb2


class FakeTransport:
    def __init__(self, closing=False):
        self._closing = closing

    def is_closing(self):
        return self._closing


def make_satellite():
    sat = object.__new__(em_esphome.EchoMuseSatellite)
    sat._log_name = "test"
    sat._transport = FakeTransport()
    sat.sent = []
    sat._send_one = sat.sent.append
    sat._on_announce = None
    sat._owning_server = None
    # _current_volume is a property on the real class; the state message is
    # stubbed instead so this test says nothing about volume reporting.
    sat._media_state_msg = lambda: api_pb2.MediaPlayerStateResponse(
        key=1, state=0, volume=0.5, muted=False
    )
    return sat


def finished(sat):
    return [
        m for m in sat.sent if isinstance(m, api_pb2.VoiceAssistantAnnounceFinished)
    ]


def fetch_returning(pcm):
    async def _fetch(url):
        return pcm

    return _fetch


def fetch_raising(exc):
    async def _fetch(url):
        raise exc

    return _fetch


# ── The reply lands after playback, not before ───────────────────────────────


def test_finished_is_not_sent_until_playback_completes(monkeypatch):
    """
    The whole point. While the audio is playing HA must still be blocked, so
    that a second announcement queues rather than talking over the first.
    """
    monkeypatch.setattr(em_esphome, "_fetch_tts_audio", fetch_returning(b"\x00\x00" * 100))
    sat = make_satellite()
    playing = asyncio.Event()
    release = asyncio.Event()

    async def slow_play(pcm):
        playing.set()
        await release.wait()

    sat._on_announce = slow_play

    async def main():
        task = asyncio.create_task(sat._run_announce("http://ha/x.flac"))
        await playing.wait()
        early = finished(sat)
        release.set()
        await task
        return early

    early = asyncio.run(main())
    assert early == [], "AnnounceFinished sent while audio was still playing"
    assert len(finished(sat)) == 1
    assert finished(sat)[0].success is True


def test_media_state_follows_the_finished_reply(monkeypatch):
    """
    Ordering, not just presence: HA reads the state after the announcement is
    over, and an announcement over music resolves to PAUSED there rather than
    a hardcoded IDLE.
    """
    monkeypatch.setattr(em_esphome, "_fetch_tts_audio", fetch_returning(b"\x00\x00" * 100))
    sat = make_satellite()

    async def play(pcm):
        return None

    sat._on_announce = play

    asyncio.run(sat._run_announce("http://ha/x.flac"))
    kinds = [type(m).__name__ for m in sat.sent]
    assert kinds.index("VoiceAssistantAnnounceFinished") < kinds.index(
        "MediaPlayerStateResponse"
    )


# ── The reply always comes ───────────────────────────────────────────────────


def test_fetch_failure_still_replies(monkeypatch):
    """
    Not replying parks HA for five minutes holding _is_announcing, after which
    every announcement fails SatelliteBusyError. success=False is strictly
    better than silence.
    """
    monkeypatch.setattr(
        em_esphome, "_fetch_tts_audio", fetch_raising(RuntimeError("ha unreachable"))
    )
    sat = make_satellite()

    asyncio.run(sat._run_announce("http://ha/x.flac"))
    assert len(finished(sat)) == 1
    assert finished(sat)[0].success is False


def test_empty_media_id_still_replies():
    sat = make_satellite()
    asyncio.run(sat._run_announce(""))
    assert len(finished(sat)) == 1
    assert finished(sat)[0].success is False


def test_no_playback_callback_reports_failure(monkeypatch):
    """
    Audio fetched but nothing to play it on (device not connected) is not a
    successful announcement, and HA should not be told it was.
    """
    monkeypatch.setattr(em_esphome, "_fetch_tts_audio", fetch_returning(b"\x00\x00" * 100))
    sat = make_satellite()

    asyncio.run(sat._run_announce("http://ha/x.flac"))
    assert finished(sat)[0].success is False


def test_empty_audio_reports_failure(monkeypatch):
    monkeypatch.setattr(em_esphome, "_fetch_tts_audio", fetch_returning(b""))
    sat = make_satellite()

    async def play(pcm):
        return None

    sat._on_announce = play

    asyncio.run(sat._run_announce("http://ha/x.flac"))
    assert finished(sat)[0].success is False


def test_a_wedged_playback_gives_up_and_replies(monkeypatch):
    """
    Our cap has to fire before HA's, or HA is the one left holding the
    announcement. The layer below is already bounded; this is the guard for
    when it isn't.
    """
    monkeypatch.setattr(em_esphome, "ANNOUNCE_TIMEOUT_S", 0.05)
    monkeypatch.setattr(em_esphome, "_fetch_tts_audio", fetch_returning(b"\x00\x00" * 100))
    sat = make_satellite()

    async def wedged(pcm):
        await asyncio.sleep(30)

    sat._on_announce = wedged

    asyncio.run(sat._run_announce("http://ha/x.flac"))
    assert len(finished(sat)) == 1
    assert finished(sat)[0].success is False


def test_our_cap_sits_under_has():
    """
    HA's _ANNOUNCEMENT_TIMEOUT_SEC is 5 minutes. Ours must be comfortably
    below it — the point of a cap here is to be the side that gives up first.
    """
    assert em_esphome.ANNOUNCE_TIMEOUT_S < 300


def test_a_closed_transport_does_not_raise(monkeypatch):
    """
    The device or HA can go away mid-announcement. Nothing to reply to, and a
    background task that raises here would be logged as an unhandled exception
    rather than anything actionable.
    """
    monkeypatch.setattr(em_esphome, "_fetch_tts_audio", fetch_returning(b"\x00\x00" * 100))
    sat = make_satellite()
    sat._transport = FakeTransport(closing=True)

    asyncio.run(sat._run_announce("http://ha/x.flac"))
    assert sat.sent == []
