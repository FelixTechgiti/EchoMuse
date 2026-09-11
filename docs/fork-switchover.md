# Eine bestehende Installation auf diesen Fork umstellen

Du betreibst Revoice aus `wilbowes/EchoMuse` — das Add-on, den
veröffentlichten Container oder beides — und möchtest stattdessen diesen Fork
betreiben.

**Lies die ganze Seite, bevor du anfängst.** Zwei der Schritte lassen sich
aus dem Dashboard nicht rückgängig machen, und einer davon (`tls/`) macht
jedes Gerät verbindungsunfähig, ohne dass irgendwo ein Fehler auftaucht, wo
du nachsehen würdest.

---

## Was gleich ist und was nicht

Der Fork ist dasselbe Programm. Gleiches Wire-Protokoll, gleiches Schema,
gleiche Home-Assistant-Entitäten, gleiches Dashboard. Was sich unterscheidet:

| | upstream | dieser Fork |
|---|---|---|
| Add-on-Repository | `github.com/wilbowes/EchoMuse` | `github.com/FelixTechgiti/Revoice` |
| Controller-Image | `ghcr.io/wilbowes/echomuse-controller` | `ghcr.io/felixtechgiti/revoice-controller` |
| Firmware-Releases | die `v*`-Tags von upstream | die `v*`-Tags dieses Forks |

Dazu die Funktionen, die dieser Fork auf dem Gerät ergänzt: die Klangkette,
die nach dem Mischen auf dem Echo selbst läuft, der LED-Ring als
Home-Assistant-Lampe mit Benachrichtigungseffekten, ein Einweg-Mute-Schalter
und das Gerät, das eigene Musik abspielt (Sendspin, Spotify Connect, AirPlay
— siehe [configuration.md](configuration.md)).

**Das Datenbankschema ist bytegleich mit dem von upstream.** Das ist Absicht,
und es ist das, was diesen Schritt umkehrbar macht: Eine Datenbank, auf der
dieser Fork gelaufen ist, startet weiterhin auf dem Upstream-Controller.
Nichts hier ist eine Einbahnstraße außer den zwei unten genannten Dingen, und
beide sind mit dem Backup zu retten, das du gleich anlegst.

---

## Die zwei Dinge, die beißen

### 1. Ein anderes Repository bedeutet ein anderes `/data`

Home Assistant leitet das Speicherverzeichnis eines Add-ons aus der
Repository-URL ab. Das Add-on dieses Forks zu installieren gibt dir also ein
**neues, leeres** `/data` — eine neue Datenbank und, weit wichtiger, eine
**neu erzeugte Zertifizierungsstelle**.

Deine Geräte tragen die alte. Sie nehmen `wss` aus dem mDNS-Eintrag des
Controllers statt aus `require_device_tls`, wählen also TLS, scheitern beim
Prüfen einer nie gesehenen Zertifizierungsstelle und trennen die Verbindung.
Sie kommen nie so weit, im Dashboard aufzutauchen — dort ist also nichts, was
es erklären würde.

Sich davon zu erholen heißt, jeden Dot per USB anzuschließen und mit dem
Einrichtungsassistenten frische Zugangsdaten aufzuspielen. **Das alte `/data`
vorher hinüberzukopieren vermeidet das vollständig** — das Vorgehen ist genau
das aus
[migrate-to-addon.md § Deine Daten hineinkopieren](migrate-to-addon.md#3-deine-daten-hineinkopieren),
nur dass die Quelle das Verzeichnis des anderen Add-ons ist statt eines
`docker-compose`-Hostpfads:

```bash
# Auf dem Home-Assistant-Host (Advanced SSH & Web Terminal, Schutzmodus aus).
# Der Ordnername ist ein Hash der Repository-URL, es gibt also jetzt ZWEI
# *_controller-Verzeichnisse, und du musst wissen, welches welches ist.
ls -ld /mnt/data/supervisor/{apps,addons}/data/*_controller
```

Das nach Änderungszeit ältere gehört upstream. Kopiere es **vor dem ersten
Start** des Fork-Add-ons in das neue — oder direkt danach, wenn du es einmal
gestartet und wieder gestoppt hast; vorher existiert das Verzeichnis nicht.

Wenn du statt des Add-ons den eigenständigen Container betreibst, gilt nichts
davon: Das Datenverzeichnis gehört dir und zieht nicht um. Ändere den
Image-Namen in `docker-compose.deploy.yml` und mach ein `docker compose pull`.

### 2. Beide Add-ons benutzen dieselben Ports

Host-Networking, 8767 / 8768 / 8770. **Stoppe das Upstream-Add-on, bevor du
dieses startest** — was als Zweites startet, kann sich nicht binden, und
solange beide laufen, hängen sich Geräte an die mDNS-Antwort, die zuerst kam,
was so aussieht, als hätte der Wechsel zufällig halb funktioniert.

---

## Versionsnummern

Fork-Releases tragen ein Suffix `-fx.N` — `controller-v2.23.0-fx.1`,
`v2.15.0-fx.1`. Upstreams Tags werden vom wöchentlichen Abgleich in dieses
Repository geholt; ein Fork-Release mit einer Upstream-Nummer würde beim
nächsten Abgleich kollidieren und ein Image veröffentlichen, das eine Version
behauptet, die es nicht ist.

Weitere Bedeutung hat es nicht: Der Controller ignoriert das Suffix beim
Vergleichen, `2.23.0-fx.1` ist also weder vor noch hinter `2.23.0`.

## Update-Quellen

`github_repo` in der Datenbank entscheidet, wohin **beide** Update-Wege
schauen: Der OTA-Abfrager liest die Releases dieses Repositorys für
Geräte-Firmware, und der Controller-Hinweis im Dashboard liest dessen
`controller-v*`-Tags.

Eine frische Datenbank bekommt diesen Fork. Eine **aus einer
Upstream-Installation kopierte** Datenbank hat beim Anlegen upstreams Namen
gespeichert, deshalb stellt der Controller sie einmal beim ersten Start um und
sagt es im Log:

```
[db] Update source set to FelixTechgiti/Revoice — this database was created by an upstream controller.
```

Zwischengespeicherte Release-Informationen aus dem alten Repository werden
zugleich verworfen, damit das Dashboard frisch abfragt, statt bis zur nächsten
zufälligen Prüfung upstreams neueste Version anzuzeigen.

Das passiert **einmal**. Wenn du lieber upstreams Firmware verfolgen möchtest,
stell es zurück, dann bleibt es so:

```
PATCH /api/system/config   {"github_repo": "wilbowes/EchoMuse"}
```

---

## Firmware

Controller und Firmware werden unabhängig versioniert, und jede Kombination
funktioniert — die beiden Hälften handeln nach Fähigkeiten aus, nicht nach
Version. **Stelle also zuerst den Controller um und lass die Geräte in Ruhe.**
Nichts geht kaputt: Ein Gerät mit Upstream-Firmware kündigt die Fähigkeiten
des Forks einfach nicht an, und jedes Bedienelement, dessen Funktion ihm
fehlt, wird ausgegraut mit Begründung angezeigt.

Aktualisiere die Firmware danach, ein Gerät nach dem anderen, im Reiter
„Updates" des Dashboards. Das Gerät behält sein vorheriges Binary im anderen
Slot und fällt nach drei schnellen Abstürzen von allein darauf zurück — ein
schlechtes Update kostet also einen Neustart und nicht ein Gerät.

---

## Zurück

1. Setze `github_repo` zurück auf `wilbowes/EchoMuse` (siehe oben), wenn dir
   wieder upstreams Firmware angeboten werden soll.
2. Stoppe dieses Add-on, starte das von upstream.
3. Kopiere `/data` in die andere Richtung, wenn du lange genug gelaufen bist,
   dass dir die Historie etwas bedeutet — das Schema ist identisch, es startet
   also.

Geräte-Firmware aus diesem Fork läuft gegen den Upstream-Controller. Die
zusätzlichen Fähigkeiten, die sie ankündigt, werden ignoriert — das
dokumentierte Verhalten für eine unbekannte Fähigkeit, in beide Richtungen.

Ein Haken, und der gehört upstream und nicht dem Fork: Der
Upstream-Controller tritt **nicht** von der Klangbearbeitung zurück, wenn ein
Gerät `output_chain` ankündigt. Jede Firmware, die die Klangkette selbst
betreibt — auch upstreams eigene —, bekommt EQ, Bass-Schutz und Limiter also
doppelt, einmal an jedem Ende. Zwei Limiter hintereinander sind hörbar. Wenn
du zurückgehst und der Klang stimmt nicht, ist das die Stelle zum Nachsehen;
`limiterEnabled` auf der Geräteseite auszuschalten ist der schnellste Weg,
das zu bestätigen.
