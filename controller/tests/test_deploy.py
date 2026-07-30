"""
Deployment-shape guards — not logic tests. The controller Dockerfile
COPYs each module explicitly, so a new em_*.py that works fine on bare
metal crash-loops the container at import time if the COPY line is
forgotten (bitten by em_scenes.py 2026-07-10 and em_oww_models.py
2026-07-19).
"""

import re
from pathlib import Path

CONTROLLER = Path(__file__).resolve().parents[1]


def test_dockerfile_copies_every_controller_module():
    dockerfile = (CONTROLLER / "Dockerfile").read_text()
    copied = set(re.findall(r"^COPY\s+(\S+\.py)\s", dockerfile, re.M))
    modules = {p.name for p in CONTROLLER.glob("em_*.py")} | {"version.py"}
    missing = sorted(modules - copied)
    assert not missing, (
        f"Dockerfile is missing COPY lines for {missing} — the container "
        f"will crash-loop at import time"
    )


def test_dashboard_bundle_is_cache_busted():
    """
    /dashboard must not hand the browser a bare /static/dashboard.js URL.

    aiohttp's add_static sends Last-Modified and ETag but no Cache-Control, so
    browsers apply heuristic freshness and serve a stale bundle without
    revalidating. That failure is invisible server-side — deploy correct, file
    correct, compiled bundle correct, browser showing the previous UI — so it
    reads as "my change didn't work" and sends you hunting in the wrong place.
    Asserted at the source level because the alternative is starting an aiohttp
    app, which this suite deliberately does not do.
    """
    import re
    from pathlib import Path

    src = (Path(__file__).resolve().parent.parent / "em_api.py").read_text()
    handler = src[src.index("async def _serve_dashboard"):]
    handler = handler[:handler.index("\nasync def ", 1)]

    assert "dashboard.js?v=" in handler, \
        "the bundle URL must carry a cache-busting token"
    assert "no-cache" in handler, \
        "dashboard.html itself must be revalidated, or the new URL is never seen"
    # A version-string token would not change between two local "dev" builds;
    # mtime changes on every rebuild.
    assert "st_mtime" in handler, \
        "cache-bust on the bundle's mtime, not on a version string"
