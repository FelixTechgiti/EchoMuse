# Driving the controller from an automation

**The short version:** under the Home Assistant add-on, anything that can
reach Home Assistant's Supervisor API can reach the EchoMuse API through
Ingress, authenticate as the Home Assistant user it belongs to, and — if that
user is an EchoMuse admin — read a device's logs and run commands on it.
No password, no port to open, no token to copy.

This exists because the person who owns the hardware was the only path between
what a device knew and anyone who could act on it. Diagnosing a fault meant
that person opening a terminal, running commands somebody else suggested, and
pasting the output back. That works exactly as long as they are awake and
willing.

---

## The path

Ingress requests arrive from Supervisor's gateway (`172.30.32.2`) carrying
`X-Remote-User-Id`, which Supervisor **strips from anything a client sends**
and re-adds itself. `em_ingressauth` treats that as proof of an authenticated
Home Assistant session, and only under the add-on — on the standalone
container the same header is attacker-supplied and is ignored. That is the
whole security content, and it is why the check is a tested pure function
rather than an `if` in a handler.

So:

1. `POST /api/auth/ingress` → `{token, role}`. No body. The identity comes
   from the headers Supervisor added.
2. Send that token as `Authorization: Bearer <token>` on every later call.

From Claude Code, the Home Assistant MCP server's add-on proxy is the
transport:

```
ha_manage_app(slug="<prefix>_controller", path="/api/auth/ingress", method="POST")
ha_manage_app(slug="<prefix>_controller", path="/api/devices", method="GET",
              request_headers={"Authorization": "Bearer <token>"})
```

The slug differs per installation — `ha_get_app(source="installed")` lists it.

## The one manual step

**A new Home Assistant user reaching EchoMuse for the first time gets
`readonly`.** `role_for` grants admin only when nobody can already administer
the controller through Ingress; the second person through the door is
read-only, because reaching this dashboard is not by itself evidence of being
trusted with a root shell on every device.

That rule is correct and should not be loosened for automations. Promote the
automation's user by hand, once:

> **Settings → Users →** find the entry (the Home Assistant MCP server appears
> as `HA-MCP Server`) **→ set the role to admin.**

Visible in a list the owner can read, revocable in the same place, and it
happens because a human decided it rather than because a program asked
nicely. If the promotion is never made, everything under *Read* below still
works and nothing under *Act* does.

## Read (`readonly` is enough)

| Call | What it answers |
|------|-----------------|
| `GET /api/devices` | The whole fleet: connectivity, firmware, capabilities, link state, last error |
| `GET /api/devices/{id}/logs?limit=N` | The device's log events, including everything the firmware relayed and every supervisor log the controller collected |
| `GET /api/devices/{id}/activity` | Turn statistics |
| `GET /api/system/status` | Controller version, update notice, deployment mode |

That covers most diagnosis. A device's own persistent log — the one that
survives a power cycle — arrives here without anyone touching the device,
because the controller fetches it whenever an update does not confirm.

## Act (`admin`)

| Call | Notes |
|------|-------|
| `POST /api/devices/{id}/exec` `{"cmd": "..."}` | One shell command, run to completion, output returned. Root, on the device. |
| `POST /api/devices/{id}/supervisor_log` | Fetch the persistent log on demand |
| `POST /api/devices/{id}/update` | OTA |
| `POST /api/devices/{id}/config` | Per-device configuration |

`exec` grants nothing the dashboard's console tab did not: that is already an
interactive root shell for an admin. It is the same capability in a shape a
program can call, which is why it sits at the same bar and not a lesser one.

**Every command is logged with the user that ran it**, in the device's own log
events, alongside `Shell session opened by <user>`. An interactive session at
least announces itself; a scriptable one that did not would be the quieter of
the two, which is the wrong way round for whoever owns the device.

## What this does not do

- **It does not reach a device that is offline.** Everything here is proxied
  by the controller, so the one fault where you most want a shell — a device
  that cannot find its controller — is the one where there is none. That is
  what the persistent supervisor log on `/data` is for; see `device/CLAUDE.md`.
- **It does not work on the standalone container**, by design. There is no
  Supervisor to vouch for the caller, so `POST /api/auth/ingress` answers 401
  and the ordinary password login is the way in.
- **It is not a tunnel into the LAN.** The only thing reachable is the
  controller's own HTTP API, through Home Assistant, as a Home Assistant user.
