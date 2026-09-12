# Einen bestehenden Echo auf emOS umstellen

Diese Anleitung ist für ein Gerät, das **schon in deiner Flotte läuft**. Für
einen frischen Echo nimm den [Schnellstart](quickstart.md) — der emOS-Ablauf
ist dort die Voreinstellung.

---

> **⚠️ Das hier schreibt die Boot-Partition deines Echo. Lies den ganzen
> Abschnitt „Bevor du anfängst", bevor du den Assistenten startest.** Ein
> Fehler an dieser Stelle kostet dich nicht eine Einstellung, sondern ein
> Gerät, das nur über ein Kabel und TWRP zurückzuholen ist.

---

## Was emOS bringt

emOS ersetzt Amazons Android-Userspace vollständig und behält nur den Kernel
des Geräts. Gegenüber FireOS auf derselben Hardware:

- **Kein `mediaserver`.** Damit verschwindet die ganze Fehlerklasse rund um
  Klinkenbuchse und Lautsprecher: niemand nimmt Revoice das PCM nach einem
  Update weg, niemand schreibt unsere Mixer-Einstellungen zurück.
- **Keine Default-Deny-Firewall.** Spotify Connect und AirPlay sind erreichbar,
  ohne dass die Firmware Regeln schreiben muss.
- **Eine serielle Konsole über USB**, mit dem LED-Ring als Boot-Fortschritt.
  Ein Gerät, das nicht hochkommt, kann man fragen statt raten.
- **`/init recovery`** startet aus dieser Konsole in TWRP. Amazons eigenes
  `reboot` kann das auf emOS nicht.
- **Rollback**: ein Boot gilt als bestätigt, wenn das Netzwerk da ist; nach
  drei unbestätigten Boots stellt emOS das letzte funktionierende Image wieder
  her und zeigt einen bernsteinfarbenen Ring.

## Was mitkommt und was nicht

`/data` überlebt das Schreiben der Boot-Partition. Deshalb ist das hier eine
Migration und keine Neuinstallation.

**Kommt mit:**

| | wo es liegt |
|---|---|
| Die Revoice-Firmware und ihr Startskript | `/data/local/bin/` |
| Link-Zugangsdaten (CA + Token) | `/data/local/etc/revoice/` |
| Der gemerkte Controller | `/data/local/etc/revoice/controller.json` |
| Mute-Zustand | `/data/local/etc/revoice/state.json` |
| Das Konsolenpasswort | `/data/local/etc/revoice/console.pw` |
| WLAN-Konfiguration | `/data/misc/wifi/wpa_supplicant.conf` |
| Wakeword-Modelle und die ONNX-Runtime | `/data/local/share/revoice/oww/` |

**Die Seriennummer ändert sich nicht**, also auch nicht die Geräte-ID. Der
Controller spricht nach dem Neustart mit derselben Zeile in seiner Datenbank:
deine per-Gerät-Konfiguration, die Aktivitätshistorie und **alle
Home-Assistant-Entitäts-IDs** bleiben, wie sie sind.

**Kommt nicht mit:** alles, was im Boot-Image steckt. Das ist der Punkt der
Übung. Praktisch heißt das: der Magisk-`service.d`-Eintrag und der
`service revoice`-Eintrag in Androids init.rc verschwinden — emOS startet
Revoice über seine eigene Diensttabelle, also ist das kein Verlust.

**Was der Assistent NICHT tun darf:** das Gerät vorher löschen. Er bietet das
an, und für eine echte Neuinstallation ist es richtig — für eine Migration
wirft es die Gerätekonfiguration weg und ändert jede
Home-Assistant-Entitäts-ID, weil HA Entitäten an der Geräteidentität festmacht
und ein neu hinzugefügtes Gerät ein neues ist. **Wähle „Migrieren".**

## Bevor du anfängst

1. **amonet-biscuit v1.1.0, nicht v2.0.0.** v2.0.0 (10. September 2026)
   ersetzt die Bootloader, und danach bootet FireOS 5 nicht mehr — und damit
   auch emOS nicht, das den FireOS-5-Kernel benutzt. Ist v2.0.0 schon drauf:
   **versuche nicht, durch Flashen von FireOS 5 oder eines älteren amonet
   zurückzukommen.** Das heißt Bootloader von Hand schreiben, und genau so
   wird ein Echo hart gebrickt. Einzelheiten ganz oben in
   [rooting.md](rooting.md).
2. **Bring die Firmware zuerst auf den aktuellen Stand.** Ab v2.33.0-fx.1
   sind alle Android-Aufrufe der Firmware auf emOS abgestimmt — davor läuft
   sie dort zwar, aber der WLAN-Wechsel aus dem Dashboard lehnt jedes Mal ab.
   Gerät → **Updates** im Dashboard.
3. **Ein Chromium-basierter Browser** (Chrome oder Edge) — der Assistent
   spricht per WebUSB und WebSerial mit dem Gerät.
4. **Ein USB-Kabel und ein paar Minuten Zeit.** Der Ablauf läuft komplett in
   TWRP; das Gerät ist währenddessen nicht erreichbar.
5. **Status 0.4: auf der Werkbank bewährt, nicht im Feld.** Eine Handvoll
   Geräte über eine Handvoll Tage. Die bekannten Lücken stehen in
   [`emos/README.md`](../emos/README.md).

## Der Ablauf

Dashboard → **Einrichtungsassistent**, emOS-Ablauf (die Voreinstellung).

1. **Gerät verbinden** — der Echo hängt am USB, läuft unter Android. Der
   Assistent erkennt die Seriennummer, findet sie in der Flotte und hält an.
   **Hier auf „Migrieren" klicken**, dann den Schritt erneut ausführen.
2. **Mit TWRP verbinden** — alles Weitere passiert in der Recovery.
3. **Boot-Image sichern** — der Assistent liest deine Boot-Partition aus und
   gibt dir die Datei. **Speichere sie und behalte sie.** Sie ist der
   Bauinput *und* der Rückweg, und zurückzuspielen dauert etwa zehn Sekunden.
   Ohne sie gibt es keinen bequemen Weg zurück.
4. **Revoice installieren / Wakeword-Assets** — beides liegt schon auf
   `/data`. Neu zu schreiben schadet nicht und stellt sicher, dass die
   Binärdateien zum aktuellen Controller passen.
5. **emOS bauen** — der Controller packt *dein* gesichertes Image mit dem
   emOS-Init neu zusammen und benutzt deinen Kernel und deine Device Trees
   wieder. Nichts von Amazon wird dabei verteilt oder gespeichert.
6. **Flashen und prüfen** — schreiben, zurücklesen, vergleichen.
7. **Neu starten und zuschauen** — der erste Boot über die serielle Konsole,
   während der Ring sich füllt. Kaltstart bis Netzwerk sind etwa 35 Sekunden.
8. **WLAN** — dein Gerät hat seine Konfiguration schon auf `/data`, es sollte
   sich also von selbst verbinden. Der Schritt wartet nur darauf, dass es sich
   beim Controller zurückmeldet.

## Danach

- Gerät → **Status** zeigt `emos` als Basissystem.
- **Config → Advanced → USB console** wird aktiv. Das Feld war ausgegraut,
  solange kein Gerät der Flotte emOS meldet — FireOS benutzt adb. Setze ein
  Konsolenpasswort: die Konsole ist sonst eine unauthentifizierte Root-Shell,
  und der WLAN-Schlüssel liegt auf dem Gerät.
- Der Firewall-Schritt der Firmware wird zum Leerlauf. Die Regeln werden noch
  geschrieben — `iptables` liegt auf Amazons `/system`, das emOS mountet —
  aber in eine Tabelle, die ohnehin alles annimmt.
- Debloat-Skript und pm-Hide-Liste werden nicht mehr an das Gerät geschickt.
  Der Controller sieht `base_os: emos` und lässt sie weg.

## Wenn etwas schiefgeht

**Der Ring bleibt rot stehen.** Eine Boot-Stufe ist gescheitert, an der
Position, die er erreicht hat. Nur zwei Stufen können so scheitern: `/system`
einhängen (Position 2) und `/data` einhängen (Position 4).

**Der Ring wird bernsteinfarben.** emOS stellt das letzte funktionierende
Image wieder her und startet neu. Das ist der eingebaute Rollback, der tut,
was er soll.

**Nichts passiert, der Ring dreht blau weiter.** Der Kernel läuft, unser Init
nicht. Spiel das gesicherte Boot-Image zurück:

```sh
# in TWRP, über adb
dd if=boot_gesichert.img of=/dev/block/mmcblk0p10
```

Das Gerät ist danach wieder auf FireOS, mit `/data` unangetastet — also mit
Revoice und deiner kompletten Konfiguration.

**Du willst zurück auf FireOS, ohne dass etwas kaputt ist.** Genauso: das
gesicherte Image zurückschreiben. Zehn Sekunden, `/data` bleibt.

**Du hast das gesicherte Image nicht mehr.** Dann bleibt nur der harte Weg:
TWRP, `cache` und `data` löschen, FireOS 5 per Sideload, danach `f1r30s.zip`
— dieser letzte Schritt ist nicht optional, ohne ihn bootet das System nicht.
Das **löscht `/data`**, also Revoice und deine Konfiguration. Genau deshalb
ist das Sichern in Schritt 3 keine Formalität.
