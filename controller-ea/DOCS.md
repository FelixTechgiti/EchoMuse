# EchoMuse — Early Access

This is the **Early Access** channel: the next controller release,
before it is general. It is the same program as the stable add-on
and the same settings; only the version differs.

**Install this instead of the stable add-on, not alongside it.**
Both use host networking and the same ports (8767, 8768, 8770), so
whichever starts second will fail to bind.

**Switching channels is a migration, not a toggle.** Home Assistant
add-ons do not share storage, so this starts with an empty database
and a newly generated certificate authority. Existing devices hold
the stable add-on's CA, and they take `wss` from the mDNS record
rather than from `require_device_tls` — so they will fail
verification and will not connect at all until you copy the stable
add-on's `/data` across, including all four files in `tls/`.

Report anything you find against the EchoMuse repository, saying
which channel you are on.

---

# EchoMuse

Runs the EchoMuse controller — wake word detection, fleet dashboard, and
Home Assistant integration for rooted Echo Dot 2nd Gen devices — as a Home
Assistant add-on instead of a separate docker-compose deployment.

## Installation

1. Install the add-on and set **Controller LAN IP address** to this Home
   Assistant host's LAN IP under Configuration.
2. Start the add-on.
3. Open the dashboard from **Open Web UI** and create the admin account
   using the setup token shown in the add-on's log.
4. Use the dashboard's **provisioning wizard** (USB, Chrome) to set up a
   rooted Echo Dot. It finds this controller automatically — no manual IP
   entry on the device side.
5. Approve the device in the dashboard once it appears as pending. Home
   Assistant then discovers it automatically via the built-in ESPHome
   integration.

## Configuration

Every option is explained inline in the add-on's Configuration tab. For the
full picture — rooting a device, the voice pipeline, every configuration
knob — see the project's own docs:

- [Quickstart](https://github.com/wilbowes/EchoMuse/blob/main/docs/quickstart.md)
- [Configuration reference](https://github.com/wilbowes/EchoMuse/blob/main/docs/configuration.md)
- [Rooting a device](https://github.com/wilbowes/EchoMuse/blob/main/docs/rooting.md)

**Note**: restart the add-on after changing configuration.
