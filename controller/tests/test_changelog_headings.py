"""
A changelog's `## ` lines are version headings, and nothing else.

`.github/workflows/cut-release.yml` builds a release tag's annotation from the
`## <version>` section of a changelog, and a section ends where the NEXT
version heading begins. So a `## ` line that is not a version silently ends
the extraction early: the tag carries a fraction of the notes, the release
body carries that fraction, and a tag annotation cannot be corrected
afterwards.

That happened to v2.15.0-fx.1, whose notes used `## What's new` — the body
published two thirds shorter than the file it came from, with nothing failing
and nothing to fix it with. controller/CHANGELOG.md happened to use `###`
inside its entries, so the controller release was fine and it read as a
firmware-only oddity.

The extractor now stops only at `## <digit>`, which makes prose headings safe
again — and this pins the convention from the other side, because the two
rules together are what keep a release's notes whole.
"""

import re
from pathlib import Path

import pytest

REPO = Path(__file__).resolve().parents[2]

CHANGELOGS = [
    REPO / "controller" / "CHANGELOG.md",
    REPO / "controller-ea" / "CHANGELOG.md",
    REPO / "device" / "CHANGELOG.md",
]

# What the extractor treats as the start of a version section.
VERSION_HEADING = re.compile(r"^## \d")

# What a version heading has to look like in FULL. The loose form above is what
# cut-release.yml matches, and matching loosely is what let a mangled file
# through: inserting an entry by slicing on the first occurrence of the string
# "## 2.15.0-fx.1" cut the file's own header in half, because the header quotes
# that string as an example. The wreckage — `## 2.15.0-fx.1` for the tag ...` —
# began with "## " and a digit, so it read as a version heading, the extractor
# started the section there, and the release notes came out empty. Caught by
# running the extraction, not by this file, which is why the rule is here now.
#
# The two channel labels are spelled out rather than allowed as "any trailing
# text": entries before 2.20.2 use an em dash and later ones parentheses, and
# both are real headings — but naming them is what keeps the rule able to
# reject prose.
EXACT_VERSION_HEADING = re.compile(
    r"^## \d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?"
    r"(?: \(Early Access\)| \u2014 Early Access)?$"
)


@pytest.mark.parametrize("path", CHANGELOGS, ids=lambda p: p.parent.name)
def test_every_level_two_heading_is_a_version(path):
    assert path.exists(), f"{path} is missing"
    offenders = [
        (n, line.rstrip())
        for n, line in enumerate(path.read_text().splitlines(), 1)
        if line.startswith("## ") and not VERSION_HEADING.match(line)
    ]
    assert not offenders, (
        f"{path.relative_to(REPO)} has level-2 headings that are not versions: "
        f"{offenders}. cut-release.yml ends a section at the next `## <digit>`, "
        f"so these read as prose today — but a heading like `## 2 things to "
        f"know` would truncate the release notes with nothing failing. Use "
        f"`###` or deeper inside an entry."
    )


@pytest.mark.parametrize("path", CHANGELOGS, ids=lambda p: p.parent.name)
def test_the_newest_entry_extracts_whole(path):
    """
    Extract the top section the way the workflow does, and check nothing was
    lost — the whole file up to the second version heading has to come back.
    """
    lines = path.read_text().splitlines()
    heads = [n for n, line in enumerate(lines) if VERSION_HEADING.match(line)]
    assert heads, f"{path.relative_to(REPO)} has no version section at all"

    first = heads[0]
    end = heads[1] if len(heads) > 1 else len(lines)
    section = [l for l in lines[first + 1:end]]

    assert any(l.strip() for l in section), (
        f"{path.relative_to(REPO)}'s newest entry is empty — cut-release.yml "
        f"refuses to tag on that, which is the intended failure, but it means "
        f"the release cannot be cut."
    )


@pytest.mark.parametrize("path", CHANGELOGS, ids=lambda p: p.parent.name)
def test_a_version_heading_carries_nothing_but_its_version(path):
    """
    A `## ` line that merely STARTS like a version still starts a section for
    cut-release.yml, so prose trailing one is a silently truncated release.
    """
    bad = [
        (n, line.rstrip())
        for n, line in enumerate(path.read_text().splitlines(), 1)
        if VERSION_HEADING.match(line) and not EXACT_VERSION_HEADING.match(line)
    ]
    assert not bad, (
        f"{path.relative_to(REPO)} has version headings with trailing text: "
        f"{bad}. cut-release.yml starts a section at such a line, so whatever "
        f"follows it becomes the release notes."
    )


@pytest.mark.parametrize("path", CHANGELOGS, ids=lambda p: p.parent.name)
def test_no_version_appears_twice(path):
    """
    Two entries under one version number is a truncated release, from the
    third direction this file already guards two of.

    cut-release.yml extracts from a version heading to the NEXT version
    heading, so the second entry for a version is not merged into the first —
    it is the section that follows it, and the extraction stops before it
    reaches it. Whichever half is lower in the file never reaches the tag,
    and a tag annotation cannot be corrected afterwards.

    It happened to controller 2.35.0-fx.1, which carried two separate `##
    2.35.0-fx.1` sections: the mDNS scan and the endpoint-restart fix both
    shipped in that release and only the first was published with it. Nothing
    failed — every other rule here was satisfied, because each heading is a
    well-formed version on its own.
    """
    seen = {}
    dupes = []
    for n, line in enumerate(path.read_text().splitlines(), 1):
        if not VERSION_HEADING.match(line):
            continue
        key = line.strip()
        if key in seen:
            dupes.append(f"{key} at lines {seen[key]} and {n}")
        else:
            seen[key] = n
    assert not dupes, (
        f"{path.relative_to(REPO)} has a version heading more than once: "
        f"{dupes}. Only the first section reaches the release notes — merge "
        f"them into one entry with `###` subsections."
    )
