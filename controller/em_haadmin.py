"""
em_haadmin.py — is this Home Assistant user an administrator?

Supervisor's ingress proxy forwards the authenticated user's id and names
and nothing else — there is no admin flag in the headers, and HA core's
ingress view sets requires_auth=False and leans on the session token, so
`panel_admin` hides the sidebar entry rather than gating the URL. Reaching
the dashboard is therefore not evidence of being an HA admin, and the only
way to find out is to ask Home Assistant.

We ask Supervisor's own `GET /auth/list`, which returns each Home Assistant
user's `group_ids`. That is gated by `auth_api: true` — access to the user
backend and nothing else. The obvious route is Home Assistant's
`config/auth/list` over Supervisor's WebSocket proxy, but that needs
`homeassistant_api: true`, which grants the whole Home Assistant API to read
one boolean. Take the narrow permission.

The cost of the narrow route is that /auth/list returns **no user id** —
only username, name, is_owner, is_active, local_only and group_ids. So the
match is by USERNAME, against `X-Remote-User-Name`.

That is safe here and is not the thing we refuse to do elsewhere. Accounts
are still keyed on the immutable HA user id (users.ha_user_id); only this
lookup matches by name, and both sides of it — the forwarded header and the
list — are read from Home Assistant at the same moment, so they agree by
construction. A rename makes the lookup MISS, which answers unknown and
leaves the stored role alone. It cannot attach one person's admin status to
another's account.

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
import logging
import os
import time
from typing import Optional

log = logging.getLogger("echomuse.haadmin")

# Supervisor's own user-backend endpoint. Requires `auth_api: true`.
AUTH_LIST_URL = "http://supervisor/auth/list"

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


async def is_admin(username: Optional[str]) -> Optional[bool]:
    """
    True/False if Home Assistant answered, None if we could not find out.

    Takes the HA USERNAME (X-Remote-User-Name), because Supervisor's
    /auth/list carries no user id. None is never an assertion about the
    user — only about the lookup.
    """
    if not username:
        # Supervisor sets the name header only when the HA user record has
        # one. Nothing to match on, so nothing is known.
        return None

    ha_user_id = username
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
        name = user.get("username")
        if not name:
            continue
        # is_owner is an admin too — the owner is not necessarily listed in
        # the admin group, and treating them as read-only would lock the
        # person who set Home Assistant up out of their own controller.
        admin = (ADMIN_GROUP in (user.get("group_ids") or [])
                 or bool(user.get("is_owner")))
        _cache[name] = (now, admin)
        if name == ha_user_id:
            found = admin

    if found is None:
        # Authenticated by Supervisor but absent from the list — a rename
        # between the header being stamped and this call, or a provider that
        # issues no username. Do not guess in either direction.
        log.warning("[haadmin] Home Assistant user %r not in the list; "
                    "leaving admin status unknown", ha_user_id)
    return found


async def _fetch_users(token: str) -> Optional[list[dict]]:
    """
    GET Supervisor's /auth/list and return the user records. Imports aiohttp
    lazily so this module stays importable (and testable) without it.
    """
    import aiohttp

    headers = {"Authorization": f"Bearer {token}"}
    async with aiohttp.ClientSession() as session:
        async with session.get(AUTH_LIST_URL, headers=headers) as resp:
            if resp.status == 403:
                # auth_api is not granted. Distinguished from a transport
                # failure because retrying cannot fix it — it is a property
                # of how the add-on is configured.
                log.warning("[haadmin] Supervisor refused /auth/list — the "
                            "add-on needs auth_api: true. Roles will fall "
                            "back to the stored value.")
                return None
            if resp.status != 200:
                log.warning("[haadmin] /auth/list returned HTTP %s", resp.status)
                return None
            body = await resp.json()

    # Supervisor wraps results as {"result": "ok", "data": {...}}.
    data = body.get("data") if isinstance(body, dict) else None
    if isinstance(data, dict):
        return data.get("users") or []
    if isinstance(data, list):
        return data
    return []


def invalidate() -> None:
    """Drop the cache. For tests and for an explicit re-check."""
    global _failed_at
    _cache.clear()
    _failed_at = None


def lookup_available() -> bool:
    """
    Can Home Assistant currently be asked about roles at all?

    Used to decide whether a manual role change would survive: if HA governs
    roles, login_via_ingress re-derives on every login and would revert it.

    Deliberately coarse — it does not check a specific user. The per-user
    answer needs the HA username, and the EchoMuse username is not reliably
    it (collisions are suffixed). Being coarse over-refuses in one narrow
    case: a renamed user whose lookup would have missed. That refusal points
    the operator at Home Assistant, which is the right advice regardless, so
    the conservative direction costs nothing.
    """
    if _token() is None:
        return False
    if _failed_at is not None and time.monotonic() - _failed_at < FAILURE_TTL_S:
        return False
    return True
