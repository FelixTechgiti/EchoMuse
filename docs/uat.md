# Abnahmetests

**Eine Checkliste, um zu bestätigen, dass Revoice auf deiner Hardware, in
deinem Haus, mit deinem Home Assistant tatsächlich tut, was es verspricht.**
Arbeite so viel davon durch, wie auf dich zutrifft, und sag uns, was
gescheitert ist. Teilergebnisse sind nützlich — ein ordentlich erledigter
Abschnitt schlägt einen überflogenen Gesamtdurchlauf.

Nichts hier braucht Entwicklungskenntnisse. Jeder Test ist etwas, das du im
Dashboard, am Gerät oder in Home Assistant tun kannst.

---

## Bevor du anfängst

**Notiere diese vier Angaben.** Jede Meldung braucht sie, und die Hälfte
unserer Rückfragen ist eine davon.

| | Wo |
|---|---|
| Controller-Version | Kopfzeile des Dashboards |
| Firmware-Version | Gerät → Reiter **Status** |
| Home-Assistant-Version | HA → Einstellungen → Über |
| Hardware | Echo Dot Gen 2, oder welches Board |

**Nimm nach Möglichkeit eine Kopie deines Aufbaus.** Manche dieser Tests
löschen ein Gerät oder spielen Zugangsdaten auf. Wo ein Test destruktiv ist,
steht es fett dabei.

### Wie man einen Fehler meldet

1. Öffne ein Issue: <https://github.com/wilbowes/EchoMuse/issues>
2. Gib die **Test-Kennung** an (z. B. `D3`), was du erwartet hast und was
   passiert ist.
3. Häng ein Support-Bundle an — **Settings → Support → Collect bundle**, dann
   Herunterladen. Es enthält keine Transkripte, keine SSIDs, keine
   Gerätebezeichnungen, keine Zugangsdaten; siehe
   [support-bundle.md](support-bundle.md) für die genaue Liste. Öffne es,
   bevor du es verschickst.
4. Notiere die **Uhrzeit**, zu der der Fehler auftrat. Die Logs im Bundle
   haben Zeitstempel, und so finden wir ihn.

### Zwei Dinge, die keine Fehler sind

- **Ein ausgegrautes Bedienelement mit einer Begründung darunter.** Der
  Controller fragt jedes Gerät, was es kann, und deaktiviert, was deine
  Firmware nicht umsetzt. Das ist so gewollt. Ein Bedienelement, das
  *aktiviert* ist und stillschweigend nichts tut, ist ein Fehler — den melde.
- **Eine Einstellung, die auf einem Gerät anders steht als in der Flotte.**
  Die Konfiguration ist je Abschnitt zugeordnet. Gerät → Status → Zeile
  `Config` sagt `Fleet` oder `Local override (n of 6)`.

### Schon bekannt — bitte nicht erneut melden

Sieh das hier durch, bevor du etwas öffnest. Passt dein Symptom, ergänze
lieber deine Versionsnummern im bestehenden Issue.

| Symptom | Issue |
|---|---|
| HAs Dialog „Sprachsatellit einrichten" läuft in einen Timeout und braucht „Wiederholen", nachdem der Klang gespielt hat | [#219](https://github.com/wilbowes/EchoMuse/issues/219) |
| Die Wiedergabe bricht mitten in einer langen gesprochenen Antwort ab | [#324](https://github.com/wilbowes/EchoMuse/issues/324) |
| Radio- oder Stream-Wiedergabe wird unterbrochen | [#325](https://github.com/wilbowes/EchoMuse/issues/325) |
| Das Abziehen des Kopfhörersteckers blockiert das Mikrofon und wirft das Gerät ab | [#117](https://github.com/wilbowes/EchoMuse/issues/117) |
| Seltsames Verhalten mit etwas an der Klinkenbuchse | [#141](https://github.com/wilbowes/EchoMuse/issues/141) |
| Helligkeitssensor fehlt auf einem Gerät mit Seriennummer `G090LF` | [#90](https://github.com/wilbowes/EchoMuse/issues/90) |
| WLAN verbindet sich nach einem Neustart nie wieder, in einem Netz ohne Internet | [#317](https://github.com/wilbowes/EchoMuse/issues/317) |
| Anderswo gestartete Musik bleibt stumm, bis ein Sprachgespräch endet | [#262](https://github.com/wilbowes/EchoMuse/issues/262) |
| Doppel- und Dreifachtippen wird unzuverlässig erkannt | [#115](https://github.com/wilbowes/EchoMuse/issues/115) |
| Hohe CPU-Last auf dem Gerät | [#176](https://github.com/wilbowes/EchoMuse/issues/176) |

---

## A — Ein Gerät in Betrieb nehmen

### A1 · Ein Gerät rooten und einrichten
**Tu:** Lass den Einrichtungsassistenten von Anfang bis Ende auf einem Gerät
laufen, das nie gerootet war. Folge [rooting.md](rooting.md).
**Erwarte:** Jeder Schritt meldet Erfolg, und das Gerät startet in einen
Zustand, in dem das Dashboard es sieht.
**Melde:** Jeden Schritt, der Erfolg meldet, das Gerät aber falsch
zurücklässt. Leg die Diagnose des Assistenten bei — er bietet sie bei einem
gescheiterten Schritt an.

### A2 · Das Gerät erscheint und lässt sich freigeben
**Tu:** Öffne das Dashboard. Finde das neue Gerät.
**Erwarte:** Es erscheint als ausstehend mit einem Reiter **Approve** und
sonst nichts. Nach der Freigabe erscheint der volle Satz Reiter: Status,
Activity, Config, Console, Updates, Logs.
**Melde:** Ein Gerät, das nie erscheint, oder eines, das bereits freigegeben
erscheint.

### A3 · Der Status stimmt
**Tu:** Gerät → **Status**.
**Erwarte:** Seriennummer, Firmware-Version, WLAN, Lautstärke, Link,
Config-Bereich, Zeile „Voice assistant" — alle gefüllt, keine zeigt `—` für
etwas, das offensichtlich existiert.
**Melde:** Jede Zeile mit `—`, während das Beschriebene funktioniert.

### A4 · Neustart überstehen
**Tu:** Zieh dem Gerät den Strom. Warte, bis es hochfährt.
**Erwarte:** Es kommt innerhalb von ein bis zwei Minuten von allein zurück,
ohne Aktion im Dashboard, und behält Lautstärke und Mute-Zustand von vorher.
**Melde:** Ein Gerät, das erneut freigegeben werden muss, oder mit anderer
Lautstärke zurückkommt.

### A5 · Controller-Neustart überstehen
**Tu:** Starte den Controller neu (Add-on neu starten oder
`docker compose restart`).
**Erwarte:** Geräte verbinden sich von allein wieder. Einstellungen, Benutzer
und Verlauf sind noch da.
**Melde:** Alles, was neu eingerichtet werden musste.

---

## B — Home Assistant

### B1 · Der Satellit wird gefunden
**Tu:** HA → Einstellungen → Geräte & Dienste. Halte nach einem ESPHome-Fund
für dein Gerät Ausschau.
**Erwarte:** Er bietet das Hinzufügen an. Danach zeigt die Geräteseite eine
Entität **Assist satellite**.
**Melde:** Gar keinen Fund; oder einen Fund, der auf das falsche Gerät zeigt.

### B2 · Der Status des Sprachassistenten ist ehrlich
**Tu:** Gerät → **Status** → Zeile **Voice assistant**.
**Erwarte:** `HA connected · port NNNNN`, solange HA verbunden ist. Stoppe HA
— es sollte auf `Waiting for HA` wechseln.
**Melde:** Eine Zeile, die HA als verbunden ausweist, obwohl es das nicht ist,
oder umgekehrt.

### B3 · Ein vollständiges Sprachgespräch
**Tu:** Sag das Wakeword und frage etwas mit gesprochener Antwort.
**Erwarte:** Ring leuchtet beim Aufwachen → Antwort kommt aus dem
Gerätelautsprecher → Ring geht in Ruhe. Gerät → **Activity** zeigt das
Gespräch mit dem Ausgang `ok`.
**Melde:** Jedes Gespräch, das mit etwas anderem als `ok` endet, obwohl du
normal gesprochen hast. Gib den Ausgangstext aus „Activity" an.

### B4 · Durchsagen
**Tu:** Rufe `assist_satellite.announce` aus den HA-Entwicklerwerkzeugen
gegen dein Gerät auf.
**Erwarte:** Sie spielt, und der Dienstaufruf kehrt fehlerfrei zurück.
**Melde:** Fehler im HA-Log oder eine Durchsage, die spielt, aber nie endet.

### B5 · Entität der Aktionstaste
**Tu:** Halte die Aktionstaste (Punkt) ~1 s. Beobachte in HA die
Ereignis-Entität **Action Button** des Geräts.
**Erwarte:** Ein `long`-Ereignis feuert. Ein kurzes Tippen startet
stattdessen ein Sprachgespräch (außer du hast „Tippen ist ein Ereignis"
aktiviert).
**Melde:** Keine Ereignis-Entität auf einem Gerät, dessen Firmware Halten
unterstützt; oder ein Halten, das ein Gespräch startet.

### B6 · Helligkeitssensor
**Tu:** Deck das Gerät ab, dann leuchte es an. Beobachte den Sensor **Ambient
Light** in HA.
**Erwarte:** Der Lux-Wert bewegt sich. Auf Hardware ohne lesbaren Sensor
sollte es **gar keine Entität** geben — keine, die bei 0 festhängt.
**Melde:** Eine Entität, die dauerhaft 0 oder „nicht verfügbar" liest.

---

## C — Wakeword

### C1 · Das Standard-Wakeword
**Tu:** Sag es aus normaler Sitzentfernung, zehnmal, in einem ruhigen Raum.
**Erwarte:** Mindestens 8 von 10 wecken das Gerät.
**Melde:** Weniger als 8. Nenne Entfernung und Modell.

### C2 · Fehlauslöser
**Tu:** Lass das Gerät eine Stunde in einem Raum mit Fernseher oder
Gesprächen.
**Erwarte:** Null oder ein unerwünschtes Aufwachen.
**Melde:** Mehr davon. Notiere, was lief.

### C3 · Die Empfindlichkeit bewirkt etwas
**Tu:** Config → Wake word → Sensitivity Richtung „Eager" schieben. C1
wiederholen.
**Erwarte:** Mehr Aufwachen, und mehr Fehlauslöser. Die Änderung wirkt, ohne
dass etwas neu gestartet wird.
**Melde:** Eine Einstellung, die gespeichert wird, aber nichts ändert.

### C4 · Ein eigenes Modell installiert sich und funktioniert
**Tu:** Trainiere eines mit [oww_forge](../oww_forge/README.md), dann Config →
Wake word → **+ Custom model** → hochladen.
**Erwarte:** Es erscheint in der Liste, lässt sich wählen, und das Gerät wacht
darauf auf.
**Melde:** Ein Modell, das hochlädt und wählbar ist, aber nie auslöst — das
ist eine bestimmte bekannte Fehlerklasse und eine Meldung wert.

### C5 · Wakeword auf dem Gerät
**Tu:** Config → Wake word → Erkennung auf dem Gerät einschalten.
**Erwarte:** Aufwachen funktioniert weiterhin. Gerät → Activity zeichnet
weiterhin Gespräche auf.
**Melde:** Aufwachen, das ganz aufhört, oder eine merklich schlechtere
Reaktionszeit.

### C6 · Mehrere Geräte antworten nicht beide
**Tu:** Sag mit zwei Geräten in Hörweite einmal das Wakeword.
**Erwarte:** Ein Gerät antwortet. Das andere nicht.
**Melde:** Beide antworten, oder keines.

---

## D — Tonausgabe

### D1 · Lautstärke
**Tu:** Ändere die Lautstärke im Dashboard, in HA und mit den Tasten am Gerät.
**Erwarte:** Alle drei stimmen überein, und der Pegel übersteht einen
Neustart.
**Melde:** Wenn eines der drei den anderen widerspricht.

### D2 · Sprache ist bei niedriger Lautstärke verständlich
**Tu:** Stelle auf ~20 %, frage etwas mit langer Antwort.
**Erwarte:** Klare Sprache, keine Verzerrung.
**Melde:** Verzerrung, Übersteuern oder Knistern. Nenne den Pegel.

### D3 · Sprache ist bei hoher Lautstärke sauber
**Tu:** Stelle auf 100 %, frage dasselbe.
**Erwarte:** Laut, aber ohne Brummen oder Aufbrechen.
**Melde:** Verzerrung bei einem bestimmten Prozentwert — die Zahl zählt.

### D4 · Der EQ bewirkt etwas
**Tu:** Config → Playback → EQ-Regler bewegen oder eine Voreinstellung wählen.
**Erwarte:** Hörbare Änderung beim nächsten Gesprochenen, ohne Neustart.
**Melde:** Kein hörbarer Unterschied, oder eine Änderung, die einen Neustart
braucht.

### D5 · Lautsprecherschutz
**Tu:** Config → Playback → prüfen, dass Limiter und Bass-Schutz an sind.
Spiel etwas Bassbetontes laut.
**Erwarte:** Es bleibt kontrolliert. Schaltest du den Limiter aus, sollte es
merklich schlechter werden.
**Melde:** Kein Unterschied zwischen an und aus.

### D6 · Die Kopfhörerbuchse
**Tu:** Steck etwas in die 3,5-mm-Buchse.
**Erwarte:** Der Ton wechselt auf die Buchse.
**Melde:** Alles, was über die bekannten Buchsenfehler in der Tabelle oben
hinausgeht.

---

## E — Musik und Ducking

### E1 · Musik spielt
**Tu:** Schick Musik aus HA an das Gerät (Music Assistant oder ein
`media_player`-Aufruf).
**Erwarte:** Sie spielt.
**Melde:** Stille, Stottern oder einen Stream, der nach fester Zeit abbricht.

### E2 · Musik wird unter einem Sprachgespräch leiser
**Tu:** Sag bei laufender Musik das Wakeword und frage etwas.
**Erwarte:** Die Musik wird leiser, die Antwort wird darüber gesprochen, die
Musik kommt wieder auf Pegel. Auf Firmware, die Mischen unterstützt, soll sie
**leiser werden**, nicht pausieren.
**Melde:** Eine vollständige Pause auf einem Gerät, dessen Status Mischen
ausweist; Musik, die nie wieder hochkommt; oder ein Absenken, das die Musik
dauerhaft leise lässt.

### E3 · Die Absenktiefe ist einstellbar
**Tu:** Config → Playback → Absenktiefe ändern. E2 wiederholen.
**Erwarte:** Hörbar andere Tiefe.
**Melde:** Keine Änderung.

### E4 · Barge-in
**Tu:** Sag während einer langen gesprochenen Antwort erneut das Wakeword.
**Erwarte:** Die Antwort bricht ab und das Gerät hört dir zu.
**Melde:** Eine Antwort, die bis zum Ende weiterläuft; oder ein abgelehntes
neues Gespräch. Gib den Activity-Ausgang des zweiten Gesprächs an.

---

## F — Timer und Wecker

*Kürzlich geändert — der Bereich, dessen sorgfältiger Test sich am meisten
lohnt.*

### F1 · Ein Timer klingelt
**Tu:** „Stell einen Timer auf eine Minute."
**Erwarte:** Das Gerät klingelt nach einer Minute.
**Melde:** Kein Klingeln, oder Klingeln auf dem falschen Gerät.

### F2 · Das Klingeln stoppen
**Tu:** Sag ihm während des Klingelns, es soll aufhören.
**Erwarte:** Es hört auf.
**Melde:** Ein Klingeln, das sich per Sprache nicht stoppen lässt. Notiere, ob
die Taste es stoppt.

### F3 · Ein Timer, der während eines Sprachgesprächs feuert
**Tu:** Stell einen kurzen Timer, starte dann ein weiteres Sprachgespräch, so
dass der Timer mitten in der Antwort feuert.
**Erwarte:** Beides wird vernünftig gehandhabt — du hörst beides, nichts geht
verloren, und das Gerät kehrt danach in Ruhe zurück.
**Melde:** Ton, der abrupt abbricht, ein Gerät, das im Klingeln hängt, oder
ein Gespräch, das nie endet. **Diese Kombination ist neu und genau das, was
wir getestet haben wollen.**

### F4 · Ein Timer, der unter abgesenkter Musik feuert
**Tu:** Musik läuft, Timer feuert.
**Erwarte:** Der Alarm ist über der Musik zu hören, danach kommt die Musik
auf vollen Pegel zurück.
**Melde:** Musik, die abgesenkt bleibt, oder einen Alarm, den du nicht hörst.

---

## G — Tasten und LEDs

### G1 · Die Aktionstaste startet ein Gespräch
**Tu:** Tippe die Punkt-Taste an.
**Erwarte:** Dasselbe Verhalten wie beim Wakeword.
**Melde:** Keine Reaktion, oder eine verzögerte.

### G2 · Mute ist echt
**Tu:** Drücke Mute. Probier das Wakeword. Probier die Aktionstaste.
**Erwarte:** Roter Ring, und über keinen der beiden Wege beginnt ein
Sprachgespräch. Das Mikrofon ist in der Hardware aus, nicht bloß ignoriert.
**Melde:** Ein Gespräch, das trotz Mute beginnt.

### G3 · Unmute stellt wieder her
**Tu:** Drücke Mute noch einmal.
**Erwarte:** Der Ring geht in Ruhe, das Wakeword funktioniert sofort.
**Melde:** Wenn du neu starten musst, um das Mikrofon zurückzubekommen.

### G4 · Ringzustände passen zum Geschehen
**Tu:** Beobachte den Ring durch ein ganzes Gespräch.
**Erwarte:** Unterscheidbare Zustände für Zuhören / Nachdenken / Sprechen, am
Ende zurück in Ruhe. Siehe [led-ring-states.md](led-ring-states.md).
**Melde:** Einen Ring, der nach dem Gespräch leuchten bleibt, oder in einer
Farbe hängt.

### G5 · Ringfarben sind einstellbar
**Tu:** Config → Ring → Farben für Zuhören und Nachdenken ändern.
**Erwarte:** Das nächste Gespräch benutzt sie.
**Melde:** Keine Änderung, oder eine, die einen Neustart braucht.

---

## H — Bluetooth-Proxy

### H1 · Der Proxy wird angeboten
**Tu:** Config → Bluetooth → aktivieren. Sieh in HA → Einstellungen → Geräte &
Dienste nach.
**Erwarte:** Das Gerät erscheint als Bluetooth-Proxy.
**Melde:** Im Dashboard aktiviert, in HA aber nicht vorhanden.

### H2 · Er findet etwas
**Tu:** Bring ein BLE-Gerät (ein Thermometer, einen Tracker) in seine Nähe.
**Erwarte:** HA sieht es über diesen Proxy.
**Melde:** Nach 10 Minuten gar keine Funde.

### H3 · Er übersteht einen Neustart
**Tu:** Starte das Gerät neu.
**Erwarte:** Der Proxy kommt von allein zurück.
**Melde:** Wenn du ihn aus- und wieder einschalten musst.

---

## I — Sicherheit und die Geräteverbindung

### I1 · Sichere Verbindung
**Tu:** Gerät → Status. Steht bei „Link" `plain ws`, drücke **Secure link**.
**Erwarte:** Das Gerät verbindet sich innerhalb weniger Sekunden neu, und
„Link" liest `wss (TLS)`.
**Melde:** Ein Gerät, das offline geht und offline bleibt. (Es sollte neu
wählen.)

### I2 · Zugangsdaten überstehen einen Neustart
**Tu:** Starte ein TLS-Gerät neu.
**Erwarte:** Es kommt auf `wss (TLS)` zurück.
**Melde:** Einen Rückfall auf unverschlüsselt.

### I3 · Die Anmeldung wird durchgesetzt
**Tu:** Melde dich ab. Versuche, das Dashboard zu öffnen, und rufe eine
API-URL direkt auf.
**Erwarte:** Beides wird abgewiesen.
**Melde:** Alles, was abgemeldet erreichbar ist.

### I4 · Nicht-Admin-Konten sind eingeschränkt
**Tu:** Lege einen Nicht-Admin-Benutzer an. Melde dich als dieser an.
**Erwarte:** Kein Reiter „Console", kein „Updates", kein „Support", keine
Benutzerverwaltung.
**Melde:** Jede Admin-Aktion, die ein Nicht-Admin erreichen kann.

---

## J — Updates

### J1 · Ein Firmware-Update wird angeboten
**Tu:** Gerät → **Updates**.
**Erwarte:** Es zeigt die installierte Version und die neueste Veröffentlichung
und bietet das Update nur an, wenn es eines gibt.
**Melde:** Ein Update, das angeboten wird, obwohl du aktuell bist, oder keines,
obwohl du zurückliegst.

### J2 · **Destruktiv** — ein Firmware-Update anwenden
**Tu:** Führ das Update aus. Beobachte es bis zum Ende.
**Erwarte:** Es überträgt, das Gerät startet neu und kommt auf der neuen
Version zurück, mit erhaltenen Einstellungen.
**Melde:** Ein Gerät, das nicht zurückkommt, oder auf der alten Version
zurückkommt und trotzdem Erfolg meldet. **Trenne es nicht mitten im Update vom
Strom.** Kommt es nach fünf Minuten nicht zurück, schreib das in die Meldung,
bevor du irgendetwas anderes tust — der Zustand, in dem es ist, ist die
Diagnose.

### J3 · Hinweis auf ein Controller-Update
**Tu:** Wenn es eine Controller-Veröffentlichung gibt, die neuer ist als deine.
**Erwarte:** Einen Hinweis im Dashboard mit lesbaren Versionshinweisen. Er
soll dir sagen, dass du aktualisieren sollst — er darf sich niemals selbst
aktualisieren.
**Melde:** Einen Hinweis mit leeren Notizen; oder jede Schaltfläche, die
behauptet, das Update auszuführen.

---

## K — Das Dashboard

### K1 · „Activity" stimmt
**Tu:** Führ fünf Gespräche. Öffne Gerät → **Activity**.
**Erwarte:** Fünf Gespräche, mit sinnvollen Ausgängen und Zeiten.
**Melde:** Fehlende Gespräche oder offensichtlich falsche Zeiten.

### K2 · Logs laden
**Tu:** Gerät → **Logs**.
**Erwarte:** Aktuelle Zeilen, sowohl vom Gerät als auch vom Controller.
**Melde:** Eine leere Ansicht auf einem Gerät, das schon eine Weile läuft.

### K3 · Config-Bereiche
**Tu:** Ändere einen Abschnitt (sagen wir „Ring") auf nur einem Gerät.
**Erwarte:** Status → Config liest `Local override (1 of 6)`. Ändere eine
Flotteneinstellung in einem *anderen* Abschnitt — dieses Gerät sollte ihr
folgen.
**Melde:** Eine Überschreibung, die in andere Abschnitte übergreift, oder ein
Gerät, das der Flotte ganz aufhört zu folgen.

### K4 · Es funktioniert auf dem Handy
**Tu:** Öffne das Dashboard auf einem Handy.
**Erwarte:** Benutzbar. Nichts abgeschnitten, kein waagerechtes Scrollen.
**Melde:** Alles, was bei dieser Breite unerreichbar ist. Ein Screenshot hilft.

### K5 · Kontrast und Lesbarkeit
**Tu:** Sieh es dir hell und dunkel an.
**Erwarte:** Alles lesbar.
**Melde:** Text mit zu wenig Kontrast — mit Screenshot.

### K6 · Support-Bundle
**Tu:** Settings → Support → Collect bundle → Herunterladen. Öffne die Datei.
**Erwarte:** Gültiges JSON. **Keine Transkripte, keine WLAN-SSID, keine
IP-Adressen, keine von dir vergebenen Gerätebezeichnungen, keine Token.**
**Melde:** Alles Private darin. Melde das privat statt in einem öffentlichen
Issue, und häng das Bundle nicht an.

---

## L — Umgang mit Störungen

### L1 · Der Controller fällt aus
**Tu:** Stoppe den Controller, während ein Gerät im Leerlauf ist.
**Erwarte:** Das Gerät merkt es, zeigt es an und verbindet sich von allein
wieder, wenn der Controller zurück ist — kein Neustart, keine erneute
Freigabe.
**Melde:** Ein Gerät, das nie zurückkommt, oder erst nach einem
Stromtrennen.

### L2 · Das WLAN fällt aus
**Tu:** Nimm den Access Point für eine Minute vom Netz, dann zurück.
**Erwarte:** Das Gerät verbindet sich von allein wieder.
**Melde:** Ein Gerät, das draußen bleibt. (Beachte das bekannte Issue #317 für
Netze ohne Internet.)

### L3 · Home Assistant fällt aus
**Tu:** Stoppe HA. Sag das Wakeword.
**Erwarte:** Der Ausfall ist sichtbar — die Zeile „Voice assistant" sollte
nicht gesund aussehen. Kommt HA zurück, funktionieren Gespräche wieder, ohne
dass du etwas anfasst.
**Melde:** Ein Dashboard, das bei gestopptem HA gesund aussieht.

### L4 · **Destruktiv** — ein Gerät löschen
**Tu:** Lösche ein Gerät im Dashboard.
**Erwarte:** Es trennt sich, verschwindet und kommt als *ausstehend* zurück,
statt still weiterzuarbeiten. Gib es erneut frei.
**Melde:** Ein gelöschtes Gerät, das weiterläuft; oder ein neu hinzugefügtes
Gerät, dessen Status keinen Sprach-Port zeigt.

---

## Einen ganzen Durchlauf melden

Wenn du einen vollständigen Durchlauf machst, ist ein Issue mit einer Tabelle
nützlicher als ein Issue pro Test:

```
Controller: 2.21.0    Firmware: v2.13.0    HA: 2026.8.3    Hardware: Echo Dot Gen 2

A1 pass   A2 pass   A3 pass   A4 pass   A5 pass
B1 pass   B2 pass   B3 pass   B4 FAIL   B5 pass   B6 n/a
C1 pass (9/10)  C2 FAIL (4 Fehlauslöser in einer Stunde, Fernseher an)  ...
```

`pass` / `fail` / `n/a` / `skipped`. Zu jedem `fail` ein Absatz und ein
Support-Bundle. Was du nicht testen konntest, ist genauso nützlich zu wissen
wie ein Fehler — es sagt uns, welchen Teilen dieser Anleitung niemand folgen
kann.
