"""
Deployment-shape guards — not logic tests. The controller Dockerfile
COPYs each module explicitly, so a new em_*.py that works fine on bare
metal crash-loops the container at import time if the COPY line is
forgotten (bitten by em_scenes.py 2026-07-10 and em_oww_models.py
2026-07-19).
"""

import re
from pathlib import Path

CONTROLLER = Path(__file__).resolve().parents[1]


def test_dockerfile_copies_every_controller_module():
    dockerfile = (CONTROLLER / "Dockerfile").read_text()
    copied = set(re.findall(r"^COPY\s+(\S+\.py)\s", dockerfile, re.M))
    modules = {p.name for p in CONTROLLER.glob("em_*.py")} | {"version.py"}
    missing = sorted(modules - copied)
    assert not missing, (
        f"Dockerfile is missing COPY lines for {missing} — the container "
        f"will crash-loop at import time"
    )


def test_dashboard_bundle_is_cache_busted():
    """
    /dashboard must not hand the browser a bare /static/dashboard.js URL.

    aiohttp's add_static sends Last-Modified and ETag but no Cache-Control, so
    browsers apply heuristic freshness and serve a stale bundle without
    revalidating. That failure is invisible server-side — deploy correct, file
    correct, compiled bundle correct, browser showing the previous UI — so it
    reads as "my change didn't work" and sends you hunting in the wrong place.
    Asserted at the source level because the alternative is starting an aiohttp
    app, which this suite deliberately does not do.
    """
    import re
    from pathlib import Path

    src = (Path(__file__).resolve().parent.parent / "em_api.py").read_text()
    handler = src[src.index("async def _serve_dashboard"):]
    handler = handler[:handler.index("\nasync def ", 1)]

    assert "dashboard.js?v=" in handler, \
        "the bundle URL must carry a cache-busting token"
    assert "no-cache" in handler, \
        "dashboard.html itself must be revalidated, or the new URL is never seen"
    # A version-string token would not change between two local "dev" builds;
    # mtime changes on every rebuild.
    assert "st_mtime" in handler, \
        "cache-bust on the bundle's mtime, not on a version string"


def test_release_notes_survive_the_whole_relay():
    """
    Release notes have to make it through four places to be useful: captured
    from the GitHub response, persisted, re-read into the cache after a
    restart, and rendered. Miss any one and the dashboard shows a version
    number with no way to judge it — which is the state this replaced.

    The restart path is the one worth pinning: the in-memory cache is
    populated from the DB when cold, so notes omitted there would appear on
    first poll and silently vanish on every controller restart until the next
    one.
    """
    from pathlib import Path
    api = (Path(__file__).resolve().parent.parent / "em_api.py").read_text()

    fetch = api[api.index("async def _fetch_latest_release"):]
    fetch = fetch[:fetch.index("\nasync def ", 1)]
    assert 'release.get("body")' in fetch or '.get("body")' in fetch, \
        "the GitHub release body must be captured"
    assert 'set_config("latest_notes"' in fetch, "notes must be persisted"

    cached = api[api.index("    # Load from DB cache"):]
    cached = cached[:cached.index("\nasync def ", 1)] if "\nasync def " in cached else cached
    assert 'get_config("latest_notes"' in cached, \
        "the DB-cache path must restore notes, or they vanish on restart"

    jsx = (Path(__file__).resolve().parent.parent / "static" / "dashboard.jsx").read_text()
    assert "release.notes" in jsx, "the dashboard must render the notes"


def test_release_workflow_publishes_the_tag_annotation():
    """
    The notes shown in the dashboard come from the annotated tag, so the
    workflow must publish that rather than only GitHub's generated commit
    list. If this drifts, every future release silently shows a commit dump to
    whoever is deciding whether to update.
    """
    from pathlib import Path
    wf = (Path(__file__).resolve().parent.parent.parent
          / ".github" / "workflows" / "release.yml").read_text()
    assert "body_path:" in wf, "the release must publish notes from a file"
    assert "%(contents)" in wf, "notes must come from the tag annotation"


def test_every_device_payload_has_an_update_path():
    """
    A payload installed only at provisioning drifts forever.

    This has now bitten twice: start_server.sh (Lounge was a revision behind
    Office, 2026-07-11) and the debloat pair (round 2 added a package and every
    fielded device needed a manual push, 2026-07-30). Both fixes were the same
    shape — an md5-compared sync riding the OTA — so this asserts that every
    file in device_payloads/ is named by a sync function, and fails when a
    fourth payload is added without one.
    """
    from pathlib import Path
    root = Path(__file__).resolve().parent.parent
    api = (root / "em_api.py").read_text()
    payloads = sorted(p.name for p in (root / "device_payloads").iterdir() if p.is_file())
    assert payloads, "device_payloads/ is empty — has it moved?"

    for name in payloads:
        assert name in api, (
            f"{name} has no update path: nothing in em_api.py references it. "
            f"A payload installed only by the provisioning wizard drifts on every "
            f"device already in the field."
        )


def test_debloat_sync_reconciles_both_halves():
    """
    The debloat is a boot script AND a pm-hide list. Round 2 added a *package*,
    so a sync that only refreshed the script would have looked like it worked
    and changed nothing on any device.
    """
    from pathlib import Path
    api = (Path(__file__).resolve().parent.parent / "em_api.py").read_text()
    fn = api[api.index("async def _sync_debloat"):]
    fn = fn[:fn.index("\nasync def ", 1)] if "\nasync def " in fn[1:] else fn

    assert "echomuse-debloat.sh" in fn, "the boot script half must be synced"
    assert "_debloat_packages()" in fn, "the pm-hide half must be reconciled"
    assert "pm hide" in fn, "drifted packages must actually be hidden"
    # Rename-based replacement: the running shell keeps the old inode.
    assert "mv " in fn and ".new" in fn, \
        "the script must be replaced by rename, not written in place"
    # md5 both before (skip when in sync) and after (verify the transfer).
    assert fn.count("md5") >= 2, "sync must md5-compare and md5-verify"


def test_debloat_reachable_without_an_ota():
    """
    The OTA-time sync cannot reach a device already on the latest firmware —
    which is the exact case that exposed the gap. A manual trigger is required,
    not a nicety.
    """
    from pathlib import Path
    root = Path(__file__).resolve().parent.parent
    api = (root / "em_api.py").read_text()
    assert '"/api/devices/{id}/debloat"' in api, "no manual debloat endpoint registered"
    jsx = (root / "static" / "dashboard.jsx").read_text()
    assert "/debloat`" in jsx, "the dashboard must be able to trigger it"


def test_stale_release_cache_is_not_returned_when_it_has_aged_out():
    """
    _get_cached_release used to fire a refresh into the background and return
    the STALE value. Two consequences, both seen on 2026-07-30: the dashboard
    reported "there's an update" only after someone pressed Check now, and an
    OTA pushed v2.9.9 while v2.9.10 was the current release.

    The refresh is now awaited when the DB cache has aged past the check
    interval, falling back to the stale value only if the fetch fails.
    """
    from pathlib import Path
    api = (Path(__file__).resolve().parent.parent / "em_api.py").read_text()
    fn = api[api.index("async def _get_cached_release"):]
    fn = fn[:fn.index("\nasync def ", 1)]

    assert "await _fetch_latest_release()" in fn, \
        "an aged-out cache must be refreshed synchronously, not in the background"
    assert "asyncio.create_task(_fetch_latest_release())" not in fn, \
        "fire-and-forget refresh returns the stale value to this caller"


def test_release_change_is_pushed_to_open_dashboards():
    """A tab already showing the Updates panel should not sit on the old
    version until someone reloads."""
    from pathlib import Path
    root = Path(__file__).resolve().parent.parent
    api = (root / "em_api.py").read_text()
    assert '"type":         "release_update"' in api or '"release_update"' in api, \
        "a release change must be broadcast on the event stream"
    jsx = (root / "static" / "dashboard.jsx").read_text()
    # And the tab must keep asking, as a fallback for a missed event.
    upd = jsx[jsx.index("if (tab !== 'updates') return;"):]
    assert "setInterval" in upd[:600], \
        "the Updates tab must refresh while open, not fetch once on entry"


def test_streamed_playback_waits_for_the_device_not_a_computed_sleep():
    """
    Turn playback must end when the DEVICE says its buffer drained, never on an
    `audio_duration - elapsed` estimate.

    That estimate was removed on 2026-07-24: it has no visibility of the
    device's own buffer and cleared the ring 6.1s early on Retreat, 3.2s on
    Lounge. The streaming path reintroduced it (PR #47) where it is worse still
    — streaming already consumes most of the audio duration, so the remainder
    computes to ~0 and the wait disappears while up to ~5.5s is queued in
    audioChanDepth.
    """
    from pathlib import Path
    src = (Path(__file__).resolve().parent.parent / "em_controller.py").read_text()
    fn = src[src.index("async def _run_streaming_post_turn_playback"):]
    fn = fn[:fn.index("\nasync def ", 1)]

    assert "playback_done" in fn, \
        "streamed playback must await the device's playback_stats"
    assert "asyncio.sleep(remaining)" not in fn, \
        "the computed drain estimate was removed on 2026-07-24 — do not restore it"


def test_send_ms_stays_socket_write_time():
    """
    send_ms is documented as socket-write time that completes near-instantly
    however slow the link is — "never read it as delivery; that mistake cost a
    whole investigation on 2026-07-20". Timing the whole streaming loop instead
    folds HA's synthesis time in and makes it read exactly like delivery.
    """
    from pathlib import Path
    src = (Path(__file__).resolve().parent.parent / "em_controller.py").read_text()
    fn = src[src.index("async def stream_speaker_chunks"):]
    fn = fn[:fn.index("\n    async def ", 1) if "\n    async def " in fn[1:] else len(fn)]
    assert "send_seconds" in fn, \
        "socket-write time must be accumulated around the send calls"

    caller = src[src.index("async def _run_streaming_post_turn_playback"):]
    caller = caller[:caller.index("\nasync def ", 1)]
    assert "device.playback_send_ms = send_ms" in caller, \
        "send_ms must come from accumulated write time, not the loop duration"


def test_meter_ring_is_raised_when_audio_starts_not_at_playback_setup():
    """
    The meter pattern renders the live speaker RMS, so it draws an UNLIT ring
    until the device's ALSA write actually begins.

    On the buffered path that was invisible: the TTS fetch had already
    completed, so frames flushed at socket speed and audio began almost at
    once. Streaming moves fetch+decode inside playback, so raising the meter at
    setup time leaves the ring dark from the end of the spinner until HA
    returns audio — seconds on a slow response, and indistinguishable from a
    failed turn (user report 2026-07-31).

    The spinner must therefore stay up until the meter has something to show.
    """
    from pathlib import Path
    src = (Path(__file__).resolve().parent.parent / "em_controller.py").read_text()
    fn = src[src.index("async def post_turn_play_esphome"):]
    fn = fn[:fn.index("\n            # P0-1")]

    assert "_meter_at_playback_start(pcm_chunks" in fn, \
        "the meter must be gated on the first audio reaching the device"

    # A meter send is legitimate inside the nested helpers (_meter_on, and the
    # dead-man refresher). What must not exist is one in the function's OWN
    # body — that runs at setup, before any audio. Nested bodies are indented
    # deeper, so indentation is the discriminator.
    assert "\n" + " " * 20 + "await device.send_led_anim(meter)" not in fn, \
        ("the meter is being raised at playback setup — on the streaming path "
         "that is before HA has returned any audio, leaving the ring dark")


def test_meter_gate_fires_for_responses_shorter_than_the_prime_window():
    """
    A response shorter than SPEAKER_PRIME_SECONDS never reaches the byte
    threshold — the device starts playing it at EOS instead. Exhaustion must
    fire the callback too, or short answers play with no ring at all.
    """
    from pathlib import Path
    src = (Path(__file__).resolve().parent.parent / "em_controller.py").read_text()
    fn = src[src.index("async def _meter_at_playback_start"):]
    fn = fn[:fn.index("\nasync def ", 1)]

    body = fn.split("async for")[1]
    assert "if not fired:" in body.rsplit("\n", 4)[-4:][0] or "if not fired" in body, \
        "the generator must fire on exhaustion for sub-prime-length responses"
    assert "SPEAKER_PRIME_SECONDS" in fn, \
        "the threshold must track the device's actual prime window"


def test_controller_update_is_advisory_only():
    """
    The dashboard may TELL you a newer controller exists; it must never offer
    to apply it.

    The controller is a container the user owns and updates with their own
    docker tooling. An in-app update would have to restart the process serving
    the page, mid-request, with no way to report the outcome — and it is an
    explicit product decision (Wil, 2026-07-31) that this stays out of the
    interface. The notice is information for a decision the user takes
    elsewhere.
    """
    from pathlib import Path
    root = Path(__file__).resolve().parent.parent

    jsx = (root / "static" / "dashboard.jsx").read_text()
    start = jsx.index("{/* Controller update notice.")
    banner = jsx[start:jsx.index("{/* Summary */}", start)]
    for forbidden in ("API.post", "API.put", "API.delete", "onClick={doUpdate"):
        assert forbidden not in banner, (
            f"the controller update notice must not perform actions, found "
            f"{forbidden!r} — updating is the user's docker command to run"
        )

    api = (root / "em_api.py").read_text()
    assert 'add_get("/api/releases/controller"' in api, \
        "the controller release endpoint must be read-only (GET)"
    for verb in ("add_post", "add_put", "add_delete"):
        assert f'{verb}("/api/releases/controller' not in api, \
            f"{verb} on /api/releases/controller would make the update actionable"


def test_controller_notes_come_from_the_tag_annotation():
    """
    controller-v* tags ship a GHCR image and no GitHub Release (CLAUDE.md,
    "Versioning / releases"), so the notes must be read from the annotated
    tag object. Reading them from the releases list would return the newest
    DEVICE firmware release instead — right shape, wrong product.
    """
    from pathlib import Path
    api = (Path(__file__).resolve().parent.parent / "em_api.py").read_text()
    fn = api[api.index("async def _fetch_controller_release"):]
    fn = fn[:fn.index("\nasync def ", 1)]
    assert "GITHUB_TAGS_URL" in fn and "GITHUB_TAG_OBJECT_URL" in fn, \
        "controller notes must come from the tag annotation, not /releases"
    assert "GITHUB_API_URL" not in fn, \
        "that is the device firmware release feed, not the controller's"


def test_every_db_call_in_em_api_exists():
    """
    A typo'd db.<name> is invisible to pyflakes (it is a valid attribute
    expression) and raises AttributeError only on the request that uses it.
    That is the same shape as the NameError which stopped wake word
    fleet-wide on 2026-07-30: green CI, clean logs, broken at runtime.

    Written after db.get_global_config() — a function that has never existed —
    reached a live endpoint.
    """
    import ast
    from pathlib import Path
    root = Path(__file__).resolve().parent.parent

    tree = ast.parse((root / "em_api.py").read_text())
    used = {
        n.attr for n in ast.walk(tree)
        if isinstance(n, ast.Attribute)
        and isinstance(n.value, ast.Name) and n.value.id == "db"
    }
    db_tree = ast.parse((root / "em_db.py").read_text())
    defined = {
        n.name for n in db_tree.body
        if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))
    } | {
        t.id for n in db_tree.body if isinstance(n, ast.Assign)
        for t in n.targets if isinstance(t, ast.Name)
    }
    missing = sorted(used - defined)
    assert not missing, f"em_api.py calls db.{{{', '.join(missing)}}} which em_db.py does not define"


def test_the_wake_word_asset_wizard_step_is_mandatory():
    """
    Wil's call, overriding my "make it skippable": every provisioned device
    carries the runtime.

    The assets are not in the firmware, so a device without them advertises
    the oww_shadow capability while being unable to use it — the exact "I
    enabled it and nothing happened" this feature exists to remove. It also
    auto-runs: a button someone can leave unpressed is not mandatory.
    """
    from pathlib import Path
    jsx = (Path(__file__).resolve().parent.parent / "static" / "dashboard.jsx").read_text()

    steps = jsx[jsx.index("const _WIZARD_STEPS = ["):]
    steps = steps[:steps.index("\n];")]
    assert "'install_oww'" in steps, "the wake word asset step is missing from the wizard"

    idx = steps.count("{ id:", 0, steps.index("'install_oww'")) - 1
    auto = jsx[jsx.index("const autoSteps = new Set(["):]
    auto = auto[:auto.index(")")]
    assert str(idx) in auto, (
        f"step {idx} (install_oww) must auto-run — a step that needs a click "
        f"is one a user can skip"
    )

    runner = jsx[jsx.index("async function runInstallOwwAssets"):]
    runner = runner[:runner.index("\n  async function ", 1)]
    assert "a.md5" in runner and "throw new Error" in runner, \
        "the push must verify md5 and fail loudly — a truncated file fails later at dlopen"


def test_turn_end_reports_real_media_state_not_a_hardcoded_idle():
    """
    Issue #53: "the esphome media player reports that it is idle even though
    the music continues to play on the echo."

    Every voice turn ended by asserting MediaPlayerState.IDLE regardless of
    what the media player was doing. The feed announces PLAYING exactly once,
    when the decoder starts, so this IDLE arrived afterwards and became HA's
    last word — the entity showed a play arrow over audible music, and nothing
    ever corrected it.

    _media_state_msg() exists for precisely this ("current media_player state
    as HA should see it — em_player truth") and was being bypassed.
    """
    from pathlib import Path
    src = (Path(__file__).resolve().parent.parent / "em_esphome.py").read_text()

    fn = src[src.index("        finally:\n            # Signal HA that the satellite has finished"):]
    fn = fn[:fn.index("self._turn_active    = False")]

    assert "self._media_state_msg()" in fn, \
        "the turn must report the real media state at the end"
    assert "state=MediaPlayerState.IDLE" not in fn, (
        "a hardcoded IDLE at turn end overwrites the feed's PLAYING and "
        "leaves HA showing idle over audible music"
    )


def test_no_unjustified_hardcoded_media_state():
    """
    Forbid the SHAPE, not just the instances.

    A hardcoded MediaPlayerState sent to HA asserts what the player is doing
    without asking em_player, so it is wrong whenever the guess is wrong — and
    it wins, because the feed announces PLAYING exactly once, at the start.

    This has now been the same bug twice: the turn-end IDLE (#53, "reports
    idle even though the music continues to play"), and then IDLE on every
    device volume report, which told Music Assistant the music had stopped
    while it was audibly playing. Fixing instances one at a time is how the
    second one survived the first fix, so this pins the rule.

    Two remain legitimate and are named explicitly:
      - PLAYING for play_media, documented as optimistic — the feed pushes
        the authoritative state moments later.
      - ANNOUNCING, a genuine transition with no em_player equivalent.

    Anything else must go through _media_state_msg(), which reads em_player
    truth.
    """
    from pathlib import Path
    src = (Path(__file__).resolve().parent.parent / "em_esphome.py").read_text()

    allowed = {"MediaPlayerState.PLAYING", "MediaPlayerState.ANNOUNCING"}
    found = [
        line.strip()
        for line in src.splitlines()
        if "state=MediaPlayerState." in line
    ]
    offenders = [
        ln for ln in found
        if not any(a in ln for a in allowed)
    ]
    assert not offenders, (
        f"hardcoded media state(s) {offenders} — use _media_state_msg() so the "
        f"entity reflects what em_player is actually doing"
    )


def test_every_deliberate_cancel_also_flushes_the_speaker():
    """
    Cancelling a turn must stop the AUDIO, not just our end of it.

    cancel_event aborts the controller's feed. It cannot touch what is already
    on the device — up to ~5.5s sits in audioChanDepth — so without a
    speaker_flush the ring clears and the device carries on talking after you
    have visibly cancelled it. Reported 2026-08-01 for the action button,
    which was the one deliberate cancel missing it while mute and barge-in
    both had it.

    Forbidding the shape rather than fixing the instance: this is the second
    bug of exactly this kind today (the other was a hardcoded MediaPlayerState
    in one of three places), and fixing them one at a time is how the second
    one survived the first.
    """
    from pathlib import Path
    src = (Path(__file__).resolve().parent.parent / "em_controller.py").read_text()

    # Each deliberate cancel site, identified by its log line / guard, paired
    # with how far to look for the flush that must accompany it.
    sites = {
        "Dot button — cancelling voice turn": 900,
        "Muted during active turn": 900,
    }
    for marker, window in sites.items():
        i = src.find(marker)
        assert i != -1, f"cancel site {marker!r} not found — has it been renamed?"
        block = src[i:i + window]
        assert "cancel_event.set()" in block, f"{marker}: no cancel"
        assert "speaker_flush" in block, (
            f"{marker}: cancels the turn but never flushes the device speaker — "
            f"the response will keep playing after the turn is cancelled"
        )
