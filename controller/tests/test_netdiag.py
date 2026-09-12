"""
The inbound-reachability diagnostics, and the property that makes them safe.

Why this module exists at all: every plane in this system is dialled BY the
device, so nothing had ever tested the other direction. On 2026-09-12 both
streaming endpoints on a healthy, connected Echo were advertised correctly
and neither accepted a TCP connection from another host on the same subnet —
and ICMP was silent too. Spotify Connect and AirPlay both require the phone
to call the speaker, so that is the whole fault, and it had been rendering as
success for weeks.
"""

import os
import re

import em_netdiag


def test_every_check_has_a_label_and_a_command():
    assert em_netdiag.CHECKS
    for label, cmd in em_netdiag.CHECKS:
        assert label.strip() and cmd.strip()


def test_the_firewall_question_is_asked_first():
    # Whoever runs this is looking for one thing, and an INPUT policy of DROP
    # would end the investigation on the first line.
    assert "iptables" in em_netdiag.CHECKS[0][1]


def test_the_script_labels_every_section():
    # This lands in a log people read weeks later. An iptables dump and a
    # netstat dump are not distinguishable at a glance, and a reader who
    # cannot tell them apart will guess.
    s = em_netdiag.script()
    for label, _ in em_netdiag.CHECKS:
        assert f"=== {label} ===" in s


def test_no_check_can_abort_the_ones_after_it():
    # A device without iptables is an ordinary answer to this question, not
    # an error. Chained with && it would swallow every remaining check, and
    # the output would look like a device that told us nothing.
    s = em_netdiag.script()
    assert "&&" not in s
    for _, cmd in em_netdiag.CHECKS:
        assert "2>&1" in cmd or "2>/dev/null" in cmd, cmd


def test_the_checks_are_read_only():
    # This runs while somebody is already confused. A diagnostic that mutates
    # state destroys the evidence it was called to collect.
    forbidden = re.compile(
        r"\b(rm|mv|mkdir|kill|killall|stop|start|reboot|setprop|ifconfig\s+\w+\s+(up|down)"
        r"|ip\s+link\s+set|iptables\s+-[AIDFXPN]\b|echo\s+[^']*>)\b")
    for label, cmd in em_netdiag.CHECKS:
        assert not forbidden.search(cmd), f"{label!r} is not read-only: {cmd}"


def _api():
    here = os.path.dirname(os.path.abspath(__file__))
    with open(os.path.join(here, "..", "em_api.py"), encoding="utf-8") as f:
        return f.read()


def test_the_endpoint_exists_and_is_admin_only():
    api = _api()
    assert '/api/devices/{id}/net_diag' in api
    i = api.index("async def _post_device_net_diag")
    # It opens a root shell on someone's device; admin is not optional.
    assert "@auth.require_admin" in api[max(0, i - 200):i]


def test_an_empty_answer_is_not_reported_as_a_finding():
    # A shell that said nothing is not a device with nothing to say. Reading
    # the first as the second is the conflation this tree keeps correcting.
    api = _api()
    body = api[api.index("async def _post_device_net_diag"):]
    body = body[:body.index("async def _post_fetch_supervisor_log")]
    assert "says nothing about the device" in body


def test_the_scan_probes_the_advertised_port():
    # The guard for the gap itself: advertised is not reachable, and until
    # this existed nothing in the controller had ever opened a connection to
    # a device.
    api = _api()
    scan = api[api.index("async def _get_device_mdns_scan"):]
    assert "_tcp_reachable(" in scan, \
        "the scan no longer checks whether the advertised port accepts a connection"
    assert '"port_open"' in scan


def test_a_refused_connection_counts_as_reachable():
    # The question is whether anything is there to answer, not whether the
    # port is open. A refusal is a host that replied; a timeout is the thing
    # a phone experiences against a silent speaker.
    api = _api()
    probe = api[api.index("async def _tcp_reachable"):]
    probe = probe[:probe.index("async def _get_device_mdns_scan")]
    assert "ConnectionRefusedError" in probe
    assert "return True" in probe.split("ConnectionRefusedError")[1][:60]
