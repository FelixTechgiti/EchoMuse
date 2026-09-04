"""
Home Assistant can close this microphone and cannot open it.

Unmuting is a PHYSICAL act. Someone muted this device by pressing the button
on it, and the red LED under that button is a promise; handing the
microphone back over the network would let a mistaken automation, a shared
Home Assistant login or a compromised controller break that promise while
the LED still makes it, in a room where nobody touched anything.

The rule is enforced at three levels, and the guards below check each,
because any one of them alone would be a runtime check somebody can drop:

  1. The control message carries NO boolean. There is nothing on the wire
     that could ask for an unmute, so the refusal cannot be forgotten.
  2. The device's MuteOnly never toggles (device-side Go test).
  3. The controller offers no unmute closure, and the switch handler refuses
     `state=False` rather than folding it.

em_esphome and em_controller are not importable here, so these are shape
guards on the shipped source — the same approach the rest of this suite
takes for those two modules.
"""

import re
from pathlib import Path

CONTROLLER = Path(__file__).resolve().parents[1]
DEVICE     = CONTROLLER.parent / "device"

_DOCSTRING = re.compile(r'"""(?:.|\n)*?"""')
_SQ_BLOCK  = re.compile(r"'''(?:.|\n)*?'''")
_DQ_STR    = re.compile(r'f?"(?:[^"\\\n]|\\.)*"')
_SQ_STR    = re.compile(r"f?'(?:[^'\\\n]|\\.)*'")


def _strip_py_comments(src: str) -> str:
    src = _DOCSTRING.sub("", src)
    src = _SQ_BLOCK.sub("", src)
    return "\n".join(re.sub(r"#.*$", "", line) for line in src.splitlines())


def _code_only(src: str) -> str:
    """
    Comments, docstrings AND string literals.

    Stripping the first two is not enough here, and finding that out is the
    point. This tree has repeatedly caught guards matching the prose that
    explains the thing they forbid. This one matched something worse: the
    REFUSAL ITSELF. A `log.warning` reading "Unmute refused ..." is the code
    doing exactly what the guard demands, and searching raw source counted
    it as the violation.

    So a guard about what the code CAN DO must not read what the code SAYS.
    """
    src = _strip_py_comments(src)
    src = _DQ_STR.sub('""', src)
    src = _SQ_STR.sub("''", src)
    return src


def _strip_go_comments(src: str) -> str:
    src = re.sub(r"/\*.*?\*/", "", src, flags=re.S)
    return "\n".join(re.sub(r"//.*$", "", line) for line in src.splitlines())


def test_the_mute_message_carries_no_boolean():
    """
    The outermost guard, and the only one that cannot be forgotten: a
    message that cannot express "unmute" needs no check to refuse one.

    A `{"muted": bool}` field would read more naturally and would move the
    rule into a runtime branch — one a later handler, a retry path or a
    well-meaning refactor can drop with nothing failing.
    """
    ctrl = _strip_py_comments((CONTROLLER / "em_controller.py").read_text())
    sends = [l.strip() for l in ctrl.splitlines() if "mute_set" in l]
    assert sends, "the controller no longer sends mute_set at all"
    for line in sends:
        assert "muted" not in line and "False" not in line and "True" not in line, (
            f"the mute message must carry no state to set: {line}"
        )

    dev  = _strip_go_comments((DEVICE / "internal/client/control.go").read_text())
    case = dev[dev.index('case "mute_set"'):]
    case = case[:case.index("case ", 10)]
    assert "json.Unmarshal" not in case, (
        "the device must not decode a payload for mute_set — there is "
        "nothing in it, and decoding one invites adding a field"
    )


def test_the_controller_has_no_unmute_path():
    """
    No closure, no message, no handler. Searched over the whole controller
    rather than one function, because the point is that the capability is
    absent from the tree, not merely unused at one call site.
    """
    ctrl = _code_only((CONTROLLER / "em_controller.py").read_text())
    esp  = _code_only((CONTROLLER / "em_esphome.py").read_text())
    for name, src in (("em_controller.py", ctrl), ("em_esphome.py", esp)):
        for forbidden in ("unmute", "_clear_mute", "mute_clear"):
            assert forbidden not in src.lower(), (
                f"{name} can {forbidden!r} — the microphone is opened at "
                f"the device and nowhere else"
            )


def test_the_switch_refuses_off_and_reports_the_real_state():
    """
    An `off` command must echo the state back UNCHANGED, so HA's toggle
    snaps to where the microphone actually is. Leaving HA showing `off`
    over a live mute is the worse half of the bug: the entity would then
    disagree with the hardware, and the hardware is right.
    """
    esp  = (CONTROLLER / "em_esphome.py").read_text()
    body = esp[esp.index("if isinstance(msg, api_pb2.SwitchCommandRequest):"):]
    body = body[:body.index("if isinstance(msg, api_pb2.LightCommandRequest):")]
    code = _strip_py_comments(body)

    assert "if msg.state:" in code, "the handler must branch on the request"
    assert "_set_mute()" in code, "an on command must reach the device"
    off = code[code.index("else:"):]
    assert "_set_mute" not in off, "an off command must not reach the device"
    assert "self._mute_state_msg()" in code, (
        "the handler must answer with the device's real state, or HA is "
        "left showing a position the microphone never took"
    )
    assert "log.warning" in body, (
        "a refused control must say so — someone pressed this, and silence "
        "is the failure mode this codebase names most often"
    )


def test_the_switch_is_gated_on_the_capability():
    """
    Older firmware ignores unknown control messages silently, so an ungated
    switch would report success and mute nothing.
    """
    esp  = _strip_py_comments((CONTROLLER / "em_esphome.py").read_text())
    decl = esp.index("ListEntitiesSwitchResponse(")
    gate = esp.rindex("if self._mute_set_capable:", 0, decl)
    assert decl - gate < 300, "the switch must sit inside the capability gate"

    dev = _strip_go_comments((DEVICE / "internal/client/control.go").read_text())
    assert '"mute_set"' in dev[dev.index("func capabilities()"):], \
        "the firmware must announce mute_set, or the controller cannot know"


def test_the_device_is_the_authority_on_mute_state():
    """
    The button changes mute with no controller involved, so the entity
    mirrors the device's own reports rather than what HA last asked for. A
    push driven by the command would leave the entity wrong after every
    button press.
    """
    ctrl = _strip_py_comments((CONTROLLER / "em_controller.py").read_text())
    handler = ctrl[ctrl.index('elif msg_type == "mute_state":'):]
    handler = handler[:handler.index("elif msg_type ==", 10)]
    assert "esphome.update_device_mute(device_id, device.muted)" in handler, (
        "every mute_state report must reach the entity, including the ones "
        "a button press produced"
    )
