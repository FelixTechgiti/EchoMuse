# Vom Docker-Container zum Home-Assistant-Add-on umziehen

Du betreibst den Revoice-Controller als eigenständigen Docker-Container und
möchtest ihn stattdessen als Home-Assistant-Add-on laufen lassen. So geht das,
ohne deine Geräte, deine Einstellungen oder die Home-Assistant-Entitäten zu
verlieren, auf denen deine Automationen schon aufbauen.

> **Begriffe.** Home Assistant benennt „Add-ons" gerade in „Apps" um. Je nach
> Version siehst du das eine oder das andere Wort, und die Pfade in dieser
> Anleitung tauchen in beiden Schreibweisen auf. Es ist dasselbe.

**Lies die ganze Seite, bevor du anfängst.** Der mit Abstand wichtigste
Schritt — `tls/` kopieren — kommt früh, und ihn zu überspringen macht jedes
Gerät verbindungsunfähig, auf eine Weise, die nicht offensichtlich ist und
sich nicht aus dem Dashboard beheben lässt.

---

## Was tatsächlich umziehen muss

Alles, was sich der Controller merkt, liegt in einem Verzeichnis neben der
Datenbankdatei. Bei einer üblichen `docker-compose`-Installation ist das
`controller/data/` auf deinem Host:

```
data/
├── revoice.db      ← Geräte, Einstellungen, Benutzer, Verlauf
├── tls/             ← die Zertifizierungsstelle, der deine Geräte vertrauen
├── oww_models/      ← eigene Wakeword-Modelle, falls du welche trainiert hast
└── recordings/      ← gespeicherte Äußerungen, falls du das eingeschaltet hast
```

Kopiere **das ganze Verzeichnis**. Nur die Datenbank zu nehmen ist der
häufigste Weg, das falsch zu machen.

### Warum `tls/` das ist, was beißt

Jedes Gerät speichert eine Kopie der Zertifizierungsstelle deines Controllers
und weigert sich, mit irgendetwas zu sprechen, das den passenden Schlüssel
nicht nachweisen kann. Diese Zertifizierungsstelle wird **einmal, beim ersten
Start** erzeugt, und ein frischer Controller erzeugt eine **neue**.

Startest du das Add-on also mit leerem Datenverzeichnis, legt es eine
Zertifizierungsstelle an, die deine Geräte nie gesehen haben. Sie wählen dann
`wss://`, scheitern beim Prüfen und trennen die Verbindung — wieder und
wieder, still, und im Dashboard steht nichts, was es erklärt, weil sie nie
weit genug kommen, um überhaupt im Dashboard aufzutauchen.

`require_device_tls` auszuschalten rettet das **nicht**. Geräte entscheiden
sich anhand des mDNS-Eintrags, den der Controller ankündigt, für TLS — nicht
anhand dieser Einstellung.

Sich davon zu erholen heißt, jeden Dot körperlich per USB anzuschließen und
frische Zugangsdaten aufzuspielen. Vier Dateien zu kopieren vermeidet das.

---

## Bevor du anfängst

**1. Notiere deine Controller-Version.**

Sie steht in der Kopfzeile des Dashboards, oder:

```bash
docker exec revoice-controller printenv EM_CONTROLLER_VERSION
```

(Steht dort `dev`, hast du das Image selbst gebaut, und nur du weißt, was
drin ist.)

Das Add-on muss **dieselbe Version oder neuer** sein. Datenbank-Upgrades gehen
nur vorwärts; ein Controller, der auf eine Datenbank einer neueren Version
trifft, weigert sich zu starten, statt zu raten. Falls dein Container der
veröffentlichten Add-on-Version voraus ist, warte, bis das Add-on aufholt.

Mehrere Versionen auf einmal zu überspringen ist in Ordnung und vorgesehen —
der Controller wendet in einem Start jedes fehlende Upgrade an und legt vorher
selbst ein Backup an (`revoice.db.pre-v<N>.bak`, neben der Datenbank).

**2. Notiere deine Einstellungen.**

Deine `.env`-Datei kommt **nicht** mit. Das Add-on hat einen eigenen
Optionsbildschirm, und du trägst diese Werte von Hand neu ein:

| `.env` | Add-on-Option |
|---|---|
| `SERVER_IP` | **Server IP** |
| `SERVER_HOST` | **Server host** |
| `MDNS_NAME` | **mDNS name** |
| `OWW_MODEL` | **Wake word model** |
| `OWW_THRESHOLD` | **Wake word threshold** |
| `DEVICE_APPROVAL` | **Device approval** |
| `REQUIRE_DEVICE_TLS` | **Require device TLS** |
| `ESPHOME_PROJECT_VERSION` | **ESPHome project version** |
| `DEBUG` | **Debug** |

Vier haben keine Option, teils absichtlich, teils nicht:

- `DB_PATH` und `API_PORT` sind im Add-on **fest**. Die Datenbank muss dort
  liegen, wo Home Assistant den Add-on-Speicher hält, und der Dashboard-Port
  muss zu dem passen, den Home Assistant weiterleitet.
- `SERVER_PORT` und `SERVER_TLS_PORT` sind **noch nicht herausgeführt**
  ([#163](https://github.com/wilbowes/EchoMuse/issues/163)). Wenn du einen
  davon vom Standard abweichend gesetzt hast, sag das vor dem Umzug in jenem
  Issue — deine Geräte sind auf diese Ports eingestellt.

**`SERVER_IP` ändert sich meistens**, weil der Controller auf deine
Home-Assistant-Maschine umzieht. Setze sie auf deren LAN-Adresse, nicht die
alte. Geräte finden den Controller per mDNS, sie folgen also — siehe
[Deine Geräte](#deine-geräte-finden-den-neuen-controller-von-allein) weiter
unten.

**3. Mach ein Backup.** Du kopierst, du verschiebst nicht — dein
ursprüngliches Datenverzeichnis ist dein Rückweg. Lösche es nicht, bevor du
zufrieden bist.

---

## Die Schritte

### 1. Den eigenständigen Controller stoppen

```bash
cd /pfad/zu/Revoice/controller
docker compose down
```

**Das ist nicht optional und nicht bloß Ordnungsliebe.** Zwei Controller in
einem Netzwerk kündigen sich beide an, und ein Gerät nimmt den ersten, den es
findet und prüfen kann — es gibt keine Möglichkeit, sie auseinanderzuhalten
([#106](https://github.com/wilbowes/EchoMuse/issues/106)). Den alten laufen zu
lassen bedeutet, dass Geräte sich an den hängen, der zuerst geantwortet hat —
und das sieht aus, als hätte der Umzug zufällig halb funktioniert.

Es zählt auch für Home Assistant: Es richtet ein ESPHome-Gerät nur dann auf
eine neue Adresse aus, wenn die alte tatsächlich verschwunden ist.

### 2. Das Add-on installieren, dann stoppen

Repository hinzufügen (**Einstellungen → Add-ons → Add-on-Store → ⋮ →
Repositories**):

```
https://github.com/FelixTechgiti/Revoice
```

**Revoice** installieren, dann **einmal starten und wieder stoppen**. Dieser
erste Start legt das Speicherverzeichnis an, in das du gleich kopierst. Trage
noch keine Optionen ein und öffne das Dashboard nicht — lässt du es laufen,
erzeugt es die Zertifizierungsstelle, die du gleich ersetzt. (Kein Schaden,
wenn doch; du überschreibst sie im nächsten Schritt.)

### 3. Deine Daten hineinkopieren

Der Speicher des Add-ons ist weder über Samba noch über den File Editor
sichtbar, dieser Schritt braucht also Shell-Zugriff auf den
Home-Assistant-Host. Installiere das Community-Add-on **Advanced SSH & Web
Terminal** und schalte in seiner Konfiguration den **Schutzmodus aus** — ohne
das sieht es das Host-Dateisystem nicht.

Finde das Verzeichnis. Der Pfad hat sich geändert, als Home Assistant Add-ons
in Apps umbenannt hat, prüfe also beide:

```bash
ls -d /mnt/data/supervisor/apps/data/*controller 2>/dev/null || \
ls -d /mnt/data/supervisor/addons/data/*controller 2>/dev/null
```

Der Ordnername ist ein Hash gefolgt von `_controller` — der Hash leitet sich
aus der Repository-URL ab und unterscheidet sich zwischen Installationen, nimm
also den Platzhalter, statt den Pfad von jemand anderem zu kopieren.

Bring dein `data/`-Verzeichnis auf die Home-Assistant-Maschine (`scp`, ein
USB-Stick, ein `/share`-Ordner — was dir passt), dann:

```bash
DEST=$(ls -d /mnt/data/supervisor/{apps,addons}/data/*controller 2>/dev/null | head -1)
cp -a /pfad/zu/deinem/data/. "$DEST"/
ls -la "$DEST" "$DEST/tls"
```

Die letzte Zeile ist die Prüfung, auf die es ankommt. Du solltest `revoice.db`
sehen und ein Verzeichnis `tls/` mit **vier** Dateien. Sind es in `tls/`
weniger, halte an und finde den Rest — Serverzertifikat und
Zertifizierungsstelle müssen zueinander passen.

### 4. Einstellungen eintragen und starten

Trage die Optionen aus Schritt 2 von *Bevor du anfängst* ein und denke daran,
**Server IP** auf die Adresse der Home-Assistant-Maschine zu ändern. Starte
das Add-on und sieh in sein Log.

Auf `Opening database: /data/revoice.db` sollten deine Gerätenamen folgen,
sowie sie sich verbinden. Ist das Add-on neuer als dein Container, siehst du
außerdem `Running N migration(s) from vX`, davor eine Zeile
`Backed up vX schema to …` und danach `Schema migrated to vY` — das ist das
Upgrade, das genau das tut, was es soll.

Öffne dann das Dashboard aus der Seitenleiste. Beachte die Warnung dazu, wer
es als Erstes öffnet, unter [Das Anmelden ändert sich](#das-anmelden-ändert-sich).

### 5. Deine Geräte prüfen

Trenne einen Dot kurz vom Strom, oder warte einfach — Geräte versuchen es von
allein erneut. Innerhalb ein bis zwei Minuten sollte er im Dashboard als
verbunden erscheinen und in der Zeile **Link** auf seinem Status-Reiter
`wss (TLS)` zeigen.

Erscheint er stattdessen als **ausstehende Freigabe**, ist deine Datenbank
nicht mitgekommen. Der Controller behandelt ihn als Gerät, das er nie getroffen
hat. Halt an, prüfe Schritt 3 und versuche es erneut — ihn hier freizugeben
würde einen zweiten Eintrag für dieselbe Hardware mit frischen Einstellungen
anlegen.

---

## Deine Geräte finden den neuen Controller von allein

Du musst keinen Dot anfassen. Sie finden den Controller per mDNS und folgen
ihm bei ihrer nächsten Verbindung zur neuen Maschine. Das Zertifikat, das sie
schon haben, prüft weiterhin durch, weil du `tls/` kopiert hast.

## Home Assistant richtet sich neu aus, wenn du die Datenbank kopiert hast

Jedes Revoice-Gerät erscheint in Home Assistant als ESPHome-Gerät auf einem
eigenen Port. Diese Portnummern stehen in der Datenbank, sie zu kopieren hält
also jedes Gerät auf dem Port, den Home Assistant bereits kennt.

Home Assistant identifiziert sie über eine MAC-Adresse, die aus der
Seriennummer des Dots abgeleitet ist, und wenn es diese MAC an einer neuen
Adresse sieht, **aktualisiert es den bestehenden Eintrag, statt einen neuen
anzulegen** — vorausgesetzt, der alte antwortet nicht mehr, weshalb Schritt 1
wichtig war. Deine Entitäten, ihre `entity_id`s und jede darauf gebaute
Automation überleben.

Hast du die Datenbank nicht kopiert, sieht Home Assistant unbekannte Geräte
auf unbekannten Ports und bietet sie als neue Funde an. Deine Automationen
zeigen dann auf Entitäten, die es nicht mehr gibt. Das lässt sich nicht durch
erneutes Freigeben retten; spiel die Datenbank ein.

## Das Anmelden ändert sich

Im eigenständigen Container hast du dich mit einem lokalen Revoice-Konto
angemeldet. Unter dem Add-on hat Home Assistant dich bereits authentifiziert
und reicht deine Identität an Revoice weiter — es gibt also keinen
Anmeldebildschirm und keine Schaltfläche **Abmelden**; abmelden würde dich
direkt wieder anmelden.

**Wer das Dashboard als Erstes öffnet, wird Administrator.** Alle anderen in
deinem Haushalt, die es öffnen, bekommen Lesezugriff.

**Öffne es selbst, als Erstes, bevor du jemandem erzählst, dass es existiert.**
Home Assistant zeigt den Eintrag in der Seitenleiste Admin-Benutzern, also
kann jeder mit einem Home-Assistant-Adminkonto vor dir dort sein.

Wenn das doch jemand tut, kann ein Administrator es unter **Einstellungen →
Benutzer** richten — dort sind alle Konten aufgelistet und lassen sich hoch-
oder herunterstufen. Der Haken: Der Bildschirm ist Administratoren
vorbehalten, es muss also *diese Person* sein, in deinem Namen. Der letzte
Administrator kann nicht herabgestuft werden, es lässt sich also niemand
komplett aussperren.

Deine alten lokalen Konten stehen weiterhin in der Datenbank und werden nicht
gelöscht, aber sie werden nicht benutzt, solange du über Home Assistant
hereinkommst.

Lesezugriff ist hier bedeutsam: Aufnahmen und Transkripte sind
Administratoren vorbehalten, und das Dashboard bietet zu jedem Gerät eine
Root-Shell an.

---

## Wenn du zurück möchtest

Dein ursprüngliches `data/`-Verzeichnis ist unangetastet — das Add-on hat
daraus kopiert und schreibt in seinen eigenen Speicher. Zurück zum Container:
Add-on stoppen, dann `docker compose up -d` wie zuvor.

Der einzige Haken ist die Fahrtrichtung. Wenn das Add-on lange genug lief, um
ein Datenbank-Upgrade anzuwenden, ist *seine* Kopie vorangeschritten und dein
Original nicht. Zurückzugehen heißt, zu deinen Daten zurückzugehen, wie sie
waren, und alles zu verlieren, was dazwischen passiert ist. Entscheide dich
innerhalb eines Tages, nicht eines Monats.

---

## Fallstricke, an einer Stelle

- **`tls/` kopieren, sonst geht nichts** — auf eine Weise, die dir keinen
  brauchbaren Fehler gibt und sich nicht aus der Ferne beheben lässt.
- **Zuerst den alten Controller stoppen.** Zwei Controller in einem Netzwerk
  sind für ein Gerät ununterscheidbar, und Home Assistant richtet ein Gerät
  nicht neu aus, das an seiner alten Adresse noch antwortet.
- **Das Add-on darf nicht älter sein als dein Container.**
  Datenbank-Upgrades sind eine Einbahnstraße.
- **`.env` zieht nicht mit um.** Trage die Optionen von Hand neu ein und
  aktualisiere `SERVER_IP` auf die Home-Assistant-Maschine.
- **`SERVER_PORT` / `SERVER_TLS_PORT` haben noch keine Add-on-Option**
  ([#163](https://github.com/wilbowes/EchoMuse/issues/163)). Wenn du sie
  geändert hast, frag vor dem Umzug nach.
- **Öffne das Dashboard selbst zuerst** — der erste Home-Assistant-Benutzer
  durch die Tür wird Administrator.
- **Early Access ist ein eigenes Add-on mit eigenem Speicher.** Es zu
  installieren ist *noch ein* Umzug mit allem oben Genannten, `tls/`
  eingeschlossen. Den Kanal zu wechseln ist kein Schalter.
- **Ein `docker compose pull` auf der alten Installation kann sie still
  zurückbringen.** Wenn du Automatisierung hast, die zieht und neu startet,
  schalte sie ab — sonst kommt das Zwei-Controller-Problem Wochen später
  wieder, wenn du es vergessen hast.

## Wenn du feststeckst

Öffne ein Issue mit angehängtem Support-Bundle (**Settings → Support →
Collect bundle** im Dashboard, dann Herunterladen). Es enthält keine
Aufnahmen, keine Transkripte, keine Netzwerknamen und keine Kontonamen —
[support-bundle.md](support-bundle.md) sagt genau, was drin ist und was
nicht.
