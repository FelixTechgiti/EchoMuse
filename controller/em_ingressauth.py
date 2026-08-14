"""
em_ingressauth.py — whether a request may be authenticated by Home Assistant.

Under the add-on, Home Assistant has already authenticated the person before
the request reaches us, so a second EchoMuse password is a lock on a door
that is already locked. Supervisor forwards the authenticated user as
X-Remote-User-Id (plus optional name headers) and, importantly, **strips any
incoming headers of those names** before proxying — so the values cannot be
supplied by the client and can be trusted.

They can be trusted *only* on a request that genuinely came through
Supervisor. That is the entire security content of this module, and it is
why the decision is a pure function with tests rather than an `if` in a
handler: the same header on the standalone container is attacker-supplied,
and honouring it there would turn "set one header" into a full admin
session — including the root shell proxy. Two conditions, never one:

  * the deployment is the add-on (INGRESS_ONLY), and
  * the request arrived from Supervisor's gateway address.

The gateway check is the same one _ingress_only_middleware already applies
to every request. This module deliberately re-derives it from its own
inputs rather than assuming the middleware ran, because the cost of the
middleware being reordered or bypassed one day is not a 403 — it is silent
authentication bypass.

What this does NOT decide is what the user may then do; role assignment is
the caller's business. See docs and config.yaml's `panel_admin`.
"""

from __future__ import annotations

from typing import NamedTuple, Optional

# Supervisor's gateway address. Every ingress request is proxied from here.
INGRESS_GATEWAY_IP = "172.30.32.2"


class IngressIdentity(NamedTuple):
    """A Home Assistant user, as forwarded by Supervisor."""
    user_id: str
    username: str
    display_name: str


VALID_ROLES = ("admin", "readonly")


def role_for(
    *,
    existing_users: int,
    configured_default: Optional[str],
    ha_is_admin: Optional[bool] = None,
) -> str:
    """
    The role a Home Assistant user is given.

    Mirrors Home Assistant's own answer where we have it: an HA admin is an
    EchoMuse admin, an HA non-admin is read-only. Supervisor does not forward
    admin status — it sends only the user id and names — so `ha_is_admin`
    comes from asking Home Assistant directly (em_haadmin), and is None
    whenever that lookup was unavailable or failed.

    The first user through the door is admin regardless. That mirrors the
    standalone container, where whoever holds the bootstrap token becomes the
    owner, and it is what removes the bootstrap step under the add-on — it
    must not depend on a network call to Home Assistant succeeding, or a
    fresh install could lock itself out of its own dashboard.

    Unknown admin status falls back to the configured default, which is
    read-only. That is the safe direction: HA's ingress view sets
    requires_auth=False and `panel_admin` only hides the sidebar entry, so
    reaching this dashboard is not by itself evidence of being trusted with
    a root shell to every device. Promotion is recoverable — make them an
    admin in Home Assistant, or PATCH /api/users/{id} when Home Assistant
    cannot be asked — whereas the reverse mistake is not recoverable by the
    person who suffers it.

    An unrecognised configured value falls back to read-only too — a typo in
    a config row must never be the thing that grants admin.
    """
    if existing_users == 0:
        return "admin"
    if ha_is_admin is True:
        return "admin"
    if ha_is_admin is False:
        return "readonly"
    value = (configured_default or "").strip()
    return value if value in VALID_ROLES else "readonly"


def decide(
    *,
    ingress_only: bool,
    remote: Optional[str],
    user_id: Optional[str],
    username: Optional[str] = None,
    display_name: Optional[str] = None,
) -> Optional[IngressIdentity]:
    """
    Return the Home Assistant identity to authenticate as, or None to fall
    back to ordinary password login.

    None is always the safe answer — it costs a login form, never access.
    """
    # Not the add-on. The header is either absent or attacker-supplied;
    # either way it means nothing here.
    if not ingress_only:
        return None

    # Did not come from Supervisor. Under host_network the container shares
    # the host's netns, so this is the only thing distinguishing a proxied
    # request from one off the LAN.
    if remote != INGRESS_GATEWAY_IP:
        return None

    # Supervisor sets the id only when the ingress session has a user
    # attached. No user means we have not been told who this is, which is
    # not the same as being told it is nobody.
    user_id = (user_id or "").strip()
    if not user_id:
        return None

    # Names are optional in Supervisor's own code — it sets each only if the
    # user record has one. Fall back through to the id, which is always
    # present, so a display name is never required to log in.
    username = (username or "").strip()
    display_name = (display_name or "").strip()

    return IngressIdentity(
        user_id=user_id,
        username=username or display_name or f"ha-{user_id[:12]}",
        display_name=display_name or username or f"ha-{user_id[:12]}",
    )
