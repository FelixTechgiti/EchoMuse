"""Extra positive training samples via Google Cloud Text-to-Speech.

Piper's LibriTTS-R generator provides sample volume (hundreds of speakers,
cheap, local); Google's Neural2/Studio/Chirp voices add a different — and
much higher-fidelity — acoustic character. A modest layer of Google samples
(a few thousand) on top of the Piper set adds voice diversity the model
can't get from a single TTS family.

Clips are written straight into the wake word's positive_train/positive_test
directories. openWakeWord's generate step counts existing files toward
n_samples, so Google clips added *before* `forge.py build` simply displace
that many Piper generations rather than growing the set.

Auth: set GOOGLE_APPLICATION_CREDENTIALS to a service-account JSON with the
Cloud Text-to-Speech API enabled (compose maps /data/google-credentials.json).
"""

import collections
import itertools
import random
import sys
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from threading import Lock

SPEAKING_RATES = [0.85, 0.95, 1.0, 1.1, 1.2]
PITCHES = [-4.0, -2.0, 0.0, 2.0, 4.0]
TEST_FRACTION = 0.1
# USD per 1M characters, as a RANGE, because the pool spans voice families
# priced an order of magnitude apart: WaveNet/Standard $4, Neural2/Polyglot
# $16, Chirp3-HD $30, Studio $160 (checked 2026-08-20). A single figure was
# quoted here before and it was Neural2's, which understates a Studio-heavy
# run by 10x. Each family also carries its OWN monthly free allowance — 1M
# characters for the premium ones, 4M for WaveNet/Standard — so a wake-word
# run of a few tens of thousands of characters is free in practice.
PRICE_MIN_PER_MCHAR = 4.0
PRICE_MAX_PER_MCHAR = 160.0


def log(msg: str) -> None:
    print(f"[google-tts] {msg}", flush=True)


def synthesize(phrases, n_samples, train_dir: Path, test_dir: Path,
               languages, include_standard=False, assume_yes=False) -> None:
    try:
        from google.api_core import exceptions as gexc
        from google.cloud import texttospeech
    except ImportError:
        sys.exit("google-cloud-texttospeech is not installed in this image")

    try:
        client = texttospeech.TextToSpeechClient()
    except Exception as e:
        sys.exit(
            f"could not create TTS client ({e}) — set GOOGLE_APPLICATION_CREDENTIALS "
            "to a service-account JSON (see oww_forge/README.md)"
        )

    languages = [l.strip() for l in languages if l.strip()]
    voices = []
    for v in client.list_voices().voices:
        if not any(code.startswith(tuple(languages)) for code in v.language_codes):
            continue
        if not include_standard and "Standard" in v.name:
            continue
        voices.append(v)
    if not voices:
        sys.exit(f"no voices matched languages {languages}")
    log(f"{len(voices)} voices across {languages}")

    n_chars = sum(len(p) for p in phrases) // len(phrases) * n_samples
    log(f"~{n_chars} characters. Google's free tier is 1M/month for the "
        f"premium families and 4M for WaveNet/Standard, each counted "
        f"separately, so this is normally free; beyond it the rate depends "
        f"on which family a clip drew "
        f"(${PRICE_MIN_PER_MCHAR:.0f}–${PRICE_MAX_PER_MCHAR:.0f} per 1M chars), "
        f"i.e. at most ${n_chars / 1e6 * PRICE_MAX_PER_MCHAR:.2f} if none of "
        f"it were free.")
    if not assume_yes:
        reply = input("continue? [y/N] ").strip().lower()
        if reply not in ("y", "yes"):
            sys.exit("aborted")

    train_dir.mkdir(parents=True, exist_ok=True)
    test_dir.mkdir(parents=True, exist_ok=True)

    combos = list(itertools.product(voices, SPEAKING_RATES, PITCHES))
    random.shuffle(combos)
    jobs = []
    for i, (voice, rate, pitch) in enumerate(itertools.islice(itertools.cycle(combos), n_samples)):
        phrase = phrases[i % len(phrases)]
        out_dir = test_dir if random.random() < TEST_FRACTION else train_dir
        dest = out_dir / f"google_{i:06d}_{voice.name}.wav"
        jobs.append((phrase, voice, rate, pitch, dest))

    # Voices that will never work, whatever we ask for. NOT the same as a
    # voice that failed once: a transient error used to land here too, and
    # since this is checked before anything else, one bad moment retired a
    # voice for the rest of the run. That is what cost 66 of 101 voices on
    # 2026-08-20 -- every Chirp and Chirp3-HD voice in the pool -- while the
    # reason was discarded by a bare `except Exception: continue`.
    bad_voices = {}          # name -> reason, for the log
    # What each voice actually accepted. Chirp rejects `pitch` outright ("This
    # voice does not support pitch parameters at this time"), and two thirds
    # of a modern en-GB/en-AU pool is Chirp -- so without this, a third of
    # every run's API calls are requests known in advance to be refused.
    # Removing them removes the load that provokes rate limiting in the first
    # place.
    voice_shape = {}         # name -> index of the request shape it accepts
    transient = collections.Counter()
    lock = Lock()

    # Permanent, in the sense that retrying changes nothing. Anything else --
    # ResourceExhausted, ServiceUnavailable, DeadlineExceeded, a dropped
    # connection -- is the run's own fault or the network's, and must not
    # retire a voice.
    permanent = tuple(
        getattr(gexc, n) for n in
        ("InvalidArgument", "PermissionDenied", "NotFound", "Unauthenticated")
        if hasattr(gexc, n)
    )

    def synth_one(job):
        phrase, voice, rate, pitch, dest = job
        if voice.name in bad_voices:
            return 0
        req = dict(
            input=texttospeech.SynthesisInput(text=phrase),
            voice=texttospeech.VoiceSelectionParams(
                language_code=voice.language_codes[0], name=voice.name
            ),
        )
        shapes = [
            dict(speaking_rate=rate, pitch=pitch),
            dict(speaking_rate=rate),
            dict(),
        ]
        # Start from the shape this voice is known to accept; only a voice we
        # have not met yet pays for the discovery.
        start = voice_shape.get(voice.name, 0)
        last = None
        for i in range(start, len(shapes)):
            try:
                resp = client.synthesize_speech(
                    **req,
                    audio_config=texttospeech.AudioConfig(
                        audio_encoding=texttospeech.AudioEncoding.LINEAR16,
                        sample_rate_hertz=16000,
                        **shapes[i],
                    ),
                )
                # LINEAR16 responses arrive with a WAV header — write as-is.
                dest.write_bytes(resp.audio_content)
                if i != start:
                    with lock:
                        voice_shape[voice.name] = i
                return 1
            except permanent as e:
                last = e
                continue          # try a simpler request
            except Exception as e:
                # Transient. Give this job up, but leave the voice alone.
                with lock:
                    transient[type(e).__name__] += 1
                return 0
        with lock:
            bad_voices[voice.name] = str(last)[:120] if last else "unknown"
        return 0

    done = 0
    with ThreadPoolExecutor(max_workers=8) as pool:
        for ok in pool.map(synth_one, jobs):
            done += ok
            if done and done % 200 == 0:
                log(f"…{done}/{n_samples}")

    if bad_voices:
        log(f"{len(bad_voices)} voices could not be used:")
        for name, why in sorted(bad_voices.items())[:5]:
            log(f"  {name}: {why}")
        if len(bad_voices) > 5:
            log(f"  …and {len(bad_voices) - 5} more")
    if transient:
        # Named separately from the above because the remedy is different:
        # these are worth re-running, and a run that hits many of them is
        # asking Google for more than it will give at this concurrency.
        total = sum(transient.values())
        log(f"{total} clips lost to transient errors "
            f"({', '.join(f'{k}×{v}' for k, v in transient.most_common(3))}) "
            f"— re-run to fill them in")
    log(f"wrote {done} clips → {train_dir.parent}")
    if done < n_samples * 0.5:
        log("WARNING: more than half the requests failed — check API quota/credentials")
