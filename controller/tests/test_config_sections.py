"""
Config section scoping — the fleet-vs-device unit.

The load-bearing test here is test_every_config_key_belongs_somewhere: a key
in DEFAULT_DEVICE_CONFIG that belongs to no section can never be overridden
per device and never appears in the dashboard, and nothing else would notice.
"""
import json
import re
from pathlib import Path

import pytest

import em_db
import em_config_sections as cs


DASHBOARD = Path(__file__).resolve().parents[1] / "static" / "dashboard.jsx"


# ─── The partition must stay total ───────────────────────────────────────────

def test_every_config_key_belongs_somewhere():
    """
    Every DEFAULT_DEVICE_CONFIG key is either in exactly one section or
    explicitly declared state. Adding a config key without assigning it a
    section makes it silently un-overridable — this is the guard for that.
    """
    mapped = set()
    for sid, section in cs.SECTIONS.items():
        for key in section["keys"]:
            assert key not in mapped, f"{key} appears in more than one section"
            mapped.add(key)

    defaults = set(em_db.DEFAULT_DEVICE_CONFIG)
    orphans = defaults - mapped - cs.STATE_KEYS
    assert not orphans, (
        f"config key(s) belong to no section and are not declared state: "
        f"{sorted(orphans)} — add them to cs.SECTIONS"
    )
    strays = mapped - defaults
    assert not strays, (
        f"section(s) reference key(s) that are not real config: {sorted(strays)}"
    )


def test_state_keys_are_not_in_any_section():
    mapped = {k for s in cs.SECTIONS.values() for k in s["keys"]}
    assert not (cs.STATE_KEYS & mapped), (
        "a STATE_KEY must not also be section-scoped — it is never "
        "fleet-inherited, so offering it as an overridable setting is a lie"
    )


def test_dashboard_section_map_matches_python():
    """
    The dashboard carries its own copy of the section->keys map for
    rendering. If the two drift, a control ends up under a toggle that does
    not govern it — visibly fine, silently wrong. Python is canonical.
    """
    src = DASHBOARD.read_text()
    m = re.search(r"const CONFIG_SECTIONS\s*=\s*(\{.*?\n\});", src, re.S)
    assert m, "CONFIG_SECTIONS not found in dashboard.jsx"
    # The literal is JSON-compatible by construction (see the comment above
    # it in dashboard.jsx) so it can be compared rather than eyeballed.
    js = json.loads(m.group(1))
    assert set(js) == set(cs.SECTIONS), (
        f"section ids differ: dashboard={sorted(js)} python={sorted(cs.SECTIONS)}"
    )
    for sid, keys in js.items():
        assert sorted(keys) == sorted(cs.SECTIONS[sid]["keys"]), (
            f"section '{sid}' keys differ between dashboard and Python"
        )


# ─── Resolution ──────────────────────────────────────────────────────────────

def _glob():
    return {"owwThreshold": 0.5, "ledScene": "standard", "micGainDb": 24,
            "agcEnabled": True, "bleProxyEnabled": False, "eqLoudness": False}


def test_no_sections_is_pure_fleet():
    dev = {"owwThreshold": 0.9, "ledScene": "pride"}
    out = cs.merge(_glob(), dev, [])
    assert out["owwThreshold"] == 0.5
    assert out["ledScene"] == "standard"


def test_only_the_overridden_section_wins():
    dev = {"owwThreshold": 0.9, "ledScene": "pride", "micGainDb": 40}
    out = cs.merge(_glob(), dev, ["ring"])
    assert out["ledScene"] == "pride"       # overridden section
    assert out["owwThreshold"] == 0.5       # fleet
    assert out["micGainDb"] == 24           # fleet


def test_all_sections_reproduces_the_old_full_override():
    dev = {"owwThreshold": 0.9, "ledScene": "pride", "micGainDb": 40}
    out = cs.merge(_glob(), dev, list(cs.SECTION_IDS))
    for k, v in dev.items():
        assert out[k] == v


def test_state_keys_always_come_from_the_device():
    """
    startupVolume is this device's hardware state. Inheriting it from the
    fleet would bring a device back at another room's volume.
    """
    glob = {**_glob(), "startupVolume": 85}
    out = cs.merge(glob, {"startupVolume": 40}, [])
    assert out["startupVolume"] == 40, "state key was clobbered by the fleet"


def test_unknown_section_ids_are_dropped():
    assert cs.normalise(["ring", "nonsense", "wakeword"]) == ["wakeword", "ring"]
    assert cs.normalise(None) == []


def test_normalise_is_canonically_ordered():
    """Stored order must not depend on click order, or diffs churn."""
    assert cs.normalise(["bluetooth", "playback"]) == ["playback", "bluetooth"]


def test_summarise_reads_naturally():
    # Counted from SECTION_IDS rather than written out: adding a section is
    # a normal thing to do, and a test that has to be edited alongside it
    # gets edited without being read.
    n = len(cs.SECTION_IDS)
    assert cs.summarise([]) == "Fleet"
    assert cs.summarise(["ring"]).startswith("Local override")
    assert f"1 of {n}" in cs.summarise(["ring"])
    assert f"{n} of {n}" in cs.summarise(list(cs.SECTION_IDS))


# ─── Migration equivalence ───────────────────────────────────────────────────

@pytest.mark.parametrize("use_global", [1, 0])
def test_v8_backfill_is_lossless(tmp_path, use_global):
    """
    The v8 migration must leave every device's effective config unchanged:
    inherit-everything -> no sections, override-everything -> all sections.
    """
    db_path = tmp_path / "t.db"
    em_db.init(str(db_path))
    em_db.register_new_device("dev1", "10.0.0.9", "vtest")
    with em_db._tx() as conn:
        conn.execute(
            "UPDATE devices SET use_global_config = ?, config = ? WHERE device_id = 'dev1'",
            (use_global, json.dumps({"ledScene": "pride", "micGainDb": 40})),
        )
        conn.execute(
            "UPDATE devices SET config_sections = ? WHERE device_id = 'dev1'",
            (json.dumps([] if use_global else list(cs.SECTION_IDS)),),
        )
    expected = 0 if use_global else len(cs.SECTION_IDS)
    assert len(em_db.get_device_config_sections("dev1")) == expected
    eff = em_db.get_effective_device_config("dev1")
    if use_global:
        assert eff["ledScene"] == em_db.DEFAULT_DEVICE_CONFIG["ledScene"]
    else:
        assert eff["ledScene"] == "pride"
        assert eff["micGainDb"] == 40


def test_reverting_a_section_discards_its_values(tmp_path):
    """
    Revert discards rather than remembers, so a section that follows the
    fleet holds no shadow values waiting to reappear months later.
    """
    em_db.init(str(tmp_path / "t.db"))
    em_db.register_new_device("dev1", "10.0.0.9", "vtest")
    em_db.set_device_config_sections("dev1", ["ring", "microphones"])
    em_db.set_device_config("dev1", {"ledScene": "pride", "micGainDb": 40})

    em_db.set_device_config_sections("dev1", ["microphones"])
    stored = em_db.get_device_config("dev1")
    assert "ledScene" not in stored, "reverted section left a shadow value"
    assert stored["micGainDb"] == 40, "still-overridden section lost its value"

    # And re-overriding starts from the fleet, not the discarded value.
    em_db.set_device_config_sections("dev1", ["ring", "microphones"])
    eff = em_db.get_effective_device_config("dev1")
    assert eff["ledScene"] == em_db.DEFAULT_DEVICE_CONFIG["ledScene"]


def test_state_key_survives_full_revert(tmp_path):
    """
    Reverting every section must not wipe startupVolume — that would make a
    device come back at the fleet's volume after any scoping change.
    """
    em_db.init(str(tmp_path / "t.db"))
    em_db.register_new_device("dev1", "10.0.0.9", "vtest")
    em_db.set_device_config_sections("dev1", list(cs.SECTION_IDS))
    em_db.set_device_config("dev1", {"ledScene": "pride", "startupVolume": 40})
    em_db.set_device_config_sections("dev1", [])
    assert em_db.get_effective_device_config("dev1")["startupVolume"] == 40


def test_v11_prunes_out_of_scope_values_from_migrated_rows(tmp_path, monkeypatch):
    """
    v8 backfilled config_sections but left the config column alone, so a
    fully-inheriting device still stored a value for every key. The device
    was unaffected (merge only reads in-scope keys) but the dashboard merged
    the stored dict over the fleet config and showed stale settings under
    sections that claimed to be following the fleet.
    """
    db_path = tmp_path / "t.db"
    em_db.init(str(db_path))
    em_db.register_new_device("dev1", "10.0.0.9", "vtest")
    # Simulate a v8-migrated row: no sections, but a full stored config.
    with em_db._tx() as conn:
        conn.execute(
            "UPDATE devices SET config_sections = '[]', config = ? WHERE device_id = 'dev1'",
            (json.dumps({"ledScene": "malevolent", "owwModel": "hey_mycroft_v0.1",
                         "micGainDb": 40, "startupVolume": 42}),),
        )
        conn.execute("UPDATE system_config SET value = '10' WHERE key = 'schema_version'")

    # Replay v11 ONLY. Rewinding schema_version and re-running _migrate would
    # otherwise re-apply every migration appended after v11 to a DB that has
    # already had them (v12's ALTER fails with "duplicate column"), so this
    # test would break on each new migration rather than on a real v11
    # regression. Migrations are append-only, so the prefix is stable.
    monkeypatch.setattr(em_db, "MIGRATIONS", em_db.MIGRATIONS[:11])
    em_db._migrate(em_db._conn)

    stored = em_db.get_device_config("dev1")
    assert "ledScene" not in stored, "out-of-scope value survived the prune"
    assert "owwModel" not in stored
    assert "micGainDb" not in stored
    # State keys are never section-scoped and must survive.
    assert stored["startupVolume"] == 42


def test_config_form_does_not_reference_a_device_it_never_receives():
    """
    DeviceConfigForm takes config, not a device — and is also rendered for the
    FLEET view, where no device exists at all. Referencing `device.something`
    inside it throws during render, which blank-screens the whole Config tab
    with no server-side symptom: the APIs all return 200 and the logs are clean.

    That happened on 2026-07-30 (`device.owwShadowCapable` used to gate a
    control). Anything a control needs about the device must arrive as an
    explicit prop, so the fleet view can supply a sensible default.
    """
    import re
    from pathlib import Path

    src = (Path(__file__).resolve().parent.parent / "static" / "dashboard.jsx").read_text()
    lines = src.split("\n")
    start = next(i for i, l in enumerate(lines) if l.startswith("function DeviceConfigForm("))
    end = next(i for i, l in enumerate(lines[start + 1:], start + 1) if l.startswith("function "))

    offenders = []
    for i in range(start, end):
        line = lines[i]
        stripped = line.strip()
        # Skip comments — the explanation of this very bug mentions `device.`.
        if stripped.startswith("//") or stripped.startswith("*"):
            continue
        code = re.sub(r"//.*$", "", line)
        if re.search(r"\bdevice\.[A-Za-z_]", code):
            offenders.append(f"line {i + 1}: {stripped[:100]}")

    assert not offenders, (
        "DeviceConfigForm references `device`, which it does not receive as a prop "
        "(and which does not exist in the fleet-config view). Pass what the control "
        "needs explicitly.\n  " + "\n  ".join(offenders)
    )


def test_the_echo_reference_override_is_scoped_and_offered():
    """
    aecRefSource pins the AEC far-end reference to the hardware loopback or
    the software tap, or leaves it detecting.

    It is config rather than a device env var for one reason: pinning it
    otherwise means editing start_server.sh on the device and restarting the
    server, which is how the hardware-vs-software comparison stayed
    unmeasured for three sessions. A measurement that expensive does not get
    made.

    Three places or it does not work: the default (so it is pushed at all),
    the section map (so it is scoped with the rest of the mic settings), and
    the dashboard (so a person can change it).
    """
    assert em_db.DEFAULT_DEVICE_CONFIG.get("aecRefSource") == "auto", \
        "must default to detecting, never to a pin — a pinned default would " \
        "disable the hardware reference on every device at once"

    assert "aecRefSource" in cs.SECTIONS["microphones"]["keys"], \
        "aecRefSource must be scoped with the other mic settings"

    jsx = DASHBOARD.read_text()
    assert "aecRefSource" in jsx, "the dashboard must offer the control"


# ── Endpoint names can never come from the fleet ─────────────────────────────

def test_endpoint_names_are_device_only():
    """
    A Spotify or AirPlay name is what the Echo calls itself in somebody's app.
    Inherited from the fleet, every Echo in the house announces the SAME name
    — which is not an ambiguous picker, it is a useless one. Not an edge case
    either: it is what happens the moment a second device exists.
    """
    assert "spotifyName" in cs.DEVICE_ONLY_KEYS
    assert "airplayName" in cs.DEVICE_ONLY_KEYS


def test_a_fleet_name_never_reaches_a_device_that_has_none():
    """
    Taking the device's value when it HAS one is only half the rule. The half
    that bites is the other one: a device with no name of its own must not
    fall back to the fleet's, or every unnamed Echo answers to one name.
    """
    fleet = {"spotifyName": "Kitchen", "airplayName": "Kitchen",
             "spotifyEnabled": True}
    eff = cs.merge(fleet, {}, [])
    assert eff["spotifyName"] == ""
    assert eff["airplayName"] == ""
    # The toggle beside it is still a fleet decision and must still inherit.
    assert eff["spotifyEnabled"] is True


def test_a_device_name_wins_even_without_the_section_override():
    """
    The device's own name applies whether or not it overrides `streaming` —
    the same unconditional treatment STATE_KEYS get, for a different reason.
    """
    fleet = {"spotifyName": "Kitchen"}
    eff = cs.merge(fleet, {"spotifyName": "Studio"}, [])
    assert eff["spotifyName"] == "Studio"


def test_the_toggles_beside_the_names_are_still_fleet_scoped():
    """
    Whether an Echo runs Spotify Connect at all is exactly the kind of thing a
    fleet decides together — only the NAME is an identity. Widening this to
    the whole streaming section would take the toggles with it.
    """
    for key in ("spotifyEnabled", "airplayEnabled", "sendspinEnabled"):
        assert key not in cs.DEVICE_ONLY_KEYS


def test_the_dashboard_mirror_of_device_only_keys_matches():
    """
    The dashboard carries its own copy so a write can be gated in the browser.
    Drift is silent in the worst direction: a key the browser thinks is
    fleet-scoped becomes uneditable on a device whose section follows the
    fleet, which is exactly the state a second device arrives in.
    """
    src = DASHBOARD.read_text(encoding="utf-8")
    m = re.search(r"const DEVICE_ONLY_KEYS = new Set\(\[(.*?)\]\)", src, re.S)
    assert m, "dashboard.jsx no longer declares DEVICE_ONLY_KEYS"
    mirrored = set(re.findall(r"'([^']+)'", m.group(1)))
    assert mirrored == set(cs.DEVICE_ONLY_KEYS), (
        f"dashboard mirror {sorted(mirrored)} != "
        f"{sorted(cs.DEVICE_ONLY_KEYS)}")
