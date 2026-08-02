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

import inspect
import json
import re

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
    # Dashboard account name. Reported against a REAL bundle on 2026-08-02:
    # "Shell session opened by wil" is ordinary prose with no quotes, no URL
    # and no identifier shape, so every other rule here passed it through.
    "username": "wilbowes",
}

ACCOUNTS = {SECRETS["username"]: "admin", "wil": "admin", "sam": "readonly"}


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
        live_state={"G090LF1180130NJG": {
            "connected": True,
            "capabilities": ["mic", "ambient_light"],
            # A whole stats dict — the path that actually leaked in a REAL
            # bundle, because it was handed over unfiltered.
            "stats": S.redact_stats({
                "cpuPct": 27.5, "wifiRssi": -58, "ambientLux": 16,
                "wifiBssid": SECRETS["bssid"], "wifiSsid": SECRETS["ssid"],
            }),
        }},
        log_tail=[
            f"[TURN] trigger=wakeword outcome=ok text='{SECRETS['speech']}'",
            "play_media: 'http://10.10.1.81:8097/flow/abc/media.flac'",
            "Playback chain: decoder spawned 3ms, first audio 194ms",
            "RTT excursion: 1450ms (idle)",
            # Bare identifiers in prose: neither quoted nor a URL, so the
            # quote and URL rules both miss them. Found by auditing a real
            # bundle against the live database — the synthetic fixture had
            # no such line, which is exactly why it passed.
            f"Device connected: G090LF1180130NJG v=v2.9.13 at {SECRETS['ip']} caps=[mic]",
            f"associated bssid {SECRETS['bssid']} freq 5805",
            f"Shell session opened by {SECRETS['username']}",
            "Shell session closed by wil",
        ],
        accounts=ACCOUNTS,
        controller_stats={"cpu_pct_1m": 12.4, "rss_mb": 210.5, "db_mb": 38.2,
                          "data_free_mb": 51204.0, "uptime_s": 3600,
                          # Not allowlisted: a data directory carries the
                          # account name on a bare-metal install.
                          "data_dir": f"/home/{SECRETS['username']}/echomuse"},
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


def test_live_stats_are_allowlisted_not_passed_through():
    """
    Regression: live.stats was handed over as a whole dict and leaked
    wifiBssid/wifiSsid into a real bundle. Passing a nested structure through
    unfiltered defeats an allowlist just as surely as using a denylist.
    """
    out = S.redact_stats({"cpuPct": 27.5, "wifiRssi": -58,
                          "wifiBssid": "aa:bb:cc:dd:ee:ff",
                          "wifiSsid": "Some-Home-Network"})
    assert out == {"cpuPct": 27.5, "wifiRssi": -58}, \
        "stats must be projected onto the allowlist, not filtered"


def test_bare_identifiers_in_log_prose_are_stripped():
    """
    "Device connected: ... at 10.10.1.60" is neither quoted nor a URL, so the
    first two rules both miss it. Real logs are full of these.
    """
    out = "\n".join(S.sanitise_log([
        "Device connected: ABC v=v2.9.13 at 10.10.1.60 caps=[mic]",
        "associated bssid 24:5a:4c:83:7b:e5 freq 5805",
    ]))
    assert "10.10.1.60" not in out and "<ip>" in out
    assert "24:5a:4c:83:7b:e5" not in out and "<mac>" in out
    assert "v=v2.9.13" in out, "the diagnostic content must survive"


def test_config_redaction_catches_credential_shaped_keys():
    out = S.redact_config({"owwThreshold": 0.5, "wifiPsk": "x", "api_token": "y",
                           "somePassword": "z", "eqBands": [1, 2]})
    assert out["owwThreshold"] == 0.5
    assert out["eqBands"] == [1, 2]
    for k in ("wifiPsk", "api_token", "somePassword"):
        assert out[k] == "<redacted>", f"{k} should have been redacted"


def test_account_names_are_stripped_from_log_prose():
    """
    Regression, from a real bundle: "Shell session opened by wil" carries the
    dashboard account name in plain prose. Nothing about it is quoted, and it
    has no identifier shape to match, so it survived every rule here.
    """
    out = "\n".join(S.sanitise_log(
        ["Shell session opened by wil", "Login failed for wilbowes"],
        accounts=ACCOUNTS))
    assert "wil" not in out and "wilbowes" not in out
    assert out.count("<admin>") == 2, "a name becomes its role, not an alias"
    assert "Shell session opened by" in out, "the event must survive the name"


def test_longer_usernames_are_replaced_before_their_prefixes():
    """
    With users `wil` and `wilbowes`, replacing the short one first leaves
    `<user>bowes` — the leak, still legible, wearing a redaction marker.
    """
    out = "\n".join(S.sanitise_log(["opened by wilbowes"],
                                   accounts={"wil": "admin", "wilbowes": "admin"}))
    assert out == "opened by <admin>"


def test_username_redaction_does_not_eat_substrings():
    """
    Word boundaries: a user called `sam` must not turn `samples=120` into
    `<user>ples=120` and destroy the diagnostic.
    """
    out = "\n".join(S.sanitise_log(["stats samples=120 for sam"], accounts={"sam": "readonly"}))
    assert "samples=120" in out and out.endswith("<readonly>")


def test_no_usernames_is_not_an_error():
    """A fresh install with no users must not blow up the bundle."""
    assert S.sanitise_log(["nothing to redact"], accounts={}) == ["nothing to redact"]
    assert S.sanitise_log(["nothing to redact"]) == ["nothing to redact"]


def test_controller_stats_are_allowlisted():
    """
    System stats are not secret in themselves, but the tempting neighbours —
    data directory, hostname, cwd — carry an account name on a bare-metal
    install, which is the same leak in a different field.
    """
    b = _bundle()
    stats = b["controller"]["stats"]
    assert stats["cpu_pct_1m"] == 12.4 and stats["rss_mb"] == 210.5
    assert stats["db_mb"] == 38.2 and stats["uptime_s"] == 3600
    assert "data_dir" not in stats, "paths must never be reported"


def test_metric_fields_match_what_the_reader_returns():
    """
    The allowlist named the TABLE's columns (`cpu_sum`, `mem_used_sum`) while
    `db.get_device_metrics` resolves those sums into averages at read — so
    every device CPU and memory figure was silently dropped from every
    bundle, which is most of the reason to include metrics at all. An
    allowlist naming a key nothing produces fails silently and looks careful.
    """
    import em_db

    src = inspect.getsource(em_db.get_device_metrics)
    returned = set(re.findall(r'"(\w+)":\s', src))
    assert returned, "could not read the keys get_device_metrics returns"

    allow = set(S._METRIC_FIELDS) - {"device_id"}  # attached by the caller
    unknown = allow - returned
    assert not unknown, (
        f"allowlisted metric fields that get_device_metrics never returns: "
        f"{sorted(unknown)} — they will silently be dropped"
    )

    # The converse is not an error (wifi_bssid_last is excluded on purpose),
    # but the resource figures are the point of the section.
    for key in ("cpu_avg", "cpu_max", "mem_used_avg", "mem_total_mb",
                "storage_used_mb", "storage_total_mb"):
        assert key in allow, f"{key} is the reason metrics are in the bundle"


def test_metrics_carry_the_device_they_belong_to():
    """
    Metrics are pooled across the fleet into one flat list, so a row without
    a device_id cannot be attributed to anything — six devices' CPU and
    memory with no way to tell whose is whose.
    """
    b = _bundle()
    assert b["metrics"], "fixture should produce a metric row"
    for m in b["metrics"]:
        assert m.get("device_id"), "every metric row must name its device"


def test_the_role_is_published_not_the_person():
    """
    A single-operator install has one account, so `user-1` would be a
    one-to-one alias for a real person. `<admin>` says the thing worth
    knowing — who had the authority to do it — and says nothing about who.
    """
    out = "\n".join(S.sanitise_log(
        ["Shell session opened by wil", "config changed by sam"],
        accounts={"wil": "admin", "sam": "readonly"}))
    assert "<admin>" in out and "<readonly>" in out
    assert "wil" not in out and "sam" not in out


def test_a_junk_role_does_not_reach_the_published_file():
    """The role is a database column, so it is checked, not trusted."""
    out = "\n".join(S.sanitise_log(
        ["opened by bob"], accounts={"bob": "admin<script>alert(1)</script>"}))
    assert out == "opened by <user>"


class TestCpuWindows:
    """
    CPU over 1m/5m/1h. Fed explicit samples so the maths is tested without
    sleeping for an hour.
    """

    def _history(self, points):
        h = S.CpuHistory()
        for t, cpu in points:
            h.add(t, cpu)
        return h

    def test_windows_report_percent_of_one_core(self):
        # 30s of wall clock, 15s of CPU consumed = 50% of one core.
        h = self._history([(0.0, 0.0), (30.0, 15.0), (60.0, 30.0)])
        out = h.windows(60.0, 30.0)
        assert out["cpu_pct_1m"] == 50.0

    def test_over_one_hundred_percent_is_reported_not_clamped(self):
        """More than a core's worth is exactly what a runaway looks like."""
        h = self._history([(0.0, 0.0), (60.0, 150.0)])
        out = h.windows(60.0, 150.0)
        assert out["cpu_pct_1m"] == 250.0

    def test_a_window_without_enough_history_is_omitted(self):
        """
        Reporting 40 seconds of data as `cpu_pct_1h` is a wrong answer; a
        missing key is one the reader can see is missing.
        """
        h = self._history([(0.0, 0.0), (30.0, 5.0), (60.0, 10.0)])
        out = h.windows(60.0, 10.0)
        assert "cpu_pct_1m" in out
        assert "cpu_pct_5m" not in out and "cpu_pct_1h" not in out

    def test_recent_load_is_distinguishable_from_an_old_burst(self):
        """
        The whole reason for windows: an hour of idle after a busy hour must
        not read the same as busy now.
        """
        pts = [(float(t), 0.0) for t in range(0, 1800, 30)]          # idle
        pts += [(float(t), (t - 1800) * 0.9) for t in range(1800, 3660, 30)]  # busy
        h = self._history(pts)
        out = h.windows(3630.0, (3630 - 1800) * 0.9)
        assert out["cpu_pct_1m"] == 90.0, "recent load must show at 1m"
        assert out["cpu_pct_1h"] < out["cpu_pct_1m"], \
            "the hour includes the idle half and must read lower"

    def test_the_ring_stays_bounded(self):
        """A day of sampling must not accumulate a day of samples."""
        h = S.CpuHistory()
        for i in range(0, 86400, 30):
            h.add(float(i), i * 0.1)
        expected = S.CpuHistory.WINDOWS[-1][0] / S.CpuHistory.INTERVAL_S
        assert len(h._samples) <= expected + 3, "ring should hold ~1h of samples"
