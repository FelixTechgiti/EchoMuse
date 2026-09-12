<div align="center">

# Revoice

**Gib deinem Echo Dot eine neue Stimme — deine.**

Revoice verwandelt einen Amazon Echo Dot der 2. Generation in einen
vollständig lokalen Sprachassistenten und Multiroom-Lautsprecher für
Home Assistant. Kein Amazon-Konto, keine Cloud, kein Ton, der dein
Netzwerk verlässt.

[Schnellstart](docs/quickstart.md) ·
[Konfiguration](docs/configuration.md) ·
[FAQ](docs/faq.md) ·
[Mitmachen](CONTRIBUTING.md)

</div>

---

Der Dot, der bei dir in der Schublade liegt, ist erstaunlich gute Hardware:
sieben Mikrofone, ein LED-Ring, ein ordentlicher Lautsprecher, ein
Klinkenausgang. Nur die Software gehört jemand anderem. Revoice ersetzt sie —
die Alexa-Firmware weicht einem kleinen Server auf dem Gerät, und ein
Controller in deinem Netzwerk meldet jeden Dot bei Home Assistant als
**ESPHome-Sprachsatellit** an. Kein Custom-Integration-Gefrickel: Home
Assistant erkennt die Geräte von selbst.

Sag dein Wakeword, sprich mit [Assist](https://www.home-assistant.io/voice_control/),
hör die Antwort aus dem Dot. Gebrauchte Geräte kosten um die 10 €.

> **⚠️ Installiere `amonet-biscuit` v2.0.0 NICHT auf einem Echo, auf dem
> Revoice läuft.**
> Version 2.0.0 des Unlocks (10. September 2026) ersetzt die Bootloader des
> Echo, und danach bootet FireOS 5 nicht mehr. Revoice läuft ausschließlich
> auf FireOS 5 — emOS eingeschlossen, denn emOS benutzt den FireOS-5-Kernel.
> Der XDA-Thread fordert bereits entsperrte Nutzer inzwischen zum Update auf.
> Wenn auf deinem Echo Revoice läuft: tu es nicht.
>
> - **Du entsperrst gerade einen neuen Echo?** Nimm **amonet-biscuit v1.1.0**,
>   das weiterhin im XDA-Thread hängt.
> - **Schon aktualisiert?** Versuche nicht, durch Flashen von FireOS 5 oder
>   eines älteren amonet zurückzukommen. v2.0.0 hat Preloader, LK und
>   TrustZone überschrieben, und die alten von Hand zurückzuschreiben ist
>   genau der Weg, auf dem ein Echo hart gebrickt wird. Revoice läuft heute
>   nicht auf FireOS 6, dieser Echo bleibt also vorerst dort.

---

## Was Revoice kann

### 🎙️ Sprache

| | |
|---|---|
| **Komplett lokal** | Wakeword, Spracherkennung, Antwort — alles läuft bei dir. Nichts geht nach draußen. |
| **Eigene Wakewords** | „Hey Biscuit", der Name deiner Katze, was du willst. Der mitgelieferte [Trainer](oww_forge/README.md) baut das Modell aus synthetischer Sprache — du musst nichts einsprechen. Fertiges Modell im Dashboard hochladen, fertig. |
| **Dazwischenreden** | Sag das Wakeword mitten in die Antwort hinein und der Assistent hält an. Ein Echo-Canceller auf dem Gerät sorgt dafür, dass er sich dabei nicht selbst hört. |
| **Mehrere Räume, eine Antwort** | Hören zwei Dots denselben Satz, antwortet genau einer — der, der dich am besten verstanden hat. Kein Chor aus dem Nachbarzimmer. |
| **Wakeword auf dem Gerät** | Optional übernimmt der Dot die Worterkennung selbst. Standardmäßig läuft sie auf dem Controller, wo neue Modelle ohne Firmware-Update ankommen. |
| **Timer** | Laufen dort, wo sie gestellt wurden, und klingeln auch dort. |

### 🎵 Musik und Audio

| | |
|---|---|
| **Vollwertiger Media Player** | Jeder Dot ist in Home Assistant ein `media_player` — Medienbrowser, Music Assistant, Radiostreams, Multiroom-Gruppen. |
| **Ducking statt Pause** | Sprichst du in laufende Musik hinein, wird sie leiser statt angehalten. Die Antwort kommt darüber, danach kommt die Musik zurück — ein Livestream verpasst nichts. |
| **Spotify Connect & AirPlay** | Der Dot taucht direkt in Spotify und auf dem iPhone als Ausgabegerät auf, ganz ohne Umweg über Home Assistant. |
| **Klang, den du einstellst** | 10-Band-EQ, Loudness, Limiter und Bass-Schutz — direkt im Dashboard, live hörbar. |
| **Kopfhörer und Verstärker** | Klinke rein, der Ton wechselt; Klinke raus, er kommt zurück. Ohne Neustart. |
| **Sagt Bescheid, wenn er spielt** | Ein eigener Sensor meldet, *ob* und *woher* gerade Ton kommt — damit ein angeschlossener Verstärker automatisch umschalten kann. |

### 🏠 Home Assistant

| | |
|---|---|
| **Wird einfach gefunden** | Über die eingebaute ESPHome-Integration. Nichts über HACS zu installieren. |
| **Der LED-Ring als Lampe** | Erscheint als `light` und lässt sich in Automationen wie jede andere Lampe schalten und färben. |
| **Aktionstaste als Auslöser** | Lange drücken feuert ein Event für deine Automationen — auch bei stummgeschaltetem Mikrofon. Kurz drücken startet weiterhin ein Gespräch, oder wahlweise ebenfalls ein Event. |
| **Helligkeitssensor** | Die meisten Dots haben einen, Amazon hat ihn nie freigegeben. Bei Revoice ist er ein Lux-Sensor, der sofort meldet, wenn das Licht angeht. |
| **Bluetooth-Proxy** | Jeder Dot leitet Bluetooth-Advertisements an Home Assistant weiter — ideal mit [Bermuda](https://github.com/agittins/bermuda) für Raumerkennung. |
| **Als Add-on installierbar** | Läuft unter dem Supervisor mit eigenem Sidebar-Panel, ohne zusätzlichen Docker-Host. |

### 🔒 Privatsphäre

| | |
|---|---|
| **Kein Phone-Home** | Keine Telemetrie, keine Analytics, kein Installationszähler. Niemand — wir eingeschlossen — kann sehen, wer Revoice benutzt. |
| **Echte Hardware-Stummschaltung** | Die Mute-Taste schaltet den Mikrofon-Wandler ab, nicht bloß eine Softwarevariable. Roter Ring, und es kommt nichts mehr durch. |
| **Verschlüsselte Geräteverbindung** | TLS mit eigener Zertifizierungsstelle und Token pro Gerät. Der Einrichtungsassistent legt das automatisch an. |
| **Die einzige Verbindung nach draußen** | Eine stündliche Abfrage bei GitHub, ob es eine neuere Version gibt — abschaltbar, siehe [Konfiguration](docs/configuration.md#was-dein-netzwerk-verlässt). |

### 🛠️ Verwaltung

| | |
|---|---|
| **Einrichtungsassistent** | Steckt den Dot per USB an: rooten, entrümpeln, WLAN, Firmware, Zertifikate — Schritt für Schritt im Browser. Danach findet das Gerät den Controller von allein. |
| **Ein Dashboard für alle Geräte** | Einstellungen global oder pro Gerät, sofort wirksam, ohne Neustart. |
| **Updates über die Luft** | Zwei Firmware-Slots mit automatischem Rückfall — ein misslungenes Update macht das Gerät nicht kaputt. |
| **Sehen, was wirklich passiert** | Wakeword-Werte, Beinahe-Treffer, Latenzen, Audio-Aussetzer pro Gespräch. Auf Wunsch bleiben die letzten Aufnahmen zum Anhören liegen — anders lässt sich die Mikrofonverstärkung nicht ehrlich beurteilen. |
| **Root-Shell und Logs** | Direkt im Browser, ohne Kabel. |
| **Support-Bundle** | Ein Klick erzeugt ein Diagnosepaket für Fehlerberichte — ohne Transkripte, Aufnahmen und WLAN-Namen. |

### 💡 Aus der Hardware geholt

Sieben Mikrofone mit Beamforming statt eines einzelnen Kanals, +24 dB
Verstärkung vor der Abtastung (der Original-Aufnahmeweg verschenkt den
größten Teil des Signals), LED-Animationen, die auf dem Gerät selbst laufen
und deshalb nicht ruckeln, und ein Mute, das den Wandler wirklich abschaltet.

---

## Was du brauchst

| Ding | Wofür |
|---|---|
| **Echo Dot 2. Generation** (Codename „biscuit") | Die Hardware. Gebraucht für ein paar Euro. |
| **Ein Rechner, der durchläuft** | Für den Controller — NAS, Mini-PC, Raspberry-Pi-Klasse oder besser. |
| **Home Assistant** | Erledigt Spracherkennung, Verstehen und Sprachausgabe. Eine funktionierende [Assist-Pipeline](https://www.home-assistant.io/voice_control/) sollte stehen. |
| **Einmalig: Laptop und USB-Kabel** | Zum Entsperren des Dots. Nur beim ersten Mal, pro Gerät. |

---

## Loslegen

**Neu hier? Fang beim [Schnellstart](docs/quickstart.md) an.** Der führt dich
von null bis zum ersten Gespräch und schickt dich zum richtigen Zeitpunkt zur
Rooting-Anleitung — statt damit anzufangen.

### 1. Dot entsperren

Das ist der einzige wirklich fummelige Teil und dauert beim ersten Mal etwa
eine Stunde: [Rooting-Anleitung](docs/rooting.md). Danach übernimmt der
Assistent im Dashboard den Rest.

### 2. Controller starten

Als **Home-Assistant-Add-on** (empfohlen, wenn du den Supervisor hast):

[![Öffne deine Home-Assistant-Instanz und zeige den Dialog zum Hinzufügen eines Add-on-Repositories.](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2FFelixTechgiti%2FRevoice)

Oder mit **Docker** auf einem beliebigen Rechner:

```bash
mkdir revoice && cd revoice
curl -O https://raw.githubusercontent.com/FelixTechgiti/Revoice/main/controller/docker-compose.deploy.yml
curl -o .env https://raw.githubusercontent.com/FelixTechgiti/Revoice/main/controller/.env.example
docker compose -f docker-compose.deploy.yml up -d
```

### 3. Gerät einrichten

Dashboard öffnen — beim Add-on über **Web-UI öffnen** in der Seitenleiste,
bei Docker unter `http://<SERVER_IP>:8768`. Der Einrichtungsassistent nimmt
den Dot per USB entgegen und gibt ihn am Ende neu gestartet ins WLAN. Er
meldet sich von selbst beim Controller, du bestätigst ihn, und Home Assistant
findet ihn kurz darauf.

---

## Wie es zusammenhängt

```
Echo Dot (Firmware) ⇄ WebSocket/TLS ⇄ Controller ⇄ ESPHome ⇄ Home Assistant
```

Der Dot ist absichtlich einfach gehalten: Er nimmt auf, bündelt die
Mikrofone und spielt ab, was er bekommt. Alles, was danebenliegen kann —
Wakeword-Bewertung, Satzende-Erkennung, Rauschunterdrückung, Klang,
Entscheidung zwischen mehreren Geräten — sitzt im Controller, wo es
beobachtbar und für die ganze Flotte auf einmal aktualisierbar ist.

Firmware und Controller werden unabhängig veröffentlicht, also kann jede
Kombination im Feld auftreten. Deshalb handeln beide Seiten **Fähigkeiten**
aus statt Versionsnummern zu vergleichen: Ein Bedienelement für etwas, das
dein Gerät nicht kann, wird ausgegraut mit Begründung angezeigt — nie als
Schalter, der stillschweigend nichts tut.

---

## Dokumentation

| | |
|---|---|
| [Schnellstart](docs/quickstart.md) | Von null zum ersten Gespräch |
| [Rooting](docs/rooting.md) | Den Dot einmalig entsperren |
| [Konfiguration](docs/configuration.md) | Jeder Regler, in verständlichen Worten erklärt |
| [FAQ](docs/faq.md) | Die Dinge, die am häufigsten schiefgehen |
| [Sprachpipeline](docs/voice-pipeline.md) | Der ganze Weg vom Mikrofon zur Antwort |
| [LED-Ring](docs/led-ring-states.md) · [Audio-Zustände](docs/audio-states.md) | Was der Ring gerade sagen will |
| [Geräte-Protokoll](docs/device-controller-interface.md) | Für alle, die eine eigene Firmware bauen |
| [SETUP.md](SETUP.md) · [JOURNAL.md](JOURNAL.md) | Wie die Hardware funktioniert und wie das herausgefunden wurde |

Die Dokumentation im Ordner `docs/` ist auf Deutsch. `SETUP.md`,
`JOURNAL.md` und `CLAUDE.md` bleiben auf Englisch — sie sind
Entwicklungsaufzeichnungen und keine Anleitungen.

---

## Bekannte Einschränkungen

- **Klinkenbuchse:** Mit bereits eingestecktem Stecker zu booten funktioniert
  unzuverlässig, und das Abziehen kann das Mikrofon rund dreißig Sekunden
  blockieren ([#117](https://github.com/wilbowes/EchoMuse/issues/117),
  [#141](https://github.com/wilbowes/EchoMuse/issues/141)). Im laufenden
  Betrieb ein- und ausstecken geht.
- **Mehrfach-Tippen** auf die Aktionstaste (doppelt, dreifach) wird im
  Controller gemessen und ist deshalb von Netzwerkschwankungen abhängig.
  Langes Drücken wird auf dem Gerät gemessen und ist zuverlässig.
- Nur die **2. Generation** des Echo Dot wird unterstützt.

---

## Umstieg von EchoMuse

> **Bitte vollständig lesen, bevor du umstellst.** Revoice ist die
> umbenannte Fortsetzung von EchoMuse, und die Umbenennung geht durch bis in
> Dateinamen, Pfade und den Namen des Docker-Images. Eine bestehende
> Installation läuft nach dem Update **nicht** einfach weiter.
>
> Was sich ändert und was du tun musst:
>
> - **Die Datenbank heißt jetzt `revoice.db`.** Benenne `echomuse.db` in
>   deinem Datenverzeichnis um, sonst startet der Controller mit einer leeren
>   Datenbank — alle Geräte, Einstellungen und Statistiken wären weg. Die
>   alte Datei wird nicht gelöscht, sie wird nur nicht mehr gefunden.
> - **Die Zertifikate stimmen nicht mehr überein.** Der Servername im
>   TLS-Zertifikat war `echomuse-controller` und heißt jetzt
>   `revoice-controller`. Bestehende Zertifikate in `data/tls/` passen
>   dazu nicht mehr. Lösche das Verzeichnis, damit eine neue
>   Zertifizierungsstelle entsteht, und schiebe anschließend jedem Gerät über
>   **Sicherer Link** im Dashboard neue Zugangsdaten. Bis das passiert ist,
>   verbinden sich die Geräte unverschlüsselt weiter — es sei denn, du hast
>   `REQUIRE_DEVICE_TLS=1` gesetzt, dann verbinden sie sich gar nicht mehr.
> - **Der Ablageort auf dem Gerät heißt jetzt `/data/local/etc/revoice`.**
>   Neue Firmware sucht dort. Der Assistent und die Aktion **Sicherer Link**
>   legen die Dateien am neuen Ort an; alte Firmware liest weiter den alten.
> - **Das Docker-Image heißt jetzt `revoice-controller`.** Es muss erst
>   einmal unter dem neuen Namen veröffentlicht werden, bevor Add-on-Updates
>   wieder durchlaufen.
>
> Wer von *upstream* EchoMuse kommt, findet in
> [docs/fork-switchover.md](docs/fork-switchover.md) das vollständige
> Verfahren samt Rückweg.

---

## Selbst bauen

Der Dot läuft auf FireOS 5 (Android 5.1). Für die Firmware wird ein eigenes
Docker-Build-Image gebraucht — normales Go-Cross-Compiling erzeugt kein
lauffähiges Binary.

```bash
git submodule update --init
cd device && docker build -t revoice-compiler compiler/ && ./compile.sh
```

Controller aus dem Quelltext: `cd controller && pip install -r requirements.txt
&& python em_controller.py` (Python 3.12) oder `docker compose up --build`.

Tests laufen lokal und in CI bei jedem Push:

```bash
cd device     && go test ./...
cd controller && python -m pytest tests/
```

---

## Mitmachen

Fehlerberichte, Korrekturen und Hardware-Erkenntnisse sind alle willkommen —
siehe [CONTRIBUTING.md](CONTRIBUTING.md). Am hilfreichsten ist ein Issue mit
angehängtem Support-Bundle (Dashboard → Support → Bundle herunterladen): Es
enthält Logs, Versionen und Messwerte für eine Ferndiagnose, aber weder
Transkripte noch Aufnahmen noch Netzwerknamen.

Vorher lohnt ein Blick in die [FAQ](docs/faq.md). Wer systematisch testen
möchte, findet im [UAT-Leitfaden](docs/uat.md) eine Checkliste.

---

## Dank

Revoice baut auf [EchoMuse](https://github.com/wilbowes/EchoMuse) von
wilbowes auf und ist ein Fork davon.

- [EchoGo](https://github.com/Binozo/EchoGo) und [GoTinyAlsa](https://github.com/Binozo/GoTinyAlsa) — Binozo, das SDK, das diese Hardware überhaupt zugänglich gemacht hat
- [amonet-biscuit](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-dot-2nd-gen-2016-biscuit.4761416/) — R0rt1z2, der Unlock
- [EchoCLI](https://github.com/Dragon863/EchoCLI) — Dragon863
- [SpeexDSP](https://gitlab.xiph.org/xiph/speexdsp) — Xiph.Org Foundation, Echo-Canceller auf dem Gerät
- [DTLN](https://github.com/breizhn/DTLN) — Nils L. Westhausen, Rauschunterdrückung im Controller
- [openWakeWord](https://github.com/dscripka/openWakeWord) — David Scripka, Wakeword-Modelle und Trainingspipeline

---

## Lizenz

MIT — siehe [LICENSE](LICENSE).

Revoice bindet mehrere Fremdkomponenten ein, die jeweils ihre eigene Lizenz
behalten. Sie sind in [NOTICE.md](NOTICE.md) aufgeführt; das Geräte-Binary
verlinkt zwei BSD-3-Clause-Komponenten, deren Copyright-Hinweise diese Datei
stellvertretend trägt.

*Revoice steht in keiner Verbindung zu Amazon. „Amazon", „Echo", „Echo Dot"
und „Alexa" sind Marken von Amazon.com, Inc.*
