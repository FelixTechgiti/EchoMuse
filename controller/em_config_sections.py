"""
Config sections — the unit of fleet-vs-device scoping.

A device used to be all-or-nothing: `use_global_config` was one boolean, so
you either inherited the entire fleet config or overrode every key of it.
That got unwieldy as the config grew — wanting a device-specific ring scene
meant forking its mic gain, wake threshold and EQ too, and they then stopped
tracking fleet changes forever.

Scoping is now per section. A device stores the SET of sections it overrides;
its effective config is the fleet config with the device's own values layered
over it for those sections only. Everything else keeps following the fleet.

This module is the single source of truth for which key belongs to which
section. `controller/static/dashboard.jsx` carries a mirror of SECTIONS for
rendering, and tests/test_config_sections.py fails if the two drift or if any
config key ends up belonging to no section.
"""

# Section id → (display label, config keys). Ids are stored in the DB, so
# they are API surface: renaming one needs a migration. Labels are display
# only and must match the dashboard Stage titles.
SECTIONS: dict[str, dict] = {
    "playback": {
        "label": "Playback",
        "keys": ["eqBands", "eqLoudness", "duckDb",
                 "limiterEnabled", "limiterThreshold", "limiterRelease",
                 "bassGuardEnabled", "bassGuardDb", "audioHoldoffMs"],
    },
    "wakeword": {
        "label": "Wake word",
        "keys": [
            "owwModel", "owwThreshold", "owwSpeexNs",
            "bargeInEnabled", "bargeInThreshold", "wakeArbitrationMs",
            "owwOnDevice",
        ],
    },
    "microphones": {
        "label": "Microphones",
        "keys": [
            "adcMicpga", "adcDigitalGain", "micGainDb",
            "beamformingEnabled", "beamAngle",
            "aecEnabled", "aecDelayMs", "aecTailMs", "aecRefSource", "nsAsr",
            "saveUtterances",
        ],
    },
    "ring": {
        "label": "Ring",
        "keys": [
            "ledScene", "ledListenColor", "ledThinkColor",
            "meterAttack", "meterDecay", "meterFloor",
            "meterGamma", "meterRef", "meterCurve",
        ],
    },
    "advanced": {
        "label": "Advanced",
        "keys": [
            "agcEnabled", "vadThreshold", "vadSpeechMs", "vadSilenceMs",
            # Already the button-turn section; these decide whether they happen.
            "buttonSingleTapEvent", "buttonMultiTapMs",
            # Fleet-level in practice: a per-device console password would be a
            # management problem with no upside. It sits in a section like
            # every other key because the partition has to stay total.
            "consolePassword", "consoleTimeoutMin",
        ],
    },
    "bluetooth": {
        "label": "Bluetooth",
        "keys": ["bleProxyEnabled"],
    },
    # Streaming protocols the DEVICE speaks for itself, with no controller
    # hop. Its own section rather than a line in playback: playback is about
    # how audio sounds once it arrives, and this is about where it comes
    # from — and a device overriding one has no reason to override the
    # other.
    "streaming": {
        "label": "Streaming",
        "keys": ["sendspinEnabled", "spotifyEnabled", "spotifyName",
                 "airplayEnabled", "airplayName", "airplayVolumeControl"],
    },
}

# Keys that live in the config dict but are NOT user-facing settings, and so
# belong to no section and are never fleet-inherited.
#
# startupVolume is persisted device STATE wearing a config key's clothes: the
# controller writes it automatically from every volume_state report, and the
# device re-applies it via SeedVolume on the first config push per run. That
# round trip is what makes volume survive a reboot, so the key has to stay —
# but it is not something you set, and it was never meaningfully editable
# (SeedVolume ignores later pushes, so the old dashboard slider did nothing
# until the device restarted and was overwritten by any real volume change).
# Fleet default 85 still does one real job: the starting point for a device
# that has never reported.
# idleRing/idleRingBrightness are the same shape one step further along: the
# ring's resting colour is set from Home Assistant's light entity and written
# back here so it survives a restart, exactly as a volume change is. Not
# fleet-inherited, for the reason volume is not — "every Echo in the house
# turns the same colour when one of them is told to" is not a fleet default,
# it is a bug — and deliberately not on a dashboard Stage: two controls owning
# one value is how they drift.
STATE_KEYS: frozenset[str] = frozenset({
    "startupVolume", "idleRing", "idleRingBrightness", "idleEffect",
})

# Keys that ARE user-facing settings but can never be inherited from the
# fleet, because their whole job is to tell two devices apart.
#
# A Spotify or AirPlay name is what the Echo calls itself in somebody's app.
# Set one at fleet level and every Echo in the house announces the SAME name,
# which does not make the picker ambiguous so much as useless — and it is not
# an edge case, it is what happens the moment a second device exists. The
# section they live in (`streaming`) is still the right home for the toggles
# beside them: whether an Echo runs Spotify Connect at all is exactly the kind
# of thing a fleet decides together.
#
# Distinct from STATE_KEYS on purpose, though the merge treats them the same.
# STATE_KEYS are not settings at all — they are hardware state the device
# writes back, and no dashboard control owns them. These are settings somebody
# types, and the dashboard must show them as device-scoped rather than
# silently ignoring a fleet value that was entered in good faith.
DEVICE_ONLY_KEYS: frozenset[str] = frozenset({"spotifyName", "airplayName"})

SECTION_IDS: tuple[str, ...] = tuple(SECTIONS)


def keys_for(section_ids) -> set[str]:
    """Every config key belonging to the given sections. Unknown ids ignored."""
    out: set[str] = set()
    for sid in section_ids or ():
        section = SECTIONS.get(sid)
        if section:
            out.update(section["keys"])
    return out


def normalise(section_ids) -> list[str]:
    """
    Filter to known section ids, in canonical SECTIONS order.

    Stored values survive a controller downgrade/upgrade, so an id we no
    longer recognise must be dropped rather than propagated — otherwise a
    stale id would silently widen or narrow an override.
    """
    if not section_ids:
        return []
    given = set(section_ids)
    return [sid for sid in SECTION_IDS if sid in given]


def merge(global_cfg: dict, device_cfg: dict, section_ids) -> dict:
    """
    Effective config: fleet, with the device's values layered over it for the
    sections it overrides.

    STATE_KEYS always come from the device when present, whatever the section
    scoping says — they are that device's own hardware state, and inheriting
    one from the fleet would mean a device coming back at another room's
    volume.
    """
    effective = dict(global_cfg)
    overridden = keys_for(section_ids)
    for key in overridden:
        if key in device_cfg:
            effective[key] = device_cfg[key]
    for key in STATE_KEYS:
        if key in device_cfg:
            effective[key] = device_cfg[key]
    # Same rule, different reason — see DEVICE_ONLY_KEYS. Taking the device's
    # value when it has one is only half of it: when it has NONE, the fleet's
    # must not leak in either, or every unnamed Echo answers to one name.
    for key in DEVICE_ONLY_KEYS:
        effective[key] = device_cfg.get(key, "")
    return effective


def summarise(section_ids) -> str:
    """Dashboard-facing one-liner for the Status panel's Config row."""
    n = len(normalise(section_ids))
    if n == 0:
        return "Fleet"
    return f"Local override ({n} of {len(SECTION_IDS)})"
