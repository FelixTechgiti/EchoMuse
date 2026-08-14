"""
em_haadmin.py — is this Home Assistant user an administrator?

Supervisor's ingress proxy forwards the authenticated user's id and names
and nothing else — there is no admin flag in the headers, and HA core's
ingress view sets requires_auth=False and leans on the session token, so
`panel_admin` hides the sidebar entry rather than gating the URL. Reaching
the dashboard is therefore not evidence of being an HA admin, and the only
way to find out is to ask Home Assistant.

We ask over Supervisor's Home Assistant WebSocket proxy using the add-on's
SUPERVISOR_TOKEN, and read `group_ids` off the matching user. This needs
`homeassistant_api: true` in config.yaml — a real privilege for the add-on,
taken deliberately so that household members who are not HA admins get a
read-only dashboard instead of a root shell to every device.

Every failure answers **None** — unavailable, not "not an admin" and not
"an admin". The caller (em_ingressauth.role_for) treats None as unknown and
falls back to read-only for new users, and to the stored role for existing
ones. That ordering matters: a Home Assistant that is slow or mid-restart
must not silently demote an admin, and must not promote anyone either.

Results are cached briefly. This runs on the login path, and an ingress
login happens on any page load without a stored session, so an uncached
lookup would put a WebSocket round trip in front of the dashboard opening.
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import time
from typing import Optional

log = logging.getLogger("echomuse.haadmin")

# Supervisor's in-container proxy to Home Assistant's WebSocket API.
WS_URL = "ws://supervisor/core/websocket"

# HA's built-in admin group. A user is an admin iff they are in it.
ADMIN_GROUP = "system-admin"

# How long an answer stays good. Short enough that revoking someone's HA
# admin takes effect within a couple of minutes, long enough that a reload
# loop does not hammer Home Assistant.
CACHE_TTL_S = 120

# The whole lookup, including connect and auth. The login path waits on
# this, so it must fail fast: a slow answer is worth less than a prompt
# fallback to the stored role.
TIMEOUT_S = 5.0

# How long a FAILURE suppresses retries. Caching a failure for CACHE_TTL_S
# would extend a momentary Home Assistant restart into two minutes of
# everyone being read-only; caching it for nothing means every dashboard
# load pays TIMEOUT_S for as long as HA is down, which is how a restart
# turns into a dashboard that feels broken. Short enough to recover within
# seconds of HA coming back, long enough that a reload does not re-wait.
FAILURE_TTL_S = 15

_cache: dict[str, tuple[float, bool]] = {}
# Set while a recent lookup failed — a single timestamp, not per user,
# because the failures this guards against are "Home Assistant is not
# answering", which is never about one account.
_failed_at: float | None = None


def _token() -> Optional[str]:
    return os.environ.get("SUPERVISOR_TOKEN") or None


def is_admin_cached(ha_user_id: str) -> Optional[bool]:
    """The cached answer, or None if absent or stale."""
    hit = _cache.get(ha_user_id)
    if hit is None:
        return None
    cached_at, value = hit
    if time.monotonic() - cached_at > CACHE_TTL_S:
        _cache.pop(ha_user_id, None)
        return None
    return value


async def is_admin(ha_user_id: str) -> Optional[bool]:
    """
    True/False if Home Assistant answered, None if we could not find out.

    None is never an assertion about the user — only about the lookup.
    """
    cached = is_admin_cached(ha_user_id)
    if cached is not None:
        return cached

    global _failed_at
    if _failed_at is not None:
        if time.monotonic() - _failed_at < FAILURE_TTL_S:
            # A recent lookup failed. Answer unknown immediately rather than
            # waiting TIMEOUT_S again — during a Home Assistant restart this
            # is the difference between a dashboard that loads and one that
            # hangs for five seconds on every page.
            return None
        _failed_at = None

    token = _token()
    if token is None:
        # Not running as an add-on, or homeassistant_api is not granted.
        # Ordinary condition on the standalone container.
        return None

    try:
        users = await asyncio.wait_for(_fetch_users(token), TIMEOUT_S)
    except asyncio.TimeoutError:
        log.warning("[haadmin] Home Assistant did not answer in %ss", TIMEOUT_S)
        _failed_at = time.monotonic()
        return None
    except Exception as e:
        log.warning("[haadmin] Could not read the Home Assistant user list: %s", e)
        _failed_at = time.monotonic()
        return None

    if users is None:
        _failed_at = time.monotonic()
        return None

    now = time.monotonic()
    found: Optional[bool] = None
    for user in users:
        uid = user.get("id")
        if not uid:
            continue
        admin = ADMIN_GROUP in (user.get("group_ids") or [])
        _cache[uid] = (now, admin)
        if uid == ha_user_id:
            found = admin

    if found is None:
        # Authenticated by Supervisor but absent from the user list. Do not
        # guess in either direction.
        log.warning("[haadmin] Home Assistant user not in the list; "
                    "leaving admin status unknown")
    return found


async def _fetch_users(token: str) -> Optional[list[dict]]:
    """
    Open Supervisor's HA WebSocket proxy, authenticate, and return the user
    list. Imports aiohttp lazily so this module stays importable (and
    testable) without it.
    """
    import aiohttp

    async with aiohttp.ClientSession() as session:
        async with session.ws_connect(WS_URL, heartbeat=None) as ws:
            # HA sends auth_required, then we authenticate, then auth_ok.
            await ws.receive_json()
            await ws.send_json({"type": "auth", "access_token": token})
            reply = await ws.receive_json()
            if reply.get("type") != "auth_ok":
                log.warning("[haadmin] Home Assistant rejected the add-on "
                            "token: %s", reply.get("type"))
                return None

            await ws.send_json({"id": 1, "type": "config/auth/list"})
            while True:
                msg = await ws.receive_json()
                if msg.get("id") != 1:
                    continue
                if not msg.get("success", False):
                    log.warning("[haadmin] config/auth/list refused: %s",
                                msg.get("error"))
                    return None
                return msg.get("result") or []


def invalidate() -> None:
    """Drop the cache. For tests and for an explicit re-check."""
    global _failed_at
    _cache.clear()
    _failed_at = None
