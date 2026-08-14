#!/usr/bin/env python3
"""
sync_channels.py — generate the Early Access add-on from the GA one.

Home Assistant has no notion of a release channel: one add-on is one
version. A channel is therefore a SECOND ADD-ON with its own slug, which
users opt into by installing it — the same shape ESPHome uses (esphome /
esphome-beta / esphome-dev).

That means two config.yaml files describing the same program, and keeping
them in step by hand is the failure this project has already paid for:
config.yaml pinning a version whose image predated the add-on shipped a
controller with no ingress support, presenting as two unrelated faults
(#160). Two files multiply that by every option, every schema entry and
every permission.

So EA is GENERATED, never edited. Everything except the add-on's identity
is copied verbatim, and tests/test_channels.py fails if the committed copy
does not match what this produces — the generator and the guard are the
same rule stated twice, so drift is a red test rather than a support
thread.

Deliberately NOT generated: `version:`. Channels advance independently —
EA pinning the GA version would defeat the entire point — so it is
preserved from the existing file when there is one, and seeded from GA on
first creation.

Usage, from controller/:
    tools/sync_channels.py            # write controller-ea/
    tools/sync_channels.py --check    # exit 1 if the tree is out of date
"""

from __future__ import annotations

import re
import shutil
import sys
from pathlib import Path

CONTROLLER = Path(__file__).resolve().parents[1]
REPO = CONTROLLER.parent

# Files a pull-only add-on needs. EA carries no build context at all — it
# pulls the same published image as GA, differing only in which tag — so
# there is no Dockerfile and no source here to fall out of step.
PRESENTATION = ("translations/en.yaml", "icon.png", "logo.png")


class Channel:
    def __init__(self, dirname: str, slug: str, name: str, panel_title: str,
                 blurb: str):
        self.dirname = dirname
        self.slug = slug
        self.name = name
        self.panel_title = panel_title
        self.blurb = blurb

    @property
    def path(self) -> Path:
        return REPO / self.dirname


EA = Channel(
    dirname="controller-ea",
    slug="controller-ea",
    name="EchoMuse (Early Access)",
    panel_title="EchoMuse EA",
    blurb=(
        "Early Access build of the EchoMuse controller — the next release, "
        "before it is general. Install this INSTEAD of the stable add-on, "
        "not alongside it: both use host networking and the same ports. "
        "Add-ons do not share storage, so switching channels starts with an "
        "empty database and a new certificate authority, and existing "
        "devices will not connect until the stable add-on's /data is copied "
        "across."
    ),
)

# Identity lines rewritten per channel. Everything else is copied byte for
# byte, which is the point: an option, a schema entry or a permission added
# to GA reaches EA without anyone remembering to do it.
def _render(ga_config: str, ch: Channel, version: str) -> str:
    out = ga_config

    header = (
        "# GENERATED FILE — DO NOT EDIT.\n"
        "#\n"
        "# Produced from controller/config.yaml by controller/tools/\n"
        "# sync_channels.py, and checked by tests/test_channels.py. Edit the\n"
        "# GA config and re-run the generator; an edit here is reverted by\n"
        "# the next sync and fails CI in the meantime.\n"
        "#\n"
        "# `version:` is the exception — channels advance independently, so\n"
        "# it is preserved across syncs and is the one field a release moves.\n"
        "#\n"
        "# Comments below are inherited verbatim from the GA config and\n"
        "# describe it: this directory has no Dockerfile and never builds,\n"
        "# it only pulls the tag named by `version:`.\n"
        "#\n"
    )

    out = re.sub(r'^name:.*$',        f'name: "{ch.name}"',        out, count=1, flags=re.M)
    out = re.sub(r'^slug:.*$',        f'slug: "{ch.slug}"',        out, count=1, flags=re.M)
    out = re.sub(r'^panel_title:.*$', f'panel_title: {ch.panel_title}', out, count=1, flags=re.M)
    out = re.sub(r'^version:.*$',     f'version: "{version}"',     out, count=1, flags=re.M)

    # description: is a single long quoted scalar in the GA file.
    out = re.sub(r'^description:.*$', f'description: "{ch.blurb}"', out, count=1, flags=re.M)

    return header + out


def _current_version(path: Path, fallback: str) -> str:
    if not path.is_file():
        return fallback
    m = re.search(r'^version:\s*"(.*)"\s*$', path.read_text(encoding="utf-8"), re.M)
    return m.group(1) if m else fallback


def generate(ch: Channel) -> dict[str, str]:
    """Return {relative path: content} for the channel, without writing."""
    ga = (CONTROLLER / "config.yaml").read_text(encoding="utf-8")
    ga_version = _current_version(CONTROLLER / "config.yaml", "0.0.0")
    version = _current_version(ch.path / "config.yaml", ga_version)
    return {"config.yaml": _render(ga, ch, version)}


def write(ch: Channel) -> None:
    ch.path.mkdir(parents=True, exist_ok=True)
    for rel, content in generate(ch).items():
        (ch.path / rel).write_text(content, encoding="utf-8")
    for rel in PRESENTATION:
        src = CONTROLLER / rel
        if not src.is_file():
            continue
        dst = ch.path / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(src, dst)


def check(ch: Channel) -> list[str]:
    """Return a list of human-readable drift descriptions ([] if in step)."""
    problems: list[str] = []
    for rel, content in generate(ch).items():
        path = ch.path / rel
        if not path.is_file():
            problems.append(f"{ch.dirname}/{rel} is missing")
        elif path.read_text(encoding="utf-8") != content:
            problems.append(f"{ch.dirname}/{rel} differs from the generated form")
    for rel in PRESENTATION:
        src = CONTROLLER / rel
        if not src.is_file():
            continue
        dst = ch.path / rel
        if not dst.is_file():
            problems.append(f"{ch.dirname}/{rel} is missing")
        elif src.read_bytes() != dst.read_bytes():
            problems.append(f"{ch.dirname}/{rel} differs from controller/{rel}")
    return problems


def main(argv: list[str]) -> int:
    channels = [EA]
    if "--check" in argv:
        problems = [p for ch in channels for p in check(ch)]
        if problems:
            print("Channel add-ons are out of date:", file=sys.stderr)
            for p in problems:
                print(f"  - {p}", file=sys.stderr)
            print("\nRun: controller/tools/sync_channels.py", file=sys.stderr)
            return 1
        print("Channel add-ons are in step with controller/")
        return 0

    for ch in channels:
        write(ch)
        print(f"wrote {ch.dirname}/ "
              f"(version {_current_version(ch.path / 'config.yaml', '?')})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
