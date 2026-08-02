"""
Support-bundle privacy contract.

A bundle exists to be attached to a public GitHub issue, so a leak here is
not recoverable — it is already on the internet by the time anyone notices.
These tests assert the contract rather than the implementation, and are
written as "this value must not appear ANYWHERE in the serialised output"
rather than "this field is absent", because a leak via an unexpected path
(a log line, a config value, a nested dict) is exactly the one nobody
predicts.
"""

import json

import em_support as S


class Row(dict):
    """Stand-in for sqlite3.Row — supports .keys() and subscripting."""
    def keys(self):
        return list(super().keys())


SECRETS = {
    "token": "tok_9f3aSECRETdeviceauth",
    "psk": "psk_SECRETnoisekey==",
    "pwhash": "$2b$12$SECRETbcrypthash",
    "session": "sess_SECRETlogin",
    "speech": "turn off the bedroom light",
    "label": "Bedroom - Sam",
    "ssid": "Bowes-Family-5G",
    "bssid": "d6:9b:22:11:aa:bb",
    "ip": "10.10.1.104",
}


def _bundle():
    device = Row({
        "device_id": "G090LF1180130NJG",
        "label": SECRETS["label"],
        "approved": 1,
        "firmware_ver": "v2.9.13",
        "ip": SECRETS["ip"],
        "token": SECRETS["token"],
        "esphome_noise_psk": SECRETS["psk"],
    })
    turn = Row({
        "id": 1, "device_id": "G090LF1180130NJG", "outcome": "ok",
        "wake_score": 0.98, "total_ms": 1200,
        "stt_text": SECRETS["speech"],
    })
    metric = Row({
        "device_id": "G090LF1180130NJG", "hour_ts": 0, "rtt_samples": 100,
        "rtt_excursions": 20, "wifi_bssid_last": SECRETS["bssid"],
        "wifi_ssid": SECRETS["ssid"],
    })
    return S.build(
        controller_version="v2.12.1",
        devices=[device],
        fleet_config={"owwThreshold": 0.5, "wifiPsk": SECRETS["psk"]},
        schema_version=16,
        turns=[turn],
        metrics=[metric],
        counters=[],
        device_configs={"G090LF1180130NJG": {"owwModel": "hey_mycroft_v0.1",
                                             "some_token": SECRETS["token"]}},
        live_state={"G090LF1180130NJG": {"connected": True,
                                         "capabilities": ["mic", "ambient_light"]}},
        log_tail=[
            f"[TURN] trigger=wakeword outcome=ok text='{SECRETS['speech']}'",
            "play_media: 'http://10.10.1.81:8097/flow/abc/media.flac'",
            "Playback chain: decoder spawned 3ms, first audio 194ms",
            "RTT excursion: 1450ms (idle)",
        ],
    )


def test_no_secret_value_appears_anywhere_in_the_bundle():
    """
    The load-bearing test. Serialise the whole thing and search it — a field
    check would miss a leak through a log line or a nested config value,
    which is the leak nobody anticipates.
    """
    text = S.to_json(_bundle())
    for name, value in SECRETS.items():
        assert value not in text, (
            f"{name} leaked into the support bundle: {value!r}"
        )


def test_speech_is_excluded_with_no_opt_in():
    """
    No flag, deliberately. A flag is a thing people tick, and this one cannot
    be untickled once the bundle is on a public issue.
    """
    import inspect
    sig = inspect.signature(S.build)
    for param in sig.parameters:
        assert "transcript" not in param.lower(), (
            f"build() grew a {param!r} parameter — speech must have no opt-in"
        )
    assert "stt_text" not in S._TURN_FIELDS


def test_labels_are_replaced_with_positional_pseudonyms():
    """Device labels are user-authored and routinely contain names."""
    b = _bundle()
    d = b["devices"][0]
    assert d["name"] == "device-1"
    assert "label" not in d


def test_the_useful_diagnostics_survive():
    """
    Privacy that removes the diagnostic value is not a win — the bundle has
    to still answer the questions it exists for.
    """
    b = _bundle()
    d = b["devices"][0]
    assert d["device_id"] == "G090LF1180130NJG", "serials correlate everything"
    assert d["firmware_ver"] == "v2.9.13"
    assert d["live"]["capabilities"] == ["mic", "ambient_light"], \
        "capabilities decide which HA entities exist — the #62 question"
    assert d["config"]["owwModel"] == "hey_mycroft_v0.1"
    assert b["turns"][0]["outcome"] == "ok"
    assert b["turns"][0]["wake_score"] == 0.98
    assert b["metrics"][0]["rtt_excursions"] == 20
    assert b["controller"]["version"] == "v2.12.1"
    assert b["controller"]["schema_version"] == 16

    log = "\n".join(b["controller_log_tail"])
    assert "Playback chain" in log and "194ms" in log, \
        "timings are the point of shipping logs at all"
    assert "RTT excursion: 1450ms" in log


def test_transcript_bearing_log_lines_are_dropped_whole():
    b = _bundle()
    log = "\n".join(b["controller_log_tail"])
    assert "[TURN]" not in log, \
        "turn traces quote transcripts verbatim and must not be sanitised in place"


def test_config_redaction_catches_credential_shaped_keys():
    out = S.redact_config({"owwThreshold": 0.5, "wifiPsk": "x", "api_token": "y",
                           "somePassword": "z", "eqBands": [1, 2]})
    assert out["owwThreshold"] == 0.5
    assert out["eqBands"] == [1, 2]
    for k in ("wifiPsk", "api_token", "somePassword"):
        assert out[k] == "<redacted>", f"{k} should have been redacted"
