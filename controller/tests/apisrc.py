"""
Extracting one function's source out of em_api.py, for tests that cannot
import it.

em_api pulls in aiohttp and the whole controller stack, so a test that wants
to exercise one function executes its SOURCE in a stub namespace instead —
which keeps what runs the code that actually ships, rather than a
reimplementation of it that can quietly diverge.

Shared rather than copied into each test file, because two copies of an
extractor is the same drift the extractor itself was fixed for.
"""

from pathlib import Path

CONTROLLER = Path(__file__).resolve().parents[1]


def extract(name: str) -> str:
    """
    The source of one top-level function in em_api.py.

    It stops at the first line back at column 0, NOT at the next `def`, and
    the difference is not cosmetic. Stopping at the next def swallows
    everything between the two — comments, module constants, annotated
    globals — and then exec()s it in a namespace that has none of em_api's
    imports. A `_thing: Optional[str] = None` added between two functions
    therefore broke a test about the release poll interval with
    `NameError: Optional`, naming neither the line nor the file that caused
    it.
    """
    src = (CONTROLLER / "em_api.py").read_text()
    start = src.index(f"def {name}")
    if src[max(0, start - 6):start] == "async ":
        start -= 6                       # keep the async keyword
    lines = src[start:].splitlines(keepends=True)
    out = [lines[0]]
    for line in lines[1:]:
        # Blank lines and anything indented belong to the function; a
        # non-empty line starting at column 0 is the next thing in the file.
        if line.strip() and not line[0].isspace():
            break
        out.append(line)
    return "".join(out)
