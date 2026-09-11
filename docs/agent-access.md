# Den Controller aus einer Automation heraus steuern

**Die Kurzfassung:** Unter dem Home-Assistant-Add-on kann alles, was die
Supervisor-API von Home Assistant erreicht, über Ingress auch die
Revoice-API erreichen, sich als der Home-Assistant-Benutzer authentifizieren,
zu dem es gehört, und — wenn dieser Benutzer Revoice-Administrator ist — die
Logs eines Geräts lesen und Befehle darauf ausführen. Kein Passwort, kein zu
öffnender Port, kein zu kopierender Token.

Das gibt es, weil die Person, der die Hardware gehört, der einzige Weg war
zwischen dem, was ein Gerät wusste, und irgendjemandem, der handeln konnte.
Einen Fehler zu diagnostizieren hieß, dass diese Person ein Terminal öffnet,
von jemand anderem vorgeschlagene Befehle ausführt und die Ausgabe
zurückkopiert. Das funktioniert genau so lange, wie sie wach und willens ist.

---

## Der Weg

Ingress-Anfragen kommen vom Gateway des Supervisors (`172.30.32.2`) und
tragen `X-Remote-User-Id`, das der Supervisor **aus allem entfernt, was ein
Client sendet**, und selbst wieder hinzufügt. `em_ingressauth` behandelt das
als Nachweis einer authentifizierten Home-Assistant-Sitzung, und das nur unter
dem Add-on — im eigenständigen Container ist derselbe Header von Angreifern
setzbar und wird ignoriert. Das ist der gesamte sicherheitsrelevante Inhalt,
und deshalb ist die Prüfung eine getestete reine Funktion und kein `if` in
einem Handler.

Also:

1. `POST /api/auth/ingress` → `{token, role}`. Kein Body. Die Identität kommt
   aus den Headern, die der Supervisor gesetzt hat.
2. Diesen Token bei jedem weiteren Aufruf als
   `Authorization: Bearer <token>` mitsenden.

Aus Claude Code heraus ist der Add-on-Proxy des Home-Assistant-MCP-Servers der
Transportweg:

```
ha_manage_app(slug="<prefix>_controller", path="/api/auth/ingress", method="POST")
ha_manage_app(slug="<prefix>_controller", path="/api/devices", method="GET",
              request_headers={"Authorization": "Bearer <token>"})
```

Der Slug unterscheidet sich je Installation — `ha_get_app(source="installed")`
listet ihn auf.

## Der eine Schritt von Hand

**Ein neuer Home-Assistant-Benutzer, der Revoice zum ersten Mal erreicht,
bekommt `readonly`.** `role_for` vergibt Adminrechte nur, wenn noch niemand
den Controller über Ingress verwalten kann; die zweite Person durch die Tür
hat Lesezugriff, denn dieses Dashboard zu erreichen ist für sich genommen kein
Beleg dafür, dass man mit einer Root-Shell auf jedem Gerät betraut ist.

Diese Regel ist richtig und sollte für Automationen nicht gelockert werden.
Stufe den Benutzer der Automation einmal von Hand hoch:

> **Einstellungen → Benutzer →** den Eintrag suchen (der
> Home-Assistant-MCP-Server erscheint als `HA-MCP Server`) **→ die Rolle auf
> Administrator setzen.**

Sichtbar in einer Liste, die der Besitzer lesen kann, an derselben Stelle
widerrufbar, und es passiert, weil ein Mensch sich dafür entschieden hat, und
nicht, weil ein Programm nett gefragt hat. Wird die Hochstufung nie gemacht,
funktioniert alles unter *Lesen* weiterhin und nichts unter *Handeln*.

## Lesen (`readonly` genügt)

| Aufruf | Was er beantwortet |
|------|-----------------|
| `GET /api/devices` | Die ganze Flotte: Erreichbarkeit, Firmware, Fähigkeiten, Verbindungszustand, letzter Fehler |
| `GET /api/devices/{id}/logs?limit=N` | Die Log-Ereignisse des Geräts, einschließlich alles von der Firmware Weitergereichten und jedes vom Controller eingesammelten Supervisor-Logs |
| `GET /api/devices/{id}/activity` | Gesprächsstatistiken |
| `GET /api/system/status` | Controller-Version, Update-Hinweis, Betriebsart |

Das deckt die meiste Diagnose ab. Das eigene dauerhafte Log eines Geräts — das,
welches einen Stromausfall übersteht — kommt hier an, ohne dass jemand das
Gerät anfassen muss, denn der Controller holt es, sobald ein Update nicht
bestätigt.

## Handeln (`admin`)

| Aufruf | Anmerkungen |
|------|-------|
| `POST /api/devices/{id}/exec` `{"cmd": "..."}` | Ein Shell-Befehl, bis zum Ende ausgeführt, Ausgabe wird zurückgegeben. Als Root, auf dem Gerät. |
| `POST /api/devices/{id}/supervisor_log` | Das dauerhafte Log auf Anforderung holen |
| `POST /api/devices/{id}/update` | OTA |
| `POST /api/devices/{id}/config` | Konfiguration je Gerät |

`exec` gewährt nichts, was der Konsolen-Reiter des Dashboards nicht schon
gewährte: Der ist für Administratoren bereits eine interaktive Root-Shell. Es
ist dieselbe Fähigkeit in einer Form, die ein Programm aufrufen kann, und
deshalb liegt sie auf derselben Hürde und nicht auf einer niedrigeren.

**Jeder Befehl wird mit dem ausführenden Benutzer protokolliert**, in den
Log-Ereignissen des Geräts, neben `Shell session opened by <user>`. Eine
interaktive Sitzung kündigt sich immerhin an; eine skriptfähige, die das nicht
täte, wäre die leisere der beiden — und das wäre für den Besitzer des Geräts
herum falsch.

## Was das nicht tut

- **Es erreicht kein Gerät, das offline ist.** Alles hier läuft über den
  Controller — ausgerechnet der Fehlerfall, in dem man sich eine Shell am
  meisten wünscht (ein Gerät, das seinen Controller nicht findet), ist also
  der, in dem es keine gibt. Dafür ist das dauerhafte Supervisor-Log auf
  `/data` da; siehe `device/CLAUDE.md`.
- **Es funktioniert nicht im eigenständigen Container**, und das ist Absicht.
  Es gibt keinen Supervisor, der für die aufrufende Seite bürgt, also
  antwortet `POST /api/auth/ingress` mit 401, und die gewöhnliche
  Passwortanmeldung ist der Weg hinein.
- **Es ist kein Tunnel ins LAN.** Erreichbar ist einzig die eigene HTTP-API des
  Controllers, durch Home Assistant hindurch, als Home-Assistant-Benutzer.
