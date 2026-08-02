# Support bundles

When something misbehaves and it isn't obvious why, a support bundle collects
the diagnostics in one file you can attach to a
[GitHub issue](https://github.com/wilbowes/EchoMuse/issues).

**Settings → Support → Download support bundle**, or
`GET /api/support/bundle` if you prefer the API. Admin only.

It is a plain JSON file. **Open it before you send it** — it is readable, and
you should be able to satisfy yourself about the contents rather than take
this page's word for it.

## What it deliberately does not contain

A bundle is meant to be attached to a public issue, so anything private in it
is public the moment you send it, permanently. It is built as an **allowlist**:
every field is named individually and everything else is dropped. A new
column added to the database is excluded until someone deliberately includes
it — the failure mode is that support loses a field, never that your data
leaks.

Excluded, with no option to include them:

| Not included | Why |
|---|---|
| **Anything you said** — transcripts, `stt_text`, saved audio | Speech from inside your home. There is no opt-in flag, because a flag is a thing people tick and this one cannot be untickled once the file is public. |
| **Device labels** | You wrote them, and they routinely contain names — "Bedroom - Sam" is a real example. Replaced with `device-1`, `device-2`… |
| **Network identifiers** — WiFi SSID, BSSID, IP addresses | An SSID is geolocatable from public wardriving databases, so publishing one discloses roughly where you live. |
| **Credentials** — device tokens, ESPHome PSKs, password hashes, login sessions | Obvious, but stated so it is checkable. |
| **URLs and quoted strings in log lines** | Media URLs carry provider paths and session tokens; quoted strings in turn traces carry transcripts. |

Log lines from sources known to carry speech are dropped **whole** rather than
edited, because partially redacting a line that quotes a transcript is a bet
on a regular expression.

## What it does contain, and why each part earns its place

| Included | Why it is needed |
|---|---|
| Controller version, schema version | Almost every "is this fixed?" question starts here. |
| Device serials | Nothing correlates without them. They identify your hardware to you; they are not otherwise meaningful. |
| Firmware version, rollback slot, approval state | Tells us whether a fix is even present on that device. |
| **Capabilities** (`mic`, `oww_shadow`, `ambient_light`…) | Decides which Home Assistant entities exist at all. "The light sensor didn't appear" is answered here in one line. |
| Configuration — thresholds, EQ, LED scenes, wake model | Behaviour, not identity. Keys whose *name* looks credential-shaped are redacted anyway. |
| Turn metadata — outcome, wake score, stage latencies, underruns | What happened and how long each stage took. No words, just timings and outcomes. |
| Hourly metrics — CPU, memory, temperature, RSSI, RTT | Trends. Signal strength is included; the network's name is not. |
| Wake counters — near-misses, on-device drops, inference timings | Wake-word behaviour without any audio. |
| Recent controller log lines, sanitised | Message types and timings, with quoted text and URLs removed. |

Roughly the last 24 hours, capped per device.

## Reviewing one before you send it

Open the file and look at the top: `redaction` states the contract, and
`devices[].name` should read `device-1`, not your room names. Searching it for
your WiFi name or something you said should come up empty.

If you find anything in there you would rather not publish, **that is a bug
and we want to hear about it** — please report it privately rather than in a
public issue.

## Retention

The bundle is a file on your machine. Nothing is uploaded anywhere by
generating one; it goes wherever you choose to put it, and deleting it is
enough. Regenerate rather than keeping old ones around — they are only useful
alongside a current problem.
